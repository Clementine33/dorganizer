package httpapi //nolint:testpackage // white-box tests exercise unexported internals

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/library"
	"github.com/onsei/organizer/backend/internal/pathnorm"
	"github.com/onsei/organizer/backend/internal/services/fileops"
)

// serverOnDB builds a server over one database file. Opening the same file
// again is the restart a directory identity has to survive.
func serverOnDB(t *testing.T, dbPath string) (http.Handler, *sqlite.Repository) {
	t.Helper()
	// Create-or-open: reopening the path must not truncate the database the
	// first server wrote.
	file, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("create database file: %v", err)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("close database file: %v", closeErr)
	}
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	return NewServer(Dependencies{
		Library:     library.NewService(repo, nil, repo, fileops.ResolveMember),
		CORSOrigins: []string{},
		Version:     "dev",
	}), repo
}

// treeFor reads one member tree by identity, with the token the harness expects.
func treeFor(t *testing.T, engine http.Handler, token, libID, identity string) (int, map[string]any) {
	t.Helper()
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	w := doRequest(t, engine, http.MethodGet,
		fmt.Sprintf("/api/v1/libraries/%s/tree?dir=%s", libID, url.QueryEscape(identity)), nil, headers)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode tree response: %v (body=%s)", err, w.Body.String())
	}
	return w.Code, body
}

// listedDirs reads the overview listing of a library, with the token the
// harness expects.
func listedDirs(t *testing.T, engine http.Handler, token, libID string) []dirResponse {
	t.Helper()
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	w := doRequest(t, engine, http.MethodGet, "/api/v1/libraries/"+libID+"/dirs", nil, headers)
	if w.Code != http.StatusOK {
		t.Fatalf("dirs status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var out struct {
		Dirs []dirResponse `json:"dirs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode dirs: %v (body=%s)", err, w.Body.String())
	}
	return out.Dirs
}

// renameInventoryDir renames a member directory on disk and records what a
// rescan of the library would record instead.
func renameInventoryDir(t *testing.T, repo *sqlite.Repository, root, from, to string) {
	t.Helper()
	fromAbs := pathnorm.NormalizeToPOSIX(filepath.Join(root, filepath.FromSlash(from)))
	toAbs := pathnorm.NormalizeToPOSIX(filepath.Join(root, filepath.FromSlash(to)))
	if err := os.Rename(filepath.FromSlash(fromAbs), filepath.FromSlash(toAbs)); err != nil {
		t.Fatalf("rename %s -> %s: %v", fromAbs, toAbs, err)
	}
	if _, err := repo.DB().Exec(
		`UPDATE entries SET path = ?, name = ? WHERE path = ?`, toAbs, to, fromAbs,
	); err != nil {
		t.Fatalf("update the inventory: %v", err)
	}
}

// TestDirIDSurvivesARescanAndARestart covers the stability a link depends on:
// the same root and path keep the same identity while the inventory grows, and
// after the process opens the same database again.
func TestDirIDSurvivesARescanAndARestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "restart.db")
	engine, repo := serverOnDB(t, dbPath)
	t.Cleanup(func() { _ = repo.Close() })

	libID, root := treeLibrary(t, engine, repo, "Music")
	seedDir(t, repo, root, "albumA")
	before := dirIDFor(t, engine, libID, "albumA")

	// A later scan adds rows; it renumbers nothing the caller navigates by.
	seedFile(t, repo, root, "albumA/01.flac", 10, nil, "flac")
	seedDir(t, repo, root, "albumA/disc2")
	if after := dirIDFor(t, engine, libID, "albumA"); after != before {
		t.Fatalf("identity after a later scan = %q, want %q", after, before)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("close repository: %v", err)
	}

	engine2, repo2 := serverOnDB(t, dbPath)
	t.Cleanup(func() { _ = repo2.Close() })
	if after := dirIDFor(t, engine2, libID, "albumA"); after != before {
		t.Fatalf("identity after a restart = %q, want %q", after, before)
	}
	if code, body := treeFor(t, engine2, "", libID, before); code != http.StatusOK || body["member_path"] != "albumA" {
		t.Fatalf("the stored identity resolved to %v / %v", code, body)
	}
}

// TestDirIDCoversUnicodeAndReservedCharacters covers the directories a name in
// an address could never carry: the identity is plain hex for all of them, and
// the answer still names the exact stored path, byte for byte.
func TestDirIDCoversUnicodeAndReservedCharacters(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies, fixture *sqlite.Repository) { repo = fixture })
	libID, root := treeLibrary(t, engine, repo, "Music")

	names := []string{
		"中文专辑",
		"日本語 アルバム",
		"한국어",
		"with space",
		"100%_hits",
		"a#b",
		"c?d",
		"🎵 emoji",
		"café",       // NFC
		"cafe\u0301", // the same word as NFD: a different path, a different identity
	}
	for _, name := range names {
		seedDir(t, repo, root, name)
	}

	seen := make(map[string]string, len(names))
	for _, name := range names {
		identity := dirIDFor(t, engine, libID, name)
		if !library.ValidDirID(identity) {
			t.Fatalf("identity of %q = %q, want 32 lowercase hex characters", name, identity)
		}
		if other, taken := seen[identity]; taken {
			t.Fatalf("%q and %q share the identity %q", other, name, identity)
		}
		seen[identity] = name

		code, body := treeFor(t, engine, "", libID, identity)
		if code != http.StatusOK {
			t.Fatalf("%q: status = %d, want 200 (body=%v)", name, code, body)
		}
		if body["member_path"] != name {
			t.Fatalf("%q resolved to %v: the stored path is answered unchanged", name, body["member_path"])
		}
		if body["dir_id"] != identity {
			t.Fatalf("%q: response identity = %v, want %q", name, body["dir_id"], identity)
		}
	}
}

// TestDirIDFollowsThePathNotTheFilesystemEntity covers the binding rule: the
// identity names a path. A directory renamed outside the application loses the
// identity it had, and a path that appears again is the same directory again —
// nothing is tracked across that gap.
func TestDirIDFollowsThePathNotTheFilesystemEntity(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies, fixture *sqlite.Repository) { repo = fixture })
	libID, root := treeLibrary(t, engine, repo, "Music")
	seedDir(t, repo, root, "albumA")
	original := dirIDFor(t, engine, libID, "albumA")

	renameInventoryDir(t, repo, root, "albumA", "renamed")
	if code, body := treeFor(t, engine, "", libID, original); code != http.StatusNotFound {
		t.Fatalf("the renamed directory's old identity resolved with %d (%v), want 404", code, body)
	}
	renamed := dirIDFor(t, engine, libID, "renamed")
	if renamed == original {
		t.Fatal("a renamed directory kept its identity: identity binds to the path")
	}

	renameInventoryDir(t, repo, root, "renamed", "albumA")
	if again := dirIDFor(t, engine, libID, "albumA"); again != original {
		t.Fatalf("the returning path got identity %q, want %q", again, original)
	}
	if code, body := treeFor(t, engine, "", libID, original); code != http.StatusOK || body["member_path"] != "albumA" {
		t.Fatalf("the returning path resolved to %v / %v", code, body)
	}
}

// TestDirIDStopsResolvingAfterARootChange covers the other invalidation: the
// library root is part of the identity, so the same relative path under a new
// root is a different directory and the old links are gone.
func TestDirIDStopsResolvingAfterARootChange(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies, fixture *sqlite.Repository) { repo = fixture })
	libID, oldRoot := treeLibrary(t, engine, repo, "Music")
	seedDir(t, repo, oldRoot, "albumA")
	oldIdentity := dirIDFor(t, engine, libID, "albumA")

	newRoot := t.TempDir()
	w := doRequest(t, engine, http.MethodPatch, "/api/v1/libraries/"+libID,
		map[string]string{"root_path": newRoot}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("root change status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	seedDir(t, repo, newRoot, "albumA")

	newIdentity := dirIDFor(t, engine, libID, "albumA")
	if newIdentity == oldIdentity {
		t.Fatal("the same path under a new root kept the old identity")
	}
	if code, body := treeFor(t, engine, "", libID, oldIdentity); code != http.StatusNotFound {
		t.Fatalf("the old root's identity resolved with %d (%v), want 404", code, body)
	}
	if code, body := treeFor(
		t,
		engine,
		"",
		libID,
		newIdentity,
	); code != http.StatusOK ||
		body["member_path"] != "albumA" {
		t.Fatalf("the new root's identity resolved to %v / %v", code, body)
	}
}

// TestADirectoryOutsideTheInventoryHasNoIdentity covers where the identity
// comes from: the scanned inventory, not the disk. A directory no scan has seen
// has no identity to name, and its path-derived value is unknown.
func TestADirectoryOutsideTheInventoryHasNoIdentity(t *testing.T) {
	var repo *sqlite.Repository
	engine := newTestServer(t, func(d *Dependencies, fixture *sqlite.Repository) { repo = fixture })
	libID, root := treeLibrary(t, engine, repo, "Music")
	if err := os.MkdirAll(filepath.Join(root, "unscanned"), 0o755); err != nil {
		t.Fatalf("create an unscanned directory: %v", err)
	}

	for _, dir := range listedDirs(t, engine, "", libID) {
		if dir.RelPath == "unscanned" {
			t.Fatal("a directory no scan has seen was listed")
		}
	}
	if code, body := treeFor(
		t,
		engine,
		"",
		libID,
		library.DirID(libID, root, "unscanned"),
	); code != http.StatusNotFound {
		t.Fatalf("status = %d (%v), want 404: resolution reads the inventory", code, body)
	}
}

// TestMemberIdentityMatchesTheOverviewListing covers the cache the two entries
// share: the identity the overview lists and the identity the record hands out
// for the same directory are one value, so both entries address one file cache
// entry (ADR 0003 §6).
func TestMemberIdentityMatchesTheOverviewListing(t *testing.T) {
	engine, repo := newWorksetServer(t)
	root := t.TempDir()
	created := req(t, engine, http.MethodPost, "/api/v1/libraries", testToken,
		map[string]string{"name": "Music", "root_path": root})
	if created.Code != http.StatusCreated {
		t.Fatalf("create library status = %d (body=%s)", created.Code, created.Body.String())
	}
	var library libraryResponse
	if err := json.Unmarshal(created.Body.Bytes(), &library); err != nil {
		t.Fatalf("decode library: %v", err)
	}
	libID := library.ID
	seedDir(t, repo, root, "albumA")
	seedFile(t, repo, root, "albumA/01.flac", 1024, nil, "flac")
	createRecord(t, engine, libID, "idem-dirid")

	listing := listedDirs(t, engine, testToken, libID)
	if len(listing) != 1 {
		t.Fatalf("listing = %+v, want the one member", listing)
	}
	w := req(t, engine, http.MethodGet, recordsPath(libID), testToken, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("record status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var current struct {
		Workset struct {
			Members []memberResponse `json:"members"`
		} `json:"workset"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode record: %v (body=%s)", err, w.Body.String())
	}
	if len(current.Workset.Members) != 1 {
		t.Fatalf("members = %+v, want one", current.Workset.Members)
	}
	member := current.Workset.Members[0]
	if member.DirID != listing[0].DirID {
		t.Fatalf(
			"the record's member identity = %q, the overview lists %q: they must be one identity",
			member.DirID, listing[0].DirID,
		)
	}
	if code, body := treeFor(t, engine, testToken, libID, member.DirID); code != http.StatusOK ||
		body["member_path"] != "albumA" {
		t.Fatalf("the identity the record handed out resolved to %v / %v", code, body)
	}
}
