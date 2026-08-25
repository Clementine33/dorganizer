package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	scanusecase "github.com/onsei/organizer/backend/internal/usecase/scan"
)

// seedScanTree writes a small real music tree on disk and returns its root.
func seedScanTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "music")
	for _, album := range []string{"album-01", "album-02"} {
		dir := filepath.Join(root, album)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "01.flac"), []byte("dummy"), 0o644); err != nil {
			t.Fatalf("write %s: %v", filepath.Join(dir, "01.flac"), err)
		}
	}
	return root
}

func TestScanSSEHappyPath(t *testing.T) {
	root := seedScanTree(t)

	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) {
		repo = d.Repo
		d.ScanService = scanusecase.NewService(d.Repo)
	})

	// Create the library pointing at the temp tree.
	w := doRequest(t, engine, http.MethodPost, "/api/v1/libraries",
		map[string]string{"name": "Music", "root_path": root}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	var lib libraryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &lib); err != nil {
		t.Fatalf("decode library: %v", err)
	}

	// POST scan with an empty body: the library root path is scanned.
	w = doRequest(t, engine, http.MethodPost, "/api/v1/libraries/"+lib.ID+"/scans",
		map[string]any{}, map[string]string{"Accept": "text/event-stream"})
	if w.Code != http.StatusOK {
		t.Fatalf("scan status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	body := w.Body.String()
	t.Logf("scan body:\n%s", body)
	for _, want := range []string{"event: started", "event: progress", "event: completed"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if !strings.Contains(body, `"scan_id"`) {
		t.Errorf("completed event data missing scan_id (body=%s)", body)
	}
	if !strings.Contains(body, `"files_scanned"`) {
		t.Errorf("completed event data missing files_scanned (body=%s)", body)
	}

	// The library_folders table must now be populated for the library.
	folders, err := repo.ListLibraryFolders(lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryFolders failed: %v", err)
	}
	if len(folders) == 0 {
		t.Error("expected library_folders populated after scan")
	}
}

// TestScanSSECancelledByRequestContext verifies that a canceled request
// context (client disconnect) yields a terminal `cancelled` SSE event and a
// `canceled` scan state, never `completed`.
func TestScanSSECancelledByRequestContext(t *testing.T) {
	root := seedScanTree(t)

	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) {
		repo = d.Repo
		d.ScanService = scanusecase.NewService(d.Repo)
	})
	libID := createLibraryViaAPI(t, engine, "Music", root)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/libraries/"+libID+"/scans", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	body := w.Body.String()
	t.Logf("cancelled scan body:\n%s", body)
	for _, want := range []string{"event: started", "event: cancelled"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "event: completed") {
		t.Errorf("canceled scan must not emit completed (body=%s)", body)
	}

	lib, err := repo.GetLibrary(libID)
	if err != nil {
		t.Fatalf("GetLibrary failed: %v", err)
	}
	if lib.LastScanStatus != "canceled" {
		t.Errorf("last_scan_status = %q, want canceled", lib.LastScanStatus)
	}
}

// TestScanSSETypedFailureEmitsErrorEvent verifies that a typed scan failure
// (invalid root path) surfaces as an `error` SSE event with the usecase code
// and marks the library scan state failed.
func TestScanSSETypedFailureEmitsErrorEvent(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) {
		repo = d.Repo
		d.ScanService = scanusecase.NewService(d.Repo)
	})
	libID := createLibraryViaAPI(t, engine, "Music", missing)

	w := doRequest(t, engine, http.MethodPost, "/api/v1/libraries/"+libID+"/scans",
		map[string]any{}, map[string]string{"Accept": "text/event-stream"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	body := w.Body.String()
	t.Logf("failed scan body:\n%s", body)
	if !strings.Contains(body, "event: error") {
		t.Errorf("body missing event: error (body=%s)", body)
	}
	if !strings.Contains(body, `"code":"ROOT_PATH_NOT_FOUND"`) {
		t.Errorf("error event data missing code ROOT_PATH_NOT_FOUND (body=%s)", body)
	}
	if strings.Contains(body, "event: completed") {
		t.Errorf("failed scan must not emit completed (body=%s)", body)
	}

	lib, err := repo.GetLibrary(libID)
	if err != nil {
		t.Fatalf("GetLibrary failed: %v", err)
	}
	if lib.LastScanStatus != "failed" {
		t.Errorf("last_scan_status = %q, want failed", lib.LastScanStatus)
	}
}

// TestScanSSENilServiceGuard verifies that an unwired (nil) ScanService
// produces a terminal error and a failed scan state instead of a mid-stream
// panic.
func TestScanSSENilServiceGuard(t *testing.T) {
	root := seedScanTree(t)

	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) { repo = d.Repo }) // ScanService left nil
	libID := createLibraryViaAPI(t, engine, "Music", root)

	w := doRequest(t, engine, http.MethodPost, "/api/v1/libraries/"+libID+"/scans",
		map[string]any{}, map[string]string{"Accept": "text/event-stream"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body=%s)", w.Code, w.Body.String())
	}
	code, _ := errorEnvelope(t, w)
	if code != "INTERNAL" {
		t.Fatalf("code = %q, want INTERNAL", code)
	}

	lib, err := repo.GetLibrary(libID)
	if err != nil {
		t.Fatalf("GetLibrary failed: %v", err)
	}
	if lib.LastScanStatus != "failed" {
		t.Errorf("last_scan_status = %q, want failed", lib.LastScanStatus)
	}
}

func TestScanSSERejectsRootOutsideLibrary(t *testing.T) {
	libraryRoot := seedScanTree(t)
	otherRoot := seedScanTree(t)

	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) {
		repo = d.Repo
		d.ScanService = scanusecase.NewService(d.Repo)
	})
	libID := createLibraryViaAPI(t, engine, "Music", libraryRoot)

	w := doRequest(t, engine, http.MethodPost, "/api/v1/libraries/"+libID+"/scans",
		map[string]string{"root_path": otherRoot}, map[string]string{"Accept": "text/event-stream"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	code, _ := errorEnvelope(t, w)
	if code != "ROOT_PATH_OUTSIDE_LIBRARY" {
		t.Fatalf("code = %q, want ROOT_PATH_OUTSIDE_LIBRARY", code)
	}
	if strings.Contains(w.Body.String(), "event:") {
		t.Fatalf("rejected scan must not start SSE (body=%s)", w.Body.String())
	}
	folders, err := repo.ListLibraryFolders(libID)
	if err != nil {
		t.Fatalf("ListLibraryFolders failed: %v", err)
	}
	if len(folders) != 0 {
		t.Fatalf("rejected scan persisted %d folders", len(folders))
	}
}

func TestScanSSEUnknownLibrary(t *testing.T) {
	engine := newTestServer(t, nil)

	w := doRequest(t, engine, http.MethodPost, "/api/v1/libraries/does-not-exist/scans",
		map[string]any{}, map[string]string{"Accept": "text/event-stream"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
	code, _ := errorEnvelope(t, w)
	if code != "LIBRARY_NOT_FOUND" {
		t.Fatalf("code = %q, want LIBRARY_NOT_FOUND", code)
	}
}
