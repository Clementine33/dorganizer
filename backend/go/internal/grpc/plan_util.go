package grpc

import (
	"github.com/google/uuid"
)

// generateEventID generates a unique event identifier.
func generateEventID() string {
	return "evt-" + uuid.NewString()
}
