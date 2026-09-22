package sqlite //nolint:testpackage // white-box tests exercise unexported internals

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/library"
)

// insertEntryAtRoot inserts an entry belonging to another library root, so a
// test can assert that a change scoped to one root leaves the others alone.
func insertEntryAtRoot(t *testing.T, repo *Repository, path, parentPath, name string, isDir bool) {
	t.Helper()
	isDirInt := 0
	if isDir {
		isDirInt = 1
	}
	root := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		root = path[:idx]
	}
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, scan_id, content_rev)
		VALUES (?, ?, ?, ?, ?, 0, 0, 'scan-1', 1)
	`, path, root, parentPath, name, isDirInt)
	if err != nil {
		t.Fatalf("failed to insert entry %s: %v", path, err)
	}
}

// seedRecordWithPlan creates a processing record with a member, an operation,
// a draft and a plan, so delete/cleanup paths have something to remove.
func seedRecordWithPlan(t *testing.T, repo *Repository, libraryID, worksetID, planID string) error {
	t.Helper()
	now := time.Now().Format(timeFormat)
	for _, stmt := range []string{
		`INSERT INTO worksets (id, title, library_id, operation_type, root_path, root_path_key, created_at, updated_at)
		 VALUES ('` + worksetID + `', 'Music', '` + libraryID + `', 'conversion', '/music', '/music', '` + now + `', '` + now + `')`,
		`INSERT INTO workset_members (workset_id, member_id, member_index, rel_path, folder_path, folder_name)
		 VALUES ('` + worksetID + `', 'm-1', 0, 'albumA', '/music/albumA', 'albumA')`,
		`INSERT INTO workset_operations (workset_id, operation_type, version, created_at, updated_at)
		 VALUES ('` + worksetID + `', 'conversion', 1, '` + now + `', '` + now + `')`,
		`INSERT INTO workset_operation_drafts (workset_id, operation_type, schema_version, draft_json, draft_hash, updated_at)
		 VALUES ('` + worksetID + `', 'conversion', 1, '{}', 'h', '` + now + `')`,
		`INSERT INTO plans (plan_id, root_path, scan_root_path, workset_id, snapshot_token, task_kind, task_schema_version, created_at)
		 VALUES ('` + planID + `', '/music', '/music', '` + worksetID + `', 'snap', 'conversion', 1, '` + now + `')`,
	} {
		if _, err := repo.DB().Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// insertEntry inserts a row into the entries table with a fixed root_path of
// /music, mirroring how scan results are stored.
func insertEntry(t *testing.T, repo *Repository, path, parentPath, name string, isDir bool) {
	t.Helper()
	isDirInt := 0
	if isDir {
		isDirInt = 1
	}
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, scan_id, content_rev)
		VALUES (?, '/music', ?, ?, ?, 0, 0, 'scan-1', 1)
	`, path, parentPath, name, isDirInt)
	if err != nil {
		t.Fatalf("failed to insert entry %s: %v", path, err)
	}
}

func TestCreateAndGetLibrary(t *testing.T) {
	repo := newTestRepository(t)

	// Windows-style root path should be stored POSIX-normalized.
	lib, err := repo.CreateLibrary("My Music", `C:\music`)
	if err != nil {
		t.Fatalf("CreateLibrary failed: %v", err)
	}
	if lib.ID == "" {
		t.Error("expected non-empty library ID")
	}
	if lib.Name != "My Music" {
		t.Errorf("expected name %q, got %q", "My Music", lib.Name)
	}
	if lib.RootPath != "C:/music" {
		t.Errorf("expected POSIX-normalized root path %q, got %q", "C:/music", lib.RootPath)
	}
	if lib.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}

	fetched, err := repo.GetLibrary(lib.ID)
	if err != nil {
		t.Fatalf("GetLibrary failed: %v", err)
	}
	if fetched.Name != "My Music" || fetched.RootPath != "C:/music" {
		t.Errorf("round-trip mismatch: got %+v", fetched)
	}

	_, err = repo.GetLibrary("no-such-id")
	if !errors.Is(err, library.ErrLibraryNotFound) {
		t.Errorf("expected library.ErrLibraryNotFound for unknown id, got %v", err)
	}
}

func TestCreateLibraryDuplicateRootPath(t *testing.T) {
	repo := newTestRepository(t)

	if _, err := repo.CreateLibrary("One", "/music"); err != nil {
		t.Fatalf("first CreateLibrary failed: %v", err)
	}

	// Same path directly.
	if _, err := repo.CreateLibrary("Two", "/music"); !errors.Is(err, library.ErrLibraryExists) {
		t.Errorf("expected library.ErrLibraryExists for duplicate root path, got %v", err)
	}

	// Same path with a different style, normalized to the same value.
	if _, err := repo.CreateLibrary("Three", `\music`); !errors.Is(err, library.ErrLibraryExists) {
		t.Errorf("expected library.ErrLibraryExists for normalized duplicate root path, got %v", err)
	}
}

func TestListAndUpdateAndDeleteLibrary(t *testing.T) {
	repo := newTestRepository(t)

	lib1, err := repo.CreateLibrary("First", "/music")
	if err != nil {
		t.Fatalf("CreateLibrary(First) failed: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	lib2, err := repo.CreateLibrary("Second", "/movies")
	if err != nil {
		t.Fatalf("CreateLibrary(Second) failed: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	lib3, err := repo.CreateLibrary("Third", "/shows")
	if err != nil {
		t.Fatalf("CreateLibrary(Third) failed: %v", err)
	}

	// List ordered by created_at (newest first).
	libraries, err := repo.ListLibraries()
	if err != nil {
		t.Fatalf("ListLibraries failed: %v", err)
	}
	if len(libraries) != 3 {
		t.Fatalf("expected 3 libraries, got %d", len(libraries))
	}
	if libraries[0].ID != lib3.ID || libraries[1].ID != lib2.ID || libraries[2].ID != lib1.ID {
		t.Errorf("expected order [%s, %s, %s], got [%s, %s, %s]",
			lib3.ID, lib2.ID, lib1.ID, libraries[0].ID, libraries[1].ID, libraries[2].ID)
	}

	// Update name and root path.
	updated, err := repo.UpdateLibrary(lib2.ID, "Second Renamed", `/cinema`)
	if err != nil {
		t.Fatalf("UpdateLibrary failed: %v", err)
	}
	if updated.Name != "Second Renamed" || updated.RootPath != "/cinema" {
		t.Errorf("update result mismatch: got %+v", updated)
	}

	// Updating to a duplicate root path must fail with ErrLibraryExists.
	if _, err := repo.UpdateLibrary(lib3.ID, "Third", "/cinema"); !errors.Is(err, library.ErrLibraryExists) {
		t.Errorf("expected library.ErrLibraryExists on update to duplicate root path, got %v", err)
	}

	// Updating an unknown library must fail with ErrLibraryNotFound.
	if _, err := repo.UpdateLibrary("no-such-id", "Nope", "/x"); !errors.Is(err, library.ErrLibraryNotFound) {
		t.Errorf("expected library.ErrLibraryNotFound on update of unknown id, got %v", err)
	}

	// A processing record (with its members, operation, draft and plan) is
	// deleted with the library: no orphaned record survives its library.
	if err := seedRecordWithPlan(t, repo, lib1.ID, "ws-1", "plan-1"); err != nil {
		t.Fatalf("seed record: %v", err)
	}

	if err := repo.DeleteLibrary(lib1.ID); err != nil {
		t.Fatalf("DeleteLibrary failed: %v", err)
	}
	if _, err := repo.GetLibrary(lib1.ID); !errors.Is(err, library.ErrLibraryNotFound) {
		t.Errorf("expected library.ErrLibraryNotFound after delete, got %v", err)
	}

	for _, query := range []struct {
		name string
		sql  string
	}{
		{"records", `SELECT COUNT(*) FROM worksets WHERE library_id = ?`},
		{"plans", `SELECT COUNT(*) FROM plans WHERE plan_id = 'plan-1'`},
		{"members", `SELECT COUNT(*) FROM workset_members WHERE workset_id = 'ws-1'`},
		{"drafts", `SELECT COUNT(*) FROM workset_operation_drafts WHERE workset_id = 'ws-1'`},
	} {
		var n int
		args := []any{}
		if strings.Contains(query.sql, "?") {
			args = append(args, lib1.ID)
		}
		if err := repo.DB().QueryRow(query.sql, args...).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", query.name, err)
		}
		if n != 0 {
			t.Errorf("expected no %s left after deleting the library, got %d", query.name, n)
		}
	}

	// Deleting an unknown library must fail with ErrLibraryNotFound.
	if err := repo.DeleteLibrary("no-such-id"); !errors.Is(err, library.ErrLibraryNotFound) {
		t.Errorf("expected library.ErrLibraryNotFound on delete of unknown id, got %v", err)
	}
}

func TestUpdateLibraryRootClearsDerivedState(t *testing.T) {
	repo := newTestRepository(t)
	lib, err := repo.CreateLibrary("Music", "/music")
	if err != nil {
		t.Fatalf("CreateLibrary failed: %v", err)
	}
	insertEntry(t, repo, "/music", "", "music", true)
	insertEntry(t, repo, "/music/album", "/music", "album", true)
	insertEntry(t, repo, "/music/album/01.flac", "/music/album", "01.flac", false)
	insertEntryAtRoot(t, repo, "/movies", "/movies", "movie.mp4", false)
	if stateErr := repo.UpdateLibraryScanState(lib.ID, "completed", "", time.Now()); stateErr != nil {
		t.Fatalf("UpdateLibraryScanState failed: %v", stateErr)
	}

	updated, err := repo.UpdateLibrary(lib.ID, "Music", "/new-music")
	if err != nil {
		t.Fatalf("UpdateLibrary failed: %v", err)
	}
	if updated.RootPath != "/new-music" {
		t.Fatalf("root_path = %q, want /new-music", updated.RootPath)
	}
	if updated.LastScanAt != nil || updated.LastScanStatus != "" || updated.LastScanError != "" {
		t.Fatalf("scan state was not reset: %+v", updated)
	}
	var stale int
	if scanErr := repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries WHERE root_path = '/music'",
	).Scan(&stale); scanErr != nil {
		t.Fatalf("count stale entries: %v", scanErr)
	}
	if stale != 0 {
		t.Fatalf("root change retained %d entries of the old root", stale)
	}
}

// TestListLibraryDirs lists every direct child directory of the root — with
// audio, without audio, and empty — with the audio and file counts of each
// subtree. The library-level recovery directory is not a member.
func TestListLibraryDirs(t *testing.T) {
	repo := newTestRepository(t)

	insertEntry(t, repo, "/music", "", "music", true)
	insertEntry(t, repo, "/music/albumA", "/music", "albumA", true)
	insertEntry(t, repo, "/music/albumB", "/music", "albumB", true)
	insertEntry(t, repo, "/music/docs", "/music", "docs", true)
	insertEntry(t, repo, "/music/empty", "/music", "empty", true)
	insertEntry(t, repo, "/music/Delete", "/music", "Delete", true)
	insertEntry(t, repo, "/music/Delete/albumA", "/music/Delete", "albumA", true)
	insertEntry(t, repo, "/music/albumA/01.flac", "/music/albumA", "01.flac", false)
	insertEntry(t, repo, "/music/albumA/disc2", "/music/albumA", "disc2", true)
	insertEntry(t, repo, "/music/albumA/disc2/02.flac", "/music/albumA/disc2", "02.flac", false)
	insertEntry(t, repo, "/music/docs/readme.txt", "/music/docs", "readme.txt", false)
	insertEntry(t, repo, "/music/Delete/albumA/old.mp3", "/music/Delete/albumA", "old.mp3", false)
	// A file directly in the root is not a member.
	insertEntry(t, repo, "/music/loose.mp3", "/music", "loose.mp3", false)

	dirs, err := repo.ListLibraryDirs("/music")
	if err != nil {
		t.Fatalf("ListLibraryDirs failed: %v", err)
	}
	got := map[string]library.LibraryDir{}
	for _, d := range dirs {
		got[d.RelPath] = *d
	}
	if len(got) != 4 {
		t.Fatalf("expected the four non-recovery child directories, got %+v", got)
	}
	if _, ok := got["Delete"]; ok {
		t.Error("the recovery directory must not be a member")
	}
	if d := got["albumA"]; d.AudioFileCount != 2 || d.FileCount != 2 || d.Name != "albumA" {
		t.Errorf("albumA counts = audio %d file %d name %q", d.AudioFileCount, d.FileCount, d.Name)
	}
	if d := got["albumB"]; d.AudioFileCount != 0 || d.FileCount != 0 {
		t.Errorf("a directory without audio is still listed, with zero counts: %+v", d)
	}
	if d := got["empty"]; d.AudioFileCount != 0 || d.FileCount != 0 {
		t.Errorf("an empty directory is still listed: %+v", d)
	}
	if d := got["docs"]; d.AudioFileCount != 0 || d.FileCount != 1 {
		t.Errorf("docs counts = audio %d file %d, want 0/1", d.AudioFileCount, d.FileCount)
	}

	counts, err := repo.DirAudioCounts("/music", []string{"albumA", "albumB", "docs", "gone", "loose.mp3"})
	if err != nil {
		t.Fatalf("DirAudioCounts failed: %v", err)
	}
	if counts["albumA"] != 2 || counts["albumB"] != 0 || counts["docs"] != 0 {
		t.Errorf("DirAudioCounts = %+v", counts)
	}
	if _, ok := counts["gone"]; ok {
		t.Error("a directory the inventory does not know must be absent, not zero")
	}
	if _, ok := counts["loose.mp3"]; ok {
		t.Error("a file is not a member directory")
	}
}

func TestListEntriesUnderPathEscapesWildcards(t *testing.T) {
	repo := newTestRepository(t)

	// Directories whose names contain LIKE wildcard characters.
	insertEntry(t, repo, "/music/foo%bar", "/music", "foo%bar", true)
	insertEntry(t, repo, "/music/foo%bar/01.flac", "/music/foo%bar", "01.flac", false)
	insertEntry(t, repo, "/music/a_b", "/music", "a_b", true)
	insertEntry(t, repo, "/music/a_b/01.wav", "/music/a_b", "01.wav", false)
	// Siblings that the unescaped patterns `/music/foo%bar/%` and
	// `/music/a_b/%` would wrongly match.
	insertEntry(t, repo, "/music/foobazbar", "/music", "foobazbar", true)
	insertEntry(t, repo, "/music/foobazbar/track.flac", "/music/foobazbar", "track.flac", false)
	insertEntry(t, repo, "/music/axb", "/music", "axb", true)
	insertEntry(t, repo, "/music/axb/01.wav", "/music/axb", "01.wav", false)

	check := func(prefix string, want []string) {
		t.Helper()
		entries, err := repo.ListEntriesUnderPath(prefix)
		if err != nil {
			t.Fatalf("ListEntriesUnderPath(%q) failed: %v", prefix, err)
		}
		var paths []string
		for _, e := range entries {
			paths = append(paths, e.Path)
		}
		if !reflect.DeepEqual(paths, want) {
			t.Errorf("ListEntriesUnderPath(%q) paths = %v, want %v", prefix, paths, want)
		}
	}

	check("/music/foo%bar", []string{"/music/foo%bar", "/music/foo%bar/01.flac"})
	check("/music/a_b", []string{"/music/a_b", "/music/a_b/01.wav"})
}

// TestListLibraryDirsEscapesWildcardPaths verifies that a directory whose name
// contains LIKE wildcard characters only collects its own audio: the unescaped
// pattern would attribute sibling files to it.
func TestListLibraryDirsEscapesWildcardPaths(t *testing.T) {
	repo := newTestRepository(t)

	// A wildcard-named directory with NO audio of its own, plus a sibling
	// whose files the unescaped pattern `/music/100%off/%` would match.
	insertEntry(t, repo, "/music/100%off", "/music", "100%off", true)
	insertEntry(t, repo, "/music/100Xoff", "/music", "100Xoff", true)
	insertEntry(t, repo, "/music/100Xoff/track.mp3", "/music/100Xoff", "track.mp3", false)

	dirs, err := repo.ListLibraryDirs("/music")
	if err != nil {
		t.Fatalf("ListLibraryDirs failed: %v", err)
	}
	counts := map[string]int{}
	for _, d := range dirs {
		counts[d.RelPath] = d.AudioFileCount
	}
	if counts["100%off"] != 0 {
		t.Errorf("wildcard-named directory collected a sibling's audio: %+v", counts)
	}
	if counts["100Xoff"] != 1 {
		t.Errorf("100Xoff audio count = %d, want 1", counts["100Xoff"])
	}
}

func TestCreateLibraryCanonicalIdentity(t *testing.T) {
	repo := newTestRepository(t)

	if _, err := repo.CreateLibrary("One", "/music"); err != nil {
		t.Fatalf("CreateLibrary(/music) failed: %v", err)
	}
	// Lexically equivalent spelling must conflict.
	if _, err := repo.CreateLibrary("Two", "/music/."); !errors.Is(err, library.ErrLibraryExists) {
		t.Errorf("expected library.ErrLibraryExists for `/music/.`, got %v", err)
	}
	// Windows-syntax roots collide on case regardless of the host OS.
	if _, err := repo.CreateLibrary("Three", `C:\Music`); err != nil {
		t.Fatalf("CreateLibrary(C:\\Music) failed: %v", err)
	}
	if _, err := repo.CreateLibrary("Four", `c:\music\`); !errors.Is(err, library.ErrLibraryExists) {
		t.Errorf("expected library.ErrLibraryExists for `c:\\music\\` vs `C:\\Music`, got %v", err)
	}
}

func TestUpdateLibraryEquivalentRootKeepsDerivedState(t *testing.T) {
	repo := newTestRepository(t)
	lib, err := repo.CreateLibrary("Music", "/music")
	if err != nil {
		t.Fatalf("CreateLibrary failed: %v", err)
	}
	insertEntry(t, repo, "/music/album", "/music", "album", true)
	if stateErr := repo.UpdateLibraryScanState(lib.ID, "completed", "", time.Now()); stateErr != nil {
		t.Fatalf("UpdateLibraryScanState failed: %v", stateErr)
	}

	// A spelling-only root edit must not invalidate folders or scan state,
	// and the stored root is the cleaned canonical form.
	updated, err := repo.UpdateLibrary(lib.ID, "Music", "/music/.")
	if err != nil {
		t.Fatalf("UpdateLibrary failed: %v", err)
	}
	if updated.RootPath != "/music" {
		t.Errorf("expected cleaned root_path /music, got %q", updated.RootPath)
	}
	if updated.LastScanAt == nil || updated.LastScanStatus != "completed" {
		t.Errorf("scan state must be retained for spelling-only root update: %+v", updated)
	}
	var kept int
	if scanErr := repo.DB().QueryRow(
		"SELECT COUNT(*) FROM entries WHERE root_path = '/music'",
	).Scan(&kept); scanErr != nil {
		t.Fatalf("count entries: %v", scanErr)
	}
	if kept != 1 {
		t.Errorf("inventory must survive a spelling-only root update, got %d rows", kept)
	}
}

func TestListLibraryDirsCaseSensitiveSiblings(t *testing.T) {
	repo := newTestRepository(t)

	// Case-distinct sibling directories, each with one audio file. SQLite's
	// ASCII-case-insensitive LIKE would attribute both files to both folders;
	// path identity must be binary.
	insertEntry(t, repo, "/music/Rock", "/music", "Rock", true)
	insertEntry(t, repo, "/music/rock", "/music", "rock", true)
	insertEntry(t, repo, "/music/Rock/a.mp3", "/music/Rock", "a.mp3", false)
	insertEntry(t, repo, "/music/rock/b.mp3", "/music/rock", "b.mp3", false)

	dirs, err := repo.ListLibraryDirs("/music")
	if err != nil {
		t.Fatalf("ListLibraryDirs failed: %v", err)
	}
	counts := map[string]int{}
	for _, d := range dirs {
		counts[d.RelPath] = d.AudioFileCount
	}
	if counts["Rock"] != 1 || counts["rock"] != 1 {
		t.Errorf("expected each case-distinct folder to count only its own subtree, got %+v", counts)
	}
}

func TestListEntriesUnderPathIsCaseSensitive(t *testing.T) {
	repo := newTestRepository(t)
	insertEntry(t, repo, "/music/Rock", "/music", "Rock", true)
	insertEntry(t, repo, "/music/rock", "/music", "rock", true)
	insertEntry(t, repo, "/music/Rock/a.mp3", "/music/Rock", "a.mp3", false)
	insertEntry(t, repo, "/music/rock/b.mp3", "/music/rock", "b.mp3", false)

	entries, err := repo.ListEntriesUnderPath("/music/Rock")
	if err != nil {
		t.Fatalf("ListEntriesUnderPath failed: %v", err)
	}
	var paths []string
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	want := []string{"/music/Rock", "/music/Rock/a.mp3"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("ListEntriesUnderPath(/music/Rock) = %v, want %v", paths, want)
	}
}
