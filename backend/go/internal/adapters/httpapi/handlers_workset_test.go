package httpapi //nolint:testpackage // white-box tests exercise unexported internals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	tasksconversion "github.com/onsei/organizer/backend/internal/tasks/conversion"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

const testToken = "test-token"

func newWorksetServer(t *testing.T) (http.Handler, *sqlite.Repository) {
	t.Helper()
	tmp := t.TempDir()
	repo, err := sqlite.NewRepository(tmp + "/test.db")
	if err != nil {
		t.Fatalf("repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	cfg := `{"prune":{"literal_tags":["SEなし"]}}`
	if err := os.WriteFile(filepath.Join(tmp, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	svc := worksetusecase.NewService(repo, 1, 1, []worksetusecase.Task{
		tasksconversion.New(tmp),
	}, nil, nil)
	handler := NewServer(Dependencies{
		Repo:           repo,
		ConfigDir:      tmp,
		Token:          testToken,
		CORSOrigins:    []string{"http://localhost:5173"},
		WorksetService: svc,
	})
	return handler, repo
}

func req(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return reqWithIfMatch(t, h, method, path, token, body, "")
}

func reqWithIfMatch(
	t *testing.T,
	h http.Handler,
	method, path, token string,
	body any,
	ifMatch string,
) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rdr)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if ifMatch != "" {
		r.Header.Set("If-Match", ifMatch)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func seedLibrary(t *testing.T, repo *sqlite.Repository) string {
	t.Helper()
	_, _ = repo.DB().
		Exec(`INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at) VALUES ('lib-1', 'Onsei', '/music', '/music', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	return "lib-1"
}

// seedMember records one library member in the scanned inventory — the
// directory row and an audio row beneath it — which is what a record's scope
// is resolved against.
func seedMember(t *testing.T, repo *sqlite.Repository, rel string) {
	t.Helper()
	_ = rel
	for _, stmt := range []string{
		`INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format)
		 VALUES ('/music/albumA', '/music', '/music', 'albumA', 1, 0, 0, '')`,
		`INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format)
		 VALUES ('/music/albumA/01.flac', '/music', '/music/albumA', '01.flac', 0, 1024, 0, 'flac')`,
	} {
		if _, err := repo.DB().Exec(stmt); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
}

// createRecord creates the current conversion record of a library through the
// API and returns its id, failing the test when the creation is refused.
func createRecord(t *testing.T, h http.Handler, libID, key string) string {
	t.Helper()
	w := reqWithIdempotency(t, h, http.MethodPut, recordsPath(libID), testToken, map[string]any{
		"folder_paths": []string{"albumA"},
	}, key)
	if w.Code != http.StatusCreated {
		t.Fatalf("create record: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Workset struct {
			WorksetID string `json:"workset_id"`
		} `json:"workset"`
		Created  bool `json:"created"`
		Recorded int  `json:"recorded"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if !created.Created || created.Workset.WorksetID == "" || created.Recorded != 1 {
		t.Fatalf("create record resp: %+v", created)
	}
	return created.Workset.WorksetID
}

// recordsPath is the current-record route of one library's conversion.
func recordsPath(libID string) string {
	return "/api/v1/libraries/" + libID + "/operations/conversion/current"
}

// draftDocumentFixture is a complete operation draft document.
func draftDocumentFixture() map[string]any {
	profile := map[string]any{
		"lossless": map[string]any{"codec": "wav"},
		"encoded":  map[string]any{"codec": "mp3", "quality": map[string]any{"kind": "bitrate", "bitrate": 320}},
	}
	return map[string]any{
		"schema_version":  1,
		"mode":            "available_sources",
		"classifier_tags": []string{"SEなし"},
		"matched":         profile,
		"unmatched":       profile,
		"members":         []any{},
	}
}

//nolint:cyclop,gocognit,gocyclo,funlen // one HTTP lifecycle walk covering every operation route
func TestWorksetHTTPLifecycle(t *testing.T) {
	h, repo := newWorksetServer(t)
	libID := seedLibrary(t, repo)
	seedMember(t, repo, "albumA")

	// The library has no record yet; the current-record read says so without
	// making it an error.
	var none struct {
		Workset *json.RawMessage `json:"workset"`
	}
	empty := req(t, h, http.MethodGet, recordsPath(libID), testToken, nil)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty current record status = %d", empty.Code)
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &none); err != nil || none.Workset != nil {
		t.Fatalf("empty current record body = %s (err=%v)", empty.Body.String(), err)
	}

	// PUT create: the record of this (library, operation) pair, with the scope
	// it actually recorded.
	createBody := map[string]any{"folder_paths": []string{"albumA"}}
	w := reqWithIdempotency(t, h, http.MethodPut, recordsPath(libID), testToken, createBody, "create-http-1")
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", w.Code, w.Body.String())
	}
	var createResp struct {
		Workset struct {
			WorksetID string `json:"workset_id"`
			Version   int    `json:"version"`
		} `json:"workset"`
		Created  bool `json:"created"`
		Recorded int  `json:"recorded"`
		Skipped  []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if createResp.Workset.WorksetID == "" || !createResp.Created || createResp.Recorded != 1 {
		t.Fatalf("create resp: %+v", createResp)
	}
	// A retried create answers with the same record instead of a second one.
	replay := reqWithIdempotency(t, h, http.MethodPut, recordsPath(libID), testToken, createBody, "create-http-1")
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status = %d, body=%s", replay.Code, replay.Body.String())
	}
	var replayResp struct {
		Workset struct {
			WorksetID string `json:"workset_id"`
		} `json:"workset"`
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayResp); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if replayResp.Created || replayResp.Workset.WorksetID != createResp.Workset.WorksetID {
		t.Fatalf("replay resp: %+v", replayResp)
	}
	// The current-record read answers the same record.
	current := req(t, h, http.MethodGet, recordsPath(libID), testToken, nil)
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), createResp.Workset.WorksetID) {
		t.Fatalf("current record = %d %s", current.Code, current.Body.String())
	}
	// The creation carries no title: the caller is not asked to name a record.
	if strings.Contains(w.Body.String(), `"title":""`) {
		t.Fatalf("a record without a title must still be created with a usable one: %s", w.Body.String())
	}
	wsPath := "/api/v1/worksets/" + createResp.Workset.WorksetID
	opPath := wsPath + "/operations/conversion"

	// The workset view carries its operations; planning state is operation-scoped.
	w = req(t, h, http.MethodGet, wsPath, testToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status = %d", w.Code)
	}
	var detail struct {
		Members    []map[string]any `json:"members"`
		Operations []struct {
			OperationType string `json:"operation_type"`
			PlanningState string `json:"planning_state"`
			Version       int    `json:"version"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.Members) != 1 {
		t.Fatalf("detail members: %+v", detail.Members)
	}
	if len(detail.Operations) != 1 || detail.Operations[0].OperationType != "conversion" ||
		detail.Operations[0].PlanningState != "unplanned" || detail.Operations[0].Version != 1 {
		t.Fatalf("detail operations: %+v", detail.Operations)
	}
	if _, leaked := detail.Members[0]["state"]; leaked {
		t.Fatal("member coverage is operation state, not member state")
	}

	// GET operation, draft.
	if got := req(t, h, http.MethodGet, opPath, testToken, nil); got.Code != http.StatusOK {
		t.Fatalf("operation status = %d body=%s", got.Code, got.Body.String())
	}
	w = req(t, h, http.MethodGet, opPath+"/draft", testToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("draft status = %d body=%s", w.Code, w.Body.String())
	}
	var draft struct {
		Version int `json:"version"`
		Task    struct {
			Kind          string `json:"kind"`
			SchemaVersion int    `json:"schema_version"`
			Payload       struct {
				SchemaVersion  int      `json:"schema_version"`
				Mode           string   `json:"mode"`
				ClassifierTags []string `json:"classifier_tags"`
				Members        []any    `json:"members"`
			} `json:"payload"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	if draft.Version != 1 || draft.Task.Kind != "conversion" || draft.Task.SchemaVersion != 1 ||
		draft.Task.Payload.SchemaVersion != 1 || draft.Task.Payload.Mode != "available_sources" {
		t.Fatalf("draft: %+v", draft)
	}
	if len(draft.Task.Payload.ClassifierTags) != 1 || len(draft.Task.Payload.Members) != 0 {
		t.Fatalf("seeded draft must be sparse: %+v", draft.Task.Payload)
	}

	// PUT draft requires If-Match on the operation version.
	saveBody := draftDocumentFixture()
	noMatch := req(t, h, http.MethodPut, opPath+"/draft", testToken, saveBody)
	if noMatch.Code != http.StatusBadRequest {
		t.Fatalf("draft save without If-Match = %d, want 400", noMatch.Code)
	}
	saved := reqWithIfMatch(t, h, http.MethodPut, opPath+"/draft", testToken, saveBody, "1")
	if saved.Code != http.StatusOK {
		t.Fatalf("put draft status = %d, body=%s", saved.Code, saved.Body.String())
	}
	var savedOp struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &savedOp); err != nil {
		t.Fatalf("decode saved operation: %v", err)
	}
	if savedOp.Version != 2 {
		t.Fatalf("operation version after save = %d, want 2", savedOp.Version)
	}
	// A stale If-Match on the operation version conflicts.
	stale := reqWithIfMatch(t, h, http.MethodPut, opPath+"/draft", testToken, saveBody, "1")
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale draft save = %d, want 409", stale.Code)
	}

	// POST revisions requires both Idempotency-Key and If-Match.
	noKey := reqWithIfMatch(t, h, http.MethodPost, opPath+"/revisions", testToken, nil, "2")
	if noKey.Code != http.StatusBadRequest {
		t.Fatalf("revisions without key status = %d, want 400", noKey.Code)
	}
	noVersion := reqWithIdempotency(t, h, http.MethodPost, opPath+"/revisions", testToken, nil, "gen-http-1")
	if noVersion.Code != http.StatusBadRequest {
		t.Fatalf("revisions without If-Match status = %d, want 400", noVersion.Code)
	}
	started := reqWithIdempotencyAndIfMatch(
		t,
		h,
		http.MethodPost,
		opPath+"/revisions",
		testToken,
		nil,
		"gen-http-1",
		"2",
	)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start generation status = %d, body=%s", started.Code, started.Body.String())
	}
	var startedBody struct {
		Created    bool `json:"created"`
		Generation struct {
			GenerationID  string `json:"generation_id"`
			OperationType string `json:"operation_type"`
		} `json:"generation"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &startedBody); err != nil {
		t.Fatalf("decode generation: %v", err)
	}
	if !startedBody.Created || startedBody.Generation.OperationType != "conversion" {
		t.Fatalf("generation body: %+v", startedBody)
	}

	// The session is addressable through its operation only.
	genPath := opPath + "/planning-sessions/" + startedBody.Generation.GenerationID
	if got := req(t, h, http.MethodGet, genPath, testToken, nil); got.Code != http.StatusOK {
		t.Fatalf("session detail = %d body=%s", got.Code, got.Body.String())
	}
	wrongOp := req(t, h, http.MethodGet,
		wsPath+"/operations/rename/planning-sessions/"+startedBody.Generation.GenerationID, testToken, nil)
	if wrongOp.Code != http.StatusNotFound {
		t.Fatalf("session through another operation = %d, want 404", wrongOp.Code)
	}
	if got := req(t, h, http.MethodGet, wsPath+"/operations/rename", testToken, nil); got.Code != http.StatusNotFound {
		t.Fatalf("unknown operation = %d, want 404", got.Code)
	}

	// Cancel the queued session; a canceled session publishes nothing.
	if got := req(t, h, http.MethodPost, genPath+"/cancel", testToken, nil); got.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", got.Code, got.Body.String())
	}
	// There is no route to a record's revision history: a record keeps the
	// current plan only, and the old list route is gone (ADR 0001 §2).
	if got := req(t, h, http.MethodGet, opPath+"/revisions", testToken, nil); got.Code == http.StatusOK {
		t.Fatalf("a record's revision history must not be readable: %s", got.Body.String())
	}

	// Renaming uses the workset metadata version only.
	renamed := reqWithIfMatch(t, h, http.MethodPatch, wsPath, testToken,
		map[string]any{"title": "改名"}, strconv.Itoa(createResp.Workset.Version))
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename status = %d body=%s", renamed.Code, renamed.Body.String())
	}
	afterRename := reqWithIfMatch(t, h, http.MethodPut, opPath+"/draft", testToken, saveBody, "2")
	if afterRename.Code != http.StatusOK {
		t.Fatalf(
			"rename must not invalidate the operation version: %d body=%s",
			afterRename.Code,
			afterRename.Body.String(),
		)
	}
}

// reqWithIdempotency sends a request with an Idempotency-Key header.
func reqWithIdempotency(
	t *testing.T,
	h http.Handler,
	method, path, token string,
	body any,
	idemKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	return reqWithIdempotencyAndIfMatch(t, h, method, path, token, body, idemKey, "")
}

func reqWithIdempotencyAndIfMatch(
	t *testing.T,
	h http.Handler,
	method, path, token string,
	body any,
	idemKey, ifMatch string,
) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rdr)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if idemKey != "" {
		r.Header.Set("Idempotency-Key", idemKey)
	}
	if ifMatch != "" {
		r.Header.Set("If-Match", ifMatch)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestWorksetAuthRequired(t *testing.T) {
	h, repo := newWorksetServer(t)
	seedLibrary(t, repo)
	seedMember(t, repo, "albumA")
	w := reqWithIdempotency(t, h, http.MethodPut, recordsPath("lib-1"), "",
		map[string]any{"folder_paths": []string{"albumA"}}, "create-auth")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no auth status = %d, want 401", w.Code)
	}
}

// TestPolicySlotsListAndUpdate verifies the fixed-three slot endpoints: fresh
// databases list exactly three empty slots in order, PUT validates name and
// policy, and the saved policy round-trips.
func TestPolicySlotsListAndUpdate(t *testing.T) {
	h, repo := newWorksetServer(t)
	seedLibrary(t, repo)
	seedMember(t, repo, "albumA")

	w := req(t, h, http.MethodGet, "/api/v1/policy-slots", testToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("slots status = %d", w.Code)
	}
	var list struct {
		Slots []struct {
			Slot   int             `json:"slot"`
			Name   string          `json:"name"`
			Policy json.RawMessage `json:"policy"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode slots: %v", err)
	}
	if len(list.Slots) != 3 {
		t.Fatalf("slots = %d, want 3", len(list.Slots))
	}
	for i, s := range list.Slots {
		if s.Slot != i+1 || s.Name != "" || string(s.Policy) != "null" {
			t.Fatalf("fresh slot %d = %+v, want empty slot", i+1, s)
		}
	}

	// PUT out-of-range slot is rejected.
	w = req(
		t,
		h,
		http.MethodPut,
		"/api/v1/policy-slots/4",
		testToken,
		map[string]any{"name": "x", "policy": map[string]any{}},
	)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "INVALID_SLOT") {
		t.Fatalf("slot 4 = %d %s, want 400 INVALID_SLOT", w.Code, w.Body.String())
	}

	// PUT invalid regex policy is rejected.
	badPolicy := map[string]any{
		"schema_version":  1,
		"classifier_tags": []string{},
		"matched":         map[string]any{"lossless": map[string]any{"codec": "wav"}},
		"unmatched":       map[string]any{"lossless": map[string]any{"codec": "wav"}},
	}
	w = req(
		t,
		h,
		http.MethodPut,
		"/api/v1/policy-slots/1",
		testToken,
		map[string]any{"name": "s1", "policy": badPolicy},
	)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "INVALID_POLICY") {
		t.Fatalf("empty tags = %d %s, want 400 INVALID_POLICY", w.Code, w.Body.String())
	}

	// PUT a valid slot then verify the round-trip.
	goodPolicy := map[string]any{
		"schema_version":  1,
		"classifier_tags": []string{"SEなし", "se_nashi"},
		"matched":         map[string]any{"lossless": map[string]any{"codec": "wav"}},
		"unmatched": map[string]any{
			"encoded": map[string]any{"codec": "mp3", "quality": map[string]any{"kind": "bitrate", "bitrate": 320}},
		},
	}
	w = req(
		t,
		h,
		http.MethodPut,
		"/api/v1/policy-slots/1",
		testToken,
		map[string]any{"name": "默认", "policy": goodPolicy},
	)
	if w.Code != http.StatusOK {
		t.Fatalf("put slot 1 = %d, body=%s", w.Code, w.Body.String())
	}
	w = req(t, h, http.MethodGet, "/api/v1/policy-slots", testToken, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode slots 2: %v", err)
	}
	if list.Slots[0].Name != "默认" || len(list.Slots[0].Policy) == 0 || string(list.Slots[0].Policy) == "null" {
		t.Fatalf("slot 1 after PUT = %+v", list.Slots[0])
	}
	if list.Slots[1].Name != "" || string(list.Slots[1].Policy) != "null" {
		t.Fatalf("slot 2 must stay empty: %+v", list.Slots[1])
	}

	// The seeded workset draft must be a complete inline policy (self-contained).
	if _, err := repo.GetPolicySlot(1); err != nil {
		t.Fatalf("get slot: %v", err)
	}
}

func TestClassifierTagsHTTPAPI(t *testing.T) {
	h, _ := newWorksetServer(t)

	// 1. Initial list: has default tags from temp config, 0 custom tags
	w := req(t, h, http.MethodGet, "/api/v1/classifier-tags", testToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list tags status = %d", w.Code)
	}
	var res struct {
		DefaultTags []string `json:"default_tags"`
		CustomTags  []struct {
			ID        int64  `json:"id"`
			Tag       string `json:"tag"`
			CreatedAt string `json:"created_at"`
		} `json:"custom_tags"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode tag library: %v", err)
	}
	if len(res.DefaultTags) == 0 || res.DefaultTags[0] != "SEなし" {
		t.Fatalf("expected default tags [SEなし], got %+v", res.DefaultTags)
	}
	if len(res.CustomTags) != 0 {
		t.Fatalf("expected 0 custom tags initially, got %d", len(res.CustomTags))
	}

	// 2. Add custom tag
	w = req(t, h, http.MethodPost, "/api/v1/classifier-tags", testToken, map[string]any{"tag": "  效果音なし  "})
	if w.Code != http.StatusCreated {
		t.Fatalf("create tag status = %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		ID  int64  `json:"id"`
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created tag: %v", err)
	}
	if created.Tag != "效果音なし" || created.ID <= 0 {
		t.Fatalf("unexpected created tag: %+v", created)
	}

	// 3. Duplicate is idempotent
	w = req(t, h, http.MethodPost, "/api/v1/classifier-tags", testToken, map[string]any{"tag": "效果音なし"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create duplicate tag status = %d", w.Code)
	}

	// 4. Verify in list
	w = req(t, h, http.MethodGet, "/api/v1/classifier-tags", testToken, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode tag library 2: %v", err)
	}
	if len(res.CustomTags) != 1 || res.CustomTags[0].Tag != "效果音なし" {
		t.Fatalf("expected 1 custom tag, got %+v", res.CustomTags)
	}

	// 5. Delete custom tag
	delURL := fmt.Sprintf("/api/v1/classifier-tags/%d", created.ID)
	w = req(t, h, http.MethodDelete, delURL, testToken, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", w.Code, w.Body.String())
	}

	// 6. Verify list is empty
	w = req(t, h, http.MethodGet, "/api/v1/classifier-tags", testToken, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if len(res.CustomTags) != 0 {
		t.Fatalf("expected 0 custom tags after delete, got %d", len(res.CustomTags))
	}
}
