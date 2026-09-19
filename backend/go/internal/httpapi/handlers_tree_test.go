package httpapi //nolint:testpackage // white-box tests exercise unexported internals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	scanusecase "github.com/onsei/organizer/backend/internal/usecase/scan"
)

// treeLibrary creates a library whose root exists on disk. The member routes
// resolve a member against the library root, which is a real directory.
func treeLibrary(t *testing.T, engine http.Handler, repo *sqlite.Repository, name string) (libID, root string) {
	t.Helper()
	root = t.TempDir()
	libID = createLibraryViaAPI(t, engine, name, root)
	insertTreeEntry(t, repo, root, root, "", "root", true, 0, nil, "")
	return libID, root
}

// seedDir creates a real directory under the library root and records it in
// the inventory, the way a scan of that library would.
func seedDir(t *testing.T, repo *sqlite.Repository, root, rel string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", abs, err)
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		parent = root
	}
	insertTreeEntry(t, repo, root, abs, parent, filepath.Base(abs), true, 0, nil, "")
	return abs
}

// seedFile writes a real file under the library root and records it in the
// inventory.
func seedFile(
	t *testing.T,
	repo *sqlite.Repository,
	root, rel string,
	size int64,
	bitrate *int64,
	format string,
) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.WriteFile(abs, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
	insertTreeEntry(t, repo, root, abs, filepath.Dir(abs), filepath.Base(abs), false, size, bitrate, format)
	return abs
}

// insertTreeEntry inserts one row into the entries table with size/bitrate/
// format metadata.
func insertTreeEntry(
	t *testing.T,
	repo *sqlite.Repository,
	root, path, parentPath, name string,
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
		VALUES (?, ?, ?, ?, ?, ?, 0, 'scan-1', 1, ?, ?)
	`,
		filepath.ToSlash(path),
		filepath.ToSlash(root),
		filepath.ToSlash(parentPath),
		name,
		isDirInt,
		size,
		bitrateVal,
		format,
	)
	if err != nil {
		t.Fatalf("failed to insert entry %s: %v", path, err)
	}
}

// TestListLibraryDirs covers the overview listing: every direct child
// directory, with its audio count as a status rather than a filter, and the
// library-relative path as the identity the caller navigates by.
func TestListLibraryDirs(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) { repo = d.Repo })

	libID, root := treeLibrary(t, engine, repo, "Music")
	seedDir(t, repo, root, "albumA")
	seedFile(t, repo, root, "albumA/01.flac", 1234, ptr(int64(320)), "flac")
	seedDir(t, repo, root, "docs")
	seedFile(t, repo, root, "docs/readme.txt", 12, nil, "")
	seedDir(t, repo, root, "Delete")
	seedDir(t, repo, root, "Delete/albumA")

	w := doRequest(t, engine, http.MethodGet, "/api/v1/libraries/"+libID+"/dirs", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var out struct {
		Dirs []dirResponse `json:"dirs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode dirs: %v (body=%s)", err, w.Body.String())
	}
	if len(out.Dirs) != 2 {
		t.Fatalf("dirs = %+v, want albumA and docs (the recovery directory is not a member)", out.Dirs)
	}
	byRel := map[string]dirResponse{}
	for _, d := range out.Dirs {
		byRel[d.RelPath] = d
	}
	album, ok := byRel["albumA"]
	if !ok {
		t.Fatalf("albumA missing from %+v", out.Dirs)
	}
	if album.Name != "albumA" || album.Path != filepath.ToSlash(filepath.Join(root, "albumA")) {
		t.Errorf("albumA = %+v", album)
	}
	if album.AudioFileCount != 1 || album.FileCount != 1 {
		t.Errorf("albumA counts = %d/%d, want 1/1", album.AudioFileCount, album.FileCount)
	}
	docs := byRel["docs"]
	if docs.AudioFileCount != 0 || docs.FileCount != 1 {
		t.Errorf("a directory without audio is still listed: %+v", docs)
	}
}

// TestMemberTree covers the member tree read: no audio filter (an empty or
// audio-less member is browsable), member-relative identity for file
// management, and the sort order the UI shows.
//

func TestMemberTree(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) { repo = d.Repo })

	libID, root := treeLibrary(t, engine, repo, "Music")
	seedDir(t, repo, root, "albumA")
	seedDir(t, repo, root, "albumA/disc2")
	seedFile(t, repo, root, "albumA/01.flac", 1234, ptr(int64(320)), "flac")
	seedFile(t, repo, root, "albumA/disc2/02.flac", 2048, nil, "flac")
	seedFile(t, repo, root, "albumA/cover.jpg", 999, nil, "")

	w := doRequest(t, engine, http.MethodGet,
		fmt.Sprintf("/api/v1/libraries/%s/tree?folder=albumA", libID), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	// treeNodeDTO mirrors the tree response shape for assertions. It is
	// recursive so nested children decode into the same typed nodes.
	type treeNodeDTO struct {
		Name     string        `json:"name"`
		Path     string        `json:"path"`
		RelPath  string        `json:"rel_path"`
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
	if tree.Path != filepath.ToSlash(filepath.Join(root, "albumA")) || tree.RelPath != "" {
		t.Errorf("tree root = %q / %q", tree.Path, tree.RelPath)
	}
	if len(tree.Children) != 3 {
		t.Fatalf("len(children) = %d, want 3 (body=%s)", len(tree.Children), w.Body.String())
	}

	// Dirs sort before files; both sort naturally by name.
	disc2 := tree.Children[0]
	if disc2.Type != "dir" || disc2.Name != "disc2" || disc2.RelPath != "disc2" {
		t.Errorf("children[0] = %+v, want dir disc2 first", disc2)
	}
	if len(disc2.Children) != 1 {
		t.Fatalf("len(disc2.Children) = %d, want 1", len(disc2.Children))
	}
	nested := disc2.Children[0]
	if nested.Name != "02.flac" || nested.RelPath != "disc2/02.flac" || nested.Type != "file" {
		t.Errorf("nested node = %+v, want file 02.flac", nested)
	}
	if nested.Size == nil || *nested.Size != 2048 {
		t.Errorf("nested size = %v, want 2048", nested.Size)
	}
	if nested.Bitrate != nil {
		t.Errorf("nested bitrate = %v, want null (absent)", nested.Bitrate)
	}

	flac := tree.Children[1]
	if flac.Type != "file" || flac.Name != "01.flac" || flac.RelPath != "01.flac" {
		t.Errorf("children[1] = %+v, want file 01.flac", flac)
	}
	if flac.Bitrate == nil || *flac.Bitrate != 320 || flac.Format != "flac" {
		t.Errorf("01.flac = %+v", flac)
	}

	cover := tree.Children[2]
	if cover.Type != "file" || cover.Name != "cover.jpg" || cover.Format != "" {
		t.Errorf("children[2] = %+v, want non-audio file cover.jpg", cover)
	}
	if len(cover.Children) != 0 {
		t.Errorf("file node children = %v, want none", cover.Children)
	}
}

// TestMemberTreeRefusals covers what the member-scoped routes refuse: a
// missing directory, a path that is not a plain relative member path, the
// recovery directory, and a directory of another library.
func TestMemberTreeRefusals(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies) { repo = d.Repo })

	libA, rootA := treeLibrary(t, engine, repo, "Music")
	libB, _ := treeLibrary(t, engine, repo, "Other")
	seedDir(t, repo, rootA, "albumA")
	seedFile(t, repo, rootA, "albumA/01.flac", 1234, nil, "flac")
	seedDir(t, repo, rootA, "Delete")

	cases := []struct {
		name   string
		libID  string
		folder string
		status int
		code   string
	}{
		{"unknown member", libA, "nope", http.StatusNotFound, "MEMBER_MISSING"},
		{"traversal", libA, "../albumA", http.StatusBadRequest, "FOLDER_PATH_INVALID"},
		{"absolute", libA, "/albumA", http.StatusBadRequest, "FOLDER_PATH_INVALID"},
		{"recovery directory", libA, "Delete", http.StatusBadRequest, "MEMBER_PATH_INVALID"},
		{"another library's directory", libB, "albumA", http.StatusNotFound, "MEMBER_MISSING"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, engine, http.MethodGet,
				fmt.Sprintf("/api/v1/libraries/%s/tree?folder=%s", tc.libID, tc.folder), nil, nil)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tc.status, w.Body.String())
			}
			code, _ := errorEnvelope(t, w)
			if code != tc.code {
				t.Fatalf("code = %q, want %q", code, tc.code)
			}
		})
	}
}

// fakeScanService is a handwritten scan double: it records the refreshes it
// was asked for and answers with the configured error.
type fakeScanService struct {
	refreshed []string
	err       error
	onRefresh func()
}

func (f *fakeScanService) Scan(
	_ context.Context,
	_ scanusecase.Request,
	_ func(scanusecase.Event),
) (scanusecase.Result, error) {
	return scanusecase.Result{}, nil
}

func (f *fakeScanService) RefreshMember(_ context.Context, folderPath, _ string) error {
	f.refreshed = append(f.refreshed, folderPath)
	if f.onRefresh != nil {
		f.onRefresh()
	}
	return f.err
}

// TestRefreshMemberTree covers the explicit refresh: the member is re-scanned
// and the refreshed tree is what the caller gets back, so a directory that
// appeared on disk can be shown without leaving the page.
func TestRefreshMemberTree(t *testing.T) {
	var repo *sqlite.Repository
	scan := &fakeScanService{}
	engine := newTestServer(t, func(d *Dependencies) {
		repo = d.Repo
		d.ScanService = scan
	})

	libID, root := treeLibrary(t, engine, repo, "Music")
	seedDir(t, repo, root, "albumA")

	// The refresh stands in for a real scan: it writes what a scan of the
	// member would have written.
	scan.onRefresh = func() {
		seedFile(t, repo, root, "albumA/01.flac", 10, nil, "flac")
	}

	w := doRequest(t, engine, http.MethodPost,
		fmt.Sprintf("/api/v1/libraries/%s/tree/refresh?folder=albumA", libID), nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if len(scan.refreshed) != 1 {
		t.Fatalf("refresh calls = %v, want one", scan.refreshed)
	}
	var out struct {
		Refreshed bool `json:"refreshed"`
		Tree      struct {
			Children []struct {
				Name string `json:"name"`
			} `json:"children"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode refresh: %v (body=%s)", err, w.Body.String())
	}
	if !out.Refreshed || len(out.Tree.Children) != 1 || out.Tree.Children[0].Name != "01.flac" {
		t.Fatalf("refreshed tree = %+v", out)
	}
}

// TestRefreshMemberTreeFailureIsReported covers T2's failure contract: a failed
// refresh is reported as a failed refresh, and it never looks like a tree.
func TestRefreshMemberTreeFailureIsReported(t *testing.T) {
	var repo *sqlite.Repository
	scan := &fakeScanService{err: scanusecase.NewError(
		scanusecase.ErrKindInternal, "SCAN_FAILED", "scan blew up", nil,
	)}
	engine := newTestServer(t, func(d *Dependencies) {
		repo = d.Repo
		d.ScanService = scan
	})

	libID, root := treeLibrary(t, engine, repo, "Music")
	seedDir(t, repo, root, "albumA")

	w := doRequest(t, engine, http.MethodPost,
		fmt.Sprintf("/api/v1/libraries/%s/tree/refresh?folder=albumA", libID), nil, nil)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body=%s)", w.Code, w.Body.String())
	}
	code, _ := errorEnvelope(t, w)
	if code != "SCAN_FAILED" {
		t.Fatalf("code = %q, want SCAN_FAILED", code)
	}
	if body := w.Body.String(); len(body) > 0 && json.Valid([]byte(body)) {
		var envelope map[string]any
		_ = json.Unmarshal([]byte(body), &envelope)
		if _, hasTree := envelope["tree"]; hasTree {
			t.Fatal("a failed refresh must not answer with a tree")
		}
	}
}
