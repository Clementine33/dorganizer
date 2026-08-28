package plan

import (
	"context"
	"errors"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// Request is the input to the Plan operation. Exactly one branch must be set:
// Workflow (declarative reconcile_audio_outputs) or SingleAction (explicit
// delete/convert of selected source files).
type Request struct {
	// LibraryID is the owning library for web-created plans; gRPC/internal
	// callers may leave it empty, in which case the plan stays unattributed.
	LibraryID string
	// PlanningRoots are the resolved folder paths (workflow branch): each is
	// an independent planning root. HTTP resolves folder IDs to paths.
	PlanningRoots []string
	Workflow      *Workflow
	SingleAction  *SingleAction
}

// Workflow is a versioned linear workflow of steps.
type Workflow struct {
	SchemaVersion int
	Steps         []WorkflowStep
}

// WorkflowStep is one linear workflow step.
type WorkflowStep struct {
	StepType string
	Policy   PolicySource
}

// PolicySource addresses an immutable named/versioned preset or an inline
// policy. Both forms resolve to the same reconcile.Policy; overrides are
// never merged.
type PolicySource struct {
	Kind          string // "preset" | "inline"
	PresetName    string
	PresetVersion int
	InlinePolicy  *reconcile.Policy
}

// SingleAction is the retained explicit single-file path (independent from
// reconcile_audio_outputs, which manages whole components).
type SingleAction struct {
	Action       string // "delete" | "convert"
	SourceFiles  []string
	TargetFormat string // required for convert (e.g. ".mp3")
}

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

// Response is the output from the Plan operation.
type Response struct {
	PlanID        string
	SnapshotToken string
	RootPath      string
	Summary       Summary
	Steps         []StepResponse
	// Single-action branch payloads.
	Operations        []Operation
	Errors            []FolderError
	SuccessfulFolders []string
	PlanKind          string // "workflow" | "single_action"
}

// Operation describes a single planned operation (single-action branch).
type Operation struct {
	Type                   string
	SourcePath             string
	TargetPath             string
	DeleteTargetPath       string
	PreconditionPath       string
	PreconditionContentRev int
	PreconditionSize       int64
	PreconditionMtime      int64
}

// FolderError represents an error scoped to a folder.
type FolderError struct {
	FolderPath string
	Code       string
	Message    string
	Retryable  bool
}

// Error represents a plan-level error.
type Error struct {
	Kind    string
	Code    string
	Message string
	Cause   error
}

// Service defines the plan usecase contract.
type Service interface {
	Plan(ctx context.Context, req Request) (Response, error)
}

// ErrorKind values for plan.Error.Kind, used to map to gRPC status codes.
const (
	ErrKindInvalidArgument = "invalid_argument"
	ErrKindInternal        = "internal"
	ErrKindAlreadyExists   = "already_exists"
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
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
