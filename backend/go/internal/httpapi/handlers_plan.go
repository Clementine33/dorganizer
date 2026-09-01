package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/pathnorm"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// planCreateRequest is the POST /api/v1/plans payload. Exactly one branch is
// required: workflow (declarative desired outputs over folder planning roots)
// or single_action (explicit delete/convert of selected source files).
type planCreateRequest struct {
	LibraryID    string               `json:"library_id"`
	FolderIDs    []string             `json:"folder_ids"`
	SourceFiles  []string             `json:"source_files"`
	Workflow     *workflowRequest     `json:"workflow"`
	SingleAction *singleActionRequest `json:"single_action"`

	// Legacy slim/prune fields: rejected explicitly, never derived silently.
	PlanType             string `json:"plan_type"`
	TargetFormat         string `json:"target_format"`
	PruneMatchedExcluded bool   `json:"prune_matched_excluded"`
}

type workflowRequest struct {
	SchemaVersion int                   `json:"schema_version"`
	Steps         []workflowStepRequest `json:"steps"`
}

type workflowStepRequest struct {
	StepType string              `json:"step_type"`
	Policy   policySourceRequest `json:"policy"`
}

type policySourceRequest struct {
	Kind    string          `json:"kind"` // preset | inline
	Name    string          `json:"name"` // preset branch
	Version int             `json:"version"`
	Policy  json.RawMessage `json:"policy"` // inline branch (reconcile.Policy JSON)
}

type singleActionRequest struct {
	Action       string   `json:"action"` // delete | convert
	SourceFiles  []string `json:"source_files"`
	TargetFormat string   `json:"target_format"`
}

// planOperationResponse mirrors one single-action operation.
type planOperationResponse struct {
	Type       string `json:"type"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
}

// planErrorResponse mirrors a folder-scoped error (single-action branch).
type planErrorResponse struct {
	FolderPath string `json:"folder_path"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
}

// planSummaryResponse mirrors the usecase summary.
type planSummaryResponse struct {
	OperationCount  int    `json:"operation_count"`
	ErrorCount      int    `json:"error_count"`
	TotalCount      int    `json:"total_count"`
	ActionableCount int    `json:"actionable_count"`
	SummaryReason   string `json:"summary_reason"`
}

// planResponse mirrors the single-action usecase response.
type planResponse struct {
	PlanID            string                  `json:"plan_id"`
	SnapshotToken     string                  `json:"snapshot_token"`
	RootPath          string                  `json:"root_path"`
	PlanKind          string                  `json:"plan_kind"`
	Summary           planSummaryResponse     `json:"summary"`
	Operations        []planOperationResponse `json:"operations"`
	Errors            []planErrorResponse     `json:"errors"`
	SuccessfulFolders []string                `json:"successful_folders"`
}

// workflowStepResponse is the layered review payload of one step.
type workflowStepResponse struct {
	StepType   string            `json:"step_type"`
	StepIndex  int               `json:"step_index"`
	Status     string            `json:"status"`
	Policy     json.RawMessage   `json:"policy"`
	PolicyHash string            `json:"policy_hash"`
	Classifier json.RawMessage   `json:"classifier"`
	Summary    json.RawMessage   `json:"summary"`
	Components []json.RawMessage `json:"components"`
}

// workflowPlanResponse is the create/detail shape for workflow plans.
type workflowPlanResponse struct {
	PlanID            string                  `json:"plan_id"`
	SnapshotToken     string                  `json:"snapshot_token"`
	RootPath          string                  `json:"root_path"`
	PlanKind          string                  `json:"plan_kind"`
	Summary           planSummaryResponse     `json:"summary"`
	Steps             []workflowStepResponse  `json:"steps"`
	Operations        []planOperationResponse `json:"operations"`
	Errors            []planErrorResponse     `json:"errors"`
	SuccessfulFolders []string                `json:"successful_folders"`
}

func rawJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

// splitTagSnapshot reverses the persisted NUL-joined tag snapshot into a JSON
// array. An empty snapshot marshals as [] rather than [""].
func splitTagSnapshot(snapshot string) []string {
	if snapshot == "" {
		return []string{}
	}
	return strings.Split(snapshot, "\x00")
}

// workflow response assembly keeps component outcomes as raw JSON snapshots so
// create and detail payloads agree byte-for-byte with the persisted outcome.

func toWorkflowPlanResponse(resp planusecase.Response) workflowPlanResponse {
	out := workflowPlanResponse{
		PlanID:        resp.PlanID,
		SnapshotToken: resp.SnapshotToken,
		RootPath:      resp.RootPath,
		PlanKind:      resp.PlanKind,
		Summary: planSummaryResponse{
			OperationCount:  resp.Summary.OperationCount,
			ErrorCount:      resp.Summary.ErrorCount,
			TotalCount:      resp.Summary.TotalCount,
			ActionableCount: resp.Summary.ActionableCount,
			SummaryReason:   resp.Summary.SummaryReason,
		},
		Operations:        []planOperationResponse{},
		Errors:            []planErrorResponse{},
		SuccessfulFolders: []string{},
	}
	for _, step := range resp.Steps {
		components := make([]json.RawMessage, 0, len(step.Components))
		for _, c := range step.Components {
			components = append(components, rawJSON(c))
		}
		out.Steps = append(out.Steps, workflowStepResponse{
			StepType:   step.StepType,
			StepIndex:  step.StepIndex,
			Status:     step.Status,
			Policy:     rawJSON(step.Policy),
			PolicyHash: step.PolicyHash,
			Classifier: rawJSON(struct {
				Tags []string `json:"tags"`
				Hash string   `json:"hash"`
			}{step.Classifier.Tags, step.Classifier.Hash}),
			Summary:    rawJSON(step.Summary),
			Components: components,
		})
	}
	return out
}

// createPlan creates a workflow or single-action plan for a library scope and
// returns the planned review payload.
func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	var req planCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeDecodeError(w, err, "invalid plan payload")
		return
	}

	if req.PlanType != "" || req.TargetFormat != "" || req.PruneMatchedExcluded {
		writeError(
			w,
			http.StatusBadRequest,
			"LEGACY_FIELDS_NOT_SUPPORTED",
			"plan_type/target_format/prune_matched_excluded are removed; use workflow or single_action",
		)
		return
	}
	if (req.Workflow == nil) == (req.SingleAction == nil) {
		writeError(
			w,
			http.StatusBadRequest,
			"INVALID_PLAN_REQUEST",
			"exactly one of workflow or single_action is required",
		)
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

	if s.deps.PlanService == nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "plan service not configured")
		return
	}

	usecaseReq := planusecase.Request{LibraryID: req.LibraryID}

	switch {
	case req.Workflow != nil:
		if len(req.FolderIDs) == 0 {
			writeError(w, http.StatusBadRequest, "SCOPE_REQUIRED", "workflow requires folder_ids as planning roots")
			return
		}
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

		steps := make([]planusecase.WorkflowStep, 0, len(req.Workflow.Steps))
		for _, step := range req.Workflow.Steps {
			policySource, err := parsePolicySource(step.Policy)
			if err != nil {
				writeError(w, http.StatusBadRequest, "INVALID_POLICY_SOURCE", err.Error())
				return
			}
			steps = append(steps, planusecase.WorkflowStep{StepType: step.StepType, Policy: policySource})
		}
		usecaseReq.Workflow = &planusecase.Workflow{
			SchemaVersion: req.Workflow.SchemaVersion,
			Steps:         steps,
		}
		usecaseReq.PlanningRoots = folderPaths

	case req.SingleAction != nil:
		if len(req.SingleAction.SourceFiles) == 0 && len(req.SourceFiles) == 0 {
			writeError(w, http.StatusBadRequest, "SCOPE_REQUIRED", "single_action requires source_files")
			return
		}
		sourceFiles := req.SingleAction.SourceFiles
		if len(sourceFiles) == 0 {
			sourceFiles = req.SourceFiles
		}
		normalized, ok := validatePlanSourceFiles(w, lib.RootPath, sourceFiles)
		if !ok {
			return
		}
		usecaseReq.SingleAction = &planusecase.SingleAction{
			Action:       req.SingleAction.Action,
			SourceFiles:  normalized,
			TargetFormat: req.SingleAction.TargetFormat,
		}
	}

	resp, err := s.deps.PlanService.Plan(r.Context(), usecaseReq)
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

	if resp.PlanKind == planusecase.PlanKindWorkflow {
		writeJSON(w, http.StatusOK, toWorkflowPlanResponse(resp))
		return
	}
	writeJSON(w, http.StatusOK, toPlanResponse(resp))
}

// resolveStepSummary rebuilds the plan summary from the persisted step
// summaries. Schema v1 has a single step; a future multi-step workflow sums
// all steps' operation counts and treats any blocked step as PARTIAL/BLOCKED.
func resolveStepSummary(steps []sqlite.WorkflowStepRecord) planSummaryResponse {
	out := planSummaryResponse{SummaryReason: "NO_MATCH"}
	var agg reconcile.StepSummary
	for _, step := range steps {
		var s reconcile.StepSummary
		if err := json.Unmarshal([]byte(step.StepSummaryJSON), &s); err != nil {
			continue
		}
		agg.ComponentCount += s.ComponentCount
		agg.BlockedCount += s.BlockedCount
		agg.OperationCount += s.OperationCount
		agg.ErrorCount += s.ErrorCount
	}
	switch {
	case agg.BlockedCount > 0 && agg.OperationCount > 0:
		out.SummaryReason = reconcile.ReasonPartial
	case agg.BlockedCount > 0:
		out.SummaryReason = reconcile.ReasonBlocked
	case agg.OperationCount > 0:
		out.SummaryReason = reconcile.ReasonActionable
	}
	out.OperationCount = agg.OperationCount
	out.ErrorCount = agg.ErrorCount
	out.TotalCount = agg.OperationCount
	out.ActionableCount = agg.OperationCount
	return out
}

// parsePolicySource decodes the inline-only policy source into the usecase shape.
func parsePolicySource(req policySourceRequest) (planusecase.PolicySource, error) {
	if req.Kind != "inline" {
		return planusecase.PolicySource{}, errors.New("unsupported policy source kind; use inline")
	}
	policy, err := parseInlinePolicy(req.Policy)
	if err != nil {
		return planusecase.PolicySource{}, err
	}
	return planusecase.PolicySource{Kind: "inline", InlinePolicy: &policy}, nil
}

func parseInlinePolicy(raw json.RawMessage) (reconcile.Policy, error) {
	var policy reconcile.Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return policy, errors.New("inline policy is not valid JSON")
	}
	return policy, nil
}

// validatePlanSourceFiles normalizes single-action source files and emits a
// 400 when they escape the library.
func validatePlanSourceFiles(w http.ResponseWriter, rootPath string, sourceFiles []string) ([]string, bool) {
	out := make([]string, 0, len(sourceFiles))
	for _, sourceFile := range sourceFiles {
		withinLibrary := pathnorm.IsWithinRoot(rootPath, sourceFile)
		isLibraryRoot := pathnorm.IsWithinRoot(sourceFile, rootPath)
		if !withinLibrary || isLibraryRoot {
			writeError(
				w,
				http.StatusBadRequest,
				"SOURCE_FILE_OUTSIDE_LIBRARY",
				"source_file must be inside the selected library",
			)
			return nil, false
		}
		resolvedWithinLibrary, err := pathnorm.IsResolvedWithinRoot(rootPath, sourceFile)
		if err != nil || !resolvedWithinLibrary {
			writeError(
				w,
				http.StatusBadRequest,
				"SOURCE_FILE_OUTSIDE_LIBRARY",
				"source_file must resolve inside the selected library",
			)
			return nil, false
		}
		out = append(out, pathnorm.NormalizeToPOSIX(sourceFile))
	}
	return out, true
}

// toPlanResponse maps a single-action usecase response to the HTTP JSON shape.
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
		PlanKind:      resp.PlanKind,
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

// planInfoResponse is one plan in GET /api/v1/plans.
type planInfoResponse struct {
	PlanID    string    `json:"plan_id"`
	RootPath  string    `json:"root_path"`
	PlanType  string    `json:"plan_type"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// listPlans returns plans for a library (when library_id is given) or across
// all libraries, newest first. Ownership is stored on the plan (not derived
// from the current folder index), so folder-scoped plans stay listed after
// rescans and the unfiltered list includes every plan.
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

	var libraryID *string
	if raw := r.URL.Query().Get("library_id"); raw != "" {
		if _, err := s.deps.Repo.GetLibrary(raw); err != nil {
			if errors.Is(err, sqlite.ErrLibraryNotFound) {
				writeError(w, http.StatusNotFound, "LIBRARY_NOT_FOUND", "library not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load library")
			return
		}
		libraryID = &raw
	}

	plans, err := s.deps.Repo.ListPlans(libraryID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to list plans")
		return
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

// getPlanDetail returns the full review payload for one plan, rebuilt from
// persisted snapshots (workflow) or plan items (single actions).
func (s *Server) getPlanDetail(w http.ResponseWriter, r *http.Request) {
	planID := r.PathValue("id")
	kind, _, err := s.deps.Repo.GetPlanWorkflowSchema(planID)
	if err != nil {
		if errors.Is(err, sqlite.ErrPlanNotFound) {
			writeError(w, http.StatusNotFound, "PLAN_NOT_FOUND", "plan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load plan")
		return
	}
	if kind == "workflow" {
		s.writeWorkflowPlanDetail(w, planID)
		return
	}
	s.writeSingleActionPlanDetail(w, planID)
}

func (s *Server) writeWorkflowPlanDetail(w http.ResponseWriter, planID string) {
	detail, err := s.deps.Repo.GetWorkflowPlanDetail(planID)
	if err != nil {
		if errors.Is(err, sqlite.ErrPlanNotFound) {
			writeError(w, http.StatusNotFound, "PLAN_NOT_FOUND", "plan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load plan")
		return
	}

	steps := make([]workflowStepResponse, 0, len(detail.Steps))
	for _, step := range detail.Steps {
		// Components carry a step_index; only attach the ones belonging to
		// this step so a future multi-step workflow renders correctly.
		components := make([]json.RawMessage, 0)
		for _, c := range detail.Components {
			if c.StepIndex != step.StepIndex {
				continue
			}
			components = append(components, json.RawMessage(c.OutcomeJSON))
		}
		steps = append(steps, workflowStepResponse{
			StepType:   step.StepType,
			StepIndex:  step.StepIndex,
			Status:     step.Status,
			Policy:     json.RawMessage(step.PolicyJSON),
			PolicyHash: step.PolicyHash,
			Classifier: rawJSON(struct {
				Tags []string `json:"tags"`
				Hash string   `json:"hash"`
			}{splitTagSnapshot(step.ClassifierTags), step.ClassifierHash}),
			Summary:    json.RawMessage(step.StepSummaryJSON),
			Components: components,
		})
	}

	// Rebuild the plan summary from the persisted step summary so create and
	// detail agree without consulting live state.
	summaryResp := resolveStepSummary(detail.Steps)
	writeJSON(w, http.StatusOK, workflowPlanResponse{
		PlanID:            detail.Plan.PlanID,
		SnapshotToken:     detail.Plan.SnapshotToken,
		RootPath:          detail.Plan.RootPath,
		PlanKind:          detail.Plan.PlanKind,
		Summary:           summaryResp,
		Steps:             steps,
		Operations:        []planOperationResponse{},
		Errors:            []planErrorResponse{},
		SuccessfulFolders: []string{},
	})
}

func (s *Server) writeSingleActionPlanDetail(w http.ResponseWriter, planID string) {
	detail, err := s.deps.Repo.GetPlanDetail(planID)
	if err != nil {
		if errors.Is(err, sqlite.ErrPlanNotFound) {
			writeError(w, http.StatusNotFound, "PLAN_NOT_FOUND", "plan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load plan")
		return
	}

	ops := make([]planOperationResponse, 0, len(detail.Items))
	for _, item := range detail.Items {
		opType := "delete"
		if item.OpType == "convert_and_delete" {
			opType = "convert"
		}
		targetPath := ""
		if item.TargetPath != nil {
			targetPath = *item.TargetPath
		}
		ops = append(ops, planOperationResponse{
			Type:       opType,
			SourcePath: item.SourcePath,
			TargetPath: targetPath,
		})
	}
	errs := make([]planErrorResponse, 0, len(detail.FolderErrors))
	for _, pe := range detail.FolderErrors {
		errs = append(errs, planErrorResponse{
			FolderPath: pe.FolderPath,
			Code:       pe.Code,
			Message:    pe.Message,
			Retryable:  pe.Retryable,
		})
	}

	summaryReason := "NO_MATCH"
	if len(ops) > 0 {
		summaryReason = "ACTIONABLE"
	}
	rootPath := detail.Plan.RootPath
	if detail.Plan.ScanRootPath != "" {
		rootPath = detail.Plan.ScanRootPath
	}

	writeJSON(w, http.StatusOK, planResponse{
		PlanID:        detail.Plan.PlanID,
		SnapshotToken: detail.Plan.SnapshotToken,
		RootPath:      rootPath,
		PlanKind:      detail.Plan.PlanKind,
		Summary: planSummaryResponse{
			OperationCount:  len(ops),
			ErrorCount:      len(errs),
			TotalCount:      len(ops),
			ActionableCount: len(ops),
			SummaryReason:   summaryReason,
		},
		Operations:        ops,
		Errors:            errs,
		SuccessfulFolders: detail.SuccessfulFolders,
	})
}
