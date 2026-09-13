package workset

import (
	"context"
	"errors"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// OperationTypeConversion is the only publicly available operation type of
// this iteration. Encode/delete filesystem actions are a different concept and
// never appear here (ADR 0004 §2).
const OperationTypeConversion = "conversion"

// Operation planning states (derived per operation, never stored).
const (
	PlanningUnplanned     = "unplanned"
	PlanningPlanned       = "planned"
	PlanningNeedsPlanning = "needs_planning"
	PlanningPlanning      = "planning"
	PlanningOrphaned      = "orphaned"
)

// Revision validation state values (revision-level, derived on read).
const (
	ValidationValid       = "valid"
	ValidationStale       = "stale"
	ValidationUnavailable = "unavailable"
)

// Error kinds for the workset usecase error envelope.
const (
	ErrKindInvalidArgument = "invalid_argument"
	ErrKindNotFound        = "not_found"
	ErrKindConflict        = "conflict"
	ErrKindPrecondition    = "precondition_failed"
	ErrKindInternal        = "internal"
)

// Error is the workset usecase error with a stable machine code. Details
// carries machine-readable reason codes (e.g. PLAN_NOT_CONFIRMABLE reasons).
type Error struct {
	Kind    string
	Code    string
	Message string
	Details []string
	Cause   error
}

// NewError creates a workset usecase error.
func NewError(kind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

// WithDetails attaches machine-readable reason codes to the error.
func (e *Error) WithDetails(details []string) *Error {
	e.Details = details
	return e
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Kind + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Kind + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

// AsError extracts a *Error from an error chain.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// LibraryRef is the owning-library snapshot exposed on the workset view.
type LibraryRef struct {
	LibraryID string
	Name      string
	RootPath  string
}

// MemberView is one album-folder member. MemberID is the stable identity
// persisted on the member row; participation and overrides belong to an
// operation, not to the member.
type MemberView struct {
	MemberID   string
	FolderID   string
	FolderPath string
	FolderName string
	RelPath    string
}

// RevisionCounts are independent plan facts (ADR 0004 §4): a component with
// changes, an unmet target, a blocked component and an unchanged component are
// separate counts that may overlap. No exclusive status label is derived.
type RevisionCounts struct {
	Members      int `json:"members"`
	Changed      int `json:"changed"`
	UnmetTargets int `json:"unmet_targets"`
	Blocked      int `json:"blocked"`
	Unchanged    int `json:"unchanged"`
}

// RevisionSummary is the compact immutable conclusion of one operation
// revision.
type RevisionSummary struct {
	PlanID          string
	RevisionIndex   int
	CreatedAt       time.Time
	Status          string
	SummaryReason   string
	Counts          RevisionCounts
	ValidationState string // valid | stale | unavailable
	Stale           *bool  // nil when validation_state == unavailable
}

// RevisionListResult is one page of revision history plus its keyset cursor.
// NextBeforeIndex is the revision_index of the last row of this page; a value
// of 0 means the page reached the oldest revision (no more pages).
type RevisionListResult struct {
	Revisions       []*RevisionSummary
	NextBeforeIndex int
}

// GenerationProgress is root-level progress of an active session.
type GenerationProgress struct {
	GenerationID   string
	Status         string
	TotalRoots     int
	CompletedRoots int
	CurrentRoot    string
	ErrorCount     int
}

// GenerationSummary is the compact terminal summary of a session.
type GenerationSummary struct {
	GenerationID string
	Status       string
	ErrorCode    string
	ErrorMessage string
	FinishedAt   time.Time
}

// OperationView is the operation-scoped aggregate. It is the state the
// workbench reads for one operation; nothing here is workset-wide.
type OperationView struct {
	WorksetID        string
	OperationType    string
	Version          int
	PlanningState    string
	CurrentRevision  *RevisionSummary
	ActiveGeneration *GenerationProgress
	LatestGeneration *GenerationSummary
}

// WorksetView is the workset metadata view: identity, library, fixed members
// and the operation entries the UI navigates to.
type WorksetView struct {
	WorksetID  string
	Title      string
	Version    int
	Library    *LibraryRef
	Members    []MemberView
	Operations []*OperationView
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CreateRequest is the POST /worksets payload.
type CreateRequest struct {
	LibraryID      string
	Title          string
	FolderIDs      []string
	IdempotencyKey string
}

// CreateResult distinguishes a fresh creation from an idempotent replay.
type CreateResult struct {
	Workset *WorksetView
	Created bool
}

// ListQuery is the workset list input.
type ListQuery struct {
	Cursor          string // "<updated_at RFC3339Nano>_<workset_id>"
	Limit           int
	LibraryID       string
	IncludeOrphaned bool
}

// RenameRequest is the PATCH /worksets/{id} payload.
type RenameRequest struct {
	Title          string
	IfMatchVersion int
}

// Draft is the persisted sparse operation draft. Version is the operation
// version (the If-Match authority); there is no separate draft counter.
type Draft struct {
	WorksetID     string
	OperationType string
	Version       int
	SchemaVersion int
	Document      *DraftDoc
	UpdatedAt     time.Time
}

// SaveDraftRequest is the PUT operation draft payload (full replacement of the
// sparse document, never a materialized full-config document).
type SaveDraftRequest struct {
	Document       *DraftDoc
	IfMatchVersion int
}

// StartGenerationRequest is the POST operation revisions payload.
type StartGenerationRequest struct {
	IfMatchVersion int
	IdempotencyKey string
}

// StartGenerationResult distinguishes a fresh session from a replay/dedup.
type StartGenerationResult struct {
	Generation *GenerationView
	Revision   *RevisionSummary // present for created:false unchanged-input replays
	Created    bool
}

// GenerationView is the session detail payload.
type GenerationView struct {
	GenerationID   string
	WorksetID      string
	OperationType  string
	Status         string
	TotalRoots     int
	CompletedRoots int
	CurrentRoot    string
	ErrorCount     int
	RevisionID     string
	ErrorCode      string
	ErrorMessage   string
	StartedAt      time.Time
	FinishedAt     time.Time
	CreatedAt      time.Time
}

// RevisionMember is one member of a frozen revision: its effective config,
// where each unit came from, and whether it participated. It is derived from
// the frozen sparse draft, never from the live one.
type RevisionMember struct {
	MemberID   string
	FolderPath string
	MemberName string
	Excluded   bool
	Policy     reconcile.Policy
	Sources    map[string]string
}

// RevisionView is the nested immutable revision detail.
type RevisionView struct {
	PlanID        string
	RevisionIndex int
	CreatedAt     time.Time
	Workflow      planusecase.Response
	Roots         []RootValidation
	// ComponentRoots maps each persisted component to its planning root. The
	// reconcile ComponentOutcome JSON intentionally carries no root identity,
	// so the HTTP layer exposes this ownership table for batch grouping.
	ComponentRoots []ComponentRootRef
	// Members is the frozen per-member effective configuration and inheritance
	// sources resolved from the revision's draft snapshot.
	Members []RevisionMember
	// Counts are the independent plan facts of this revision.
	Counts RevisionCounts
}

// ComponentRootRef is the stable component-to-root ownership of a revision.
type ComponentRootRef struct {
	StepIndex      int    `json:"step_index"`
	ComponentIndex int    `json:"component_index"`
	ComponentID    string `json:"component_id"`
	RootIndex      int    `json:"root_index"`
}

// RootValidation is per-root validation of a revision snapshot.
type RootValidation struct {
	RootIndex            int
	RootPath             string
	RootStatus           string
	RootErrorCode        string
	RootErrorMessage     string
	Stale                bool
	InventoryFingerprint string
	EntryCount           int
}

// ConfirmRequest carries the If-Match version for a confirmation.
type ConfirmRequest struct {
	IfMatchVersion int
}

// ConfirmResult distinguishes a first confirmation from an idempotent repeat.
type ConfirmResult struct {
	Confirmation ConfirmationView
	Created      bool
}

// ConfirmationView is the confirmation state of one revision.
type ConfirmationView struct {
	Confirmed        bool   `json:"confirmed"`
	ConfirmedVersion int    `json:"confirmed_version,omitempty"`
	ConfirmedAt      string `json:"confirmed_at,omitempty"`
}

// Dispatcher is the background FIFO generation scheduler handle for main
// wiring (Start once at process startup, Stop at graceful shutdown).
type Dispatcher interface {
	Start()
	Stop()
}

// Service is the workset usecase contract. Every operation-scoped method takes
// the workset id and the operation type: no mutable state is addressable by
// workset id alone.
type Service interface {
	DispatcherHandle() Dispatcher
	CreateWorkset(ctx context.Context, req CreateRequest) (*CreateResult, error)
	ListWorksets(ctx context.Context, q ListQuery) ([]*WorksetView, string, error)
	GetWorkset(ctx context.Context, id string) (*WorksetView, error)
	RenameWorkset(ctx context.Context, id string, req RenameRequest) (*WorksetView, error)
	GetOperation(ctx context.Context, worksetID, operationType string) (*OperationView, error)
	GetDraft(ctx context.Context, worksetID, operationType string) (*Draft, error)
	SaveDraft(ctx context.Context, worksetID, operationType string, req SaveDraftRequest) (*OperationView, error)
	StartGeneration(
		ctx context.Context,
		worksetID, operationType string,
		req StartGenerationRequest,
	) (*StartGenerationResult, error)
	GetGeneration(ctx context.Context, worksetID, operationType, generationID string) (*GenerationView, error)
	CancelGeneration(ctx context.Context, worksetID, operationType, generationID string) (*GenerationView, error)
	Subscribe(
		ctx context.Context,
		worksetID, operationType, generationID string,
		emit func(event string, data any) error,
	) error
	ListRevisions(
		ctx context.Context,
		worksetID, operationType string,
		beforeIndex, limit int,
	) (*RevisionListResult, error)
	GetRevision(ctx context.Context, worksetID, operationType, planID string) (*RevisionView, error)
	ConfirmRevision(
		ctx context.Context,
		worksetID, operationType, planID string,
		req ConfirmRequest,
	) (*ConfirmResult, error)
	GetConfirmation(ctx context.Context, worksetID, operationType, planID string) (*ConfirmationView, error)
}

// toGenerationView converts a persisted session row to the view payload.
func toGenerationView(g *sqlite.PlanGeneration) *GenerationView {
	return &GenerationView{
		GenerationID:   g.GenerationID,
		WorksetID:      g.WorksetID,
		OperationType:  g.OperationType,
		Status:         g.Status,
		TotalRoots:     g.TotalRoots,
		CompletedRoots: g.CompletedRoots,
		CurrentRoot:    g.CurrentRoot,
		ErrorCount:     g.ErrorCount,
		RevisionID:     g.RevisionID,
		ErrorCode:      g.ErrorCode,
		ErrorMessage:   g.ErrorMessage,
		StartedAt:      g.StartedAt,
		FinishedAt:     g.FinishedAt,
		CreatedAt:      g.CreatedAt,
	}
}
