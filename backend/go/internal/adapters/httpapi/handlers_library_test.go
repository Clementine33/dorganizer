package httpapi //nolint:testpackage // white-box tests exercise unexported internals

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/admission"
)

//nolint:gocognit,funlen // CRUD scenario with many branches
func TestLibrariesCRUD(t *testing.T) {
	engine := newTestServer(t, nil) // empty token: no auth needed for CRUD flow

	// Set by the create subtest, read by the later ones (subtests run in order).
	var createdID string

	t.Run("create", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodPost, "/api/v1/libraries",
			map[string]string{"name": "Music", "root_path": "/music"}, nil)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
		}
		var lib libraryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &lib); err != nil {
			t.Fatalf("decode library: %v (body=%s)", err, w.Body.String())
		}
		if lib.ID == "" {
			t.Fatal("created library missing id")
		}
		if lib.Name != "Music" {
			t.Fatalf("name = %q, want Music", lib.Name)
		}
		if lib.RootPath != "/music" {
			t.Fatalf("root_path = %q, want /music", lib.RootPath)
		}
		createdID = lib.ID
	})

	t.Run("duplicate root path conflicts", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodPost, "/api/v1/libraries",
			map[string]string{"name": "Music Two", "root_path": "/music"}, nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "LIBRARY_EXISTS" {
			t.Fatalf("code = %q, want LIBRARY_EXISTS", code)
		}
	})

	t.Run("list", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/libraries", nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var out struct {
			Libraries []libraryResponse `json:"libraries"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode list: %v (body=%s)", err, w.Body.String())
		}
		if len(out.Libraries) != 1 {
			t.Fatalf("len(libraries) = %d, want 1", len(out.Libraries))
		}
		if out.Libraries[0].ID != createdID {
			t.Fatalf("list id = %q, want %q", out.Libraries[0].ID, createdID)
		}
	})

	t.Run("get by id", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/libraries/"+createdID, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var lib libraryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &lib); err != nil {
			t.Fatalf("decode library: %v", err)
		}
		if lib.Name != "Music" {
			t.Fatalf("name = %q, want Music", lib.Name)
		}
	})

	t.Run("patch name", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodPatch, "/api/v1/libraries/"+createdID,
			map[string]string{"name": "Classical"}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var lib libraryResponse
		if err := json.Unmarshal(w.Body.Bytes(), &lib); err != nil {
			t.Fatalf("decode library: %v", err)
		}
		if lib.Name != "Classical" {
			t.Fatalf("name = %q, want Classical", lib.Name)
		}
		if lib.RootPath != "/music" {
			t.Fatalf("root_path = %q, want /music (unchanged by name-only patch)", lib.RootPath)
		}
	})

	t.Run("unknown id not found", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodGet, "/api/v1/libraries/does-not-exist", nil, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
		}
		code, _ := errorEnvelope(t, w)
		if code != "LIBRARY_NOT_FOUND" {
			t.Fatalf("code = %q, want LIBRARY_NOT_FOUND", code)
		}
	})

	t.Run("delete", func(t *testing.T) {
		w := doRequest(t, engine, http.MethodDelete, "/api/v1/libraries/"+createdID, nil, nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", w.Code, w.Body.String())
		}
		// Deleted: a subsequent get must 404.
		w = doRequest(t, engine, http.MethodGet, "/api/v1/libraries/"+createdID, nil, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("post-delete status = %d, want 404 (body=%s)", w.Code, w.Body.String())
		}
	})
}

// TestRootChangeTakesTheFileManagementSlot covers the admission rule that a
// root change is a path-rewriting action (ADR 0002 §2; ADR 0001 §5): it goes through while
// nothing else holds the slot, releases it again, and is refused — not queued —
// while a scan is running.
func TestRootChangeTakesTheFileManagementSlot(t *testing.T) {
	gate := admission.NewGate(nil)
	engine := newTestServer(t, func(d *Dependencies) { testGate(d, gate) })
	libID := createLibraryViaAPI(t, engine, "Music", "/music")

	for _, root := range []string{"/new-music", "/music"} {
		w := doRequest(t, engine, http.MethodPatch, "/api/v1/libraries/"+libID,
			map[string]string{"root_path": root}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("root change to %s: status = %d, want 200 (body=%s)", root, w.Code, w.Body.String())
		}
	}

	release, err := gate.BeginScan()
	if err != nil {
		t.Fatalf("begin scan: %v", err)
	}
	defer release()
	w := doRequest(t, engine, http.MethodPatch, "/api/v1/libraries/"+libID,
		map[string]string{"root_path": "/third"}, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status while scanning = %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	if code, _ := errorEnvelope(t, w); code != "BUSY" {
		t.Fatalf("code = %q, want BUSY", code)
	}
}

func TestPatchLibraryRootInvalidatesDerivedFolders(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) { repo = d.Repo })
	libID := createLibraryViaAPI(t, engine, "Music", "/music")

	if _, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format)
		VALUES ('/music/album', '/music', '/music', 'album', 1, 0, 0, '')
	`); err != nil {
		t.Fatalf("seed library directory: %v", err)
	}
	if err := repo.UpdateLibraryScanState(libID, "completed", "", time.Now()); err != nil {
		t.Fatalf("UpdateLibraryScanState failed: %v", err)
	}

	w := doRequest(t, engine, http.MethodPatch, "/api/v1/libraries/"+libID,
		map[string]string{"root_path": "/new-music"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var updated libraryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated library: %v", err)
	}
	if updated.LastScanAt != nil || updated.LastScanStatus != "" || updated.LastScanError != "" {
		t.Fatalf("scan state was not reset: %+v", updated)
	}

	// The inventory of the old root is gone: the listing endpoint arrives with
	// the workbench routes, so this asserts the storage fact.
	var stale int
	if scanErr := repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries WHERE root_path = '/music'",
	).Scan(&stale); scanErr != nil {
		t.Fatalf("count stale entries: %v", scanErr)
	}
	if stale != 0 {
		t.Fatalf("root change retained %d stale entries", stale)
	}
}
