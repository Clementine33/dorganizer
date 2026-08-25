package scan

import (
	"context"
	"errors"
)

// Request is the input to the Scan operation.
type Request struct {
	RootPath string
}

// Event is emitted during a scan. Type is one of started|progress|completed
// (plus error on failure).
type Event struct {
	Type         string
	Stage        string
	FilesScanned int
	DirsScanned  int
	ScanID       string
	Message      string
}

// Result is the output from the Scan operation.
type Result struct {
	ScanID       string
	RootPath     string
	FilesScanned int
}

// Service defines the scan usecase contract.
type Service interface {
	Scan(ctx context.Context, req Request, emit func(Event)) (Result, error)
}

// ErrorKind values for Error.Kind, used to map to gRPC status codes.
const (
	ErrKindInvalidArgument = "invalid_argument"
	ErrKindInternal        = "internal"
)

// Error represents a scan-level error.
type Error struct {
	Kind    string
	Code    string
	Message string
	Cause   error
}

// NewError creates a scan-level error with a kind that the adapter can map to gRPC.
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

// AsError extracts a *scan.Error from an error chain. Returns nil, false if not a scan.Error.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
