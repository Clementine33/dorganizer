package httpapi

import (
	"context"
	"errors"
	"net/http"

	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// ==================== DTOs ====================

// worksetCreateRequest is the POST /api/v1/worksets payload.
type worksetCreateRequest struct {
	LibraryID string   `json:"library_id"`
	Title     string   `json:"title"`
	FolderIDs []string `json:"folder_ids"`
}

// worksetPatchRequest is the PATCH /api/v1/worksets/{id} payload.
type worksetPatchRequest struct {
	Title string `json:"title"`
}

// libraryRefResponse is the owning-library snapshot.
type libraryRefResponse struct {
	LibraryID string `json:"library_id"`
	Name      string `json:"name"`
	RootPath  string `json:"root_path"`
}

// memberResponse is one album-folder member. Participation and overrides are
// operation state, not member state.
type memberResponse struct {
	MemberID   string `json:"member_id"`
	FolderID   string `json:"folder_id"`
	FolderPath string `json:"folder_path"`
	FolderName string `json:"folder_name"`
	RelPath    string `json:"rel_path"`
}

// revisionCountsResponse carries the independent plan facts of a revision.
type revisionCountsResponse struct {
	Members      int `json:"members"`
	Changed      int `json:"changed"`
	UnmetTargets int `json:"unmet_targets"`
	Blocked      int `json:"blocked"`
	Unchanged    int `json:"unchanged"`
}

// currentRevisionResponse is the compact immutable conclusion.
type currentRevisionResponse struct {
	PlanID          string                 `json:"plan_id"`
	RevisionIndex   int                    `json:"revision_index"`
	CreatedAt       string                 `json:"created_at"`
	Status          string                 `json:"status"`
	SummaryReason   string                 `json:"summary_reason"`
	Counts          revisionCountsResponse `json:"counts"`
	ValidationState string                 `json:"validation_state"`
	Stale           *bool                  `json:"stale"`
}

// generationProgressResponse is root-level progress of an active session.
type generationProgressResponse struct {
	GenerationID   string `json:"generation_id"`
	Status         string `json:"status"`
	TotalRoots     int    `json:"total_roots"`
	CompletedRoots int    `json:"completed_roots"`
	CurrentRoot    string `json:"current_root"`
	ErrorCount     int    `json:"error_count"`
}

// generationSummaryResponse is the terminal session summary.
type generationSummaryResponse struct {
	GenerationID string `json:"generation_id"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	FinishedAt   string `json:"finished_at"`
}

// operationResponse is the operation-scoped aggregate. This is the only shape
// that carries planning state: a workset itself is never planned.
type operationResponse struct {
	WorksetID        string                            `json:"workset_id"`
	OperationType    string                            `json:"operation_type"`
	Version          int                               `json:"version"`
	PlanningState    string                            `json:"planning_state"`
	CurrentRevision  *currentRevisionResponse          `json:"current_revision"`
	ActiveGeneration *generationProgressResponse       `json:"active_generation"`
	LatestGeneration *generationSummaryResponse        `json:"latest_generation"`
	ActiveExecution  *worksetusecase.ExecutionProgress `json:"active_execution"`
	LatestExecution  *worksetusecase.ExecutionRef      `json:"latest_execution"`
}

// worksetResponse is the workset metadata view.
type worksetResponse struct {
	WorksetID  string              `json:"workset_id"`
	Title      string              `json:"title"`
	Version    int                 `json:"version"`
	Library    *libraryRefResponse `json:"library"`
	Members    []memberResponse    `json:"members"`
	Operations []operationResponse `json:"operations"`
	UpdatedAt  string              `json:"updated_at"`
	CreatedAt  string              `json:"created_at"`
}

// worksetListResponse is the list payload.
type worksetListResponse struct {
	Worksets   []worksetResponse `json:"worksets"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

func toOperationResponse(v *worksetusecase.OperationView) operationResponse {
	out := operationResponse{
		WorksetID:     v.WorksetID,
		OperationType: v.OperationType,
		Version:       v.Version,
		PlanningState: v.PlanningState,
	}
	if v.CurrentRevision != nil {
		out.CurrentRevision = toCurrentRevisionResponse(v.CurrentRevision)
	}
	if v.ActiveGeneration != nil {
		out.ActiveGeneration = &generationProgressResponse{
			GenerationID:   v.ActiveGeneration.GenerationID,
			Status:         v.ActiveGeneration.Status,
			TotalRoots:     v.ActiveGeneration.TotalRoots,
			CompletedRoots: v.ActiveGeneration.CompletedRoots,
			CurrentRoot:    v.ActiveGeneration.CurrentRoot,
			ErrorCount:     v.ActiveGeneration.ErrorCount,
		}
	}
	if v.LatestGeneration != nil {
		out.LatestGeneration = &generationSummaryResponse{
			GenerationID: v.LatestGeneration.GenerationID,
			Status:       v.LatestGeneration.Status,
			ErrorCode:    v.LatestGeneration.ErrorCode,
			ErrorMessage: v.LatestGeneration.ErrorMessage,
			FinishedAt:   v.LatestGeneration.FinishedAt.UTC().Format(timeFormatJSON),
		}
	}
	out.ActiveExecution = v.ActiveExecution
	out.LatestExecution = v.LatestExecution
	return out
}

func toWorksetResponse(v *worksetusecase.WorksetView) worksetResponse {
	out := worksetResponse{
		WorksetID:  v.WorksetID,
		Title:      v.Title,
		Version:    v.Version,
		UpdatedAt:  v.UpdatedAt.UTC().Format(timeFormatJSON),
		CreatedAt:  v.CreatedAt.UTC().Format(timeFormatJSON),
		Members:    make([]memberResponse, 0, len(v.Members)),
		Operations: make([]operationResponse, 0, len(v.Operations)),
	}
	if v.Library != nil {
		out.Library = &libraryRefResponse{
			LibraryID: v.Library.LibraryID,
			Name:      v.Library.Name,
			RootPath:  v.Library.RootPath,
		}
	}
	for _, m := range v.Members {
		out.Members = append(out.Members, memberResponse{
			MemberID:   m.MemberID,
			FolderID:   m.FolderID,
			FolderPath: m.FolderPath,
			FolderName: m.FolderName,
			RelPath:    m.RelPath,
		})
	}
	for _, op := range v.Operations {
		out.Operations = append(out.Operations, toOperationResponse(op))
	}
	return out
}

func toCurrentRevisionResponse(r *worksetusecase.RevisionSummary) *currentRevisionResponse {
	return &currentRevisionResponse{
		PlanID:        r.PlanID,
		RevisionIndex: r.RevisionIndex,
		CreatedAt:     r.CreatedAt.UTC().Format(timeFormatJSON),
		Status:        r.Status,
		SummaryReason: r.SummaryReason,
		Counts: revisionCountsResponse{
			Members:      r.Counts.Members,
			Changed:      r.Counts.Changed,
			UnmetTargets: r.Counts.UnmetTargets,
			Blocked:      r.Counts.Blocked,
			Unchanged:    r.Counts.Unchanged,
		},
		ValidationState: r.ValidationState,
		Stale:           r.Stale,
	}
}

// ==================== Handlers ====================

// worksetService guards a nil service in Dependencies.
func (s *Server) worksetService() (worksetusecase.Service, error) {
	if s.deps.WorksetService == nil {
		return nil, errors.New("workset service not configured")
	}
	return s.deps.WorksetService, nil
}

// streamSessionEvents opens one SSE stream and delegates to a session
// subscribe function. A missing session is reported as a structured error
// event, because the 200 status is already committed with the stream headers.
func streamSessionEvents(
	w http.ResponseWriter,
	r *http.Request,
	notFoundCode string,
	subscribe func(ctx context.Context, emit func(event string, data any) error) error,
) {
	sw, err := newSSEWriter(w)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	emit := func(event string, data any) error {
		return sw.Send(event, data)
	}
	if err := subscribe(r.Context(), emit); err != nil {
		if werr, ok := worksetusecase.AsError(err); ok && werr.Code == notFoundCode {
			_ = sw.Send("error", map[string]string{"code": notFoundCode, "message": werr.Message})
			return
		}
		_ = sw.Send("error", map[string]string{"code": "INTERNAL", "message": "streaming failed"})
	}
}

// createWorkset handles POST /api/v1/worksets.
func (s *Server) createWorkset(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	var req worksetCreateRequest
	if decodeErr := decodeJSON(w, r, &req); decodeErr != nil {
		writeDecodeError(w, decodeErr, "invalid workset payload")
		return
	}
	idemKey := r.Header.Get("Idempotency-Key")
	res, err := svc.CreateWorkset(r.Context(), worksetusecase.CreateRequest{
		LibraryID:      req.LibraryID,
		Title:          req.Title,
		FolderIDs:      req.FolderIDs,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	status := http.StatusCreated
	if !res.Created {
		status = http.StatusOK
	}
	writeJSON(w, status, struct {
		Workset worksetResponse `json:"workset"`
		Created bool            `json:"created"`
	}{Workset: toWorksetResponse(res.Workset), Created: res.Created})
}

// listWorksets handles GET /api/v1/worksets.
func (s *Server) listWorksets(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	limit := min(queryInt(r, "limit", 50), 200)
	includeOrphaned := r.URL.Query().Get("status") != "active"
	views, next, err := svc.ListWorksets(r.Context(), worksetusecase.ListQuery{
		Cursor:          r.URL.Query().Get("cursor"),
		Limit:           limit,
		LibraryID:       r.URL.Query().Get("library_id"),
		IncludeOrphaned: includeOrphaned,
	})
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	out := make([]worksetResponse, 0, len(views))
	for _, v := range views {
		out = append(out, toWorksetResponse(v))
	}
	writeJSON(w, http.StatusOK, worksetListResponse{Worksets: out, NextCursor: next})
}

// getWorkset handles GET /api/v1/worksets/{id}.
func (s *Server) getWorkset(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	view, err := svc.GetWorkset(r.Context(), r.PathValue("id"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toWorksetResponse(view))
}

// patchWorkset handles PATCH /api/v1/worksets/{id} (rename). Renaming is
// guarded by the workset metadata version and never by an operation version,
// so it cannot invalidate an operation draft.
func (s *Server) patchWorkset(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	var req worksetPatchRequest
	if decodeErr := decodeJSON(w, r, &req); decodeErr != nil {
		writeDecodeError(w, decodeErr, "invalid workset payload")
		return
	}
	version, valid := ifMatchVersion(r)
	if !valid {
		writeError(w, http.StatusBadRequest, "VERSION_REQUIRED", "If-Match header with the workset version is required")
		return
	}
	view, err := svc.RenameWorkset(r.Context(), r.PathValue("id"), worksetusecase.RenameRequest{
		Title: req.Title, IfMatchVersion: version,
	})
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toWorksetResponse(view))
}
