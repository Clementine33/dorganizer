package e2e //nolint:testpackage // shares the backend-binary e2e harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// mustFFmpegEncode renders a short sine tone with real ffmpeg.
//
//nolint:gosec // fixed ffmpeg binary with test-controlled arguments
func mustFFmpegEncode(t *testing.T, args ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "ffmpeg", append([]string{"-nostdin", "-v", "error", "-y"}, args...)...)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, out)
	}
}

// executionView mirrors the execution detail payload for the assertions.
type executionView struct {
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
	PlanID      string `json:"plan_id"`
	Options     struct {
		DeleteMode string `json:"delete_mode"`
	} `json:"options"`
	TotalComponents     int    `json:"total_components"`
	CompletedComponents int    `json:"completed_components"`
	TotalOperations     int    `json:"total_operations"`
	CompletedOperations int    `json:"completed_operations"`
	ErrorCode           string `json:"error_code"`
	ErrorMessage        string `json:"error_message"`
	Components          []struct {
		ComponentIndex  int      `json:"component_index"`
		RootPath        string   `json:"root_path"`
		Status          string   `json:"status"`
		Stage           string   `json:"stage"`
		Committed       []string `json:"committed"`
		Removed         []string `json:"removed"`
		Remaining       []string `json:"remaining"`
		Recovery        []string `json:"recovery"`
		ErrorCode       string   `json:"error_code"`
		InventorySynced bool     `json:"inventory_synced"`
	} `json:"components"`
}

// waitForTerminalExecution polls the session until it reaches a terminal
// status.
func waitForTerminalExecution(
	t *testing.T,
	client *http.Client,
	ctx context.Context,
	base, opPath, execID, token string,
) executionView {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for {
		var view executionView
		code := doJSON(t, client, ctx, base, http.MethodGet, opPath+"/executions/"+execID, token, nil, &view)
		if code != http.StatusOK {
			t.Fatalf("GET execution: %d", code)
		}
		switch view.Status {
		case "succeeded", "failed", "canceled", "interrupted":
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("execution %s did not finish in time (status %s)", execID, view.Status)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitForLatestRevision waits for the operation's generation to complete and
// returns the published revision id.
func waitForLatestRevision(
	t *testing.T,
	client *http.Client,
	ctx context.Context,
	base, token, wsPath, opPath string,
) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var wsDetail struct {
			Operations []struct {
				LatestGeneration *struct {
					GenerationID string `json:"generation_id"`
				} `json:"latest_generation"`
			} `json:"operations"`
		}
		_ = doJSON(t, client, ctx, base, http.MethodGet, wsPath, token, nil, &wsDetail)
		if len(wsDetail.Operations) == 1 && wsDetail.Operations[0].LatestGeneration != nil {
			var genDetail struct {
				Status     string `json:"status"`
				RevisionID string `json:"revision_id"`
				ErrorCode  string `json:"error_code"`
			}
			if code := doJSON(
				t, client, ctx, base, http.MethodGet,
				opPath+"/planning-sessions/"+wsDetail.Operations[0].LatestGeneration.GenerationID,
				token, nil, &genDetail,
			); code == http.StatusOK {
				if genDetail.Status == "completed" {
					return genDetail.RevisionID
				}
				if genDetail.Status == "failed" || genDetail.Status == "canceled" {
					t.Fatalf("generation terminal: %+v", genDetail)
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("generation not completed in time")
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// createScannedWorkset creates the workset over the named scanned albums and
// returns its operation-scoped paths.
func createScannedWorkset(
	t *testing.T,
	client *http.Client,
	ctx context.Context,
	base, token, libID string,
	folders []struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	},
	albums []string,
) (string, string) {
	t.Helper()
	byName := map[string]string{}
	for _, f := range folders {
		byName[filepath.Base(filepath.FromSlash(f.Path))] = f.ID
	}
	folderIDs := make([]string, 0, len(albums))
	for _, album := range albums {
		id, ok := byName[album]
		if !ok {
			t.Fatalf("scanned folder %s missing from %+v", album, folders)
		}
		folderIDs = append(folderIDs, id)
	}
	var ws struct {
		Workset struct {
			WorksetID string `json:"workset_id"`
		} `json:"workset"`
	}
	if code := doJSON(
		t, client, ctx, base, http.MethodPost, "/api/v1/worksets", token,
		map[string]any{"library_id": libID, "title": "E2E 実行", "folder_ids": folderIDs}, &ws,
	); code != http.StatusCreated {
		t.Fatalf("create workset: %d", code)
	}
	wsPath := "/api/v1/worksets/" + ws.Workset.WorksetID
	return wsPath, wsPath + "/operations/conversion"
}

// execFixtureHTTP is the scanned workset fixture shared by the execution e2e
// scenarios: one library with the given album folders, a scanned inventory and
// a workset over every album.
type execFixtureHTTP struct {
	base      string
	client    *http.Client
	ctx       context.Context
	token     string
	libID     string
	rootPath  string
	wsPath    string
	opPath    string
	revision  string
	opVersion int
}

// setupScannedWorkset scans the fixture root and creates a workset over the
// album folders, then generates and waits for the first revision.
func setupScannedWorkset(
	t *testing.T,
	binPath, dataDir, rootPath string,
	albums []string,
	draft map[string]any,
) *execFixtureHTTP {
	t.Helper()
	const token = "e2e-token"
	proc := startBackendBinary(t, binPath, dataDir, token)
	base := fmt.Sprintf("http://127.0.0.1:%d", proc.httpPort)
	client := &http.Client{Timeout: 120 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	t.Cleanup(cancel)

	var lib struct{ ID string }
	if code := doJSON(
		t, client, ctx, base, http.MethodPost, "/api/v1/libraries", token,
		map[string]string{"name": "E2E Exec", "root_path": filepath.ToSlash(rootPath)}, &lib,
	); code != http.StatusCreated {
		t.Fatalf("create library: %d", code)
	}
	events := postScanSSE(t, client, ctx, base, lib.ID, token)
	if events["completed"] == nil {
		t.Fatalf("scan SSE missing completed: %+v", events)
	}
	var folders struct {
		Folders []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"folders"`
	}
	if code := doJSON(
		t, client, ctx, base, http.MethodGet, "/api/v1/libraries/"+lib.ID+"/folders", token, nil, &folders,
	); code != http.StatusOK {
		t.Fatalf("list folders: %d", code)
	}
	wsPath, opPath := createScannedWorkset(t, client, ctx, base, token, lib.ID, folders.Folders, albums)

	var draftResp struct{ Version int }
	if code := doJSON(
		t,
		client,
		ctx,
		base,
		http.MethodGet,
		opPath+"/draft",
		token,
		nil,
		&draftResp,
	); code != http.StatusOK {
		t.Fatalf("get draft: %d", code)
	}
	version := draftResp.Version
	if draft != nil {
		var saved struct{ Version int }
		if code := doJSONWithHeaders(
			t, client, ctx, base, http.MethodPut, opPath+"/draft", token,
			map[string]string{"If-Match": strconv.Itoa(version)}, draft, &saved,
		); code != http.StatusOK {
			t.Fatalf("save draft: %d", code)
		}
		version = saved.Version
	}
	if code := doJSONWithHeaders(
		t, client, ctx, base, http.MethodPost, opPath+"/revisions", token,
		map[string]string{"Idempotency-Key": "exec-gen-1", "If-Match": strconv.Itoa(version)},
		nil, nil,
	); code != http.StatusAccepted {
		t.Fatalf("start generation: %d", code)
	}
	revisionID := waitForLatestRevision(t, client, ctx, base, token, wsPath, opPath)
	return &execFixtureHTTP{
		base: base, client: client, ctx: ctx, token: token, libID: lib.ID,
		rootPath: rootPath, wsPath: wsPath, opPath: opPath, revision: revisionID,
		opVersion: version + 1,
	}
}

//nolint:funlen,gocognit,gocyclo,cyclop // e2e scenario
func TestHTTPWorksetExecutionLoop(t *testing.T) {
	binPath := buildBackendBinary(t)
	dataDir := t.TempDir()
	rootPath := filepath.Join(dataDir, "music")
	albumA := filepath.Join(rootPath, "albumA")
	albumB := filepath.Join(rootPath, "albumB")
	for _, dir := range []string{albumA, albumB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	// albumA is fully satisfied (wav + 320k mp3): its component has zero
	// operations. albumB holds a below-target 128k mp3 beside its wav source
	// and a quality-unverifiable aac variant: the component rebuilds the mp3
	// from the wav and declares the aac obsolete.
	mustFFmpegEncode(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=0.4", filepath.Join(albumA, "00.wav"))
	mustFFmpegEncode(t, "-i", filepath.Join(albumA, "00.wav"), "-b:a", "320k", filepath.Join(albumA, "00.mp3"))
	mustFFmpegEncode(t, "-f", "lavfi", "-i", "sine=frequency=660:duration=0.4", filepath.Join(albumB, "00.wav"))
	mustFFmpegEncode(t, "-i", filepath.Join(albumB, "00.wav"), "-b:a", "128k", filepath.Join(albumB, "00.mp3"))
	mustFFmpegEncode(
		t,
		"-i",
		filepath.Join(albumB, "00.wav"),
		"-c:a",
		"aac",
		"-b:a",
		"128k",
		filepath.Join(albumB, "00.m4a"),
	)

	satisfiedA, err := os.Stat(filepath.Join(albumA, "00.mp3"))
	if err != nil {
		t.Fatalf("stat albumA mp3: %v", err)
	}
	// The default seeded draft (available_sources, wav + mp3@320) is the plan
	// under test: no draft edit happens here.
	f := setupScannedWorkset(t, binPath, dataDir, rootPath, []string{"albumA", "albumB"}, nil)
	version := f.opVersion

	// Start the session, then let the SSE connection drop: the worker must keep
	// going, and the reconnect (here: the detail GET) must show the truth.
	var started struct {
		Created   bool          `json:"created"`
		Execution executionView `json:"execution"`
	}
	if code := doJSONWithHeaders(
		t, f.client, f.ctx, f.base, http.MethodPost,
		f.opPath+"/revisions/"+f.revision+"/executions", f.token,
		map[string]string{"Idempotency-Key": "exec-1", "If-Match": strconv.Itoa(version)},
		map[string]any{}, &started,
	); code != http.StatusAccepted {
		t.Fatalf("start execution: %d", code)
	}
	execID := started.Execution.ExecutionID
	if started.Execution.Status != "queued" || started.Execution.Options.DeleteMode != "soft" {
		t.Fatalf("started session = %+v", started.Execution)
	}
	eventsCtx, stopEvents := context.WithCancel(f.ctx)
	sseReq, err := http.NewRequestWithContext(
		eventsCtx, http.MethodGet, f.base+f.opPath+"/executions/"+execID+"/events", nil,
	)
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	sseReq.Header.Set("Authorization", "Bearer "+f.token)
	sseResp, err := f.client.Do(sseReq)
	if err != nil {
		t.Fatalf("events stream: %v", err)
	}
	head := make([]byte, 512)
	if _, readErr := sseResp.Body.Read(head); readErr != nil {
		t.Fatalf("read snapshot event: %v", readErr)
	}
	stopEvents()
	_ = sseResp.Body.Close()

	done := waitForTerminalExecution(t, f.client, f.ctx, f.base, f.opPath, execID, f.token)
	if done.Status != "succeeded" {
		t.Fatalf("execution terminal = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	if done.TotalComponents != 2 || done.CompletedComponents != 2 {
		t.Fatalf("components = %d/%d, want 2/2", done.CompletedComponents, done.TotalComponents)
	}
	if done.TotalOperations != 2 || done.CompletedOperations != 2 {
		t.Fatalf("operations = %d/%d, want 2/2 (albumB encode + albumB remove)",
			done.CompletedOperations, done.TotalOperations)
	}
	var committed, removed, recovery []string
	for _, c := range done.Components {
		if c.Status != "succeeded" || !c.InventorySynced {
			t.Fatalf("component %d = %+v", c.ComponentIndex, c)
		}
		committed = append(committed, c.Committed...)
		removed = append(removed, c.Removed...)
		recovery = append(recovery, c.Recovery...)
	}
	if len(committed) != 1 || filepath.Base(filepath.FromSlash(committed[0])) != "00.mp3" {
		t.Fatalf("committed = %v, want the rebuilt albumB mp3", committed)
	}
	if len(removed) != 1 || filepath.Base(filepath.FromSlash(removed[0])) != "00.m4a" {
		t.Fatalf("removed = %v, want the obsolete albumB aac", removed)
	}

	// Disk facts: the satisfied album is untouched, the obsolete variant and
	// the replaced mp3 are preserved under Delete/, and the mp3 now holds the
	// frozen target quality.
	if kept, keptErr := os.Stat(filepath.Join(albumA, "00.mp3")); keptErr != nil || kept.Size() != satisfiedA.Size() {
		t.Fatalf("albumA must stay untouched: %v", keptErr)
	}
	if _, statErr := os.Stat(filepath.Join(albumB, "00.m4a")); statErr == nil {
		t.Fatal("albumB/00.m4a must have left its place")
	}
	for _, preserved := range []string{"00.m4a", "00.mp3"} {
		recovered, statErr := os.Stat(filepath.Join(albumB, "Delete", preserved))
		if statErr != nil || recovered.Size() == 0 {
			t.Fatalf("albumB/Delete/%s missing: %v", preserved, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(albumB, "00.wav")); statErr != nil {
		t.Fatalf("the qualified wav source stays in place in relaxed mode: %v", statErr)
	}
	//nolint:gosec // fixed ffprobe binary with test-controlled arguments
	probe := exec.CommandContext(t.Context(), "ffprobe", "-v", "error",
		"-select_streams", "a:0", "-show_entries", "stream=bit_rate", "-of", "json",
		filepath.Join(albumB, "00.mp3"))
	raw, err := probe.Output()
	if err != nil {
		t.Fatalf("ffprobe rebuilt mp3: %v", err)
	}
	var probed struct {
		Streams []struct {
			BitRate string `json:"bit_rate"`
		} `json:"streams"`
	}
	if decodeErr := json.Unmarshal(raw, &probed); decodeErr != nil || len(probed.Streams) != 1 {
		t.Fatalf("probe payload = %s (%v)", raw, decodeErr)
	}
	rate, err := strconv.Atoi(probed.Streams[0].BitRate)
	if err != nil || rate < 319000 {
		t.Fatalf("rebuilt bitrate = %s, want >= 319000", probed.Streams[0].BitRate)
	}
	if len(recovery) < 2 {
		t.Fatalf("recovery must disclose every preserved file: %v", recovery)
	}

	// The observed inventory was synced to the disk: the executed revision no
	// longer matches it (a rescan would be needed to re-plan), and the revision
	// detail names the session that ran.
	var revDetail struct {
		Execution *struct {
			ExecutionID string `json:"execution_id"`
			Status      string `json:"status"`
		} `json:"execution"`
	}
	if code := doJSON(
		t, f.client, f.ctx, f.base, http.MethodGet, f.opPath+"/revisions/"+f.revision, f.token, nil, &revDetail,
	); code != http.StatusOK {
		t.Fatalf("revision detail: %d", code)
	}
	if revDetail.Execution == nil || revDetail.Execution.ExecutionID != execID {
		t.Fatalf("revision execution ref = %+v", revDetail.Execution)
	}
	var opValidation struct {
		CurrentRevision *struct {
			ValidationState string `json:"validation_state"`
			Stale           *bool  `json:"stale"`
		} `json:"current_revision"`
	}
	if code := doJSON(
		t, f.client, f.ctx, f.base, http.MethodGet, f.opPath, f.token, nil, &opValidation,
	); code != http.StatusOK {
		t.Fatalf("operation view: %d", code)
	}
	if opValidation.CurrentRevision == nil || opValidation.CurrentRevision.ValidationState != "stale" {
		t.Fatalf("revision validation = %+v, want stale after the inventory was synced",
			opValidation.CurrentRevision)
	}

	// The same revision cannot run again; the same key replays its session.
	replayBody := map[string]any{}
	var replay struct {
		Created   bool          `json:"created"`
		Execution executionView `json:"execution"`
	}
	if code := doJSONWithHeaders(
		t, f.client, f.ctx, f.base, http.MethodPost,
		f.opPath+"/revisions/"+f.revision+"/executions", f.token,
		map[string]string{"Idempotency-Key": "exec-1", "If-Match": strconv.Itoa(version)},
		replayBody, &replay,
	); code != http.StatusOK || replay.Execution.ExecutionID != execID || replay.Created {
		t.Fatalf("replay = %d %+v", code, replay)
	}
	var conflict struct {
		Code    string   `json:"code"`
		Details []string `json:"details"`
	}
	if code := doJSONWithHeaders(
		t, f.client, f.ctx, f.base, http.MethodPost,
		f.opPath+"/revisions/"+f.revision+"/executions", f.token,
		map[string]string{"Idempotency-Key": "exec-2", "If-Match": strconv.Itoa(version)},
		replayBody, &conflict,
	); code != http.StatusConflict || conflict.Code != "PLAN_NOT_EXECUTABLE" {
		t.Fatalf("second execution of the revision = %d %+v", code, conflict)
	}
	var alreadyRan bool
	for _, d := range conflict.Details {
		alreadyRan = alreadyRan || d == "ALREADY_EXECUTED"
	}
	if !alreadyRan {
		t.Fatalf("second execution details = %v, want ALREADY_EXECUTED", conflict.Details)
	}

	// The operation view exposes the terminal session for the workbench.
	var opView struct {
		ActiveExecution *struct {
			ExecutionID string `json:"execution_id"`
		} `json:"active_execution"`
		LatestExecution *struct {
			ExecutionID string `json:"execution_id"`
			Status      string `json:"status"`
		} `json:"latest_execution"`
	}
	if code := doJSON(
		t, f.client, f.ctx, f.base, http.MethodGet, f.opPath, f.token, nil, &opView,
	); code != http.StatusOK {
		t.Fatalf("operation view: %d", code)
	}
	if opView.ActiveExecution != nil || opView.LatestExecution == nil ||
		opView.LatestExecution.ExecutionID != execID || opView.LatestExecution.Status != "succeeded" {
		t.Fatalf("operation execution state = %+v / %+v", opView.ActiveExecution, opView.LatestExecution)
	}
}

// TestHTTPWorksetExecutionRejectsChangedDisk proves the real disk precheck: a
// file that changed after planning (and before any rescan) fails the
// component before anything is written.
func TestHTTPWorksetExecutionRejectsChangedDisk(t *testing.T) {
	binPath := buildBackendBinary(t)
	dataDir := t.TempDir()
	rootPath := filepath.Join(dataDir, "music")
	album := filepath.Join(rootPath, "albumA")
	if err := os.MkdirAll(album, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A below-target 128k mp3 beside its wav source: the frozen plan encodes the
	// wav onto the mp3 path, so the wav is an operation source.
	mustFFmpegEncode(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=0.4", filepath.Join(album, "00.wav"))
	mustFFmpegEncode(t, "-i", filepath.Join(album, "00.wav"), "-b:a", "128k", filepath.Join(album, "00.mp3"))
	before, err := os.Stat(filepath.Join(album, "00.mp3"))
	if err != nil {
		t.Fatalf("stat mp3: %v", err)
	}
	f := setupScannedWorkset(t, binPath, dataDir, rootPath, []string{"albumA"}, nil)
	version := f.opVersion

	// The wav is rewritten after the plan was frozen: same path, new content, and
	// the DB still holds the scanned facts (no rescan happened).
	if writeErr := os.WriteFile(
		filepath.Join(album, "00.wav"), []byte("not-the-scanned-bytes-anymore"), 0o644,
	); writeErr != nil {
		t.Fatalf("rewrite wav: %v", writeErr)
	}

	var started struct {
		Execution executionView `json:"execution"`
	}
	if code := doJSONWithHeaders(
		t, f.client, f.ctx, f.base, http.MethodPost,
		f.opPath+"/revisions/"+f.revision+"/executions", f.token,
		map[string]string{"Idempotency-Key": "exec-disk", "If-Match": strconv.Itoa(version)},
		map[string]any{}, &started,
	); code != http.StatusAccepted {
		t.Fatalf("start execution: %d", code)
	}
	done := waitForTerminalExecution(t, f.client, f.ctx, f.base, f.opPath, started.Execution.ExecutionID, f.token)
	if done.Status != "failed" || done.ErrorCode != "COMPONENT_FILE_CHANGED" {
		t.Fatalf("changed-disk execution = %s (%s: %s)", done.Status, done.ErrorCode, done.ErrorMessage)
	}
	if len(done.Components) != 1 || done.Components[0].Status != "failed" || done.Components[0].Stage != "precheck" {
		t.Fatalf("component report = %+v", done.Components)
	}
	if len(done.Components[0].Removed) != 0 || len(done.Components[0].Committed) != 0 {
		t.Fatalf("a precheck refusal must not change files: %+v", done.Components[0])
	}
	if _, statErr := os.Stat(filepath.Join(album, "00.wav")); statErr != nil {
		t.Fatalf("the changed file must stay where it is: %v", statErr)
	}
	//nolint:gosec // fixed ffprobe binary with test-controlled arguments
	probe := exec.CommandContext(t.Context(), "ffprobe", "-v", "error",
		"-select_streams", "a:0", "-show_entries", "stream=bit_rate", "-of", "json",
		filepath.Join(album, "00.mp3"))
	raw, err := probe.Output()
	if err != nil {
		t.Fatalf("ffprobe untouched mp3: %v", err)
	}
	var probed struct {
		Streams []struct {
			BitRate string `json:"bit_rate"`
		} `json:"streams"`
	}
	if decodeErr := json.Unmarshal(raw, &probed); decodeErr != nil || len(probed.Streams) != 1 {
		t.Fatalf("probe payload = %s", raw)
	}
	if probed.Streams[0].BitRate == "" {
		t.Fatal("the refused run must leave a readable mp3")
	}
	after, err := os.Stat(filepath.Join(album, "00.mp3"))
	if err != nil || after.Size() != before.Size() {
		t.Fatalf("the mp3 must be byte-identical after the refusal: %v", err)
	}
}
