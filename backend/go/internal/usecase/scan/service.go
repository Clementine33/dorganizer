package scan

import (
	"context"
	"fmt"
	"os"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/scanner"
)

type serviceImpl struct {
	repo *sqlite.Repository
}

// NewService creates a new scan service backed by the sqlite repository.
func NewService(repo *sqlite.Repository) Service {
	return &serviceImpl{repo: repo}
}

// Scan validates the root path, streams started/progress/completed events via
// emit, and returns the scan result. On failure it emits an error event and
// returns a typed *Error where applicable.
func (s *serviceImpl) Scan(ctx context.Context, req Request, emit func(Event)) (Result, error) {
	if emit == nil {
		emit = func(Event) {}
	}

	if req.RootPath == "" {
		err := NewError(ErrKindInvalidArgument, "ROOT_PATH_REQUIRED", "root_path is required", nil)
		emit(Event{Type: "error", Stage: "scan", Message: err.Message})
		return Result{}, err
	}

	// Classify validation failures so a missing root, a non-directory root,
	// and an inaccessible root (e.g. EACCES) are not conflated, and report each
	// as a terminal `error` event per the scan lifecycle contract.
	fi, statErr := os.Stat(req.RootPath)
	var validationErr *Error
	switch {
	case statErr != nil && os.IsNotExist(statErr):
		validationErr = NewError(ErrKindInvalidArgument, "ROOT_PATH_NOT_FOUND", fmt.Sprintf("root_path not found: %s", req.RootPath), statErr)
	case statErr != nil:
		validationErr = NewError(ErrKindInternal, "ROOT_PATH_STAT_FAILED", fmt.Sprintf("failed to access root_path: %s", req.RootPath), statErr)
	case !fi.IsDir():
		validationErr = NewError(ErrKindInvalidArgument, "ROOT_PATH_NOT_DIRECTORY", fmt.Sprintf("root_path is not a directory: %s", req.RootPath), nil)
	}
	if validationErr != nil {
		emit(Event{Type: "error", Stage: "scan", Message: validationErr.Message})
		return Result{}, validationErr
	}

	emit(Event{Type: "started", Stage: "scan", Message: fmt.Sprintf("Scanning %s", req.RootPath)})

	svc := scanner.NewScannerService(scanner.NewSQLiteRepositoryAdapter(s.repo))
	var lastProgress scanner.Progress
	scanID, err := svc.ScanRootCtx(ctx, req.RootPath, scanner.WithProgress(func(p scanner.Progress) {
		lastProgress = p
		emit(Event{Type: "progress", Stage: "scan", FilesScanned: p.FilesScanned, DirsScanned: p.DirsScanned})
	}))
	if err != nil {
		emit(Event{Type: "error", Stage: "scan", Message: fmt.Sprintf("Scan failed: %v", err)})
		return Result{}, err
	}

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
