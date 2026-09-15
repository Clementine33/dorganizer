package workset

import (
	"context"
	"encoding/json"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Task is one kind of work a workset operation can carry. The workset module
// owns the operation lifecycle — identity, ownership, versioning, drafts,
// revisions, confirmations, planning and execution sessions — and a Task
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
	// (ADR 0004 §3, C13). It runs synchronously at enqueue time.
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
	// ordered execution units. Business block reasons are returned as
	// fail-forward values alongside a nil error.
	FreezeExecution(repo *sqlite.Repository, in RevisionFacts, deleteMode string) (FrozenExecution, []string, error)

	// RunUnit executes one frozen unit and reports its observed facts; the
	// generic side applies them to the report and the observed inventory.
	RunUnit(ctx context.Context, repo *sqlite.Repository, in UnitRunInput) (UnitResult, error)

	// RevisionMembers resolves each member's frozen effective configuration
	// from the revision's draft snapshot, with its inheritance sources.
	RevisionMembers(in RevisionFacts) ([]RevisionMemberFacts, error)

	// ReviewRevision rebuilds the reviewable payload of a persisted revision
	// from its stored rows.
	ReviewRevision(in RevisionFacts) (PlanReview, error)
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
type FrozenExecution struct {
	Units           []ExecutionUnit
	TotalOperations int
}

// UnitRunInput is one unit run's context. DeleteMode is the frozen session
// option as the wire and the session row carry it today; it folds into an
// opaque options payload when the execution request payload moves.
type UnitRunInput struct {
	WorksetRoot string
	DeleteMode  string
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
