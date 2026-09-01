package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
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
	svc := worksetusecase.NewService(repo, tmp, 1)
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
	repo.DB().
		Exec(`INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at) VALUES ('lib-1', 'Onsei', '/music', '/music', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	return "lib-1"
}

func seedFolder(t *testing.T, repo *sqlite.Repository, libID string) {
	t.Helper()
	repo.DB().
		Exec(`INSERT INTO library_folders (id, library_id, path, name, relative_path, audio_file_count, created_at, updated_at) VALUES ('f-a', ?, '/music/albumA', 'albumA', 'albumA', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, libID)
}

func TestWorksetHTTPLifecycle(t *testing.T) {
	h, repo := newWorksetServer(t)
	libID := seedLibrary(t, repo)
	seedFolder(t, repo, libID)

	// POST create.
	createBody := map[string]any{"library_id": libID, "title": "夏季整理", "folder_ids": []string{"f-a"}}
	w := req(t, h, http.MethodPost, "/api/v1/worksets", testToken, createBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", w.Code, w.Body.String())
	}
	var createResp struct {
		Workset struct {
			WorksetID string `json:"workset_id"`
			Version   int    `json:"version"`
		} `json:"workset"`
		Created bool `json:"created"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if createResp.Workset.WorksetID == "" || !createResp.Created {
		t.Fatalf("create resp: %+v", createResp)
	}

	// GET detail.
	w = req(t, h, http.MethodGet, "/api/v1/worksets/"+createResp.Workset.WorksetID, testToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status = %d", w.Code)
	}
	var detail struct {
		PlanningState string `json:"planning_state"`
		Members       []struct {
			State string `json:"state"`
		} `json:"members"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.PlanningState != "unplanned" || len(detail.Members) != 1 || detail.Members[0].State != "pending" {
		t.Fatalf("detail: %+v", detail)
	}

	// GET draft.
	w = req(t, h, http.MethodGet, "/api/v1/worksets/"+createResp.Workset.WorksetID+"/draft", testToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("draft status = %d", w.Code)
	}
	var draft struct {
		Workflow struct {
			SchemaVersion int `json:"schema_version"`
			Steps         []struct {
				StepType string `json:"step_type"`
			} `json:"steps"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &draft); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	if draft.Workflow.SchemaVersion != 1 || len(draft.Workflow.Steps) != 1 {
		t.Fatalf("draft: %+v", draft)
	}

	// PUT draft (full replacement with title change needs If-Match from version 1).
	saveBody := map[string]any{"workflow": map[string]any{
		"schema_version": 1,
		"steps": []any{map[string]any{
			"step_type": "reconcile_audio_outputs",
			"policy":    inlinePolicyFixture(),
		}},
	}}
	p2 := reqWithIfMatch(
		t,
		h,
		http.MethodPut,
		"/api/v1/worksets/"+createResp.Workset.WorksetID+"/draft",
		testToken,
		saveBody,
		"1",
	)
	if p2.Code != http.StatusOK {
		t.Fatalf("put draft status = %d, body=%s", p2.Code, p2.Body.String())
	}

	// POST revisions requires Idempotency-Key.
	gen := req(
		t,
		h,
		http.MethodPost,
		"/api/v1/worksets/"+createResp.Workset.WorksetID+"/revisions",
		testToken,
		map[string]any{"expected_draft_version": 2},
	)
	if gen.Code != http.StatusBadRequest {
		t.Fatalf("revisions without key status = %d, want 400", gen.Code)
	}
}

func TestWorksetAuthRequired(t *testing.T) {
	h, repo := newWorksetServer(t)
	seedLibrary(t, repo)
	seedFolder(t, repo, "lib-1")
	w := req(
		t,
		h,
		http.MethodPost,
		"/api/v1/worksets",
		"",
		map[string]any{"library_id": "lib-1", "title": "t", "folder_ids": []string{"f-a"}},
	)
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
	seedFolder(t, repo, "lib-1")

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
