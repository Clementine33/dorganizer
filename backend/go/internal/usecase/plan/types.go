package plan

import (
	"errors"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// RootInput is one planning root with the effective conversion policy it is
// planned with. The policy travels with its root — there is no side channel.
type RootInput struct {
	Path   string
	Policy reconcile.Policy
}

// Input is the frozen input of one planning run, in request order.
type Input struct {
	// Policy is the run's baseline policy (the draft's common conversion
	// config). It is persisted with the revision; every root still plans with
	// its own effective policy.
	Policy reconcile.Policy
	// Roots are the planning roots in request order; at least one is required.
	Roots []RootInput
	// MarkMissingRoots marks a root absent from the scanned inventory as
	// root_status=missing with SOURCE_MISSING and counts it into the summary
	// as blocked/error. Workset generation enables this.
	MarkMissingRoots bool
	// Progress is invoked after each root in request order (best effort; nil
	// skips it). CompletedRoots is 1-based at call time.
	Progress func(Progress)
}

// Progress is a root-level progress report for async generation. It carries
// root counts only — a fake percentage is never derived here.
type Progress struct {
	CompletedRoots int
	TotalRoots     int
	CurrentRoot    string
}

// RootFacts is one root's frozen input facts: what the plan was made from.
type RootFacts struct {
	Index                int
	Path                 string
	Identity             string
	InventoryFingerprint string
	Count                int
	Status               string // ok | missing
	ErrorCode            string
	ErrorMessage         string
}

// PlannedComponent is one planned component: its revision-wide index, the
// owning root's index, and the reconcile outcome.
type PlannedComponent struct {
	Index     int
	RootIndex int
	Outcome   reconcile.ComponentOutcome
}

// Snapshot is the frozen outcome of one planning run: the resolved policy
// facts, the per-root input facts and the per-component outcomes. It names no
// storage types; callers persist it through their own adapter.
type Snapshot struct {
	RootPath       string // display scope (roots joined with " + ")
	Policy         reconcile.Policy
	PolicyJSON     string
	PolicyHash     string
	ClassifierTags string // normalized NUL-joined tag snapshot
	Classifier     reconcile.Classifier
	Summary        reconcile.StepSummary
	Status         string // ok | partially_blocked | blocked
	Roots          []RootFacts
	Components     []PlannedComponent
}

// Error represents a plan-level error.
type Error struct {
	Kind    string
	Code    string
	Message string
	Cause   error
}

// ErrorKind values for Error.Kind, used to map to gRPC status codes.
const (
	ErrKindInvalidArgument = "invalid_argument"
	ErrKindInternal        = "internal"
)

// NewError creates a plan-level error with a kind that the adapter can map to gRPC.
func NewError(kind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Kind + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Kind + ": " + e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}

// AsError extracts a *plan.Error from an error chain. Returns nil, false if not a plan.Error.
func AsError(err error) (*Error, bool) {
	if e, ok := errors.AsType[*Error](err); ok {
		return e, true
	}
	return nil, false
}
