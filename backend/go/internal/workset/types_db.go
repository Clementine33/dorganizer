package workset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// Workset is the persisted current processing record of one
// (library, operation) pair. Version is the metadata concurrency counter: only
// renames advance it. Draft and generation state live on the operation
// (workset_operations).
type Workset struct {
	ID            string
	Title         string
	LibraryID     string // "" when orphaned (library row vanished outside the delete path)
	OperationType string
	RootPath      string // snapshot: library root at creation
	RootPathKey   string // snapshot: canonical root identity at creation
	Version       int
	// CreationIdemKey is the key that created this record; "" when not set.
	CreationIdemKey string
	// CreationRequestHash is the canonical hash of the creation request that
	// owns the key: a replay with a different request is a conflict.
	CreationRequestHash string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// WorksetMember is one ordered member directory. MemberID is the stable
// identity (assigned at creation, never regenerated); RelPath is the durable
// normalized library-relative path the member is addressed by — a rescan
// cannot change it; MemberIndex is ordering only. FolderPath and FolderName
// are display snapshots of the same directory.
type WorksetMember struct {
	WorksetID   string
	MemberID    string
	MemberIndex int
	RelPath     string
	FolderPath  string
	FolderName  string
}

// ErrWorksetNotFound is returned when a workset cannot be found.
var ErrWorksetNotFound = errors.New("workset not found")

// ErrWorksetIdemConflict is returned when a workset create collides with an
// existing creation idempotency key.
var ErrWorksetIdemConflict = errors.New("workset idempotency key conflict")

// ErrVersionConflict is returned when an If-Match version precondition fails
// on a workset or operation mutation.
var ErrVersionConflict = errors.New("version conflict")

// ErrOperationNotFound is returned when a workset operation row is absent.
var ErrOperationNotFound = errors.New("operation not found")

// ErrCurrentRecordChanged is returned when a replace finds a current record
// other than the one the caller expected: another client already replaced it
// (or created one), and the newer record is never overwritten.
var ErrCurrentRecordChanged = errors.New("current record changed")

// ErrRecordBusy is returned when the record that would be replaced still has a
// queued or running planning session or execution.
var ErrRecordBusy = errors.New("record has a queued or running session")

// PlanStepRecord is one persisted payload row of a plan (task-owned content:
// the conversion task stores its resolved policy/classifier snapshots here).
type PlanStepRecord struct {
	StepIndex           int
	StepType            string
	Status              string
	PolicySchemaVersion int
	PolicyJSON          string
	PolicyHash          string
	ClassifierTags      string // canonical normalized tags snapshot, "\x00"-joined
	ClassifierHash      string
	StepSummaryJSON     string
}

// PlanRootRecord is a persisted planning root with its inventory
// fingerprint. RootStatus is "ok" for planned roots and "missing" for member
// folders whose subtree no longer exists; RootErrorCode/Message carry the
// stable machine outcome for missing roots (SOURCE_MISSING).
type PlanRootRecord struct {
	RootIndex            int
	RootPath             string
	RootIdentity         string
	InventoryFingerprint string
	EntryCount           int
	RootStatus           string
	RootErrorCode        string
	RootErrorMessage     string
}

// PlanComponentRecord is one persisted unit outcome of a plan (task-owned
// content: the conversion task stores its component outcomes here).
type PlanComponentRecord struct {
	StepIndex      int
	ComponentIndex int
	ComponentID    string
	RootIndex      int
	Partition      string
	Status         string
	ReasonCode     string
	OutcomeJSON    string
}

// PlanDetail is the full persisted review payload of one plan.
type PlanDetail struct {
	Plan       Plan
	Steps      []PlanStepRecord
	Roots      []PlanRootRecord
	Components []PlanComponentRecord
}

// ErrPlanNotFound is returned when a plan cannot be found.
var ErrPlanNotFound = errors.New("plan not found")

// Planning session statuses.
const (
	GenStatusQueued      = "queued"
	GenStatusRunning     = "running"
	GenStatusCompleted   = "completed"
	GenStatusFailed      = "failed"
	GenStatusCanceled    = "canceled"
	GenStatusInterrupted = "interrupted"
)

// PlanGeneration is one persisted planning session, owned by exactly one
// operation of one workset.
type PlanGeneration struct {
	GenerationID         string
	WorksetID            string
	OperationType        string
	Status               string
	IdempotencyKey       string
	RequestHash          string
	ExpectedDraftVersion int
	RequestJSON          string
	TotalRoots           int
	CompletedRoots       int
	CurrentRoot          string
	ErrorCount           int
	CancelRequested      bool
	RevisionID           string // "" until completed
	ErrorCode            string
	ErrorMessage         string
	StartedAt            time.Time
	FinishedAt           time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// ErrGenerationNotFound is returned when a planning session cannot be found.
var ErrGenerationNotFound = errors.New("generation not found")

// ErrGenerationIdemConflict is returned when a generation start collides with
// an active/terminal idempotency key.
var ErrGenerationIdemConflict = errors.New("generation idempotency key conflict")

// Execution session statuses. Terminal statuses never regress.
const (
	ExecStatusQueued      = "queued"
	ExecStatusRunning     = "running"
	ExecStatusSucceeded   = "succeeded"
	ExecStatusFailed      = "failed"
	ExecStatusCanceled    = "canceled"
	ExecStatusInterrupted = "interrupted"
)

// PlanExecution is one persisted execution session: the durable record of one
// revision being executed against the disk. RequestJSON freezes the ordered
// execution units (generic identity plus the task's opaque payload) and the
// frozen session options at creation time. The per-component facts live in
// execution_component_results, one row per component: this row carries the
// session state, the counters derived from those rows, and where the run is.
type PlanExecution struct {
	ExecutionID              string
	WorksetID                string
	OperationType            string
	PlanID                   string
	Status                   string
	IdempotencyKey           string
	RequestHash              string
	ExpectedOperationVersion int
	RequestJSON              string
	TotalComponents          int
	CompletedComponents      int
	TotalOperations          int
	CompletedOperations      int
	CurrentRoot              string
	CurrentComponentID       string
	CurrentComponentIndex    int
	CurrentPhase             string
	CancelRequested          bool
	ErrorCode                string
	ErrorMessage             string
	StartedAt                time.Time
	FinishedAt               time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// ExecutionGuards are the persisted facts an execution start must re-check
// inside its own write transaction: the read-only eligibility gates cannot
// freeze the operation version or the draft hash on their own.
type ExecutionGuards struct {
	ExpectedOperationVersion int
	ExpectedCurrentRevision  string
	ExpectedDraftHash        string
}

// ExecutionPosition is the position of a running session: the component it is
// working on, or the empty position once the run is over. The counters are
// derived from the component result rows inside the same transaction, so they
// can never drift from the facts.
type ExecutionPosition struct {
	CurrentRoot           string
	CurrentComponentID    string
	CurrentComponentIndex int
	CurrentPhase          string
}

// ExecutionComponentResult is one component's observed result. The frozen
// identity of the component (id, root path, partition, operation count) is not
// repeated here: it lives in the session's frozen worklist and is joined back
// on read.
type ExecutionComponentResult struct {
	ComponentIndex      int
	Status              string
	CompletedOperations int
	ResultJSON          string
	UpdatedAt           time.Time
}

// ErrExecutionNotFound is returned when an execution session cannot be found.
var ErrExecutionNotFound = errors.New("execution not found")

// ErrExecutionIdemConflict is returned when an execution start collides with an
// existing idempotency key of the same operation.
var ErrExecutionIdemConflict = errors.New("execution idempotency key conflict")

// ErrDraftChanged is returned when a guarded execution start observes a draft
// hash that no longer matches the revision's frozen one.
var ErrDraftChanged = errors.New("draft changed")

// ErrWorksetOrphaned is returned when a guarded execution start observes a
// workset whose library is gone.
var ErrWorksetOrphaned = errors.New("workset is orphaned")

// ErrExecutionNotEligible is the residual outcome of a guarded execution start
// whose guard predicates refused the insert without a re-readable cause.
var ErrExecutionNotEligible = errors.New("execution is not eligible")

// Operation is one independent Workset Operation (ADR 0001 §1) keyed by
// (workset, type). Version is the operation concurrency counter advanced by
// draft saves and revision publication; CurrentRevisionID is "" until the
// first successful generation publishes.
type Operation struct {
	WorksetID         string
	OperationType     string
	Version           int
	CurrentRevisionID string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// OperationDraft is the mutable sparse draft of one operation.
type OperationDraft struct {
	WorksetID     string
	OperationType string
	SchemaVersion int
	DraftJSON     string
	DraftHash     string
	UpdatedAt     time.Time
}

// OperationRevision is the immutable revision association row. ExcludedScope
// (NUL-joined member_ids) and DraftSnapshot (frozen sparse draft JSON) are part
// of the immutable snapshot (ADR 0001 §2).
type OperationRevision struct {
	PlanID           string
	WorksetID        string
	OperationType    string
	RevisionIndex    int
	DraftHash        string
	MemberHash       string
	OperationVersion int
	ExcludedScope    string
	DraftSnapshot    string
	CreatedAt        time.Time
}

// OperationRevisionPersist bundles the atomic completion payload: the plan
// plan snapshot inserts, the revision association, the operation's
// current-revision promotion and the generation completion. DraftHash and
// MemberHash are the canonical frozen inputs for dedup and needs_planning
// derivation.
type OperationRevisionPersist struct {
	PlanID        string
	RootPath      string
	SnapshotToken string
	LibraryID     string
	DraftHash     string
	MemberHash    string
	// OperationVersion is the operation version observed at enqueue time,
	// frozen on the revision for audit.
	OperationVersion int
	// ExcludedScope is the NUL-joined set of member_ids excluded from planning
	// for this revision; empty when nothing was excluded.
	ExcludedScope string
	// DraftSnapshot is the frozen sparse draft JSON: the review UI resolves the
	// per-member effective configs and inheritance sources from it.
	DraftSnapshot string
	// TaskSchemaVersion is the plan payload's schema version, stored on the
	// plan row so the review envelope can name it.
	TaskSchemaVersion int
	Steps             []PlanStepRecord
	Roots             []PlanRootRecord
	Components        []PlanComponentRecord
}

// CanonicalJSONHash hashes JSON canonically: objects are recursively
// key-sorted, so key order and whitespace never change the hash; array order
// is preserved because step/member order is semantic. Unparseable input
// falls back to a raw-byte hash. Draft hashing and the draft-hash migration
// must use this same function so backfilled and freshly saved hashes agree.
func CanonicalJSONHash(b []byte) string {
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	canonical, err := json.Marshal(canonicalizeValue(v))
	if err != nil {
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// Plan represents a persisted plan.
type Plan struct {
	PlanID            string
	RootPath          string
	ScanRootPath      string
	LibraryID         string // nullable: owning library when known
	SnapshotToken     string
	Status            string // ready, executed, stale, canceled, failed
	TaskKind          string // the Task that owns the payload
	TaskSchemaVersion int    // >0 once the payload schema is known
	CreatedAt         time.Time
}

// ErrRevisionNotFound is returned when an operation revision cannot be found.
var ErrRevisionNotFound = errors.New("revision not found")

// canonicalizeValue recursively normalizes for canonical hashing. Map
// iteration + encoding/json map marshaling yields sorted keys.
func canonicalizeValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = canonicalizeValue(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = canonicalizeValue(t[i])
		}
		return t
	default:
		return v
	}
}
