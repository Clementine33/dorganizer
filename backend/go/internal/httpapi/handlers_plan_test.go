package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// =============================================================================
// Helpers
// =============================================================================

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

// planOperationDTO mirrors the operation objects in POST /api/v1/plans.
type planOperationDTO struct {
	Type       string `json:"type"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
}

// planErrorDTO mirrors the folder-scoped errors in POST /api/v1/plans.
type planErrorDTO struct {
	FolderPath string `json:"folder_path"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
}

// planSummaryDTO mirrors the summary object in POST /api/v1/plans.
type planSummaryDTO struct {
	OperationCount  int    `json:"operation_count"`
	ErrorCount      int    `json:"error_count"`
	TotalCount      int    `json:"total_count"`
	ActionableCount int    `json:"actionable_count"`
	SummaryReason   string `json:"summary_reason"`
}

// planResponseDTO mirrors the POST /api/v1/plans response body.
type planResponseDTO struct {
	PlanID            string             `json:"plan_id"`
	SnapshotToken     string             `json:"snapshot_token"`
	RootPath          string             `json:"root_path"`
	Operations        []planOperationDTO `json:"operations"`
	Errors            []planErrorDTO     `json:"errors"`
	Summary           planSummaryDTO     `json:"summary"`
	SuccessfulFolders []string           `json:"successful_folders"`
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
// the shared temp repository, and returns the engine plus the repo handle.
func newPlanTestServer(t *testing.T) (http.Handler, *sqlite.Repository) {
	t.Helper()
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) {
		repo = d.Repo
		d.PlanService = planusecase.NewService(d.Repo, "")
	})
	return engine, repo
}

// seedFolderLibrary seeds a library with two flac files under albumA and
// returns the library ID and the albumA folder.
func seedFolderLibrary(t *testing.T, engine http.Handler, repo *sqlite.Repository) (libID string, folder *sqlite.LibraryFolder) {
	t.Helper()
	libID = createLibraryViaAPI(t, engine, "Music", "/music")

	insertEntryMeta(t, repo, "/music", "", "music", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA", "/music", "albumA", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA/01.flac", "/music/albumA", "01.flac", false, 1234, nil, "flac")
	insertEntryMeta(t, repo, "/music/albumA/02.flac", "/music/albumA", "02.flac", false, 2048, nil, "flac")
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

// decodePlanResponse decodes the POST /api/v1/plans response body.
func decodePlanResponse(t *testing.T, w *httptest.ResponseRecorder) planResponseDTO {
	t.Helper()
	var out planResponseDTO
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode plan response: %v (body=%s)", err, w.Body.String())
	}
	return out
}

func writeTestAudioFile(t *testing.T, root, relative string) string {
	t.Helper()
	filePath := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("create audio parent directory: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("audio"), 0o644); err != nil {
		t.Fatalf("create audio file: %v", err)
	}
	return filePath
}

// =============================================================================
// =============================================================================

func TestCreatePlanWithFolderIDs(t *testing.T) {
	engine, repo := newPlanTestServer(t)
	libID, folder := seedFolderLibrary(t, engine, repo)

	w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id":    libID,
		"folder_ids":    []string{folder.ID},
		"plan_type":     "slim",
		"target_format": "slim:mode2",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	out := decodePlanResponse(t, w)
	if out.PlanID == "" {
		t.Error("plan_id should not be empty")
	}
	if out.RootPath != "/music" {
		t.Errorf("root_path = %q, want /music", out.RootPath)
	}
	if len(out.Operations) != 2 {
		t.Fatalf("len(operations) = %d, want 2 (body=%s)", len(out.Operations), w.Body.String())
	}
	wantOps := []planOperationDTO{
		{Type: "convert", SourcePath: "/music/albumA/01.flac", TargetPath: "/music/albumA/01.m4a"},
		{Type: "convert", SourcePath: "/music/albumA/02.flac", TargetPath: "/music/albumA/02.m4a"},
	}
	for i, want := range wantOps {
		if out.Operations[i] != want {
			t.Errorf("operations[%d] = %+v, want %+v", i, out.Operations[i], want)
		}
	}
	if len(out.Errors) != 0 {
		t.Errorf("errors = %+v, want none", out.Errors)
	}
	if len(out.SuccessfulFolders) != 1 || out.SuccessfulFolders[0] != "/music/albumA" {
		t.Errorf("successful_folders = %v, want [/music/albumA]", out.SuccessfulFolders)
	}
	summary := out.Summary
	if summary.OperationCount != 2 || summary.ErrorCount != 0 || summary.TotalCount != 2 || summary.ActionableCount != 2 {
		t.Errorf("summary = %+v, want operation_count=2 error_count=0 total_count=2 actionable_count=2", summary)
	}
	if summary.SummaryReason != "ACTIONABLE" {
		t.Errorf("summary_reason = %q, want ACTIONABLE", summary.SummaryReason)
	}

	// The handler must have resolved folder_ids to library folder paths before
	// calling the plan usecase: the persisted plan root is the folder path
	// (/music/albumA), not the folder or library ID.
	plan, err := repo.GetPlan(out.PlanID)
	if err != nil {
		t.Fatalf("GetPlan failed: %v", err)
	}
	if plan.RootPath != "/music/albumA" {
		t.Errorf("persisted root_path = %q, want /music/albumA (folder path resolved)", plan.RootPath)
	}
	if plan.PlanType != "slim" {
		t.Errorf("persisted plan_type = %q, want slim", plan.PlanType)
	}
	items, err := repo.ListPlanItems(out.PlanID)
	if err != nil {
		t.Fatalf("ListPlanItems failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(plan_items) = %d, want 2", len(items))
	}
	for _, it := range items {
		if it.OpType != "convert_and_delete" {
			t.Errorf("item op_type = %q, want convert_and_delete", it.OpType)
		}
		if it.TargetPath == nil || *it.TargetPath == "" {
			t.Errorf("item %q missing target_path", it.SourcePath)
		}
	}
}

func TestCreatePlanWithSourceFiles(t *testing.T) {
	engine, repo := newPlanTestServer(t)
	libraryRoot := t.TempDir()
	libID := createLibraryViaAPI(t, engine, "Music", libraryRoot)

	sources := []string{
		writeTestAudioFile(t, libraryRoot, filepath.Join("albumA", "01.flac")),
		writeTestAudioFile(t, libraryRoot, filepath.Join("albumA", "02.flac")),
	}
	w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id":   libID,
		"source_files": sources,
		"plan_type":    "single_delete",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	out := decodePlanResponse(t, w)
	if out.PlanID == "" {
		t.Error("plan_id should not be empty")
	}
	if len(out.Operations) != 2 {
		t.Fatalf("len(operations) = %d, want 2 (body=%s)", len(out.Operations), w.Body.String())
	}
	bySource := make(map[string]planOperationDTO, len(out.Operations))
	for _, op := range out.Operations {
		bySource[op.SourcePath] = op
	}
	for _, src := range sources {
		op, ok := bySource[src]
		if !ok {
			t.Errorf("missing operation for source %q (ops=%+v)", src, out.Operations)
			continue
		}
		if op.Type != "delete" {
			t.Errorf("op %q type = %q, want delete", src, op.Type)
		}
		// The usecase stages deletes under rootPath/Delete/; assert the exact
		// staged target for this delete operation.
		want := filepath.Join(libraryRoot, "albumA", "Delete", path.Base(src))
		if op.TargetPath != want {
			t.Errorf("op %q target_path = %q, want %q", src, op.TargetPath, want)
		}
	}
	if out.Summary.OperationCount != 2 || out.Summary.ErrorCount != 0 {
		t.Errorf("summary = %+v, want operation_count=2 error_count=0", out.Summary)
	}

	// Each source file must be persisted as a plan item for later execution.
	items, err := repo.ListPlanItems(out.PlanID)
	if err != nil {
		t.Fatalf("ListPlanItems failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(plan_items) = %d, want 2", len(items))
	}
}

func TestCreatePlanValidation(t *testing.T) {
	t.Run("no scope returns 400 SCOPE_REQUIRED", func(t *testing.T) {
		engine, _ := newPlanTestServer(t)
		libID := createLibraryViaAPI(t, engine, "Music", "/music")

		w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
			"library_id": libID,
			"plan_type":  "slim",
		}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "SCOPE_REQUIRED" {
			t.Fatalf("code = %q, want SCOPE_REQUIRED", code)
		}
	})

	t.Run("empty arrays count as no scope", func(t *testing.T) {
		engine, _ := newPlanTestServer(t)
		libID := createLibraryViaAPI(t, engine, "Music", "/music")

		w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
			"library_id":   libID,
			"folder_ids":   []string{},
			"source_files": []string{},
			"plan_type":    "slim",
		}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "SCOPE_REQUIRED" {
			t.Fatalf("code = %q, want SCOPE_REQUIRED", code)
		}
	})

	t.Run("unknown library returns 404", func(t *testing.T) {
		engine, _ := newPlanTestServer(t)

		w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
			"library_id":   "does-not-exist",
			"source_files": []string{"/music/01.flac"},
			"plan_type":    "single_delete",
		}, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "LIBRARY_NOT_FOUND" {
			t.Fatalf("code = %q, want LIBRARY_NOT_FOUND", code)
		}
	})

	t.Run("unknown folder id returns 404", func(t *testing.T) {
		engine, repo := newPlanTestServer(t)
		libID, _ := seedFolderLibrary(t, engine, repo)

		w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
			"library_id": libID,
			"folder_ids": []string{"no-such-folder"},
			"plan_type":  "slim",
		}, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "LIBRARY_FOLDER_NOT_FOUND" {
			t.Fatalf("code = %q, want LIBRARY_FOLDER_NOT_FOUND", code)
		}
	})

	t.Run("malformed json returns 400", func(t *testing.T) {
		engine, _ := newPlanTestServer(t)

		w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", "not-json", nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "INVALID_ARGUMENT" {
			t.Fatalf("code = %q, want INVALID_ARGUMENT", code)
		}
	})

	t.Run("source files outside the library return 400", func(t *testing.T) {
		engine, _ := newPlanTestServer(t)
		libID := createLibraryViaAPI(t, engine, "Music", "/music")

		w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
			"library_id":   libID,
			"source_files": []string{"/music/../outside/01.flac"},
			"plan_type":    "single_delete",
		}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "SOURCE_FILE_OUTSIDE_LIBRARY" {
			t.Fatalf("code = %q, want SOURCE_FILE_OUTSIDE_LIBRARY", code)
		}
	})

	t.Run("source files escaping through a symlink return 400", func(t *testing.T) {
		engine, _ := newPlanTestServer(t)
		workspace := t.TempDir()
		libraryRoot := filepath.Join(workspace, "library")
		outsideRoot := filepath.Join(workspace, "outside")
		if err := os.MkdirAll(libraryRoot, 0o755); err != nil {
			t.Fatalf("create library root: %v", err)
		}
		if err := os.MkdirAll(outsideRoot, 0o755); err != nil {
			t.Fatalf("create outside root: %v", err)
		}
		outsideFile := filepath.Join(outsideRoot, "01.flac")
		if err := os.WriteFile(outsideFile, []byte("audio"), 0o644); err != nil {
			t.Fatalf("create outside file: %v", err)
		}
		linkPath := filepath.Join(libraryRoot, "linked")
		if err := os.Symlink(outsideRoot, linkPath); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		libID := createLibraryViaAPI(t, engine, "Music", libraryRoot)
		w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
			"library_id":   libID,
			"source_files": []string{filepath.Join(linkPath, "01.flac")},
			"plan_type":    "single_delete",
		}, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "SOURCE_FILE_OUTSIDE_LIBRARY" {
			t.Fatalf("code = %q, want SOURCE_FILE_OUTSIDE_LIBRARY", code)
		}
	})
}

// TestCreatePlanErrorMapping verifies how plan usecase errors map to HTTP
// statuses and error-envelope codes.
func TestCreatePlanErrorMapping(t *testing.T) {
	resolveAndCall := func(t *testing.T, service planusecase.Service) *httptest.ResponseRecorder {
		t.Helper()
		var repo *sqlite.Repository
		engine := newTestServer(t, func(d *Dependencies) {
			repo = d.Repo
			d.PlanService = service
		})
		libID, folder := seedFolderLibrary(t, engine, repo)

		w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
			"library_id": libID,
			"folder_ids": []string{folder.ID},
			"plan_type":  "slim",
		}, nil)
		return w
	}

	t.Run("invalid_argument maps to 400 with code passthrough", func(t *testing.T) {
		var gotReq planusecase.Request
		stub := &stubPlanService{planFn: func(_ context.Context, req planusecase.Request) (planusecase.Response, error) {
			gotReq = req
			return planusecase.Response{}, planusecase.NewError(planusecase.ErrKindInvalidArgument, "MISSING_SOURCE_FILES", "source_files required for single_delete/single_convert", nil)
		}}
		w := resolveAndCall(t, stub)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "MISSING_SOURCE_FILES" {
			t.Fatalf("code = %q, want MISSING_SOURCE_FILES", code)
		}
		// The handler must pass the resolved folder path, not the folder ID.
		if len(gotReq.FolderPaths) != 1 || gotReq.FolderPaths[0] != "/music/albumA" {
			t.Errorf("FolderPaths = %v, want [/music/albumA]", gotReq.FolderPaths)
		}
		if gotReq.PlanType != "slim" {
			t.Errorf("PlanType = %q, want slim", gotReq.PlanType)
		}
	})

	t.Run("already_exists maps to 409 with code passthrough", func(t *testing.T) {
		stub := &stubPlanService{planFn: func(_ context.Context, _ planusecase.Request) (planusecase.Response, error) {
			return planusecase.Response{}, planusecase.NewError(planusecase.ErrKindAlreadyExists, "PLAN_ID_CONFLICT", "plan already exists", nil)
		}}
		w := resolveAndCall(t, stub)

		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "PLAN_ID_CONFLICT" {
			t.Fatalf("code = %q, want PLAN_ID_CONFLICT", code)
		}
	})

	t.Run("internal maps to 500 INTERNAL", func(t *testing.T) {
		stub := &stubPlanService{planFn: func(_ context.Context, _ planusecase.Request) (planusecase.Response, error) {
			return planusecase.Response{}, planusecase.NewError(planusecase.ErrKindInternal, "ANALYZE_FAILED", "analyze exploded", nil)
		}}
		w := resolveAndCall(t, stub)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "INTERNAL" {
			t.Fatalf("code = %q, want INTERNAL", code)
		}
	})

	t.Run("non-plan error maps to 500 INTERNAL", func(t *testing.T) {
		stub := &stubPlanService{planFn: func(_ context.Context, _ planusecase.Request) (planusecase.Response, error) {
			return planusecase.Response{}, context.DeadlineExceeded
		}}
		w := resolveAndCall(t, stub)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "INTERNAL" {
			t.Fatalf("code = %q, want INTERNAL", code)
		}
	})
}

func TestCreatePlanServiceNotConfigured(t *testing.T) {
	// PlanService left nil: the handler must fail cleanly, not panic.
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) { repo = d.Repo })
	libID, folder := seedFolderLibrary(t, engine, repo)

	w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id": libID,
		"folder_ids": []string{folder.ID},
		"plan_type":  "slim",
	}, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body=%s)", w.Code, w.Body.String())
	}
	code, _ := errorEnvelope(t, w)
	if code != "INTERNAL" {
		t.Fatalf("code = %q, want INTERNAL", code)
	}
}

// =============================================================================
// GET /api/v1/plans
// =============================================================================

// createSingleDeletePlan creates a single_delete plan through the API and
// returns its plan ID.
func createSingleDeletePlan(t *testing.T, engine http.Handler, libID string, sources []string) string {
	t.Helper()
	w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id":   libID,
		"source_files": sources,
		"plan_type":    "single_delete",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("create plan status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	out := decodePlanResponse(t, w)
	return out.PlanID
}

// createFolderPlan creates a slim folder-scoped plan through the API and
// returns its plan ID.
func createFolderPlan(t *testing.T, engine http.Handler, libID, folderID string) string {
	t.Helper()
	w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
		"library_id":    libID,
		"folder_ids":    []string{folderID},
		"plan_type":     "slim",
		"target_format": "slim:mode2",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("create folder plan status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	out := decodePlanResponse(t, w)
	return out.PlanID
}

// TestCreatePlanSuccessivePlansGetDistinctIDs verifies that immediate
// successive POST /api/v1/plans calls each get a unique plan ID (plan IDs are
// generated at second resolution in the usecase, so persistence used to
// collide with 409 PLAN_ID_CONFLICT within the same second).
func TestCreatePlanSuccessivePlansGetDistinctIDs(t *testing.T) {
	engine, _ := newPlanTestServer(t)
	libraryRoot := t.TempDir()
	libID := createLibraryViaAPI(t, engine, "Music", libraryRoot)

	ids := make(map[string]bool)
	for i := range 5 {
		src := writeTestAudioFile(t, libraryRoot, fmt.Sprintf("track-%02d.flac", i))
		w := doRequest(t, engine, http.MethodPost, "/api/v1/plans", map[string]any{
			"library_id":   libID,
			"source_files": []string{src},
			"plan_type":    "single_delete",
		}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("plan %d: status = %d, want 200 (body=%s)", i, w.Code, w.Body.String())
		}
		out := decodePlanResponse(t, w)
		if !strings.HasPrefix(out.PlanID, "plan-") {
			t.Fatalf("plan_id %q lost the plan- prefix", out.PlanID)
		}
		if ids[out.PlanID] {
			t.Fatalf("duplicate plan_id %q on successive POSTs", out.PlanID)
		}
		ids[out.PlanID] = true
	}
}

func TestListPlans(t *testing.T) {
	engine, repo := newPlanTestServer(t)
	libraryRootA := t.TempDir()
	libraryRootB := t.TempDir()
	libA := createLibraryViaAPI(t, engine, "Music", libraryRootA)
	// libB must exist so the all-plans listing iterates its root too.
	createLibraryViaAPI(t, engine, "Other", libraryRootB)

	sourceA := writeTestAudioFile(t, libraryRootA, "01.flac")
	planA := createSingleDeletePlan(t, engine, libA, []string{sourceA})

	// Insert the second plan directly: plan IDs are second-resolution
	// timestamps, so two POSTs within the same second would collide (a
	// pre-existing usecase property, not an HTTP concern). Give it a later
	// CreatedAt so the newest-first order is deterministic.
	if err := repo.CreatePlan(&sqlite.Plan{
		PlanID:    "plan-list-test-b",
		RootPath:  libraryRootB,
		PlanType:  "single_delete",
		Status:    "ready",
		CreatedAt: time.Now().Add(time.Second),
	}); err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}
	const planB = "plan-list-test-b"

	t.Run("by library returns only that library's plans", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/plans?library_id="+libA, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var out struct {
			Plans []planInfoDTO `json:"plans"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode plans: %v (body=%s)", err, w.Body.String())
		}
		if len(out.Plans) != 1 {
			t.Fatalf("len(plans) = %d, want 1 (body=%s)", len(out.Plans), w.Body.String())
		}
		p := out.Plans[0]
		if p.PlanID != planA {
			t.Errorf("plan_id = %q, want %q", p.PlanID, planA)
		}
		// The plan root must be under (or equal to) the selected library root.
		if p.RootPath != libraryRootA {
			t.Errorf("root_path = %q, want %q", p.RootPath, libraryRootA)
		}
		if p.PlanType != "single_delete" {
			t.Errorf("plan_type = %q, want single_delete", p.PlanType)
		}
		if p.Status != "ready" {
			t.Errorf("status = %q, want ready", p.Status)
		}
		if _, err := time.Parse(time.RFC3339, p.CreatedAt); err != nil {
			t.Errorf("created_at = %q is not RFC3339: %v", p.CreatedAt, err)
		}
	})

	t.Run("without library_id returns all plans newest first", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/plans", nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var out struct {
			Plans []planInfoDTO `json:"plans"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode plans: %v (body=%s)", err, w.Body.String())
		}
		if len(out.Plans) != 2 {
			t.Fatalf("len(plans) = %d, want 2 (body=%s)", len(out.Plans), w.Body.String())
		}
		// planB was created after planA, so it must sort first (created_at DESC).
		if out.Plans[0].PlanID != planB || out.Plans[1].PlanID != planA {
			t.Errorf("plan order = [%s %s], want [%s %s]", out.Plans[0].PlanID, out.Plans[1].PlanID, planB, planA)
		}
		if !sort.SliceIsSorted(out.Plans, func(i, j int) bool {
			return out.Plans[i].CreatedAt > out.Plans[j].CreatedAt
		}) {
			t.Errorf("plans not sorted by created_at desc: %+v", out.Plans)
		}
	})

	t.Run("limit caps the result set", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/plans?limit=1", nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var out struct {
			Plans []planInfoDTO `json:"plans"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode plans: %v (body=%s)", err, w.Body.String())
		}
		if len(out.Plans) != 1 {
			t.Fatalf("len(plans) = %d, want 1 (body=%s)", len(out.Plans), w.Body.String())
		}
		if out.Plans[0].PlanID != planB {
			t.Errorf("plan_id = %q, want %q (newest)", out.Plans[0].PlanID, planB)
		}
	})

	t.Run("unknown library returns 404", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/plans?library_id=does-not-exist", nil, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "LIBRARY_NOT_FOUND" {
			t.Fatalf("code = %q, want LIBRARY_NOT_FOUND", code)
		}
	})
}

// TestListPlansIncludesFolderScopedPlans verifies that a folder-scoped plan
// (persisted with its scope folder as root, e.g. /music/albumA) still appears
// in the library-filtered listing, whose roots are the library root plus every
// library folder path (root under/equal to the library root).
func TestListPlansIncludesFolderScopedPlans(t *testing.T) {
	engine, repo := newPlanTestServer(t)
	libID, folder := seedFolderLibrary(t, engine, repo)

	const rootPlan = "plan-list-root"
	if err := repo.CreatePlan(&sqlite.Plan{
		PlanID:    rootPlan,
		RootPath:  "/music",
		PlanType:  "single_delete",
		Status:    "ready",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}
	folderPlan := createFolderPlan(t, engine, libID, folder.ID)

	w := doRequest(t, engine, http.MethodGet, "/api/v1/plans?library_id="+libID, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var out struct {
		Plans []planInfoDTO `json:"plans"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode plans: %v (body=%s)", err, w.Body.String())
	}
	if len(out.Plans) != 2 {
		t.Fatalf("len(plans) = %d, want 2 (body=%s)", len(out.Plans), w.Body.String())
	}

	byID := make(map[string]planInfoDTO, len(out.Plans))
	roots := make(map[string]bool, len(out.Plans))
	for _, p := range out.Plans {
		byID[p.PlanID] = p
		roots[p.RootPath] = true
	}
	if _, ok := byID[rootPlan]; !ok {
		t.Errorf("library-filtered list missing root-scoped plan %q (body=%s)", rootPlan, w.Body.String())
	}
	if _, ok := byID[folderPlan]; !ok {
		t.Errorf("library-filtered list missing folder-scoped plan %q (body=%s)", folderPlan, w.Body.String())
	}
	// Both roots are under/equal to the library root /music.
	for root := range roots {
		if root != "/music" && !strings.HasPrefix(root, "/music/") {
			t.Errorf("plan root %q is not under the library root /music", root)
		}
	}
}
