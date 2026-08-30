package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func reqWithIfMatch(t *testing.T, h http.Handler, method, path, token string, body any, ifMatch string) *httptest.ResponseRecorder {
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
	repo.DB().Exec(`INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at) VALUES ('lib-1', 'Onsei', '/music', '/music', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	return "lib-1"
}

func seedFolder(t *testing.T, repo *sqlite.Repository, libID string) {
	t.Helper()
	repo.DB().Exec(`INSERT INTO library_folders (id, library_id, path, name, relative_path, audio_file_count, created_at, updated_at) VALUES ('f-a', ?, '/music/albumA', 'albumA', 'albumA', 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, libID)
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
			"policy":    map[string]any{"kind": "preset", "name": "compact", "version": 1},
		}},
	}}
	p2 := reqWithIfMatch(t, h, http.MethodPut, "/api/v1/worksets/"+createResp.Workset.WorksetID+"/draft", testToken, saveBody, "1")
	if p2.Code != http.StatusOK {
		t.Fatalf("put draft status = %d, body=%s", p2.Code, p2.Body.String())
	}

	// POST revisions requires Idempotency-Key.
	gen := req(t, h, http.MethodPost, "/api/v1/worksets/"+createResp.Workset.WorksetID+"/revisions", testToken, map[string]any{"expected_draft_version": 2})
	if gen.Code != http.StatusBadRequest {
		t.Fatalf("revisions without key status = %d, want 400", gen.Code)
	}
}

func TestWorksetAuthRequired(t *testing.T) {
	h, repo := newWorksetServer(t)
	seedLibrary(t, repo)
	seedFolder(t, repo, "lib-1")
	w := req(t, h, http.MethodPost, "/api/v1/worksets", "", map[string]any{"library_id": "lib-1", "title": "t", "folder_ids": []string{"f-a"}})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no auth status = %d, want 401", w.Code)
	}
}

// TestWorkflowPresetsList verifies the read-only preset registry endpoint and
// the round-trip contract the frontend depends on: a preset policy fetched
// here, submitted as an inline policy source, must be accepted by SaveDraft.
func TestWorkflowPresetsList(t *testing.T) {
	h, repo := newWorksetServer(t)
	seedLibrary(t, repo)
	seedFolder(t, repo, "lib-1")

	w := req(t, h, http.MethodGet, "/api/v1/workflow-presets", testToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("presets status = %d", w.Code)
	}
	var list struct {
		Presets []struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
			Policy  struct {
				SchemaVersion int `json:"schema_version"`
				Classifier    struct {
					Name    string `json:"name"`
					Version int    `json:"version"`
				} `json:"classifier"`
				Matched struct {
					Lossless *struct {
						Codec   string `json:"codec"`
						Quality *struct {
							Kind    string `json:"kind"`
							Bitrate int    `json:"bitrate"`
						} `json:"quality"`
					} `json:"lossless"`
					Encoded *struct {
						Codec   string `json:"codec"`
						Quality *struct {
							Kind    string `json:"kind"`
							Bitrate int    `json:"bitrate"`
						} `json:"quality"`
					} `json:"encoded"`
				} `json:"matched"`
			} `json:"policy"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode presets: %v", err)
	}
	if len(list.Presets) != 3 {
		t.Fatalf("presets = %d, want 3 (balanced/compact/archive)", len(list.Presets))
	}
	byName := map[string]int{}
	for i, p := range list.Presets {
		byName[p.Name] = i
		if p.Policy.SchemaVersion != 1 {
			t.Fatalf("preset %s schema_version = %d", p.Name, p.Policy.SchemaVersion)
		}
		if p.Policy.Classifier.Name != "effect-direction" || p.Policy.Classifier.Version != 1 {
			t.Fatalf("preset %s classifier = %+v", p.Name, p.Policy.Classifier)
		}
	}
	if _, ok := byName["balanced"]; !ok {
		t.Fatalf("balanced preset missing: %+v", byName)
	}

	// Round-trip: submit the balanced preset's resolved policy as an inline
	// policy source for a workset draft; the backend must accept it.
	balanced := list.Presets[byName["balanced"]]
	inlinePolicy := map[string]any{
		"schema_version": balanced.Policy.SchemaVersion,
		"classifier": map[string]any{
			"name":    balanced.Policy.Classifier.Name,
			"version": balanced.Policy.Classifier.Version,
		},
		"matched":   balancedPolicyProfile(balanced.Policy.Matched),
		"unmatched": map[string]any{},
	}
	// Fetch the full policy JSON generically so unmatched is preserved too.
	var raw struct {
		Presets []struct {
			Name   string          `json:"name"`
			Policy json.RawMessage `json:"policy"`
		} `json:"presets"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	var fullPolicy map[string]any
	for _, p := range raw.Presets {
		if p.Name == "balanced" {
			if err := json.Unmarshal(p.Policy, &fullPolicy); err != nil {
				t.Fatalf("decode balanced policy: %v", err)
			}
		}
	}
	inlinePolicy = fullPolicy

	ws := req(t, h, http.MethodPost, "/api/v1/worksets", testToken, map[string]any{"library_id": "lib-1", "title": "t", "folder_ids": []string{"f-a"}})
	if ws.Code != http.StatusCreated {
		t.Fatalf("create workset: %d", ws.Code)
	}
	var created struct {
		Workset struct {
			WorksetID string `json:"workset_id"`
		} `json:"workset"`
	}
	_ = json.Unmarshal(ws.Body.Bytes(), &created)

	saveBody := map[string]any{"workflow": map[string]any{
		"schema_version": 1,
		"steps": []any{map[string]any{
			"step_type": "reconcile_audio_outputs",
			"policy":    map[string]any{"kind": "inline", "policy": inlinePolicy},
		}},
	}}
	save := reqWithIfMatch(t, h, http.MethodPut, "/api/v1/worksets/"+created.Workset.WorksetID+"/draft", testToken, saveBody, "1")
	if save.Code != http.StatusOK {
		t.Fatalf("inline preset round-trip save = %d, body=%s", save.Code, save.Body.String())
	}
}

// balancedPolicyProfile converts the decoded matched profile back into a
// generic map for the inline submission.
func balancedPolicyProfile(p any) map[string]any {
	m, ok := p.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return m
}
