package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/onsei/organizer/backend/internal/pathnorm"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// planCreateRequest is the POST /api/v1/plans payload. Scope is provided
// either via folder_ids (resolved to library-scoped folder paths) or
// source_files; at least one of the two must be present.
type planCreateRequest struct {
	LibraryID            string   `json:"library_id"`
	FolderIDs            []string `json:"folder_ids"`
	SourceFiles          []string `json:"source_files"`
	PlanType             string   `json:"plan_type"`
	TargetFormat         string   `json:"target_format"`
	PruneMatchedExcluded bool     `json:"prune_matched_excluded"`
}

// planOperationResponse mirrors one plan usecase Operation with snake_case JSON.
type planOperationResponse struct {
	Type       string `json:"type"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
}

// planErrorResponse mirrors one plan usecase FolderError with snake_case JSON.
type planErrorResponse struct {
	FolderPath string `json:"folder_path"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
}

// planSummaryResponse mirrors the plan usecase Summary with snake_case JSON.
type planSummaryResponse struct {
	OperationCount  int    `json:"operation_count"`
	ErrorCount      int    `json:"error_count"`
	TotalCount      int    `json:"total_count"`
	ActionableCount int    `json:"actionable_count"`
	SummaryReason   string `json:"summary_reason"`
}

// planResponse mirrors the plan usecase Response with snake_case JSON.
type planResponse struct {
	PlanID            string                  `json:"plan_id"`
	SnapshotToken     string                  `json:"snapshot_token"`
	RootPath          string                  `json:"root_path"`
	Summary           planSummaryResponse     `json:"summary"`
	Operations        []planOperationResponse `json:"operations"`
	Errors            []planErrorResponse     `json:"errors"`
	SuccessfulFolders []string                `json:"successful_folders"`
}

// toPlanResponse maps a plan usecase response to the HTTP JSON shape without
// interpreting outcomes: the usecase owns summary semantics.
func toPlanResponse(resp planusecase.Response) planResponse {
	ops := make([]planOperationResponse, 0, len(resp.Operations))
	for _, op := range resp.Operations {
		ops = append(ops, planOperationResponse{
			Type:       op.Type,
			SourcePath: op.SourcePath,
			TargetPath: op.TargetPath,
		})
	}
	errs := make([]planErrorResponse, 0, len(resp.Errors))
	for _, pe := range resp.Errors {
		errs = append(errs, planErrorResponse{
			FolderPath: pe.FolderPath,
			Code:       pe.Code,
			Message:    pe.Message,
			Retryable:  pe.Retryable,
		})
	}
	return planResponse{
		PlanID:        resp.PlanID,
		SnapshotToken: resp.SnapshotToken,
		RootPath:      resp.RootPath,
		Summary: planSummaryResponse{
			OperationCount:  resp.Summary.OperationCount,
			ErrorCount:      resp.Summary.ErrorCount,
			TotalCount:      resp.Summary.TotalCount,
			ActionableCount: resp.Summary.ActionableCount,
			SummaryReason:   resp.Summary.SummaryReason,
		},
		Operations:        ops,
		Errors:            errs,
		SuccessfulFolders: resp.SuccessfulFolders,
	}
}

// createPlan creates a plan for a library scope (folders or source files) and
// returns the planned operations and summary. Folder IDs are resolved to
// library-scoped folder paths before the plan usecase runs, so paths are never
// normalized or re-based in HTTP.
func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	var req planCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid plan payload")
		return
	}

	lib, err := s.deps.Repo.GetLibrary(req.LibraryID)
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryNotFound) {
			writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load library")
		return
	}

	if len(req.FolderIDs) == 0 && len(req.SourceFiles) == 0 {
		writeError(w, http.StatusBadRequest, "SCOPE_REQUIRED", "folder_ids or source_files is required")
		return
	}
	sourceFiles := make([]string, len(req.SourceFiles))
	for i, sourceFile := range req.SourceFiles {
		withinLibrary := pathnorm.IsWithinRoot(lib.RootPath, sourceFile)
		isLibraryRoot := pathnorm.IsWithinRoot(sourceFile, lib.RootPath)
		if !withinLibrary || isLibraryRoot {
			writeError(w, http.StatusBadRequest, "SOURCE_FILE_OUTSIDE_LIBRARY", "source_file must be inside the selected library")
			return
		}
		resolvedWithinLibrary, err := pathnorm.IsResolvedWithinRoot(lib.RootPath, sourceFile)
		if err != nil || !resolvedWithinLibrary {
			writeError(w, http.StatusBadRequest, "SOURCE_FILE_OUTSIDE_LIBRARY", "source_file must resolve inside the selected library")
			return
		}
		sourceFiles[i] = pathnorm.NormalizeToPOSIX(sourceFile)
	}

	// Resolve folder IDs to paths within the library. GetLibraryFolder scopes
	// by library ID, so a folder belonging to another library 404s here.
	folderPaths := make([]string, 0, len(req.FolderIDs))
	for _, folderID := range req.FolderIDs {
		folder, err := s.deps.Repo.GetLibraryFolder(lib.ID, folderID)
		if err != nil {
			if errors.Is(err, sqlite.ErrLibraryFolderNotFound) {
				writeError(w, http.StatusNotFound, "LIBRARY_FOLDER_NOT_FOUND", "folder not found in library")
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load folder")
			return
		}
		folderPaths = append(folderPaths, folder.Path)
	}

	// The plan service is optional in Dependencies (nil until wired); guard
	// before calling so an unwired server reports a clean error.
	if s.deps.PlanService == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "plan service not configured")
		return
	}

	resp, err := s.deps.PlanService.Plan(r.Context(), planusecase.Request{
		PlanType:             req.PlanType,
		TargetFormat:         req.TargetFormat,
		SourceFiles:          sourceFiles,
		FolderPaths:          folderPaths,
		PruneMatchedExcluded: req.PruneMatchedExcluded,
	})
	if err != nil {
		if planErr, ok := planusecase.AsError(err); ok {
			switch planErr.Kind {
			case planusecase.ErrKindInvalidArgument:
				writeError(w, http.StatusBadRequest, planErr.Code, planErr.Message)
				return
			case planusecase.ErrKindAlreadyExists:
				writeError(w, http.StatusConflict, planErr.Code, planErr.Message)
				return
			}
			// ErrKindInternal and unknown kinds fall through to 500.
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create plan")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create plan")
		return
	}

	writeJSON(w, http.StatusOK, toPlanResponse(resp))
}

// planInfoResponse is one plan in GET /api/v1/plans.
type planInfoResponse struct {
	PlanID    string    `json:"plan_id"`
	RootPath  string    `json:"root_path"`
	PlanType  string    `json:"plan_type"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// listPlans returns plans for a library (when library_id is given) or across
// all libraries, newest first. It reuses the repository's ListPlansByRoot
// method; no plan-specific SQL is introduced here. For a library, plans are
// listed for the library root and for every library folder path, because a
// folder-scoped plan is persisted with its scope folder as root (e.g.
// /music/albumA) and must still appear under the owning library (/music).
func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	var roots []string
	if libraryID := r.URL.Query().Get("library_id"); libraryID != "" {
		lib, err := s.deps.Repo.GetLibrary(libraryID)
		if err != nil {
			if errors.Is(err, sqlite.ErrLibraryNotFound) {
				writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load library")
			return
		}
		roots = []string{lib.RootPath}
		folders, err := s.deps.Repo.ListLibraryFolders(lib.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list folders")
			return
		}
		for _, f := range folders {
			roots = append(roots, f.Path)
		}
	} else {
		libs, err := s.deps.Repo.ListLibraries()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list libraries")
			return
		}
		for _, l := range libs {
			roots = append(roots, l.RootPath)
		}
	}

	// Merge per-root plans, deduping by plan ID (libraries can share roots).
	seen := make(map[string]bool)
	plans := make([]*sqlite.Plan, 0)
	for _, root := range roots {
		rootPlans, err := s.deps.Repo.ListPlansByRoot(root)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list plans")
			return
		}
		for _, p := range rootPlans {
			if seen[p.PlanID] {
				continue
			}
			seen[p.PlanID] = true
			plans = append(plans, p)
		}
	}

	sort.SliceStable(plans, func(i, j int) bool {
		return plans[i].CreatedAt.After(plans[j].CreatedAt)
	})
	if len(plans) > limit {
		plans = plans[:limit]
	}

	out := make([]planInfoResponse, 0, len(plans))
	for _, p := range plans {
		out = append(out, planInfoResponse{
			PlanID:    p.PlanID,
			RootPath:  p.RootPath,
			PlanType:  p.PlanType,
			Status:    p.Status,
			CreatedAt: p.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, struct {
		Plans []planInfoResponse `json:"plans"`
	}{Plans: out})
}
