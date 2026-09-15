package plan

import (
	"errors"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// Workflow/step constants for schema v1.
const (
	WorkflowSchemaVersion  = 1
	StepTypeReconcileAudio = "reconcile_audio_outputs"
)

// Workflow is a versioned linear workflow of steps. SchemaVersion 1 is the
// legacy steps-only draft; schema 2 adds member_settings (ADR 0003 batch
// draft). Workset drafts use schema 2.
type Workflow struct {
	SchemaVersion  int             `json:"schema_version"`
	Steps          []WorkflowStep  `json:"steps"`
	MemberSettings []MemberSetting `json:"member_settings,omitempty"`
}

// WorkflowStep is one linear workflow step. StepID is the stable identity of
// the step instance within a draft (never derived from step_type or order).
type WorkflowStep struct {
	StepID   string       `json:"step_id,omitempty"`
	StepType string       `json:"step_type"`
	Policy   PolicySource `json:"policy"`
}

// MemberSetting is one member's batch-draft record: exclusion flag plus
// full-replacement per-step config overrides. Members without a record
// participate and inherit every step default.
type MemberSetting struct {
	MemberID      string         `json:"member_id"`
	Excluded      bool           `json:"excluded"`
	StepOverrides []StepOverride `json:"step_overrides,omitempty"`
}

// StepOverride fully replaces one step's default config for one member.
// Config uses the same shape as the step's inline policy (reconcile.Policy).
type StepOverride struct {
	StepID string           `json:"step_id"`
	Config reconcile.Policy `json:"config"`
}

// PolicySource is the workflow step's policy payload. Only the inline form is
// valid: policies are complete snapshots, never references to global state.
type PolicySource struct {
	Kind         string            `json:"kind"` // "inline"
	InlinePolicy *reconcile.Policy `json:"policy,omitempty"`
}

// WorkflowSchemaVersionV2 is the extended batch-draft schema (ADR 0003): the
// steps above plus member_settings with exclusions and per-step overrides.
const WorkflowSchemaVersionV2 = 2

// Summary summarizes the plan result, owned by the usecase layer.
type Summary struct {
	OperationCount  int
	ErrorCount      int
	TotalCount      int
	ActionableCount int
	SummaryReason   string
}

// StepResponse is the reviewable outcome of one workflow step, reconstructed
// from persisted snapshots (never from live preset/classifier state).
type StepResponse struct {
	StepType   string
	StepIndex  int
	Status     string // ok | partially_blocked | blocked
	Policy     reconcile.Policy
	PolicyHash string
	Classifier reconcile.Classifier
	Components []reconcile.ComponentOutcome
	Summary    reconcile.StepSummary
}

// Response is the reconstructed review snapshot of one workflow plan.
type Response struct {
	PlanID        string
	SnapshotToken string
	RootPath      string
	Summary       Summary
	Steps         []StepResponse
	PlanKind      string // "workflow"
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
