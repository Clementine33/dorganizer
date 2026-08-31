package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// =============================================================================
// Helpers
// =============================================================================

// inlinePolicyFixture is the inline workflow policy payload replacing the
// removed compiled presets: literal tags + wav/mp3@320 both partitions.
func inlinePolicyFixture() map[string]any {
	profile := map[string]any{
		"lossless": map[string]any{"codec": "wav"},
		"encoded":  map[string]any{"codec": "mp3", "quality": map[string]any{"kind": "bitrate", "bitrate": 320}},
	}
	return map[string]any{
		"kind": "inline",
		"policy": map[string]any{
			"schema_version":   1,
			"classifier_tags":  []string{"SEなし"},
			"matched":          profile,
			"unmatched":        profile,
		},
	}
}

// stubPlanService is a controllable plan usecase stub for mapping tests.
type stubPlanService struct {
	planFn func(ctx context.Context, req planusecase.Request) (planusecase.Response, error)
}

func (s *stubPlanService) Plan(ctx context.Context, req planusecase.Request) (planusecase.Response, error) {
	if s.planFn == nil {
		return planusecase.Response{}, nil
	}
	return s.planFn(ctx, req)
}

type planSummaryDTO struct {
	OperationCount  int    `json:"operation_count"`
	ErrorCount      int    `json:"error_count"`
	TotalCount      int    `json:"total_count"`
	ActionableCount int    `json:"actionable_count"`
	SummaryReason   string `json:"summary_reason"`
}

type planOperationDTO struct {
	Type       string `json:"type"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
}

type planErrorDTO struct {
	FolderPath string `json:"folder_path"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
}

type workflowStepDTO struct {
	StepType   string            `json:"step_type"`
	StepIndex  int               `json:"step_index"`
	Status     string            `json:"status"`
	Policy     json.RawMessage   `json:"policy"`
	PolicyHash string            `json:"policy_hash"`
	Classifier json.RawMessage   `json:"classifier"`
	Summary    planSummaryDTO    `json:"summary"`
	Components []json.RawMessage `json:"components"`
}

type workflowPlanDTO struct {
	PlanID        string             `json:"plan_id"`
	SnapshotToken string             `json:"snapshot_token"`
	RootPath      string             `json:"root_path"`
	PlanKind      string             `json:"plan_kind"`
	Summary       planSummaryDTO     `json:"summary"`
	Steps         []workflowStepDTO  `json:"steps"`
	Operations    []planOperationDTO `json:"operations"`
	Errors        []planErrorDTO     `json:"errors"`
}

type planResponseDTO struct {
	PlanID     string             `json:"plan_id"`
	RootPath   string             `json:"root_path"`
	PlanKind   string             `json:"plan_kind"`
	Summary    planSummaryDTO     `json:"summary"`
	Operations []planOperationDTO `json:"operations"`
	Errors     []planErrorDTO     `json:"errors"`
}

// planInfoDTO mirrors one item of GET /api/v1/plans.
type planInfoDTO struct {
	PlanID    string `json:"plan_id"`
	RootPath  string `json:"root_path"`
	PlanType  string `json:"plan_type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// newPlanTestServer builds a test server with a real plan service wired over
// the shared temp repository.
func newPlanTestServer(t *testing.T) (http.Handler, *sqlite.Repository) {
	t.Helper()
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) {
		repo = d.Repo
		d.PlanService = planusecase.NewService(d.Repo, "")
	})
	return engine, repo
}

// seedWorkflowFolder seeds a library with one folder holding flac+mp3 pairs
// (no wav, no bitrate facts) so a balanced preset is actionable.
func seedWorkflowFolder(t *testing.T, engine http.Handler, repo *sqlite.Repository) (libID string, folder *sqlite.LibraryFolder) {
	t.Helper()
	libID = createLibraryViaAPI(t, engine, "Music", "/music")

	insertEntryMeta(t, repo, "/music", "", "music", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA", "/music", "albumA", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA/01.flac", "/music/albumA", "01.flac", false, 1234, nil, "flac")
	insertEntryMeta(t, repo, "/music/albumA/01.mp3", "/music/albumA", "01.mp3", false, 1234, int64Ptr(0), "mpeg")
	insertEntryMeta(t, repo, "/music/albumA/02.flac", "/music/albumA", "02.flac", false, 2048, nil, "flac")
	insertEntryMeta(t, repo, "/music/albumA/02.mp3", "/music/albumA", "02.mp3", false, 2048, int64Ptr(0), "mpeg")
	if _, err := repo.ReplaceLibraryFolders(libID, "/music"); err != nil {
		t.Fatalf("ReplaceLibraryFolders failed: %v", err)
	}
	folders, err := repo.ListLibraryFolders(libID)
	if err != nil {
		t.Fatalf("ListLibraryFolders failed: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("expected 1 folder, got %d", len(folders))
	}
	return libID, folders[0]
}

func int64Ptr(v int64) *int64 { return &v }

// =============================================================================
// Workflow create
// =============================================================================

func TestCreateWorkflowPlanWithFolderIDs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX /music paths; skip on Windows")
	}
	engine, repo := newPlanTestServer(t)
	libID, folder := seedWorkflowFolder(t, engine, repo)

	w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id": libID,
		"folder_ids": []string{folder.ID},
		"workflow": map[string]any{
			"schema_version": 1,
			"steps": []any{map[string]any{
				"step_type": "reconcile_audio_outputs",
				"policy": inlinePolicyFixture(),
			}},
		},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var plan workflowPlanDTO
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode workflow plan: %v (body=%s)", err, w.Body.String())
	}
	if plan.PlanID == "" {
		t.Fatal("workflow plan_id empty")
	}
	if plan.PlanKind != "workflow" {
		t.Fatalf("plan_kind = %q, want workflow", plan.PlanKind)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(plan.Steps))
	}
	step := plan.Steps[0]
	if step.StepType != "reconcile_audio_outputs" {
		t.Fatalf("step_type = %q", step.StepType)
	}
	if len(step.Components) != 1 {
		t.Fatalf("components = %d, want 1", len(step.Components))
	}
	if plan.Summary.SummaryReason != "ACTIONABLE" {
		t.Fatalf("summary_reason = %q, want ACTIONABLE", plan.Summary.SummaryReason)
	}
	if plan.Summary.OperationCount == 0 {
		t.Fatal("expected actionable operations for flac+mp3 pairs under balanced preset")
	}
}

func TestCreateWorkflowPlanLegacyFieldsRejected(t *testing.T) {
	engine, repo := newPlanTestServer(t)
	libID, folder := seedWorkflowFolder(t, engine, repo)

	w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id":    libID,
		"folder_ids":    []string{folder.ID},
		"plan_type":     "slim",
		"target_format": "slim:mode1",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "LEGACY_FIELDS_NOT_SUPPORTED") {
		t.Fatalf("body missing LEGACY_FIELDS_NOT_SUPPORTED: %s", w.Body.String())
	}
}

func TestCreateWorkflowPlanSchemaErrors(t *testing.T) {
	engine, repo := newPlanTestServer(t)
	libID, folder := seedWorkflowFolder(t, engine, repo)

	cases := []struct {
		name   string
		body   map[string]any
		code   string
		status int
	}{
		{
			name: "bad schema version",
			body: map[string]any{
				"library_id": libID, "folder_ids": []string{folder.ID},
				"workflow": map[string]any{"schema_version": 99, "steps": []any{map[string]any{"step_type": "reconcile_audio_outputs", "policy": inlinePolicyFixture()}}},
			},
			code: "INVALID_WORKFLOW_SCHEMA", status: http.StatusBadRequest,
		},
		{
			name: "unsupported step",
			body: map[string]any{
				"library_id": libID, "folder_ids": []string{folder.ID},
				"workflow": map[string]any{"schema_version": 1, "steps": []any{map[string]any{"step_type": "rename_files", "policy": inlinePolicyFixture()}}},
			},
			code: "UNSUPPORTED_STEP", status: http.StatusBadRequest,
		},
		{
			name: "unsupported policy source",
			body: map[string]any{
				"library_id": libID, "folder_ids": []string{folder.ID},
				"workflow": map[string]any{"schema_version": 1, "steps": []any{map[string]any{"step_type": "reconcile_audio_outputs", "policy": map[string]any{"kind": "preset", "name": "nope", "version": 1}}}},
			},
			code: "INVALID_POLICY_SOURCE", status: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", tc.body, nil)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tc.status, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.code) {
				t.Fatalf("body missing %s: %s", tc.code, w.Body.String())
			}
		})
	}
}

func TestCreateWorkflowPlanRequiresScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture; skip on Windows")
	}
	engine, repo := newPlanTestServer(t)
	libID, _ := seedWorkflowFolder(t, engine, repo)

	w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id": libID,
		"workflow": map[string]any{
			"schema_version": 1,
			"steps":          []any{map[string]any{"step_type": "reconcile_audio_outputs", "policy": inlinePolicyFixture()}},
		},
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "SCOPE_REQUIRED") {
		t.Fatalf("body missing SCOPE_REQUIRED: %s", w.Body.String())
	}
}

func TestCreateSingleActionDelete(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture; skip on Windows")
	}
	engine, repo := newPlanTestServer(t)
	root := t.TempDir()
	source := filepath.Join(root, "01.mp3")
	if err := os.WriteFile(source, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	sourcePosix := filepath.ToSlash(source)
	rootPosix := filepath.ToSlash(root)

	libID := createLibraryViaAPI(t, engine, "Music", rootPosix)
	insertEntryMeta(t, repo, rootPosix, "", "Music", true, 0, nil, "")
	insertEntryMeta(t, repo, sourcePosix, rootPosix, "01.mp3", false, 5, int64Ptr(320000), "mpeg")

	w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id": libID,
		"single_action": map[string]any{
			"action":       "delete",
			"source_files": []string{sourcePosix},
		},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var plan planResponseDTO
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode single action plan: %v", err)
	}
	if plan.PlanKind != "single_action" {
		t.Fatalf("plan_kind = %q, want single_action", plan.PlanKind)
	}
	if len(plan.Operations) != 1 || plan.Operations[0].Type != "delete" {
		t.Fatalf("operations = %+v, want one delete", plan.Operations)
	}
	if plan.Operations[0].SourcePath != sourcePosix {
		t.Fatalf("source = %q", plan.Operations[0].SourcePath)
	}
}

func TestCreatePlanServiceNotConfigured(t *testing.T) {
	engine := newTestServer(t, func(d *Dependencies) {
		_ = d.Repo
		// PlanService left nil.
	})
	libID := createLibraryViaAPI(t, engine, "Music", "/music")
	w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id": libID,
		"workflow": map[string]any{
			"schema_version": 1,
			"steps":          []any{map[string]any{"step_type": "reconcile_audio_outputs", "policy": inlinePolicyFixture()}},
		},
	}, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body=%s)", w.Code, w.Body.String())
	}
}

func TestGetWorkflowPlanDetailRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture; skip on Windows")
	}
	engine, repo := newPlanTestServer(t)
	libID, folder := seedWorkflowFolder(t, engine, repo)

	body := map[string]any{
		"library_id": libID,
		"folder_ids": []string{folder.ID},
		"workflow": map[string]any{
			"schema_version": 1,
			"steps": []any{map[string]any{
				"step_type": "reconcile_audio_outputs",
				"policy":    inlinePolicyFixture(),
			}},
		},
	}
	_ = repo
	created := doRequest(t, engine, http.MethodPost, "/api/v1/plans", body, nil)
	if created.Code != http.StatusOK {
		t.Fatalf("create status = %d (body=%s)", created.Code, created.Body.String())
	}
	var plan workflowPlanDTO
	if err := json.Unmarshal(created.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	// Cold GET rebuilds the same layered payload from snapshots.
	w := doRequest(t, engine, http.MethodGet, "/api/v1/plans/"+plan.PlanID, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status = %d (body=%s)", w.Code, w.Body.String())
	}
	var detail workflowPlanDTO
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.PlanKind != "workflow" {
		t.Fatalf("detail plan_kind = %q", detail.PlanKind)
	}
	if len(detail.Steps) != 1 {
		t.Fatalf("detail steps = %d, want 1", len(detail.Steps))
	}
	if len(detail.Steps[0].Components) != len(plan.Steps[0].Components) {
		t.Fatalf("detail components %d != create components %d", len(detail.Steps[0].Components), len(plan.Steps[0].Components))
	}
	if detail.Steps[0].PolicyHash != plan.Steps[0].PolicyHash {
		t.Fatalf("detail policy_hash %q != create %q", detail.Steps[0].PolicyHash, plan.Steps[0].PolicyHash)
	}
	if detail.Summary.SummaryReason != plan.Summary.SummaryReason {
		t.Fatalf("detail summary %q != create %q", detail.Summary.SummaryReason, plan.Summary.SummaryReason)
	}
}

func TestGetWorkflowPlanDetailNotFound(t *testing.T) {
	engine, _ := newPlanTestServer(t)
	w := doRequest(t, engine, http.MethodGet, "/api/v1/plans/plan-missing", nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestSingleActionDetailCarriesPlanKind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture; skip on Windows")
	}
	engine, repo := newPlanTestServer(t)
	root := t.TempDir()
	source := filepath.Join(root, "01.mp3")
	if err := os.WriteFile(source, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	sourcePosix := filepath.ToSlash(source)
	rootPosix := filepath.ToSlash(root)

	libID := createLibraryViaAPI(t, engine, "Music", rootPosix)
	insertEntryMeta(t, repo, rootPosix, "", "Music", true, 0, nil, "")
	insertEntryMeta(t, repo, sourcePosix, rootPosix, "01.mp3", false, 5, int64Ptr(320000), "mpeg")

	created := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id": libID,
		"single_action": map[string]any{
			"action":       "delete",
			"source_files": []string{sourcePosix},
		},
	}, nil)
	if created.Code != http.StatusOK {
		t.Fatalf("create status = %d (body=%s)", created.Code, created.Body.String())
	}
	var plan planResponseDTO
	if err := json.Unmarshal(created.Body.Bytes(), &plan); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if plan.PlanKind != "single_action" {
		t.Fatalf("create plan_kind = %q, want single_action", plan.PlanKind)
	}

	got := doRequest(t, engine, http.MethodGet, "/api/v1/plans/"+plan.PlanID, nil, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("detail status = %d (body=%s)", got.Code, got.Body.String())
	}
	var detail planResponseDTO
	if err := json.Unmarshal(got.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.PlanKind != "single_action" {
		t.Fatalf("detail plan_kind = %q, want single_action (create/detail agreement)", detail.PlanKind)
	}
	if len(detail.Operations) != 1 || detail.Operations[0].Type != "delete" {
		t.Fatalf("detail operations = %+v, want one delete", detail.Operations)
	}
}
