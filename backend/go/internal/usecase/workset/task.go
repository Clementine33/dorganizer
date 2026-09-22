package workset

import (
	"context"
	"encoding/json"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
)

// Task is one kind of work a workset operation can carry. The workset module
// owns the operation lifecycle — identity, ownership, versioning, drafts,
// revisions, planning and execution sessions — and a Task
// supplies the business content behind it.
//
// Contract (binding on every Task implementation):
//
//  1. Admission. The generic side keeps ownership, version, transaction,
//     session and idempotency constraints; the Task reports the business
//     health of a frozen revision (input facts that moved, units that cannot
//     run) through EvaluateRevision.
//  2. Payload schema versions. A payload schema version the Task does not
//     know is rejected at every entry point (read, draft write, confirm,
//     execute) with a stable error code — never half-read.
//  3. Options. Session options are frozen with the session and take part in
//     the idempotency request hash.
//  4. Progress and cancellation. A Task reports progress at its own unit
//     boundaries, observes the cancellation of the context it is handed, and
//     never moves a terminal session status backwards.
//
// Errors crossing the seam use the workset error envelope (NewError / AsError)
// so codes and messages reach the HTTP layer unchanged.
type Task interface {
	// Kind is the stable operation-type identifier: the {type} path segment
	// and the value stored on operations and revisions.
	Kind() string

	// SeedDraft returns the raw draft document a newly created operation
	// starts from, its canonical content hash, and the document schema
	// version.
	SeedDraft() (raw []byte, hash string, schemaVersion int)

	// ValidateDraft checks one submitted draft document against the workset's
	// members. Only malformed documents are rejected here; business
	// completeness is enforced at planning time.
	ValidateDraft(raw []byte, members []*sqlite.WorksetMember) error

	// NormalizeDraft returns the canonical sparse form of a validated draft
	// document, its content hash, and the document schema version.
	NormalizeDraft(
		raw []byte,
		members []*sqlite.WorksetMember,
	) (canonical []byte, hash string, schemaVersion int, err error)

	// ValidateSessionInput checks that a frozen session input can produce a
	// plan: the executable business validation behind the generation boundary
	// (ADR 0006 §1). It runs synchronously at enqueue time.
	ValidateSessionInput(rawDraft []byte, members []*sqlite.WorksetMember) error

	// PlanSession plans one generation session from its frozen input and
	// returns the snapshot the generic side persists as a revision. Session
	// claiming, progress persistence and terminal transitions stay generic.
	PlanSession(ctx context.Context, repo *sqlite.Repository, in PlanSessionInput) (*PlanSnapshot, error)

	// EvaluateRevision reports the business health of one frozen revision:
	// whether its live input facts still match the frozen ones, and whether
	// any stored unit is marked as unable to run.
	EvaluateRevision(repo *sqlite.Repository, in RevisionFacts) (RevisionHealth, error)

	// FreezeExecution validates that a revision can run and freezes its
	// ordered execution units plus the task's opaque execution options.
	// Business block reasons are returned as fail-forward values alongside a
	// nil error.
	FreezeExecution(repo *sqlite.Repository, in RevisionFacts) (FrozenExecution, []string, error)

	// PrepareUnit prechecks one frozen unit and returns it ready to encode;
	// nothing is written yet. A failure of the unit's own work is returned as a
	// workset error carrying its stage and code, so the session can report the
	// unit — never a neighbour — as failed.
	PrepareUnit(ctx context.Context, repo *sqlite.Repository, in UnitRunInput) (PreparedUnit, error)

	// RevisionMembers resolves each member's frozen effective configuration
	// from the revision's draft snapshot, with its inheritance sources.
	RevisionMembers(in RevisionFacts) ([]RevisionMemberFacts, error)

	// ReviewRevision rebuilds the reviewable payload of a persisted revision
	// from its stored rows.
	ReviewRevision(in RevisionFacts) (PlanReview, error)
}

// PreparedUnit is one frozen unit that passed its business precheck and wrote
// nothing yet. The session drives it in three steps: every encode task is
// delivered exactly once — possibly concurrently with other units' tasks —
// then either Commit lands them in frozen order or Discard cleans them because
// the session stopped first.
type PreparedUnit interface {
	// EncodeTasks is how many encode tasks Commit expects.
	EncodeTasks() int
	// EncodeTask materializes one staged output. Indices may run concurrently
	// and are each delivered exactly once. A task that stops because the
	// session stopped returns an error wrapping context.Canceled — the session
	// never reads that as work failing — and a failure of the task's own work
	// is a workset error carrying its stage and code.
	EncodeTask(ctx context.Context, index int) error
	// Commit lands the encoded outputs in frozen order and reports the unit's
	// observed facts. It is called once, after every EncodeTask completed.
	Commit(ctx context.Context) (UnitResult, error)
	// Discard cleans the staged outputs of a unit that will not commit and
	// reports the stopped unit's facts; a nil cause reports a cancellation.
	Discard(cause error) UnitResult
}

// PlanSessionInput is the frozen input of one generation session.
type PlanSessionInput struct {
	RawDraft []byte
	Members  []*sqlite.WorksetMember
	Progress func(PlanProgress)
}

// PlanProgress is a root-level progress report for async generation. It
// carries root counts only — a fake percentage is never derived here.
type PlanProgress struct {
	CompletedRoots int
	TotalRoots     int
	CurrentRoot    string
}

// PlanRootFacts is one root's frozen input facts: what the plan was made from.
type PlanRootFacts struct {
	Index                int
	Path                 string
	Identity             string
	InventoryFingerprint string
	Count                int
	Status               string // ok | missing
	ErrorCode            string
	ErrorMessage         string
}

// PlannedUnit is one planned unit of a revision: the generic placement facts
// plus the task's opaque payload (the persisted outcome JSON).
type PlannedUnit struct {
	Index      int
	RootIndex  int
	ID         string
	Partition  string
	Status     string
	ReasonCode string
	Payload    json.RawMessage
}

// PlanSnapshot is what one planning pass freezes into a revision: generic
// input facts plus the task's opaque payloads.
type PlanSnapshot struct {
	RootPath             string
	Payload              json.RawMessage // plan-level payload (persisted as the step policy JSON)
	PayloadHash          string
	PayloadSchemaVersion int
	Tags                 string // task tag snapshot (persisted NUL-joined)
	TagsHash             string
	Summary              json.RawMessage // persisted as the step summary JSON
	Status               string
	ExcludedScope        string
	Roots                []PlanRootFacts
	Units                []PlannedUnit
}

// PlanSummary is the generic plan-summary fact set. Every task's plan summary
// JSON carries at least these keys.
type PlanSummary struct {
	ComponentCount int    `json:"component_count"`
	BlockedCount   int    `json:"blocked_count"`
	OperationCount int    `json:"operation_count"`
	ErrorCount     int    `json:"error_count"`
	UnmetTargets   int    `json:"unmet_targets,omitempty"`
	SummaryReason  string `json:"summary_reason"`
}

// RevisionFacts is the persisted revision data a task evaluates, executes or
// reviews. Callers fill the fields the operation needs.
type RevisionFacts struct {
	DraftSnapshot []byte
	Members       []*sqlite.WorksetMember
	Detail        *sqlite.PlanDetail
}

// RevisionHealth is a task's business evaluation of one frozen revision.
type RevisionHealth struct {
	InputMoved   bool
	UnitsBlocked bool
	// StaleRoots are the root indexes whose live input facts no longer match
	// the frozen ones.
	StaleRoots []int
}

// ExecutionUnit is one frozen execution unit: the generic identity the
// progress and report rows carry, plus the task's opaque payload.
type ExecutionUnit struct {
	Index      int             `json:"index"`
	RootIndex  int             `json:"root_index"`
	ID         string          `json:"unit_id"`
	RootPath   string          `json:"root_path"`
	Partition  string          `json:"partition"`
	Operations int             `json:"operations"`
	Payload    json.RawMessage `json:"payload"`
}

// FrozenExecution is a task's frozen plan for one execution session.
// Options is the task-owned session configuration (conversion: the delete
// mode its draft declared); the generic side stores and forwards it untouched.
type FrozenExecution struct {
	Units           []ExecutionUnit
	TotalOperations int
	Options         json.RawMessage
}

// UnitRunInput is one unit run's context. Options is the frozen session
// options payload exactly as the task froze them at the start of the session.
type UnitRunInput struct {
	WorksetRoot string
	Options     json.RawMessage
	Unit        ExecutionUnit
	// Outcome is the unit's frozen plan payload, as stored in the revision.
	Outcome json.RawMessage
}

// UnitResult is one unit run's observed facts.
type UnitResult struct {
	Canceled       bool
	Stage          string
	ErrorCode      string
	ErrorMessage   string
	Committed      []string
	Removed        []string
	Remaining      []string
	Recovery       []string
	InventoryError string
	// InventoryRemoved are entry paths whose rows the generic side drops, and
	// InventoryRefreshed the on-disk facts it re-reads into the inventory.
	InventoryRemoved   []string
	InventoryRefreshed []sqlite.InventoryFile
	// Generated are the generation credentials of the outputs this unit
	// committed: what the next plan reads to accept them without re-encoding.
	Generated []sqlite.GenerationRecord
}

// RevisionMemberFacts is one member's frozen effective configuration.
type RevisionMemberFacts struct {
	MemberID   string
	FolderPath string
	Excluded   bool
	Payload    json.RawMessage // task payload: the effective config
	Sources    map[string]string
}

// PlanReview is the reviewable payload of one persisted revision.
type PlanReview struct {
	Payload json.RawMessage // plan-level payload (policy JSON)
	Tags    []string
	TagHash string
	Units   []UnitReview
}

// UnitReview is one persisted unit of a revision, re-encoded for review.
type UnitReview struct {
	Payload    json.RawMessage
	Operations int
}
