package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/onsei/organizer/backend/internal/admission"
)

// Request is one scan of a library root (or of a member directory inside it).
// The RootPath is the exact path to scan; the LibraryID is the library row the
// outcome is recorded on.
type Request struct {
	LibraryID string
	RootPath  string
}

// Event is emitted during a scan. Type is one of started|progress|completed
// (plus error on failure).
type Event struct {
	Type         string
	Stage        string
	FilesScanned int
	DirsScanned  int
	ScanID       string
	Message      string
}

// Result is the output of one scan.
type Result struct {
	ScanID       string
	RootPath     string
	FilesScanned int
}

// ErrorKind values for Error.Kind, used to map to HTTP status codes.
const (
	ErrKindInvalidArgument = "invalid_argument"
	ErrKindInternal        = "internal"
	ErrKindConflict        = "conflict"
)

// Error is a scan-level error carrying the stable code the HTTP layer maps.
type Error struct {
	Kind    string
	Code    string
	Message string
	Cause   error
}

// NewError creates a scan-level error.
func NewError(kind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Kind + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Kind + ": " + e.Message
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error { return e.Cause }

// AsError extracts an *Error from an error chain.
func AsError(err error) (*Error, bool) {
	if e, ok := errors.AsType[*Error](err); ok {
		return e, true
	}
	return nil, false
}

// Store is the storage the scanning entry needs beyond staging: whether an
// execution is running against a root right now. The plan executions live in
// the database and outlive any single request, so the entry asks rather than
// tracks them.
type Store interface {
	HasActiveExecutionForRoot(rootPath string) (bool, error)
}

// RecordScanState records the outcome of a scan on the library row. The fact
// belongs to the library and the scan that produced it belongs here, so it
// crosses as a one-method seam the library module implements.
type RecordScanState func(libraryID, status, message string, at time.Time) error

// Service is the scanning entry the HTTP layer calls. Admission is a separate,
// explicit step: it must happen before the response is committed, so a refusal
// is still a JSON envelope rather than an error inside a stream that already
// started (ADR 0002 §2).
type Service interface {
	// AdmitLibraryScan takes the admission slot for a full library scan. It
	// answers the execution conflict first: a scan rewrites the inventory a
	// running execution validates against.
	AdmitLibraryScan(rootPath string) (func(), error)

	// AdmitMemberRefresh takes the admission slot for a page refresh of one
	// member directory. A refresh reads one subtree rather than rewriting the
	// inventory, so it does not take the execution check its full-scan sibling
	// takes.
	AdmitMemberRefresh() (func(), error)

	// Scan validates the root, streams started/progress/completed events via
	// emit, records the outcome on the library row, and returns the result. On
	// failure it returns a typed *Error where applicable.
	Scan(ctx context.Context, req Request, emit func(Event)) (Result, error)

	// RefreshMember re-scans one member directory into the stored inventory. It
	// takes no admission: it is called by paths that already hold the slot.
	RefreshMember(ctx context.Context, folderPath, rootPath string) error
}

// service is the scanning entry.
type service struct {
	pipeline *Pipeline
	store    Store
	gate     *admission.Gate
	record   RecordScanState
}

// NewService creates the scanning entry. gate is the process-wide admission
// control; a nil gate means this process has no file management wired, which
// is the state every test server starts in. record writes the outcome back to
// the library row.
func NewService(pipeline *Pipeline, store Store, gate *admission.Gate, record RecordScanState) Service {
	return &service{pipeline: pipeline, store: store, gate: gate, record: record}
}

// beginScan registers one scanning operation, or reports the slot as busy.
func (s *service) beginScan() (func(), error) {
	if s.gate == nil {
		return func() {}, nil
	}
	return s.gate.BeginScan()
}

// AdmitLibraryScan takes the slot for a full scan of one root.
func (s *service) AdmitLibraryScan(rootPath string) (func(), error) {
	executing, err := s.store.HasActiveExecutionForRoot(rootPath)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active executions", err)
	}
	if executing {
		return nil, NewError(
			ErrKindConflict,
			"EXECUTION_IN_PROGRESS",
			"cancel or wait for the active execution before scanning",
			nil,
		)
	}
	return s.beginScan()
}

// AdmitMemberRefresh takes the slot for one member refresh.
func (s *service) AdmitMemberRefresh() (func(), error) {
	return s.beginScan()
}

// Scan validates the root, runs the pipeline and records the outcome on the
// library row. A canceled scan and a failed scan are both recorded, because
// the library's last-scan state is what the workbench shows.
func (s *service) Scan(ctx context.Context, req Request, emit func(Event)) (Result, error) {
	if emit == nil {
		emit = func(Event) {}
	}

	if req.RootPath == "" {
		return s.fail(req, NewError(ErrKindInvalidArgument, "ROOT_PATH_REQUIRED", "root_path is required", nil), emit)
	}

	// Classify validation failures so a missing root, a non-directory root,
	// and an inaccessible root (e.g. EACCES) are not conflated, and report each
	// as a terminal `error` event per the scan lifecycle contract.
	fi, statErr := os.Stat(req.RootPath)
	var validationErr *Error
	switch {
	case statErr != nil && os.IsNotExist(statErr):
		validationErr = NewError(
			ErrKindInvalidArgument,
			"ROOT_PATH_NOT_FOUND",
			fmt.Sprintf("root_path not found: %s", req.RootPath),
			statErr,
		)
	case statErr != nil:
		validationErr = NewError(
			ErrKindInternal,
			"ROOT_PATH_STAT_FAILED",
			fmt.Sprintf("failed to access root_path: %s", req.RootPath),
			statErr,
		)
	case !fi.IsDir():
		validationErr = NewError(
			ErrKindInvalidArgument,
			"ROOT_PATH_NOT_DIRECTORY",
			fmt.Sprintf("root_path is not a directory: %s", req.RootPath),
			nil,
		)
	}
	if validationErr != nil {
		return s.fail(req, validationErr, emit)
	}

	emit(Event{Type: "started", Stage: "scan", Message: fmt.Sprintf("Scanning %s", req.RootPath)})

	var lastProgress Progress
	scanID, err := s.pipeline.ScanRootCtx(ctx, req.RootPath, WithProgress(func(p Progress) {
		lastProgress = p
		emit(Event{Type: "progress", Stage: "scan", FilesScanned: p.FilesScanned, DirsScanned: p.DirsScanned})
	}))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.recordOutcome(req, "canceled", "")
			emit(Event{Type: "error", Stage: "scan", Message: "scan canceled"})
			return Result{}, err
		}
		scanErr := NewError(ErrKindInternal, "SCAN_FAILED", fmt.Sprintf("Scan failed: %v", err), err)
		return s.fail(req, scanErr, emit)
	}

	s.recordOutcome(req, "completed", "")
	emit(Event{
		Type:         "completed",
		Stage:        "scan",
		FilesScanned: lastProgress.FilesScanned,
		DirsScanned:  lastProgress.DirsScanned,
		ScanID:       scanID,
		Message:      fmt.Sprintf("Scan completed (scan ID: %s)", scanID),
	})

	return Result{
		ScanID:       scanID,
		RootPath:     req.RootPath,
		FilesScanned: lastProgress.FilesScanned,
	}, nil
}

// fail emits the terminal error event and records the outcome.
func (s *service) fail(req Request, err *Error, emit func(Event)) (Result, error) {
	s.recordOutcome(req, "failed", err.Message)
	emit(Event{Type: "error", Stage: "scan", Message: err.Message})
	return Result{}, err
}

// recordOutcome writes what the library row shows about this scan. A recording
// failure is reported rather than swallowed: the scan's own result is already
// decided, but the row the workbench reads must not silently keep a stale one.
func (s *service) recordOutcome(req Request, status, message string) {
	if s.record == nil || req.LibraryID == "" {
		return
	}
	_ = s.record(req.LibraryID, status, message, time.Now())
}
