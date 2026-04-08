package errors

// DomainErrorCode represents domain-specific error codes
type DomainErrorCode int

const (
	// TOOL_NOT_FOUND indicates the required tool (ffmpeg, etc) is not available
	TOOL_NOT_FOUND DomainErrorCode = iota + 1
	// FILE_LOCKED indicates the file is locked by another process
	FILE_LOCKED
	// PLAN_STALE indicates the plan is stale and needs to be regenerated
	PLAN_STALE
)

// String returns the string representation of the error code
func (c DomainErrorCode) String() string {
	switch c {
	case TOOL_NOT_FOUND:
		return "TOOL_NOT_FOUND"
	case FILE_LOCKED:
		return "FILE_LOCKED"
	case PLAN_STALE:
		return "PLAN_STALE"
	default:
		return "UNKNOWN"
	}
}
