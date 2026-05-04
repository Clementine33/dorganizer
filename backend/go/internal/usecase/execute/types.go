package execute

import (
	"context"
	"errors"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Event represents a single execution event streamed to clients.
type Event struct {
	Type            string
	Stage           string
	Code            string
	Message         string
	PlanID          string
	RootPath        string
	FolderPath      string
	ItemSourcePath  string
	ItemTargetPath  string
	ProgressPercent int32
	EventID         string
	Timestamp       time.Time
}

// EventSink receives execution events during plan execution.
type EventSink interface {
	Emit(Event) error
}

// Request is the input to the Execute operation.
type Request struct {
	PlanID     string
	SoftDelete bool
}

// Result is the outcome of executing a plan.
type Result struct {
	PlanID       string
	RootPath     string
	Status       string
	ErrorCode    string
	ErrorMessage string
}

// Error represents an execute-level error.
type Error struct {
	Kind    string
	Code    string
	Message string
	Cause   error
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

// ErrorKind values for execute.Error.Kind, used to map to gRPC status codes.
const (
	ErrKindInvalidArgument    = "invalid_argument"
	ErrKindNotFound           = "not_found"
	ErrKindInternal           = "internal"
	ErrKindFailedPrecondition = "failed_precondition"
)

// NewError creates an execute-level error with a kind that the adapter can map to gRPC.
func NewError(kind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

// AsError extracts a *Error from an error chain. Returns nil, false if not an execute.Error.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Service defines the execute usecase contract.
type Service interface {
	Execute(ctx context.Context, req Request, sink EventSink) (Result, error)
}

// NewService creates a new execute service with real orchestration logic.
func NewService(repo *sqlite.Repository, configDir string) Service {
	return &serviceImpl{repo: repo, configDir: configDir}
}
