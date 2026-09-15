package httpapi //nolint:testpackage // white-box tests exercise unexported internals

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// seedConfirmedRevision writes a plan snapshot, its revision association, the
// promotion and (optionally) the confirmation of the single-member workset.
func seedConfirmedRevision(
	t *testing.T,
	repo *sqlite.Repository,
	svc worksetusecase.Service,
	libID, worksetID, planID string,
	confirm bool,
) int {
	t.Helper()
	draft, err := svc.GetDraft(t.Context(), worksetID, worksetusecase.OperationTypeConversion)
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	raw, hash, err := worksetusecase.MarshalDraft(draft.Document)
	if err != nil {
		t.Fatalf("MarshalDraft: %v", err)
	}
	if err := sqlite.CreateWorkflowPlanTx(
		repo.DB(), planID, "workflow", "/music", "snap-"+planID, libID,
		nil, nil, nil,
	); err != nil {
		t.Fatalf("CreateWorkflowPlanTx: %v", err)
	}
	now := time.Now().Format(time.RFC3339Nano)
	if _, err := repo.DB().Exec(`
		INSERT INTO workset_operation_revisions
			(plan_id, workset_id, operation_type, revision_index, draft_hash, member_hash, operation_version, excluded_scope, draft_snapshot, created_at)
		VALUES (?, ?, 'conversion', 1, ?, 'members', 0, '', ?, ?)
	`, planID, worksetID, hash, raw, now); err != nil {
		t.Fatalf("insert revision: %v", err)
	}
	if _, err := repo.DB().Exec(`
		UPDATE workset_operations SET current_revision_id = ?, version = version + 1, updated_at = ?
		WHERE workset_id = ? AND operation_type = 'conversion'
	`, planID, now, worksetID); err != nil {
		t.Fatalf("promote revision: %v", err)
	}
	op := getOperationVersion(t, svc, worksetID)
	if confirm {
		if _, err := svc.ConfirmRevision(
			t.Context(), worksetID, worksetusecase.OperationTypeConversion, planID,
			worksetusecase.ConfirmRequest{IfMatchVersion: op},
		); err != nil {
			t.Fatalf("ConfirmRevision: %v", err)
		}
	}
	return op
}

func getOperationVersion(t *testing.T, svc worksetusecase.Service, worksetID string) int {
	t.Helper()
	view, err := svc.GetOperation(t.Context(), worksetID, worksetusecase.OperationTypeConversion)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	return view.Version
}

//nolint:funlen,gocognit,gocyclo,cyclop // one HTTP lifecycle walk over the execution routes
func TestExecutionHTTPStartGatesAndShapes(t *testing.T) {
	h, repo := newWorksetServer(t)
	libID := seedLibrary(t, repo)
	seedFolder(t, repo, libID)
	w := req(t, h, http.MethodPost, "/api/v1/worksets", testToken,
		map[string]any{"library_id": libID, "title": "実行", "folder_ids": []string{"f-a"}})
	if w.Code != http.StatusCreated {
		t.Fatalf("create workset: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Workset struct {
			WorksetID string `json:"workset_id"`
		} `json:"workset"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode workset: %v", err)
	}
	wsID := created.Workset.WorksetID
	svc := worksetusecase.NewService(repo, t.TempDir(), 1)
	opPath := "/api/v1/worksets/" + wsID + "/operations/conversion"
	execPath := opPath + "/revisions/plan-http/executions"

	// Unconfirmed revision: the plan is not executable.
	version := seedConfirmedRevision(t, repo, svc, libID, wsID, "plan-http", false)
	w = reqWithIdempotencyAndIfMatch(
		t,
		h,
		http.MethodPost,
		execPath,
		testToken,
		nil,
		"exec-http-1",
		strconv.Itoa(version),
	)
	if w.Code != http.StatusConflict {
		t.Fatalf("unconfirmed start = %d, want 409: %s", w.Code, w.Body.String())
	}
	var conflict struct {
		Code    string   `json:"code"`
		Details []string `json:"details"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.Code != "PLAN_NOT_EXECUTABLE" || len(conflict.Details) != 1 || conflict.Details[0] != "NOT_CONFIRMED" {
		t.Fatalf("conflict = %+v", conflict)
	}

	// Confirm, then a stale If-Match still loses the race.
	if _, err := svc.ConfirmRevision(
		t.Context(), wsID, worksetusecase.OperationTypeConversion, "plan-http",
		worksetusecase.ConfirmRequest{IfMatchVersion: version},
	); err != nil {
		t.Fatalf("ConfirmRevision: %v", err)
	}
	w = reqWithIdempotencyAndIfMatch(
		t, h, http.MethodPost, execPath, testToken, nil, "exec-http-1", strconv.Itoa(version-1),
	)
	if w.Code != http.StatusConflict || !containsCode(w.Body.Bytes(), "VERSION_CONFLICT") {
		t.Fatalf("stale If-Match start = %d %s", w.Code, w.Body.String())
	}

	// Required headers.
	w = reqWithIfMatch(t, h, http.MethodPost, execPath, testToken, nil, strconv.Itoa(version))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("start without Idempotency-Key = %d, want 400", w.Code)
	}
	w = reqWithIdempotency(t, h, http.MethodPost, execPath, testToken, nil, "exec-http-1")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("start without If-Match = %d, want 400", w.Code)
	}
	w = reqWithIdempotencyAndIfMatch(
		t, h, http.MethodPost, execPath, testToken, map[string]any{"delete_mode": "medium"},
		"exec-http-1", strconv.Itoa(version),
	)
	if w.Code != http.StatusBadRequest || !containsCode(w.Body.Bytes(), "INVALID_DELETE_MODE") {
		t.Fatalf("invalid delete mode = %d %s", w.Code, w.Body.String())
	}

	// First start: 202 with the created session, defaulting to soft delete.
	w = reqWithIdempotencyAndIfMatch(
		t,
		h,
		http.MethodPost,
		execPath,
		testToken,
		map[string]any{},
		"exec-http-1",
		strconv.Itoa(version),
	)
	if w.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	var startResp struct {
		Created   bool                         `json:"created"`
		Execution worksetusecase.ExecutionView `json:"execution"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if !startResp.Created || startResp.Execution.Status != "queued" ||
		startResp.Execution.DeleteMode != "soft" || startResp.Execution.PlanID != "plan-http" {
		t.Fatalf("start resp = %+v", startResp)
	}
	execID := startResp.Execution.ExecutionID

	// The key replays the same session; a different revision or delete mode is a
	// conflict.
	w = reqWithIdempotencyAndIfMatch(
		t,
		h,
		http.MethodPost,
		execPath,
		testToken,
		map[string]any{},
		"exec-http-1",
		strconv.Itoa(version),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("replay = %d, want 200", w.Code)
	}
	var replay struct {
		Created   bool                         `json:"created"`
		Execution worksetusecase.ExecutionView `json:"execution"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &replay); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if replay.Created || replay.Execution.ExecutionID != execID {
		t.Fatalf("replay = %+v", replay)
	}
	w = reqWithIdempotencyAndIfMatch(
		t, h, http.MethodPost, execPath, testToken, map[string]any{"delete_mode": "hard"},
		"exec-http-1", strconv.Itoa(version),
	)
	if w.Code != http.StatusConflict || !containsCode(w.Body.Bytes(), "IDEMPOTENCY_KEY_REUSED") {
		t.Fatalf("key reuse with a different mode = %d %s", w.Code, w.Body.String())
	}

	// A second session for the same operation is refused while one is active.
	w = reqWithIdempotencyAndIfMatch(
		t,
		h,
		http.MethodPost,
		execPath,
		testToken,
		map[string]any{},
		"exec-http-2",
		strconv.Itoa(version),
	)
	if w.Code != http.StatusConflict || !containsCode(w.Body.Bytes(), "EXECUTION_IN_PROGRESS") {
		t.Fatalf("second start = %d %s", w.Code, w.Body.String())
	}

	// Library deletion waits for the session.
	w = req(t, h, http.MethodDelete, "/api/v1/libraries/"+libID, testToken, nil)
	if w.Code != http.StatusConflict || !containsCode(w.Body.Bytes(), "EXECUTION_IN_PROGRESS") {
		t.Fatalf("library delete during execution = %d %s", w.Code, w.Body.String())
	}

	// The operation view exposes the active session; the revision exposes its
	// session reference.
	w = req(t, h, http.MethodGet, opPath, testToken, nil)
	var opView struct {
		ActiveExecution *worksetusecase.ExecutionProgress `json:"active_execution"`
		LatestExecution *worksetusecase.ExecutionRef      `json:"latest_execution"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &opView); err != nil {
		t.Fatalf("decode operation: %v", err)
	}
	if opView.ActiveExecution == nil || opView.ActiveExecution.ExecutionID != execID ||
		opView.LatestExecution == nil || opView.LatestExecution.Status != "queued" {
		t.Fatalf("operation execution state = %+v / %+v", opView.ActiveExecution, opView.LatestExecution)
	}
	w = req(t, h, http.MethodGet, opPath+"/revisions/plan-http", testToken, nil)
	var revView struct {
		Execution *worksetusecase.ExecutionRef `json:"execution"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &revView); err != nil {
		t.Fatalf("decode revision: %v", err)
	}
	if revView.Execution == nil || revView.Execution.ExecutionID != execID {
		t.Fatalf("revision execution ref = %+v", revView.Execution)
	}

	// Detail and ownership scoping.
	w = req(t, h, http.MethodGet, opPath+"/executions/"+execID, testToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET execution = %d %s", w.Code, w.Body.String())
	}
	w = req(t, h, http.MethodGet, opPath+"/executions/exec-missing", testToken, nil)
	if w.Code != http.StatusNotFound || !containsCode(w.Body.Bytes(), "EXECUTION_NOT_FOUND") {
		t.Fatalf("unknown execution = %d %s", w.Code, w.Body.String())
	}
	w = req(t, h, http.MethodGet, "/api/v1/worksets/ws-other/operations/conversion/executions/"+execID, testToken, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workset execution = %d, want 404", w.Code)
	}
	w = req(t, h, http.MethodGet, opPath+"/executions/"+execID, "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated execution = %d, want 401", w.Code)
	}

	// Cancel is idempotent on a terminal session.
	w = req(t, h, http.MethodPost, opPath+"/executions/"+execID+"/cancel", testToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel = %d %s", w.Code, w.Body.String())
	}
	var canceled worksetusecase.ExecutionView
	if err := json.Unmarshal(w.Body.Bytes(), &canceled); err != nil {
		t.Fatalf("decode cancel: %v", err)
	}
	if canceled.Status != "canceled" {
		t.Fatalf("canceled status = %s", canceled.Status)
	}
	w = req(t, h, http.MethodPost, opPath+"/executions/"+execID+"/cancel", testToken, nil)
	if w.Code != http.StatusOK || !containsCode(w.Body.Bytes(), "canceled") {
		t.Fatalf("repeat cancel = %d %s", w.Code, w.Body.String())
	}

	// A terminal session no longer blocks the library.
	w = req(t, h, http.MethodDelete, "/api/v1/libraries/"+libID, testToken, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("library delete after terminal session = %d %s", w.Code, w.Body.String())
	}
}

func containsCode(body []byte, code string) bool {
	var envelope struct {
		Code    string `json:"code"`
		Status  string `json:"status"`
		Details []string
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	if envelope.Code == code || envelope.Status == code {
		return true
	}
	return slices.Contains(envelope.Details, code)
}
