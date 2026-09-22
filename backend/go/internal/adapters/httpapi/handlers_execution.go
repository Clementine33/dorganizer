package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/onsei/organizer/backend/internal/workset"
)

// startExecution handles POST
// /api/v1/worksets/{id}/operations/{type}/revisions/{planId}/executions.
// If-Match (operation version) and Idempotency-Key are both required. The body
// is optional and carries one thing: `folder_paths` scopes the session to those
// members of the record, and an absent or empty list runs the whole revision.
// The worklist and the session options stay the frozen revision's.
func (s *Server) startExecution(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
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
	var body struct {
		FolderPaths []string `json:"folder_paths"`
	}
	if decodeErr := decodeJSONAllowEmpty(w, r, &body); decodeErr != nil {
		writeDecodeError(w, decodeErr, "invalid execution request body")
		return
	}
	res, err := svc.StartExecution(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("type"),
		r.PathValue("planId"),
		workset.StartExecutionRequest{
			IfMatchVersion: version,
			IdempotencyKey: idemKey,
			FolderPaths:    body.FolderPaths,
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
		Created   bool                  `json:"created"`
		Execution workset.ExecutionView `json:"execution"`
	}{Created: res.Created, Execution: *res.Execution})
}

// getExecution handles GET
// /api/v1/worksets/{id}/operations/{type}/executions/{executionId}. The
// optional components_from/components_limit parameters page a large run: the
// component list is ordered by component index, so a client walks it from the
// index after the last one it holds. Without them the response carries every
// component, as it always has.
func (s *Server) getExecution(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	page, err := executionPageOf(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_PAGE", err.Error())
		return
	}
	view, err := svc.GetExecution(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("type"),
		r.PathValue("executionId"),
		page,
	)
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// executionPageOf reads the optional paging parameters of a detail request.
func executionPageOf(r *http.Request) (workset.ExecutionPage, error) {
	var page workset.ExecutionPage
	for name, target := range map[string]*int{
		"components_from":  &page.FromIndex,
		"components_limit": &page.Limit,
	} {
		raw := r.URL.Query().Get(name)
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return workset.ExecutionPage{}, fmt.Errorf("%s must be a non-negative integer", name)
		}
		*target = value
	}
	return page, nil
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
