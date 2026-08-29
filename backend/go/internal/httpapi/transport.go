package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// maxRequestBodyBytes caps JSON request bodies: these are small metadata
// payloads, and an unbounded reader would let a client drive memory/CPU usage.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// payloadError carries a status/code for request-body rejections so handlers
// can surface oversized vs malformed payloads distinctly.
type payloadError struct {
	status  int
	code    string
	message string
}

func (e *payloadError) Error() string { return e.message }

func asPayloadError(err error) (*payloadError, bool) {
	var pe *payloadError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// writeDecodeError maps a decode failure to its envelope. Strict validation
// errors carry their own status; anything unexpected stays a 400.
func writeDecodeError(w http.ResponseWriter, err error, fallback string) {
	if pe, ok := asPayloadError(err); ok {
		writeError(w, pe.status, pe.code, pe.message)
		return
	}
	writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", fallback)
}

// decodeJSON decodes exactly one JSON object from the request body, rejecting
// unknown fields, trailing content, and bodies larger than 1 MiB.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	body := http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return classifyDecodeError(err)
	}
	// Require EOF after the single value: reject trailing objects/garbage.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return &payloadError{http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON payload"}
	}
	return nil
}

// decodeJSONAllowEmpty is decodeJSON for endpoints where the body is optional:
// an empty body decodes to the zero-value destination. Malformed non-empty
// bodies stay invalid.
func decodeJSONAllowEmpty(w http.ResponseWriter, r *http.Request, dst any) error {
	body := http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // empty body ≡ zero-value request
		}
		return classifyDecodeError(err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return &payloadError{http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON payload"}
	}
	return nil
}

func classifyDecodeError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return &payloadError{http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body too large"}
	}
	return &payloadError{http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON payload"}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}
