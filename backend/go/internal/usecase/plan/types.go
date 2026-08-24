package plan

import (
	"context"
	"errors"
)

// Operation describes a single planned operation.
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

// Summary summarizes the plan result, owned by the usecase layer.
// The gRPC adapter maps these fields directly into the protobuf response.
type Summary struct {
	OperationCount  int
	ErrorCount      int
	TotalCount      int
	ActionableCount int
	SummaryReason   string
}

// Error represents a plan-level error.
type Error struct {
	Kind    string
	Code    string
	Message string
	Cause   error
}

// Request is the input to the Plan operation.
type Request struct {
	PlanType             string
	TargetFormat         string
	SourceFiles          []string
	FolderPath           string
	FolderPaths          []string
	PruneMatchedExcluded bool
}

// Response is the output from the Plan operation.
type Response struct {
	PlanID            string
	SnapshotToken     string
	Operations        []Operation
	Errors            []FolderError
	SuccessfulFolders []string
	RootPath          string
	Summary           Summary
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
