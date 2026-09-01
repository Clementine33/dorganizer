package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// TestHTTPWorksetGenerationLoop boots the real backend, creates and scans a
// library, creates a workset from album folders, starts an async generation on
// the seeded balanced draft, polls the generation detail to completion,
// reviews the immutable revision, verifies unchanged-generation replay, then
// deletes the library and checks the orphan workspace is read-only.
func TestHTTPWorksetGenerationLoop(t *testing.T) {
	binPath := buildBackendBinary(t)
	dataDir := t.TempDir()
	rootPath := filepath.Join(dataDir, "music")
	mustWriteFile(t, filepath.Join(rootPath, "albumA", "test1.mp3"), "dummy audio")
	mustWriteFile(t, filepath.Join(rootPath, "albumA", "test1.flac"), "dummy audio")
	mustWriteFile(t, filepath.Join(rootPath, "albumA", "test2.mp3"), "dummy audio")
	mustWriteFile(t, filepath.Join(rootPath, "albumA", "test2.flac"), "dummy audio")

	const token = "e2e-token"
	proc := startBackendBinary(t, binPath, dataDir, token)
	base := fmt.Sprintf("http://127.0.0.1:%d", proc.httpPort)
	client := &http.Client{Timeout: 120 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Create + scan the library.
	libReq := map[string]string{"name": "E2E", "root_path": filepath.ToSlash(rootPath)}
	var lib struct{ ID string }
	if code := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodPost,
		"/api/v1/libraries",
		token,
		libReq,
		&lib,
	); code != http.StatusCreated {
		t.Fatalf("create lib: %d", code)
	}
	events := postScanSSE(t, client, ctx, base, lib.ID, token)
	if events["completed"] == nil {
		t.Fatal("scan SSE missing completed")
	}

	var folders struct {
		Folders []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"folders"`
	}
	if code := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodGet,
		"/api/v1/libraries/"+lib.ID+"/folders",
		token,
		nil,
		&folders,
	); code != http.StatusOK ||
		len(folders.Folders) == 0 {
		t.Fatalf("folders: code=%d n=%d", code, len(folders.Folders))
	}
	var albumFolderID string
	for _, f := range folders.Folders {
		if filepath.Base(filepath.FromSlash(f.Path)) == "albumA" {
			albumFolderID = f.ID
		}
	}
	if albumFolderID == "" {
		t.Fatal("albumA folder not found")
	}

	// Create a workset.
	wsReq := map[string]any{"library_id": lib.ID, "title": "夏季整理", "folder_ids": []string{albumFolderID}}
	var wsResp struct {
		Workset struct {
			WorksetID string `json:"workset_id"`
			Version   int    `json:"version"`
		} `json:"workset"`
		Created bool `json:"created"`
	}
	if code := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodPost,
		"/api/v1/worksets",
		token,
		wsReq,
		&wsResp,
	); code != http.StatusCreated {
		t.Fatalf("create workset: %d", code)
	}
	wsID := wsResp.Workset.WorksetID
	if wsID == "" {
		t.Fatal("empty workset id")
	}

	// Start generation on the seeded balanced draft. Idempotency-Key required.
	genReq := map[string]any{"expected_draft_version": 1}
	if code := doJSONWithHeaders(
		t,
		client,
		ctx,
		base,
		http.MethodPost,
		"/api/v1/worksets/"+wsID+"/revisions",
		token,
		map[string]string{"Idempotency-Key": "gen-key-1"},
		genReq,
		nil,
	); code != http.StatusAccepted {
		t.Fatalf("start generation: %d", code)
	}

	// Poll the workset detail until the active generation completes.
	var lastGenID string
	var genDetail struct {
		Status       string `json:"status"`
		RevisionID   string `json:"revision_id"`
		CompletedInt int    `json:"completed_roots"`
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		var wsDetail struct {
			ActiveGeneration *struct {
				GenerationID string `json:"generation_id"`
			} `json:"active_generation"`
			LatestGeneration *struct {
				GenerationID string `json:"generation_id"`
			} `json:"latest_generation"`
		}
		_ = doJSON(t, client, ctx, base, http.MethodGet, "/api/v1/worksets/"+wsID, token, nil, &wsDetail)
		if wsDetail.ActiveGeneration != nil {
			lastGenID = wsDetail.ActiveGeneration.GenerationID
		} else if wsDetail.LatestGeneration != nil {
			lastGenID = wsDetail.LatestGeneration.GenerationID
		}
		if lastGenID == "" {
			if time.Now().After(deadline) {
				t.Fatal("no generation id found")
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		code := doJSON(
			t,
			client,
			ctx,
			base,
			http.MethodGet,
			"/api/v1/worksets/"+wsID+"/planning-sessions/"+lastGenID,
			token,
			nil,
			&genDetail,
		)
		if code != http.StatusOK {
			t.Fatalf("generation detail: %d", code)
		}
		if genDetail.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("generation not completed in time: %+v", genDetail)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if genDetail.RevisionID == "" {
		t.Fatalf("completed generation missing revision_id: %+v", genDetail)
	}

	// Review the nested immutable revision.
	var rev struct {
		PlanID string `json:"plan_id"`
		Roots  []struct {
			RootIndex int    `json:"root_index"`
			RootPath  string `json:"root_path"`
		} `json:"roots"`
		ComponentRoots []struct {
			ComponentID string `json:"component_id"`
			RootIndex   int    `json:"root_index"`
		} `json:"component_roots"`
		Workflow struct {
			Summary struct {
				SummaryReason string `json:"summary_reason"`
			} `json:"summary"`
		} `json:"workflow"`
	}
	if code := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodGet,
		"/api/v1/worksets/"+wsID+"/revisions/"+genDetail.RevisionID,
		token,
		nil,
		&rev,
	); code != http.StatusOK {
		t.Fatalf("revision detail: %d", code)
	}
	if rev.Workflow.Summary.SummaryReason == "" {
		t.Fatalf("revision missing summary_reason: %+v", rev)
	}
	// Every component must map to a valid planning root (stable ownership for
	// batch grouping; albumA is root 0).
	if len(rev.ComponentRoots) == 0 {
		t.Fatalf("revision missing component_roots: %+v", rev)
	}
	rootIndexes := map[int]string{}
	for _, r := range rev.Roots {
		rootIndexes[r.RootIndex] = r.RootPath
	}
	for _, cr := range rev.ComponentRoots {
		if _, ok := rootIndexes[cr.RootIndex]; !ok {
			t.Fatalf("component %s maps to unknown root %d: %+v", cr.ComponentID, cr.RootIndex, rev.Roots)
		}
		if cr.ComponentID == "" {
			t.Fatalf("component_roots contains empty component_id: %+v", rev.ComponentRoots)
		}
	}

	// Unchanged generation returns created:false with the same revision.
	var replay struct {
		Created  bool `json:"created"`
		Revision struct {
			PlanID string `json:"plan_id"`
		} `json:"revision"`
	}
	code := doJSONWithHeaders(
		t,
		client,
		ctx,
		base,
		http.MethodPost,
		"/api/v1/worksets/"+wsID+"/revisions",
		token,
		map[string]string{"Idempotency-Key": "gen-key-2"},
		genReq,
		&replay,
	)
	if code != http.StatusOK || replay.Created || replay.Revision.PlanID != genDetail.RevisionID {
		t.Fatalf("replay: code=%d replay=%+v", code, replay)
	}

	// Delete the library (no active generation) succeeds and orphans the workset.
	if code := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodDelete,
		"/api/v1/libraries/"+lib.ID,
		token,
		nil,
		nil,
	); code != http.StatusNoContent {
		t.Fatalf("delete library: %d", code)
	}
	var orphan struct {
		PlanningState   string `json:"planning_state"`
		CurrentRevision *struct {
			ValidationState string `json:"validation_state"`
		} `json:"current_revision"`
	}
	if code := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodGet,
		"/api/v1/worksets/"+wsID,
		token,
		nil,
		&orphan,
	); code != http.StatusOK {
		t.Fatalf("orphan detail: %d", code)
	}
	if orphan.PlanningState != "orphaned" || orphan.CurrentRevision == nil ||
		orphan.CurrentRevision.ValidationState != "unavailable" {
		t.Fatalf("orphan: %+v", orphan)
	}

	// Orphaned workset rejects draft save.
	var draftResp struct {
		Code string `json:"code"`
	}
	code = doJSONWithHeaders(t, client, ctx, base, http.MethodPut,
		"/api/v1/worksets/"+wsID+"/draft", token, map[string]string{"If-Match": "1"},
		map[string]any{"workflow": map[string]any{
			"schema_version": 1,
			"steps": []any{
				map[string]any{
					"step_type": "reconcile_audio_outputs",
					"policy":    map[string]any{"kind": "preset", "name": "compact", "version": 1},
				},
			},
		}}, &draftResp)
	if code != http.StatusConflict || draftResp.Code != "ORPHANED_WORKSET" {
		t.Fatalf("orphan draft save: code=%d resp=%+v", code, draftResp)
	}
}

// doJSONWithHeaders is doJSON with extra request headers (If-Match, Idempotency-Key).
func doJSONWithHeaders(
	t *testing.T,
	client *http.Client,
	ctx context.Context,
	base, method, path, token string,
	headers map[string]string,
	body any,
	out any,
) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if out != nil {
		raw, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Logf("raw body: %s", raw)
			t.Fatalf("decode: %v", err)
		}
	}
	return resp.StatusCode
}
