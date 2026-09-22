package httpapi

import (
	"context"
	"net/http"

	"github.com/onsei/organizer/backend/internal/workset"
)

// generationViewResponse is the session detail payload.
type generationViewResponse struct {
	GenerationID   string `json:"generation_id"`
	WorksetID      string `json:"workset_id"`
	OperationType  string `json:"operation_type"`
	Status         string `json:"status"`
	TotalRoots     int    `json:"total_roots"`
	CompletedRoots int    `json:"completed_roots"`
	CurrentRoot    string `json:"current_root"`
	ErrorCount     int    `json:"error_count"`
	RevisionID     string `json:"revision_id"`
	ErrorCode      string `json:"error_code"`
	ErrorMessage   string `json:"error_message"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	CreatedAt      string `json:"created_at"`
}

func toGenerationViewResponse(g *workset.GenerationView) generationViewResponse {
	out := generationViewResponse{
		GenerationID:   g.GenerationID,
		WorksetID:      g.WorksetID,
		OperationType:  g.OperationType,
		Status:         g.Status,
		TotalRoots:     g.TotalRoots,
		CompletedRoots: g.CompletedRoots,
		CurrentRoot:    g.CurrentRoot,
		ErrorCount:     g.ErrorCount,
		RevisionID:     g.RevisionID,
		ErrorCode:      g.ErrorCode,
		ErrorMessage:   g.ErrorMessage,
	}
	if !g.StartedAt.IsZero() {
		out.StartedAt = g.StartedAt.UTC().Format(timeFormatJSON)
	}
	if !g.FinishedAt.IsZero() {
		out.FinishedAt = g.FinishedAt.UTC().Format(timeFormatJSON)
	}
	if !g.CreatedAt.IsZero() {
		out.CreatedAt = g.CreatedAt.UTC().Format(timeFormatJSON)
	}
	return out
}

// startGeneration handles POST .../operations/{type}/revisions. The body is
// empty: If-Match (operation version) is the whole precondition.
func (s *Server) startGeneration(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	var req struct{}
	if decodeErr := decodeJSONAllowEmpty(w, r, &req); decodeErr != nil {
		writeDecodeError(w, decodeErr, "invalid generation payload")
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
	res, err := svc.StartGeneration(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("type"),
		workset.StartGenerationRequest{
			IfMatchVersion: version,
			IdempotencyKey: idemKey,
		},
	)
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	if !res.Created {
		// Two created:false shapes: an unchanged-input replay carries the
		// current revision; an idempotent-key replay carries the existing
		// generation instead. Encoding both keeps the nil-revision replay
		// from dereferencing a nil pointer.
		if res.Revision != nil {
			writeJSON(w, http.StatusOK, struct {
				Created  bool                     `json:"created"`
				Revision *currentRevisionResponse `json:"revision"`
			}{Created: false, Revision: toCurrentRevisionResponse(res.Revision)})
			return
		}
		if res.Generation != nil {
			writeJSON(w, http.StatusAccepted, struct {
				Created    bool                   `json:"created"`
				Generation generationViewResponse `json:"generation"`
			}{Created: false, Generation: toGenerationViewResponse(res.Generation)})
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "generation replay returned no result")
		return
	}
	writeJSON(w, http.StatusAccepted, struct {
		Created    bool                   `json:"created"`
		Generation generationViewResponse `json:"generation"`
	}{Created: true, Generation: toGenerationViewResponse(res.Generation)})
}

// getGeneration handles GET .../operations/{type}/planning-sessions/{genId}.
func (s *Server) getGeneration(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	g, err := svc.GetGeneration(r.Context(), r.PathValue("id"), r.PathValue("type"), r.PathValue("genId"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toGenerationViewResponse(g))
}

// generationEvents handles GET .../planning-sessions/{genId}/events (SSE).
func (s *Server) generationEvents(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	streamSessionEvents(w, r, "GENERATION_NOT_FOUND", func(ctx context.Context, emit func(string, any) error) error {
		return svc.Subscribe(ctx, r.PathValue("id"), r.PathValue("type"), r.PathValue("genId"), emit)
	})
}

// cancelGeneration handles POST .../planning-sessions/{genId}/cancel.
func (s *Server) cancelGeneration(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	g, err := svc.CancelGeneration(r.Context(), r.PathValue("id"), r.PathValue("type"), r.PathValue("genId"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toGenerationViewResponse(g))
}
