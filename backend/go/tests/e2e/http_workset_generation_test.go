package e2e //nolint:testpackage // white-box tests exercise unexported internals

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestHTTPWorksetGenerationLoop boots the real backend, creates and scans a
// library, creates its current conversion record from the scanned album
// folders, starts an async generation on the seeded balanced draft, polls the
// generation detail to completion, reviews the immutable plan, verifies
// unchanged-generation replay, then deletes the library and checks that its
// record went with it while the media stayed on disk.
//
//nolint:gocognit,gocyclo,cyclop,funlen // e2e generation loop
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

	var dirs struct {
		Dirs []struct {
			RelPath string `json:"rel_path"`
			Path    string `json:"path"`
		} `json:"dirs"`
	}
	if code := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodGet,
		"/api/v1/libraries/"+lib.ID+"/dirs",
		token,
		nil,
		&dirs,
	); code != http.StatusOK ||
		len(dirs.Dirs) == 0 {
		t.Fatalf("dirs: code=%d n=%d", code, len(dirs.Dirs))
	}
	albumRel := ""
	for _, d := range dirs.Dirs {
		if d.RelPath == "albumA" {
			albumRel = d.RelPath
		}
	}
	if albumRel == "" {
		t.Fatal("albumA not found in the overview listing")
	}

	// Create the library's current conversion record.
	wsReq := map[string]any{"folder_paths": []string{albumRel}}
	var wsResp struct {
		Workset struct {
			WorksetID string `json:"workset_id"`
			Version   int    `json:"version"`
		} `json:"workset"`
		Created bool `json:"created"`
	}
	if code := doJSONWithHeaders(
		t,
		client,
		ctx,
		base,
		http.MethodPut,
		"/api/v1/libraries/"+lib.ID+"/operations/conversion/current",
		token,
		map[string]string{"Idempotency-Key": "create-e2e-1"},
		wsReq,
		&wsResp,
	); code != http.StatusCreated {
		t.Fatalf("create record: %d", code)
	}
	wsID := wsResp.Workset.WorksetID
	if wsID == "" {
		t.Fatal("empty workset id")
	}

	wsPath := "/api/v1/worksets/" + wsID
	opPath := wsPath + "/operations/conversion"

	// Start generation on the seeded draft. Idempotency-Key and If-Match (the
	// operation version) are both required.
	if code := doJSONWithHeaders(
		t,
		client,
		ctx,
		base,
		http.MethodPost,
		opPath+"/revisions",
		token,
		map[string]string{"Idempotency-Key": "gen-key-1", "If-Match": "1"},
		nil,
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
			Operations []struct {
				OperationType    string `json:"operation_type"`
				ActiveGeneration *struct {
					GenerationID string `json:"generation_id"`
				} `json:"active_generation"`
				LatestGeneration *struct {
					GenerationID string `json:"generation_id"`
				} `json:"latest_generation"`
			} `json:"operations"`
		}
		_ = doJSON(t, client, ctx, base, http.MethodGet, wsPath, token, nil, &wsDetail)
		if len(wsDetail.Operations) != 1 || wsDetail.Operations[0].OperationType != "conversion" {
			t.Fatalf("workset operations: %+v", wsDetail.Operations)
		}
		if wsDetail.Operations[0].ActiveGeneration != nil {
			lastGenID = wsDetail.Operations[0].ActiveGeneration.GenerationID
		} else if wsDetail.Operations[0].LatestGeneration != nil {
			lastGenID = wsDetail.Operations[0].LatestGeneration.GenerationID
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
			opPath+"/planning-sessions/"+lastGenID,
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
		Summary struct {
			SummaryReason string `json:"summary_reason"`
		} `json:"summary"`
		Task struct {
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		} `json:"task"`
	}
	if code := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodGet,
		opPath+"/revisions/"+genDetail.RevisionID,
		token,
		nil,
		&rev,
	); code != http.StatusOK {
		t.Fatalf("revision detail: %d", code)
	}
	if rev.Summary.SummaryReason == "" {
		t.Fatalf("revision missing summary_reason: %+v", rev)
	}
	if rev.Task.Kind != "conversion" || len(rev.Task.Payload) == 0 {
		t.Fatalf("revision missing task payload: %+v", rev)
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
		opPath+"/revisions",
		token,
		map[string]string{"Idempotency-Key": "gen-key-2", "If-Match": "1"},
		nil,
		&replay,
	)
	if code != http.StatusOK || replay.Created || replay.Revision.PlanID != genDetail.RevisionID {
		t.Fatalf("replay: code=%d replay=%+v", code, replay)
	}

	// Delete the library (no active generation) succeeds and orphans the workset.
	if delCode := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodDelete,
		"/api/v1/libraries/"+lib.ID,
		token,
		nil,
		nil,
	); delCode != http.StatusNoContent {
		t.Fatalf("delete library: %d", delCode)
	}
	// The library is gone with its record: the current-record read answers
	// "none", the old record is not addressable, and its plans went with it
	// (spec L1). The media on disk is untouched.
	var orphan struct {
		Operations []struct {
			OperationType string `json:"operation_type"`
		} `json:"operations"`
	}
	if getCode := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodGet,
		wsPath,
		token,
		nil,
		&orphan,
	); getCode != http.StatusNotFound {
		t.Fatalf("a deleted library's record must not resolve: %d", getCode)
	}
	var current struct {
		Workset *json.RawMessage `json:"workset"`
	}
	if getCode := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodGet,
		"/api/v1/libraries/"+lib.ID+"/operations/conversion/current",
		token,
		nil,
		&current,
	); getCode != http.StatusNotFound {
		t.Fatalf("a deleted library must not answer for a record: %d", getCode)
	}

	// Deleting the library moved no media: the album folders are still there.
	for _, name := range []string{"test1.mp3", "test1.flac"} {
		if _, statErr := os.Stat(filepath.Join(rootPath, "albumA", name)); statErr != nil {
			t.Fatalf("library deletion touched media: %v", statErr)
		}
	}

	// The removed record rejects writes by not existing.
	var draftResp struct {
		Code string `json:"code"`
	}
	code = doJSONWithHeaders(t, client, ctx, base, http.MethodPut,
		opPath+"/draft", token, map[string]string{"If-Match": "1"},
		map[string]any{
			"schema_version":  1,
			"mode":            "available_sources",
			"classifier_tags": []string{"SEなし"},
			"matched":         map[string]any{"lossless": map[string]any{"codec": "wav"}},
			"unmatched":       map[string]any{"lossless": map[string]any{"codec": "wav"}},
			"members":         []any{},
		}, &draftResp)
	if code != http.StatusNotFound {
		t.Fatalf("a deleted record must reject writes: code=%d resp=%+v", code, draftResp)
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
