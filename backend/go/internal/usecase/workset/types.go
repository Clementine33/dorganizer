package workset

import (
	"context"
	"errors"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// Planning state values (workset-level, derived, never stored).
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

// Member coverage states against the current revision.
const (
	MemberPlanned = "planned"
	MemberPending = "pending"
	MemberMissing = "missing"
)

// Generation kind discriminator for the session row.
const (
	GenerationKind   = "workflow"
	RequestKindStart = "workflow"
)

// Error kinds for the workset usecase error envelope.
const (
	ErrKindInvalidArgument = "invalid_argument"
	ErrKindNotFound        = "not_found"
	ErrKindConflict        = "conflict"
	ErrKindPrecondition    = "precondition_failed"
	ErrKindInternal        = "internal"
)

// Error is the workset usecase error with a stable machine code.
type Error struct {
	Kind    string
	Code    string
	Message string
	Cause   error
}

// NewError creates a workset usecase error.
func NewError(kind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
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

// MemberView is one album-folder member with its coverage state.
type MemberView struct {
	FolderID   string
	FolderPath string
	FolderName string
	RelPath    string
	State      string
}

// RevisionSummary is the compact immutable conclusion of the current revision.
type RevisionSummary struct {
	PlanID          string
	RevisionIndex   int
	CreatedAt       time.Time
	Status          string
	SummaryReason   string
	BlockedCount    int
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

// WorksetView is the feed-ready / detail aggregate view.
type WorksetView struct {
	WorksetID        string
	Title            string
	Version          int
	Library          *LibraryRef
	PlanningState    string
	CurrentRevision  *RevisionSummary
	ActiveGeneration *GenerationProgress
	LatestGeneration *GenerationSummary
	Members          []MemberView
	UpdatedAt        time.Time
	CreatedAt        time.Time
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

// Feed filter values for the workset feed. Categories are mutually exclusive
// and user-facing; error wins over pending, pending wins over normal.
const (
	FeedAll     = "all"
	FeedPending = "pending"
	FeedNormal  = "normal"
	FeedError   = "error"
)

// ValidFeed reports whether s is a recognized feed filter value.
func ValidFeed(s string) bool {
	switch s {
	case FeedAll, FeedPending, FeedNormal, FeedError:
		return true
	}
	return false
}

// ListQuery is the feed listing input.
type ListQuery struct {
	Cursor          string // "<updated_at RFC3339Nano>_<workset_id>"
	Limit           int
	LibraryID       string
	IncludeOrphaned bool
	Feed            string // "" or "all" means no filtering
}

// RenameRequest is the PATCH /worksets/{id} payload.
type RenameRequest struct {
	Title          string
	IfMatchVersion int
}

// Draft is the persisted workflow draft view. Version is the aggregate
// worksets.version (the single mutation authority); there is no separate
// draft concurrency counter.
type Draft struct {
	WorksetID             string
	Version               int
	WorkflowSchemaVersion int
	Workflow              planusecase.Workflow
	WorkflowJSON          string
	DraftHash             string
	UpdatedAt             time.Time
}

// SaveDraftRequest is the PUT /worksets/{id}/draft payload.
type SaveDraftRequest struct {
	Workflow       planusecase.Workflow
	IfMatchVersion int
}

// StartGenerationRequest is the POST /worksets/{id}/revisions payload.
type StartGenerationRequest struct {
	ExpectedDraftVersion int
	IdempotencyKey       string
	IfMatchVersion       int
}

// StartGenerationResult distinguishes a fresh session from a replay/dedup.
type StartGenerationResult struct {
	Generation *GenerationView
	Revision   *RevisionSummary // present for 200 created:false replays
	Created    bool
}

// GenerationView is the session detail payload.
type GenerationView struct {
	GenerationID   string
	WorksetID      string
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

// Dispatcher is the background FIFO generation scheduler handle for main
// wiring (Start once at process startup, Stop at graceful shutdown).
type Dispatcher interface {
	Start()
	Stop()
}

// Service is the workset usecase contract.
type Service interface {
	// DispatcherHandle exposes the background generation dispatcher for main
	// wiring (start after startup interrupt; stop at graceful shutdown).
	DispatcherHandle() Dispatcher
	CreateWorkset(ctx context.Context, req CreateRequest) (*CreateResult, error)
	ListWorksets(ctx context.Context, q ListQuery) ([]*WorksetView, string, error)
	GetWorkset(ctx context.Context, id string) (*WorksetView, error)
	RenameWorkset(ctx context.Context, id string, req RenameRequest) (*WorksetView, error)
	GetDraft(ctx context.Context, id string) (*Draft, error)
	SaveDraft(ctx context.Context, id string, req SaveDraftRequest) (*WorksetView, error)
	StartGeneration(ctx context.Context, id string, req StartGenerationRequest) (*StartGenerationResult, error)
	GetGeneration(ctx context.Context, worksetID, generationID string) (*GenerationView, error)
	CancelGeneration(ctx context.Context, worksetID, generationID string) (*GenerationView, error)
	Subscribe(ctx context.Context, worksetID, generationID string, emit func(event string, data any) error) error
	ListRevisions(ctx context.Context, worksetID string, beforeIndex, limit int) (*RevisionListResult, error)
	GetRevision(ctx context.Context, worksetID, planID string) (*RevisionView, error)
}

// sanitizeGenerationForView hides unstable fields from view payloads.
func toGenerationView(g *sqlite.PlanGeneration) *GenerationView {
	out := &GenerationView{
		GenerationID:   g.GenerationID,
		WorksetID:      g.WorksetID,
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
	return out
}
