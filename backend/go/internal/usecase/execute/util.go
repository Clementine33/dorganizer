package execute

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// firstNonEmpty returns the first non-empty string from the given values.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// generateEventID generates a unique event identifier.
func generateEventID() string {
	return "evt-" + uuid.NewString()
}

// isSQLiteBusyLockedError checks if the error is a SQLite busy/locked error.
func isSQLiteBusyLockedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "sqlite_locked")
}

// newEvent creates a new Event with the given fields and auto-generated ID/timestamp.
func newEvent(eventType, stage, code, message string) Event {
	return Event{
		Type:      eventType,
		Stage:     stage,
		Code:      code,
		Message:   message,
		EventID:   generateEventID(),
		Timestamp: time.Now(),
	}
}

// nonFailedFolders returns the subset of folders that are NOT in the failed set.
func nonFailedFolders(allFolders []string, failed map[string]bool) []string {
	var out []string
	for _, f := range allFolders {
		if !failed[f] {
			out = append(out, f)
		}
	}
	return out
}
