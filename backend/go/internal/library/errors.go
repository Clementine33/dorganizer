package library

import "errors"

var (
	// ErrLibraryExists is returned when a library root path already exists.
	ErrLibraryExists = errors.New("library already exists")
	// ErrLibraryNotFound is returned when a library cannot be found.
	ErrLibraryNotFound = errors.New("library not found")
	// ErrLibraryHasWorksets is returned when a library root change is refused
	// because a processing record is linked to the library: the record's member
	// paths are relative to the root, so rebinding it would unbind them all.
	ErrLibraryHasWorksets = errors.New("library has worksets")
	// ErrGenerationInProgress is returned when an owned record has a queued or
	// running planning session, which refuses a deletion and an execution start
	// alike: a session may not run against a library that is going away, and an
	// execution may not start while the plan it would run is still being made.
	ErrGenerationInProgress = errors.New("generation in progress")
	// ErrExecutionInProgress is returned when an owned record has a queued or
	// running execution, which refuses a deletion and a second execution start.
	ErrExecutionInProgress = errors.New("execution in progress")
)
