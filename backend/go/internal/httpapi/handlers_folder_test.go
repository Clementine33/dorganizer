package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// int64ptr returns a pointer to v for nullable DTO assertions.
func int64ptr(v int64) *int64 { return &v }

// insertEntryMeta inserts a row into the entries table with size/bitrate/
// format metadata, under the fixed root /music.
func insertEntryMeta(
	t *testing.T,
	repo *sqlite.Repository,
	path, parentPath, name string,
	isDir bool,
	size int64,
	bitrate *int64,
	format string,
) {
	t.Helper()
	isDirInt := 0
	if isDir {
		isDirInt = 1
	}
	var bitrateVal any
	if bitrate != nil {
		bitrateVal = *bitrate
	}
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, scan_id, content_rev, bitrate, format)
		VALUES (?, '/music', ?, ?, ?, ?, 0, 'scan-1', 1, ?, ?)
	`, path, parentPath, name, isDirInt, size, bitrateVal, format)
	if err != nil {
		t.Fatalf("failed to insert entry %s: %v", path, err)
	}
}

// createLibraryViaAPI creates a library through the API and returns its ID.
func createLibraryViaAPI(t *testing.T, engine http.Handler, name, rootPath string) string {
	t.Helper()
	w := doRequest(t, engine, http.MethodPost, "/api/v1/libraries",
		map[string]string{"name": name, "root_path": rootPath}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	var lib libraryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &lib); err != nil {
		t.Fatalf("decode library: %v", err)
	}
	return lib.ID
}

func TestListLibraryFolders(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) { repo = d.Repo })

	libID := createLibraryViaAPI(t, engine, "Music", "/music")

	// Seed entries and derive the folders through the repo, mirroring a scan.
	insertEntryMeta(t, repo, "/music", "", "music", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA", "/music", "albumA", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA/01.flac", "/music/albumA", "01.flac", false, 1234, int64ptr(320), "flac")
	if _, err := repo.ReplaceLibraryFolders(libID, "/music"); err != nil {
		t.Fatalf("ReplaceLibraryFolders failed: %v", err)
	}

	w := doRequest(t, engine, http.MethodGet, "/api/v1/libraries/"+libID+"/folders", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var out struct {
		Folders []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Path           string `json:"path"`
			RelativePath   string `json:"relative_path"`
			AudioFileCount int    `json:"audio_file_count"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode folders: %v (body=%s)", err, w.Body.String())
	}
	if len(out.Folders) != 1 {
		t.Fatalf("len(folders) = %d, want 1 (body=%s)", len(out.Folders), w.Body.String())
	}
	f := out.Folders[0]
	if f.ID == "" {
		t.Error("folder missing id")
	}
	if f.Name != "albumA" {
		t.Errorf("name = %q, want albumA", f.Name)
	}
	if f.Path != "/music/albumA" {
		t.Errorf("path = %q, want /music/albumA", f.Path)
	}
	if f.RelativePath != "albumA" {
		t.Errorf("relative_path = %q, want albumA", f.RelativePath)
	}
	if f.AudioFileCount != 1 {
		t.Errorf("audio_file_count = %d, want 1", f.AudioFileCount)
	}
}

func TestFolderTree(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) { repo = d.Repo })

	libID := createLibraryViaAPI(t, engine, "Music", "/music")

	insertEntryMeta(t, repo, "/music", "", "music", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA", "/music", "albumA", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA/disc2", "/music/albumA", "disc2", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA/01.flac", "/music/albumA", "01.flac", false, 1234, int64ptr(320), "flac")
	insertEntryMeta(t, repo, "/music/albumA/disc2/02.flac", "/music/albumA/disc2", "02.flac", false, 2048, nil, "flac")
	insertEntryMeta(t, repo, "/music/albumA/cover.jpg", "/music/albumA", "cover.jpg", false, 999, nil, "")
	if _, err := repo.ReplaceLibraryFolders(libID, "/music"); err != nil {
		t.Fatalf("ReplaceLibraryFolders failed: %v", err)
	}
	folders, err := repo.ListLibraryFolders(libID)
	if err != nil {
		t.Fatalf("ListLibraryFolders failed: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("expected 1 folder, got %d", len(folders))
	}

	w := doRequest(t, engine, http.MethodGet,
		fmt.Sprintf("/api/v1/libraries/%s/folders/%s/tree", libID, folders[0].ID), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	// treeNodeDTO mirrors the tree response shape for assertions. It is
	// recursive so nested children decode into the same typed nodes.
	type treeNodeDTO struct {
		Name     string        `json:"name"`
		Path     string        `json:"path"`
		Type     string        `json:"type"`
		Size     *int64        `json:"size"`
		Bitrate  *int32        `json:"bitrate"`
		Format   string        `json:"format"`
		Children []treeNodeDTO `json:"children"`
	}
	var out struct {
		Tree treeNodeDTO `json:"tree"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode tree: %v (body=%s)", err, w.Body.String())
	}

	tree := out.Tree
	if tree.Name != "albumA" {
		t.Errorf("tree name = %q, want albumA (no full path in names)", tree.Name)
	}
	if tree.Path != "/music/albumA" {
		t.Errorf("tree path = %q, want /music/albumA", tree.Path)
	}
	if tree.Type != "dir" {
		t.Errorf("tree type = %q, want dir", tree.Type)
	}
	if len(tree.Children) != 3 {
		t.Fatalf("len(children) = %d, want 3 (body=%s)", len(tree.Children), w.Body.String())
	}

	// Dirs sort before files; both sort naturally by name.
	disc2 := tree.Children[0]
	if disc2.Type != "dir" || disc2.Name != "disc2" {
		t.Errorf("children[0] = %+v, want dir disc2 first", disc2)
	}
	if len(disc2.Children) != 1 {
		t.Fatalf("len(disc2.Children) = %d, want 1", len(disc2.Children))
	}
	nested := disc2.Children[0]
	if nested.Name != "02.flac" || nested.Path != "/music/albumA/disc2/02.flac" || nested.Type != "file" {
		t.Errorf("nested node = %+v, want file 02.flac", nested)
	}
	if nested.Size == nil || *nested.Size != 2048 {
		t.Errorf("nested size = %v, want 2048", nested.Size)
	}
	if nested.Bitrate != nil {
		t.Errorf("nested bitrate = %v, want null (absent)", nested.Bitrate)
	}
	if nested.Format != "flac" {
		t.Errorf("nested format = %q, want flac", nested.Format)
	}

	flac := tree.Children[1]
	if flac.Type != "file" || flac.Name != "01.flac" || flac.Path != "/music/albumA/01.flac" {
		t.Errorf("children[1] = %+v, want file 01.flac", flac)
	}
	if flac.Size == nil || *flac.Size != 1234 {
		t.Errorf("01.flac size = %v, want 1234", flac.Size)
	}
	if flac.Bitrate == nil || *flac.Bitrate != 320 {
		t.Errorf("01.flac bitrate = %v, want 320", flac.Bitrate)
	}
	if flac.Format != "flac" {
		t.Errorf("01.flac format = %q, want flac", flac.Format)
	}
	if len(flac.Children) != 0 {
		t.Errorf("file node children = %v, want none", flac.Children)
	}

	cover := tree.Children[2]
	if cover.Type != "file" || cover.Name != "cover.jpg" {
		t.Errorf("children[2] = %+v, want file cover.jpg", cover)
	}
	if cover.Bitrate != nil {
		t.Errorf("cover.jpg bitrate = %v, want null (absent)", cover.Bitrate)
	}
	if cover.Format != "" {
		t.Errorf("cover.jpg format = %q, want empty", cover.Format)
	}
}

func TestFolderTreeForeignFolder(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) { repo = d.Repo })

	libA := createLibraryViaAPI(t, engine, "Music", "/music")
	libB := createLibraryViaAPI(t, engine, "Other", "/other")

	insertEntryMeta(t, repo, "/music", "", "music", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA", "/music", "albumA", true, 0, nil, "")
	insertEntryMeta(t, repo, "/music/albumA/01.flac", "/music/albumA", "01.flac", false, 1234, nil, "flac")
	if _, err := repo.ReplaceLibraryFolders(libA, "/music"); err != nil {
		t.Fatalf("ReplaceLibraryFolders failed: %v", err)
	}
	folders, err := repo.ListLibraryFolders(libA)
	if err != nil {
		t.Fatalf("ListLibraryFolders failed: %v", err)
	}
	if len(folders) != 1 {
		t.Fatalf("expected 1 folder, got %d", len(folders))
	}

	// The folder belongs to libA; requesting it under libB must 404.
	w := doRequest(t, engine, http.MethodGet,
		fmt.Sprintf("/api/v1/libraries/%s/folders/%s/tree", libB, folders[0].ID), nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
	code, _ := errorEnvelope(t, w)
	if code != "FOLDER_NOT_FOUND" {
		t.Fatalf("code = %q, want FOLDER_NOT_FOUND", code)
	}
}
