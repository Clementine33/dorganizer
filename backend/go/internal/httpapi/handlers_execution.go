package httpapi

import (
	"context"
	"net/http"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// executionStartRequest is the POST .../revisions/{planId}/executions payload.
// The client chooses only the delete mode; the file worklist is the frozen
// revision's.
type executionStartRequest struct {
	DeleteMode string `json:"delete_mode"`
}

// startExecution handles POST
// /api/v1/worksets/{id}/operations/{type}/revisions/{planId}/executions.
// If-Match (operation version) and Idempotency-Key are both required.
func (s *Server) startExecution(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	var req executionStartRequest
	if decodeErr := decodeJSONAllowEmpty(w, r, &req); decodeErr != nil {
		writeDecodeError(w, decodeErr, "invalid execution payload")
		return
	}
	idemKey := r.Header.Get("Idempotency-Key")
	if idemKey == "" {
		writeError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required")
		return
	}
	version, valid := ifMatchVersion(r)
	if !valid {
		writeError(
			w,
			http.StatusBadRequest,
			"VERSION_REQUIRED",
			"If-Match header with the operation version is required",
		)
		return
	}
	res, err := svc.StartExecution(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("type"),
		r.PathValue("planId"),
		worksetusecase.StartExecutionRequest{
			IfMatchVersion: version,
			IdempotencyKey: idemKey,
			DeleteMode:     req.DeleteMode,
		},
	)
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	status := http.StatusAccepted
	if !res.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, struct {
		Created   bool                         `json:"created"`
		Execution worksetusecase.ExecutionView `json:"execution"`
	}{Created: res.Created, Execution: *res.Execution})
}

// getExecution handles GET
// /api/v1/worksets/{id}/operations/{type}/executions/{executionId}.
func (s *Server) getExecution(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	view, err := svc.GetExecution(r.Context(), r.PathValue("id"), r.PathValue("type"), r.PathValue("executionId"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// cancelExecution handles POST
// /api/v1/worksets/{id}/operations/{type}/executions/{executionId}/cancel.
func (s *Server) cancelExecution(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	view, err := svc.CancelExecution(r.Context(), r.PathValue("id"), r.PathValue("type"), r.PathValue("executionId"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// executionEvents handles GET
// /api/v1/worksets/{id}/operations/{type}/executions/{executionId}/events
// (SSE): an authoritative snapshot first, then progress and a terminal event.
func (s *Server) executionEvents(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	streamSessionEvents(w, r, "EXECUTION_NOT_FOUND", func(ctx context.Context, emit func(string, any) error) error {
		return svc.SubscribeExecution(ctx, r.PathValue("id"), r.PathValue("type"), r.PathValue("executionId"), emit)
	})
}
