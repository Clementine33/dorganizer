package execute

import (
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
