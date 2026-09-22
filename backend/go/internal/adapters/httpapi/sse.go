package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// SSEWriter streams server-sent events to the response. It wraps an
// http.ResponseWriter and an http.Flusher so every Send is pushed to the
// client immediately, keeping SSE-over-POST synchronous with the request
// lifecycle.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// newSSEWriter creates an SSEWriter for a response if the writer supports
// streaming.
func newSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	current := w
	for {
		if flusher, ok := current.(http.Flusher); ok {
			return &SSEWriter{w: w, flusher: flusher}, nil
		}
		unwrapper, ok := current.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil, fmt.Errorf("response writer does not support streaming")
		}
		current = unwrapper.Unwrap()
	}
}

// Send writes one SSE event with the given name and JSON-encoded data, then
// flushes so the client receives it immediately.
func (s *SSEWriter) Send(event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}
