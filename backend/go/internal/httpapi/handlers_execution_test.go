package httpapi //nolint:testpackage // white-box tests exercise unexported internals

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	tasksconversion "github.com/onsei/organizer/backend/internal/tasks/conversion"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// seedRevision writes a plan snapshot, its revision association and the
// promotion of the single-member workset, and returns the operation version.
func seedRevision(
	t *testing.T,
	repo *sqlite.Repository,
	svc worksetusecase.Service,
	libID, worksetID, planID string,
) int {
	t.Helper()
	draft, err := svc.GetDraft(t.Context(), worksetID, worksetusecase.OperationTypeConversion)
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}
	doc, err := tasksconversion.ParseDraft(string(draft.Document))
	if err != nil {
		t.Fatalf("parse draft: %v", err)
	}
	raw, hash, err := tasksconversion.MarshalDraft(doc)
	if err != nil {
		t.Fatalf("MarshalDraft: %v", err)
	}
	if err := sqlite.CreatePlanTx(
		repo.DB(), planID, "conversion", 1, "/music", "snap-"+planID, libID, worksetID,
		nil, nil, nil,
	); err != nil {
		t.Fatalf("CreatePlanTx: %v", err)
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
	return getOperationVersion(t, svc, worksetID)
}

func getOperationVersion(t *testing.T, svc worksetusecase.Service, worksetID string) int {
	t.Helper()
	view, err := svc.GetOperation(t.Context(), worksetID, worksetusecase.OperationTypeConversion)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	return view.Version
}

//nolint:funlen,gocyclo,cyclop // one HTTP lifecycle walk over the execution routes
func TestExecutionHTTPStartGatesAndShapes(t *testing.T) {
	h, repo := newWorksetServer(t)
	libID := seedLibrary(t, repo)
	seedMember(t, repo, "albumA")
	wsID := createRecord(t, h, libID, "create-exec-http")
	svc := worksetusecase.NewService(repo, 1, 1, []worksetusecase.Task{
		tasksconversion.New(t.TempDir()),
	}, nil, nil)
	opPath := "/api/v1/worksets/" + wsID + "/operations/conversion"
	execPath := opPath + "/revisions/plan-http/executions"

	// A promoted revision is directly executable; only the If-Match race gate
	// can refuse the start.
	version := seedRevision(t, repo, svc, libID, wsID, "plan-http")
	w := reqWithIdempotencyAndIfMatch(
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

	// First start: 202 with the created session; the session options come from
	// the frozen revision's draft (the seeded default: soft deletion).
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
	var startOptions struct {
		DeleteMode string `json:"delete_mode"`
	}
	if err := json.Unmarshal(startResp.Execution.Options, &startOptions); err != nil {
		t.Fatalf("decode session options: %v", err)
	}
	if !startResp.Created || startResp.Execution.Status != "queued" ||
		startOptions.DeleteMode != "soft" || startResp.Execution.PlanID != "plan-http" {
		t.Fatalf("start resp = %+v (options %q)", startResp, startOptions.DeleteMode)
	}
	execID := startResp.Execution.ExecutionID

	// The key replays the same session.
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

	// A second session for the same operation is refused while one is active.
	w = reqWithIdempotencyAndIfMatch(
		t,
		h,
		http.MethodPost,
		execPath,
		testToken,
		nil,
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
