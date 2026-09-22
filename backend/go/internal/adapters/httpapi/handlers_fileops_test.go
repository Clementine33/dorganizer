package httpapi //nolint:testpackage // white-box tests exercise unexported internals

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/admission"
	"github.com/onsei/organizer/backend/internal/services/fileops"
	scanusecase "github.com/onsei/organizer/backend/internal/usecase/scan"
)

// blockingScan is a scan double that holds the scanning slot until released,
// so a test can observe what the API does while a scan is running.
type blockingScan struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingScan() *blockingScan {
	return &blockingScan{started: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingScan) Scan(
	ctx context.Context,
	_ scanusecase.Request,
	emit func(scanusecase.Event),
) (scanusecase.Result, error) {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return scanusecase.Result{}, ctx.Err()
	}
	emit(scanusecase.Event{Type: "completed"})
	return scanusecase.Result{ScanID: "scan-blocking", RootPath: "/music"}, nil
}

func (b *blockingScan) RefreshMember(ctx context.Context, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

// fileOpsServer wires the real file-management service and the admission gate
// into the HTTP layer, which is where the two write paths must meet.
func fileOpsServer(t *testing.T) (http.Handler, string, *blockingScan) {
	t.Helper()
	var repo *sqlite.Repository
	scan := newBlockingScan()
	root := t.TempDir()
	var gate *admission.Gate
	handler := newTestServer(t, func(d *Dependencies) {
		repo = d.Repo
		d.ScanService = scan
		gate = admission.NewGate(d.Repo.HasActiveSession)
		d.Gate = gate
		d.FileOps = fileops.NewService(gate, func(context.Context, string, string) error { return nil })
	})
	if _, err := repo.CreateLibrary("Music", filepath.ToSlash(root)); err != nil {
		t.Fatalf("create library: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "albumA"), 0o755); err != nil {
		t.Fatalf("mkdir member: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "albumA", "01.flac"), []byte("audio"), 0o644); err != nil {
		t.Fatalf("write member file: %v", err)
	}
	return handler, root, scan
}

// TestFileOperationRefusedWhileAScanRuns is the API-level half of the
// admission contract (ADR 0002 §2): a running scan refuses direct file
// management with a busy answer and the file stays exactly where it was.
func TestFileOperationRefusedWhileAScanRuns(t *testing.T) {
	handler, root, scan := fileOpsServer(t)
	libs, _ := listLibrariesViaAPI(t, handler)
	if len(libs) == 0 {
		t.Fatal("no library")
	}
	libID := libs[0]

	scanDone := make(chan int, 1)
	go func() {
		w := doRequest(t, handler, http.MethodPost, "/api/v1/libraries/"+libID+"/scans",
			map[string]any{}, map[string]string{"Accept": "text/event-stream"})
		scanDone <- w.Code
	}()
	select {
	case <-scan.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the scan never started")
	}

	w := doRequest(t, handler, http.MethodPost, "/api/v1/libraries/"+libID+"/file-operations", map[string]any{
		"member_path": "albumA",
		"operation":   "soft_delete",
		"items":       []map[string]any{{"source": "01.flac"}},
	}, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("file operation during a scan = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	code, _ := errorEnvelope(t, w)
	if code != "BUSY" {
		t.Fatalf("code = %q, want BUSY", code)
	}
	if _, err := os.Stat(filepath.Join(root, "albumA", "01.flac")); err != nil {
		t.Fatalf("a refused request must not have moved the file: %v", err)
	}

	close(scan.release)
	if code := <-scanDone; code != http.StatusOK {
		t.Fatalf("scan status = %d, want 200", code)
	}

	// Once the scan is gone the same request goes through.
	w = doRequest(t, handler, http.MethodPost, "/api/v1/libraries/"+libID+"/file-operations", map[string]any{
		"member_path": "albumA",
		"operation":   "soft_delete",
		"items":       []map[string]any{{"source": "01.flac"}},
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("file operation after the scan = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var result fileops.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Succeeded != 1 || !result.Refresh.OK {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, fileops.RecoveryDir, "albumA", "01.flac")); err != nil {
		t.Fatalf("the file was not recycled where the result says: %v", err)
	}
}

// TestFileOperationNeedsRealPaths covers what the API refuses before any file
// is touched: an unknown member, a traversal and a member outside the library.
func TestFileOperationNeedsRealPaths(t *testing.T) {
	handler, _, _ := fileOpsServer(t)
	libs, _ := listLibrariesViaAPI(t, handler)
	libID := libs[0]

	cases := []struct {
		name   string
		body   map[string]any
		status int
	}{
		{
			"unknown member",
			map[string]any{
				"member_path": "gone", "operation": "soft_delete",
				"items": []map[string]any{{"source": "01.flac"}},
			},
			http.StatusBadRequest,
		},
		{
			"traversal in the member",
			map[string]any{
				"member_path": "../etc", "operation": "soft_delete",
				"items": []map[string]any{{"source": "01.flac"}},
			},
			http.StatusBadRequest,
		},
		{
			"unsupported operation",
			map[string]any{
				"member_path": "albumA", "operation": "chmod",
				"items": []map[string]any{{"source": "01.flac"}},
			},
			http.StatusBadRequest,
		},
		{
			"no items",
			map[string]any{"member_path": "albumA", "operation": "soft_delete", "items": []map[string]any{}},
			http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, handler, http.MethodPost, "/api/v1/libraries/"+libID+"/file-operations", tc.body, nil)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tc.status, w.Body.String())
			}
		})
	}
}

// listLibrariesViaAPI reads the library list through the API.
func listLibrariesViaAPI(t *testing.T, handler http.Handler) ([]string, string) {
	t.Helper()
	w := doRequest(t, handler, http.MethodGet, "/api/v1/libraries", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list libraries: %d", w.Code)
	}
	var out struct {
		Libraries []struct {
			ID string `json:"id"`
		} `json:"libraries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode libraries: %v", err)
	}
	ids := make([]string, 0, len(out.Libraries))
	for _, lib := range out.Libraries {
		ids = append(ids, lib.ID)
	}
	return ids, ""
}
