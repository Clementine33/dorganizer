package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

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

func TestPatchLibraryRootInvalidatesDerivedFoldersAndPlanAssociation(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) { repo = d.Repo })
	libID := createLibraryViaAPI(t, engine, "Music", "/music")

	if _, err := repo.DB().Exec(`
		INSERT INTO library_folders (id, library_id, path, name, relative_path, audio_file_count)
		VALUES ('folder-old', ?, '/music/album', 'album', 'album', 1)
	`, libID); err != nil {
		t.Fatalf("seed library folder: %v", err)
	}
	if err := repo.UpdateLibraryScanState(libID, "completed", "", time.Now()); err != nil {
		t.Fatalf("UpdateLibraryScanState failed: %v", err)
	}
	if err := repo.CreatePlan(&sqlite.Plan{
		PlanID:    "plan-old-root",
		RootPath:  "/music/album",
		PlanType:  "single_delete",
		Status:    "ready",
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
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

	w = doRequest(t, engine, http.MethodGet, "/api/v1/libraries/"+libID+"/folders", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("folders status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var folders struct {
		Folders []folderResponse `json:"folders"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &folders); err != nil {
		t.Fatalf("decode folders: %v", err)
	}
	if len(folders.Folders) != 0 {
		t.Fatalf("root change retained %d stale folders", len(folders.Folders))
	}

	w = doRequest(t, engine, http.MethodGet, "/api/v1/plans?library_id="+libID, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("plans status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var plans struct {
		Plans []planInfoDTO `json:"plans"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &plans); err != nil {
		t.Fatalf("decode plans: %v", err)
	}
	if len(plans.Plans) != 0 {
		t.Fatalf("old-root plan remained associated after root change: %+v", plans.Plans)
	}
}
