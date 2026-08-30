package httpapi

import (
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

// worksetDraftRequest is the PUT /api/v1/worksets/{id}/draft payload. It uses
// the same workflow step/policy shape as the plan create path.
type worksetDraftRequest struct {
	Workflow workflowRequest `json:"workflow"`
}

// generationStartRequest is the POST /api/v1/worksets/{id}/revisions payload.
type generationStartRequest struct {
	ExpectedDraftVersion int `json:"expected_draft_version"`
}

// libraryRefResponse is the owning-library snapshot.
type libraryRefResponse struct {
	LibraryID string `json:"library_id"`
	Name      string `json:"name"`
	RootPath  string `json:"root_path"`
}

// memberResponse is one album-folder member with coverage state.
type memberResponse struct {
	FolderID   string `json:"folder_id"`
	FolderPath string `json:"folder_path"`
	FolderName string `json:"folder_name"`
	RelPath    string `json:"rel_path"`
	State      string `json:"state"`
}

// currentRevisionResponse is the compact immutable conclusion.
type currentRevisionResponse struct {
	PlanID          string `json:"plan_id"`
	RevisionIndex   int    `json:"revision_index"`
	CreatedAt       string `json:"created_at"`
	Status          string `json:"status"`
	SummaryReason   string `json:"summary_reason"`
	BlockedCount    int    `json:"blocked_count"`
	ValidationState string `json:"validation_state"`
	Stale           *bool  `json:"stale"`
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

// worksetResponse is the feed-ready aggregate view.
type worksetResponse struct {
	WorksetID        string                      `json:"workset_id"`
	Title            string                      `json:"title"`
	Version          int                         `json:"version"`
	Library          *libraryRefResponse         `json:"library"`
	PlanningState    string                      `json:"planning_state"`
	CurrentRevision  *currentRevisionResponse    `json:"current_revision"`
	ActiveGeneration *generationProgressResponse `json:"active_generation"`
	LatestGeneration *generationSummaryResponse  `json:"latest_generation"`
	Members          []memberResponse            `json:"members"`
	UpdatedAt        string                      `json:"updated_at"`
	CreatedAt        string                      `json:"created_at"`
}

// worksetListResponse is the feed payload.
type worksetListResponse struct {
	Worksets   []worksetResponse `json:"worksets"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// draftResponse is the persisted workflow draft. There is intentionally no
// separate draft concurrency counter: worksets.version is the single mutation
// authority, so the draft response carries that version directly.
type draftResponse struct {
	WorksetID             string           `json:"workset_id"`
	Version               int              `json:"version"`
	WorkflowSchemaVersion int              `json:"workflow_schema_version"`
	Workflow              workflowResponse `json:"workflow"`
	UpdatedAt             string           `json:"updated_at"`
}

// generationViewResponse is the session detail payload.
type generationViewResponse struct {
	GenerationID   string `json:"generation_id"`
	WorksetID      string `json:"workset_id"`
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

// revisionListResponse is revision history payload.
type revisionListResponse struct {
	Revisions []currentRevisionResponse `json:"revisions"`
}

func toWorksetResponse(v *worksetusecase.WorksetView) worksetResponse {
	out := worksetResponse{
		WorksetID:     v.WorksetID,
		Title:         v.Title,
		Version:       v.Version,
		PlanningState: v.PlanningState,
		UpdatedAt:     v.UpdatedAt.UTC().Format(timeFormatJSON),
		CreatedAt:     v.CreatedAt.UTC().Format(timeFormatJSON),
		Members:       make([]memberResponse, 0, len(v.Members)),
	}
	if v.Library != nil {
		out.Library = &libraryRefResponse{LibraryID: v.Library.LibraryID, Name: v.Library.Name, RootPath: v.Library.RootPath}
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
	for _, m := range v.Members {
		out.Members = append(out.Members, memberResponse{
			FolderID: m.FolderID, FolderPath: m.FolderPath, FolderName: m.FolderName, RelPath: m.RelPath, State: m.State,
		})
	}
	return out
}

func toCurrentRevisionResponse(r *worksetusecase.RevisionSummary) *currentRevisionResponse {
	return &currentRevisionResponse{
		PlanID:          r.PlanID,
		RevisionIndex:   r.RevisionIndex,
		CreatedAt:       r.CreatedAt.UTC().Format(timeFormatJSON),
		Status:          r.Status,
		SummaryReason:   r.SummaryReason,
		BlockedCount:    r.BlockedCount,
		ValidationState: r.ValidationState,
		Stale:           r.Stale,
	}
}

// ==================== Handlers ====================

// workspaceService guards a nil service in Dependencies.
func (s *Server) worksetService() (worksetusecase.Service, error) {
	if s.deps.WorksetService == nil {
		return nil, errors.New("workset service not configured")
	}
	return s.deps.WorksetService, nil
}

// createWorkset handles POST /api/v1/worksets.
func (s *Server) createWorkset(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	var req worksetCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid workset payload")
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
	limit := queryInt(r, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	includeOrphaned := true
	if r.URL.Query().Get("status") == "active" {
		includeOrphaned = false
	}
	feed := r.URL.Query().Get("feed")
	if feed != "" && !worksetusecase.ValidFeed(feed) {
		writeError(w, http.StatusBadRequest, "INVALID_FEED_FILTER", "feed must be one of all|pending|normal|error")
		return
	}
	views, next, err := svc.ListWorksets(r.Context(), worksetusecase.ListQuery{
		Cursor:          r.URL.Query().Get("cursor"),
		Limit:           limit,
		LibraryID:       r.URL.Query().Get("library_id"),
		IncludeOrphaned: includeOrphaned,
		Feed:            feed,
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

// patchWorkset handles PATCH /api/v1/worksets/{id} (rename).
func (s *Server) patchWorkset(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	var req worksetPatchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid workset payload")
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

// getWorksetDraft handles GET /api/v1/worksets/{id}/draft.
func (s *Server) getWorksetDraft(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	d, err := svc.GetDraft(r.Context(), r.PathValue("id"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draftResponse{
		WorksetID:             d.WorksetID,
		Version:               d.Version,
		WorkflowSchemaVersion: d.WorkflowSchemaVersion,
		Workflow:              toWorkflowResponse(d.Workflow),
		UpdatedAt:             d.UpdatedAt.UTC().Format(timeFormatJSON),
	})
}

// putWorksetDraft handles PUT /api/v1/worksets/{id}/draft (full replacement).
func (s *Server) putWorksetDraft(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	var req worksetDraftRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid draft payload")
		return
	}
	version, valid := ifMatchVersion(r)
	if !valid {
		writeError(w, http.StatusBadRequest, "VERSION_REQUIRED", "If-Match header with the workset version is required")
		return
	}
	view, err := svc.SaveDraft(r.Context(), r.PathValue("id"), worksetusecase.SaveDraftRequest{
		Workflow:       toWorkflow(req.Workflow),
		IfMatchVersion: version,
	})
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toWorksetResponse(view))
}

// startGeneration handles POST /api/v1/worksets/{id}/revisions.
func (s *Server) startGeneration(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	var req generationStartRequest
	if err := decodeJSONAllowEmpty(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid generation payload")
		return
	}
	idemKey := r.Header.Get("Idempotency-Key")
	if idemKey == "" {
		writeError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required")
		return
	}
	res, err := svc.StartGeneration(r.Context(), r.PathValue("id"), worksetusecase.StartGenerationRequest{
		ExpectedDraftVersion: req.ExpectedDraftVersion,
		IdempotencyKey:       idemKey,
	})
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

// getGeneration handles GET /api/v1/worksets/{id}/planning-sessions/{genId}.
func (s *Server) getGeneration(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	g, err := svc.GetGeneration(r.Context(), r.PathValue("id"), r.PathValue("genId"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toGenerationViewResponse(g))
}

// generationEvents handles GET /api/v1/worksets/{id}/planning-sessions/{genId}/events (SSE).
func (s *Server) generationEvents(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
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
	if err := svc.Subscribe(r.Context(), r.PathValue("id"), r.PathValue("genId"), emit); err != nil {
		if werr, ok := worksetusecase.AsError(err); ok && werr.Code == "GENERATION_NOT_FOUND" {
			_ = sw.Send("error", map[string]string{"code": "GENERATION_NOT_FOUND", "message": werr.Message})
			return
		}
		_ = sw.Send("error", map[string]string{"code": "INTERNAL", "message": "streaming failed"})
	}
}

// cancelGeneration handles POST /api/v1/worksets/{id}/planning-sessions/{genId}/cancel.
func (s *Server) cancelGeneration(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	g, err := svc.CancelGeneration(r.Context(), r.PathValue("id"), r.PathValue("genId"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toGenerationViewResponse(g))
}

// listRevisions handles GET /api/v1/worksets/{id}/revisions.
func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	limit := queryInt(r, "limit", 50)
	if limit > 200 {
		limit = 200
	}
	before := queryInt(r, "before_index", 0)
	revs, err := svc.ListRevisions(r.Context(), r.PathValue("id"), before, limit)
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	out := make([]currentRevisionResponse, 0, len(revs))
	for _, rv := range revs {
		out = append(out, *toCurrentRevisionResponse(rv))
	}
	writeJSON(w, http.StatusOK, revisionListResponse{Revisions: out})
}

// getRevision handles GET /api/v1/worksets/{id}/revisions/{planId}.
func (s *Server) getRevision(w http.ResponseWriter, r *http.Request) {
	svc, err := s.worksetService()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "workset service not configured")
		return
	}
	rv, err := svc.GetRevision(r.Context(), r.PathValue("id"), r.PathValue("planId"))
	if err != nil {
		writeWorksetError(w, err)
		return
	}
	// A revision over zero components would otherwise marshal as null; the
	// frontend contract is always an array.
	componentRoots := rv.ComponentRoots
	if componentRoots == nil {
		componentRoots = []worksetusecase.ComponentRootRef{}
	}
	writeJSON(w, http.StatusOK, struct {
		PlanID         string                            `json:"plan_id"`
		RevisionIndex  int                               `json:"revision_index"`
		CreatedAt      string                            `json:"created_at"`
		Roots          []rootValidationResponse          `json:"roots"`
		ComponentRoots []worksetusecase.ComponentRootRef `json:"component_roots"`
		Workflow       workflowPlanResponse              `json:"workflow"`
	}{PlanID: rv.PlanID, RevisionIndex: rv.RevisionIndex, CreatedAt: rv.CreatedAt.UTC().Format(timeFormatJSON), Roots: toRoots(rv.Roots), ComponentRoots: componentRoots, Workflow: toWorkflowPlanResponse(rv.Workflow)})
}

// ==================== helpers ====================
// (toWorkflow/toWorkflowResponse/writeWorksetError live in helpers_workset.go)
