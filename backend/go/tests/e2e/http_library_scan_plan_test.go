package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestHTTPLibraryScanPlanLoop boots the real backend binary as a subprocess
// (ONSEI_DATA_DIR=<temp>), parses the ONSEI_BACKEND_READY handshake for both
// the gRPC and HTTP ports, then drives the HTTP/SSE library workflow end to
// end: health -> create library -> SSE scan -> list folders -> plan.
func TestHTTPLibraryScanPlanLoop(t *testing.T) {
	binPath := buildBackendBinary(t)

	dataDir := t.TempDir()
	rootPath := filepath.Join(dataDir, "music")
	// albumA holds lossy+lossless stems so a slim:mode1 plan yields deletes.
	mustWriteFile(t, filepath.Join(rootPath, "albumA", "test1.mp3"), "dummy audio")
	mustWriteFile(t, filepath.Join(rootPath, "albumA", "test1.flac"), "dummy audio")
	mustWriteFile(t, filepath.Join(rootPath, "albumA", "test2.mp3"), "dummy audio")
	mustWriteFile(t, filepath.Join(rootPath, "albumA", "test2.flac"), "dummy audio")
	// albumB is audio-only so the folders index is non-empty across the root.
	mustWriteFile(t, filepath.Join(rootPath, "albumB", "song1.mp3"), "dummy audio")
	mustWriteFile(t, filepath.Join(rootPath, "albumB", "song2.mp3"), "dummy audio")

	const token = "e2e-token"
	proc := startBackendBinary(t, binPath, dataDir, token)
	t.Logf("backend ready: grpc_port=%d http_port=%d", proc.grpcPort, proc.httpPort)

	base := fmt.Sprintf("http://127.0.0.1:%d", proc.httpPort)
	client := &http.Client{Timeout: 60 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// GET /api/v1/health is public.
	var health struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	if code := doJSON(t, client, ctx, base, http.MethodGet, "/api/v1/health", "", nil, &health); code != http.StatusOK {
		t.Fatalf("GET /api/v1/health: status %d, want 200", code)
	}
	if !health.OK {
		t.Fatalf("GET /api/v1/health: ok=false, want true")
	}

	// POST /api/v1/libraries
	libReq := map[string]string{"name": "E2E Library", "root_path": filepath.ToSlash(rootPath)}
	var lib struct {
		ID       string `json:"id"`
		RootPath string `json:"root_path"`
	}
	if code := doJSON(t, client, ctx, base, http.MethodPost, "/api/v1/libraries", token, libReq, &lib); code != http.StatusCreated {
		t.Fatalf("POST /api/v1/libraries: status %d, want 201", code)
	}
	if lib.ID == "" {
		t.Fatal("POST /api/v1/libraries: empty library id")
	}
	if lib.RootPath != filepath.ToSlash(rootPath) {
		t.Fatalf("POST /api/v1/libraries: root_path %q, want %q", lib.RootPath, filepath.ToSlash(rootPath))
	}

	// POST /api/v1/libraries/:id/scans over SSE, read to completion.
	events := postScanSSE(t, client, ctx, base, lib.ID, token)
	if ev := events["error"]; ev != nil {
		t.Fatalf("scan SSE contained error event: %v", ev)
	}
	if events["started"] == nil {
		t.Fatal("scan SSE missing 'started' event")
	}
	if events["completed"] == nil {
		t.Fatal("scan SSE missing 'completed' event")
	}
	var completed struct {
		ScanID   string `json:"scan_id"`
		RootPath string `json:"root_path"`
	}
	if err := json.Unmarshal(events["completed"], &completed); err != nil {
		t.Fatalf("parse scan completed event: %v", err)
	}
	if completed.ScanID == "" {
		t.Fatal("scan completed event has empty scan_id")
	}

	// GET /api/v1/libraries/:id/folders must be non-empty after the scan.
	var folders struct {
		Folders []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"folders"`
	}
	code := doJSON(t, client, ctx, base, http.MethodGet, "/api/v1/libraries/"+lib.ID+"/folders", token, nil, &folders)
	if code != http.StatusOK {
		t.Fatalf("GET /folders: status %d, want 200", code)
	}
	if len(folders.Folders) == 0 {
		t.Fatal("GET /folders: expected non-empty folder list after scan")
	}

	// POST /api/v1/plans scoped to albumA (mp3+flac pairs). The folders
	// response ordering is not part of the contract, so locate albumA by name.
	var albumFolderID string
	for _, f := range folders.Folders {
		if strings.HasSuffix(filepath.ToSlash(f.Path), "/albumA") {
			albumFolderID = f.ID
			break
		}
	}
	if albumFolderID == "" {
		t.Fatalf("folders response missing albumA: %+v", folders.Folders)
	}
	// POST /api/v1/plans with the workflow contract: inline literal-tag policy
	// over the albumA planning root. albumA holds flac+mp3 pairs with unknown
	// bitrates, so the balanced profile (wav + mp3-320) is actionable (lossless
	// and encoded lanes rebuild from the observed flac source).
	planReq := map[string]any{
		"library_id": lib.ID,
		"folder_ids": []string{albumFolderID},
		"workflow": map[string]any{
			"schema_version": 1,
			"steps": []any{map[string]any{
				"step_type": "reconcile_audio_outputs",
				"policy":    inlineWorkflowPolicy(),
			}},
		},
	}
	var plan struct {
		PlanID   string `json:"plan_id"`
		PlanKind string `json:"plan_kind"`
		Summary  struct {
			OperationCount  int    `json:"operation_count"`
			ErrorCount      int    `json:"error_count"`
			ActionableCount int    `json:"actionable_count"`
			SummaryReason   string `json:"summary_reason"`
		} `json:"summary"`
		Steps []struct {
			StepType   string `json:"step_type"`
			Status     string `json:"status"`
			Components []struct {
				ComponentID string `json:"component_id"`
				Status      string `json:"status"`
			} `json:"components"`
		} `json:"steps"`
	}
	code = doJSON(t, client, ctx, base, http.MethodPost, "/api/v1/plans", token, planReq, &plan)
	if code != http.StatusOK {
		t.Fatalf("POST /api/v1/plans: status %d, want 200", code)
	}
	if plan.PlanID == "" {
		t.Fatal("POST /api/v1/plans: empty plan_id")
	}
	if plan.PlanKind != "workflow" {
		t.Fatalf("plan_kind = %q, want workflow", plan.PlanKind)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].StepType != "reconcile_audio_outputs" {
		t.Fatalf("steps = %+v, want one reconcile_audio_outputs step", plan.Steps)
	}
	if plan.Summary.OperationCount == 0 {
		t.Fatalf("expected actionable operations for flac+mp3 pairs under balanced preset (summary=%+v)", plan.Summary)
	}
	if plan.Summary.OperationCount != plan.Summary.ActionableCount {
		t.Fatalf("summary.actionable_count %d != operation_count %d", plan.Summary.ActionableCount, plan.Summary.OperationCount)
	}
	if plan.Summary.SummaryReason != "ACTIONABLE" {
		t.Fatalf("summary_reason = %q, want ACTIONABLE", plan.Summary.SummaryReason)
	}
	if len(plan.Steps[0].Components) == 0 {
		t.Fatal("workflow step has no components")
	}

	t.Logf("http e2e workflow complete: library=%s scan_id=%s folders=%d plan=%s ops=%d components=%d",
		lib.ID, completed.ScanID, len(folders.Folders), plan.PlanID, plan.Summary.OperationCount, len(plan.Steps[0].Components))
}

// buildBackendBinary compiles the backend into a fresh temp dir and returns
// the binary path. It runs `go build` from the backend/go module root, which
// is two levels above this test package's directory.
func buildBackendBinary(t *testing.T) string {
	t.Helper()
	backendGoDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve backend/go dir: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "onsei-organizer-backend")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/onsei-organizer-backend")
	cmd.Dir = backendGoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build backend failed: %v\n%s", err, out)
	}
	return bin
}

// backendProc holds the running backend subprocess and its parsed ports.
type backendProc struct {
	cmd      *exec.Cmd
	grpcPort int
	httpPort int
	token    string
}

// inlineWorkflowPolicy is the inline literal-tag policy payload (matched and
// unmatched both want wav + mp3@320), replacing the removed balanced preset.
func inlineWorkflowPolicy() map[string]any {
	profile := map[string]any{
		"lossless": map[string]any{"codec": "wav"},
		"encoded":  map[string]any{"codec": "mp3", "quality": map[string]any{"kind": "bitrate", "bitrate": 320}},
	}
	return map[string]any{
		"kind": "inline",
		"policy": map[string]any{
			"schema_version":  1,
			"classifier_tags": []string{"SEなし"},
			"matched":         profile,
			"unmatched":       profile,
		},
	}
}

// startBackendBinary launches the backend with ONSEI_DATA_DIR set and a
// never-closed stdin pipe (the backend cancels on stdin EOF, so the pipe keeps
// it alive for the whole test), then parses the ready handshake.
func startBackendBinary(t *testing.T, binPath, dataDir, token string) backendProc {
	t.Helper()
	// Seed the classifier tag config: no compiled-in defaults remain, so a
	// fresh data dir gets its literal tags from config.json.
	cfgPath := filepath.Join(dataDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"prune":{"literal_tags":["SEなし"]}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	stdinR, stdinW := io.Pipe()
	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(),
		"ONSEI_DATA_DIR="+dataDir,
		"ONSEI_TOKEN="+token,
	)
	cmd.Stdin = stdinR
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("backend stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start backend: %v", err)
	}

	type handshakeResult struct {
		line     string
		grpcPort int
		httpPort int
		err      error
	}
	handshake := make(chan handshakeResult, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "ONSEI_BACKEND_READY") {
				continue
			}
			var port, httpPort int
			var tok, ver string
			n, err := fmt.Sscanf(line, "ONSEI_BACKEND_READY port=%d token=%s version=%s http_port=%d",
				&port, &tok, &ver, &httpPort)
			if err != nil || n != 4 {
				handshake <- handshakeResult{line: line, err: fmt.Errorf("parse handshake fields: n=%d err=%v", n, err)}
				return
			}
			handshake <- handshakeResult{line: line, grpcPort: port, httpPort: httpPort}
			// Keep draining stdout until EOF so the backend never blocks on a
			// full pipe; the handshake result is already delivered.
			for scanner.Scan() {
			}
			return
		}
		handshake <- handshakeResult{err: fmt.Errorf("handshake not found before stdout EOF: %v", scanner.Err())}
	}()

	var hs handshakeResult
	select {
	case hs = <-handshake:
	case <-time.After(30 * time.Second):
		hs.err = fmt.Errorf("timed out waiting for handshake")
	}
	if hs.err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("backend handshake: %v", hs.err)
	}
	if hs.grpcPort == 0 || hs.httpPort == 0 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("backend handshake %q: missing port", hs.line)
	}

	proc := backendProc{cmd: cmd, grpcPort: hs.grpcPort, httpPort: hs.httpPort, token: token}
	t.Cleanup(func() {
		_ = stdinW.Close()
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	return proc
}

// sseEvents is event-name -> raw data JSON for a parsed SSE stream.
type sseEvents map[string]json.RawMessage

// postScanSSE POSTs /libraries/:id/scans and reads the SSE body to
// completion, then parses `event:`/`data:` blocks.
func postScanSSE(t *testing.T, client *http.Client, ctx context.Context, base, libraryID, token string) sseEvents {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/api/v1/libraries/"+libraryID+"/scans", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("scan request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("scan request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("scan: status %d, body %s", resp.StatusCode, raw)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scan SSE body: %v", err)
	}

	events := sseEvents{}
	for _, block := range strings.Split(string(body), "\n\n") {
		var evName string
		var data json.RawMessage
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				evName = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = json.RawMessage(strings.TrimPrefix(line, "data: "))
			}
		}
		if evName != "" {
			events[evName] = data
		}
	}
	return events
}

// doJSON performs an authenticated JSON request and decodes the response
// body into out (when non-nil), returning the HTTP status code.
func doJSON(t *testing.T, client *http.Client, ctx context.Context, base, method, path, token string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode response %s: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
