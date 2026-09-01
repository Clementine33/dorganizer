package sqlite

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

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
	if !errors.Is(err, ErrLibraryNotFound) {
		t.Errorf("expected ErrLibraryNotFound for unknown id, got %v", err)
	}
}

func TestCreateLibraryDuplicateRootPath(t *testing.T) {
	repo := newTestRepository(t)

	if _, err := repo.CreateLibrary("One", "/music"); err != nil {
		t.Fatalf("first CreateLibrary failed: %v", err)
	}

	// Same path directly.
	if _, err := repo.CreateLibrary("Two", "/music"); !errors.Is(err, ErrLibraryExists) {
		t.Errorf("expected ErrLibraryExists for duplicate root path, got %v", err)
	}

	// Same path with a different style, normalized to the same value.
	if _, err := repo.CreateLibrary("Three", `\music`); !errors.Is(err, ErrLibraryExists) {
		t.Errorf("expected ErrLibraryExists for normalized duplicate root path, got %v", err)
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
	if _, err := repo.UpdateLibrary(lib3.ID, "Third", "/cinema"); !errors.Is(err, ErrLibraryExists) {
		t.Errorf("expected ErrLibraryExists on update to duplicate root path, got %v", err)
	}

	// Updating an unknown library must fail with ErrLibraryNotFound.
	if _, err := repo.UpdateLibrary("no-such-id", "Nope", "/x"); !errors.Is(err, ErrLibraryNotFound) {
		t.Errorf("expected ErrLibraryNotFound on update of unknown id, got %v", err)
	}

	// Seed a folder row so DeleteLibrary can cascade it away.
	if _, err := repo.DB().Exec(`
		INSERT INTO library_folders (id, library_id, path, name, relative_path, audio_file_count)
		VALUES ('folder-1', ?, '/music/albumA', 'albumA', 'albumA', 1)
	`, lib1.ID); err != nil {
		t.Fatalf("failed to seed library_folder: %v", err)
	}

	if err := repo.DeleteLibrary(lib1.ID); err != nil {
		t.Fatalf("DeleteLibrary failed: %v", err)
	}
	if _, err := repo.GetLibrary(lib1.ID); !errors.Is(err, ErrLibraryNotFound) {
		t.Errorf("expected ErrLibraryNotFound after delete, got %v", err)
	}

	// FK cascade should have removed the folder row.
	var folderCount int
	if err := repo.DB().
		QueryRow(`SELECT COUNT(*) FROM library_folders WHERE library_id = ?`, lib1.ID).
		Scan(&folderCount); err != nil {
		t.Fatalf("failed to count library_folders: %v", err)
	}
	if folderCount != 0 {
		t.Errorf("expected cascade delete of library_folders, got %d rows remaining", folderCount)
	}

	// Deleting an unknown library must fail with ErrLibraryNotFound.
	if err := repo.DeleteLibrary("no-such-id"); !errors.Is(err, ErrLibraryNotFound) {
		t.Errorf("expected ErrLibraryNotFound on delete of unknown id, got %v", err)
	}
}

func TestUpdateLibraryRootClearsDerivedState(t *testing.T) {
	repo := newTestRepository(t)
	lib, err := repo.CreateLibrary("Music", "/music")
	if err != nil {
		t.Fatalf("CreateLibrary failed: %v", err)
	}
	if _, err := repo.DB().Exec(`
		INSERT INTO library_folders (id, library_id, path, name, relative_path, audio_file_count)
		VALUES ('folder-old', ?, '/music/album', 'album', 'album', 1)
	`, lib.ID); err != nil {
		t.Fatalf("seed library folder: %v", err)
	}
	if err := repo.UpdateLibraryScanState(lib.ID, "completed", "", time.Now()); err != nil {
		t.Fatalf("UpdateLibraryScanState failed: %v", err)
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
	folders, err := repo.ListLibraryFolders(lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryFolders failed: %v", err)
	}
	if len(folders) != 0 {
		t.Fatalf("root change retained %d stale folders", len(folders))
	}
}

func TestReplaceLibraryFolders(t *testing.T) {
	repo := newTestRepository(t)

	lib, err := repo.CreateLibrary("Music", "/music")
	if err != nil {
		t.Fatalf("CreateLibrary failed: %v", err)
	}

	t.Run("keeps only direct children that contain audio", func(t *testing.T) {
		// Root and child dirs.
		insertEntry(t, repo, "/music", "", "music", true)
		insertEntry(t, repo, "/music/albumA", "/music", "albumA", true)
		insertEntry(t, repo, "/music/albumB", "/music", "albumB", true)
		insertEntry(t, repo, "/music/docs", "/music", "docs", true)
		// Audio under albumA, including a nested dir.
		insertEntry(t, repo, "/music/albumA/01.flac", "/music/albumA", "01.flac", false)
		insertEntry(t, repo, "/music/albumA/disc2", "/music/albumA", "disc2", true)
		insertEntry(t, repo, "/music/albumA/disc2/02.flac", "/music/albumA/disc2", "02.flac", false)
		// Non-audio files under docs.
		insertEntry(t, repo, "/music/docs/readme.txt", "/music/docs", "readme.txt", false)
		// albumB has no audio. A nested audio file alone must not surface disc2.

		n, err := repo.ReplaceLibraryFolders(lib.ID, "/music")
		if err != nil {
			t.Fatalf("ReplaceLibraryFolders failed: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected 1 folder, got %d", n)
		}

		folders, err := repo.ListLibraryFolders(lib.ID)
		if err != nil {
			t.Fatalf("ListLibraryFolders failed: %v", err)
		}
		if len(folders) != 1 {
			t.Fatalf("expected 1 folder, got %d", len(folders))
		}
		f := folders[0]
		if f.Path != "/music/albumA" {
			t.Errorf("expected path /music/albumA, got %q", f.Path)
		}
		if f.Name != "albumA" {
			t.Errorf("expected name albumA, got %q", f.Name)
		}
		if f.RelativePath != "albumA" {
			t.Errorf("expected relative_path albumA, got %q", f.RelativePath)
		}
		if f.AudioFileCount != 2 {
			t.Errorf("expected audio_file_count 2 (01.flac + disc2/02.flac), got %d", f.AudioFileCount)
		}

		// GetLibraryFolder round-trip.
		got, err := repo.GetLibraryFolder(lib.ID, f.ID)
		if err != nil {
			t.Fatalf("GetLibraryFolder failed: %v", err)
		}
		if got.ID != f.ID || got.Path != f.Path {
			t.Errorf("GetLibraryFolder mismatch: got %+v", got)
		}
	})

	t.Run("includes every direct child that has audio", func(t *testing.T) {
		// Give albumB audio and replace again: albumA and albumB both qualify.
		insertEntry(t, repo, "/music/albumB/track.mp3", "/music/albumB", "track.mp3", false)

		n, err := repo.ReplaceLibraryFolders(lib.ID, "/music")
		if err != nil {
			t.Fatalf("ReplaceLibraryFolders failed: %v", err)
		}
		if n != 2 {
			t.Fatalf("expected 2 folders, got %d", n)
		}

		folders, err := repo.ListLibraryFolders(lib.ID)
		if err != nil {
			t.Fatalf("ListLibraryFolders failed: %v", err)
		}
		if len(folders) != 2 {
			t.Fatalf("expected 2 folders, got %d", len(folders))
		}
		if folders[0].Path != "/music/albumA" || folders[1].Path != "/music/albumB" {
			t.Errorf("expected folders [albumA, albumB] by path, got [%s, %s]",
				folders[0].Path, folders[1].Path)
		}
		if folders[1].AudioFileCount != 1 {
			t.Errorf("expected albumB audio_file_count 1, got %d", folders[1].AudioFileCount)
		}
	})
}

func TestGetLibraryFolderNotFound(t *testing.T) {
	repo := newTestRepository(t)

	lib, err := repo.CreateLibrary("Music", "/music")
	if err != nil {
		t.Fatalf("CreateLibrary failed: %v", err)
	}

	_, err = repo.GetLibraryFolder(lib.ID, "no-such-folder")
	if !errors.Is(err, ErrLibraryFolderNotFound) {
		t.Errorf("expected ErrLibraryFolderNotFound for unknown folder id, got %v", err)
	}
}

// TestListEntriesUnderPathEscapesWildcards verifies that LIKE wildcard
// characters inside a path prefix (%, _) are treated literally, not as
// patterns. Without escaping, a folder named "foo%bar" would match files
// under a sibling like "foobazbar".
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

// TestReplaceLibraryFoldersEscapesWildcardPaths verifies that a directory
// whose name contains LIKE wildcard characters only collects its own audio:
// the unescaped pattern would attribute sibling files to it.
func TestReplaceLibraryFoldersEscapesWildcardPaths(t *testing.T) {
	repo := newTestRepository(t)

	lib, err := repo.CreateLibrary("Music", "/music")
	if err != nil {
		t.Fatalf("CreateLibrary failed: %v", err)
	}

	// A wildcard-named directory with NO audio of its own, plus a sibling
	// whose files the unescaped pattern `/music/100%off/%` would match.
	insertEntry(t, repo, "/music/100%off", "/music", "100%off", true)
	insertEntry(t, repo, "/music/100Xoff", "/music", "100Xoff", true)
	insertEntry(t, repo, "/music/100Xoff/track.mp3", "/music/100Xoff", "track.mp3", false)

	n, err := repo.ReplaceLibraryFolders(lib.ID, "/music")
	if err != nil {
		t.Fatalf("ReplaceLibraryFolders failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 folder, got %d", n)
	}

	folders, err := repo.ListLibraryFolders(lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryFolders failed: %v", err)
	}
	if len(folders) != 1 || folders[0].Path != "/music/100Xoff" {
		t.Errorf("expected only folder /music/100Xoff, got %+v", folders)
	}
}

func TestCreateLibraryCanonicalIdentity(t *testing.T) {
	repo := newTestRepository(t)

	if _, err := repo.CreateLibrary("One", "/music"); err != nil {
		t.Fatalf("CreateLibrary(/music) failed: %v", err)
	}
	// Lexically equivalent spelling must conflict.
	if _, err := repo.CreateLibrary("Two", "/music/."); !errors.Is(err, ErrLibraryExists) {
		t.Errorf("expected ErrLibraryExists for `/music/.`, got %v", err)
	}
	// Windows-syntax roots collide on case regardless of the host OS.
	if _, err := repo.CreateLibrary("Three", `C:\Music`); err != nil {
		t.Fatalf("CreateLibrary(C:\\Music) failed: %v", err)
	}
	if _, err := repo.CreateLibrary("Four", `c:\music\`); !errors.Is(err, ErrLibraryExists) {
		t.Errorf("expected ErrLibraryExists for `c:\\music\\` vs `C:\\Music`, got %v", err)
	}
}

func TestUpdateLibraryEquivalentRootKeepsDerivedState(t *testing.T) {
	repo := newTestRepository(t)
	lib, err := repo.CreateLibrary("Music", "/music")
	if err != nil {
		t.Fatalf("CreateLibrary failed: %v", err)
	}
	if _, err := repo.DB().Exec(`
		INSERT INTO library_folders (id, library_id, path, name, relative_path, audio_file_count)
		VALUES ('folder-1', ?, '/music/album', 'album', 'album', 1)
	`, lib.ID); err != nil {
		t.Fatalf("seed library folder: %v", err)
	}
	if err := repo.UpdateLibraryScanState(lib.ID, "completed", "", time.Now()); err != nil {
		t.Fatalf("UpdateLibraryScanState failed: %v", err)
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
	folders, err := repo.ListLibraryFolders(lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryFolders failed: %v", err)
	}
	if len(folders) != 1 {
		t.Errorf("folder list must survive spelling-only root update, got %d folders", len(folders))
	}
}

func TestReplaceLibraryFoldersCaseSensitiveSiblings(t *testing.T) {
	repo := newTestRepository(t)
	lib, err := repo.CreateLibrary("Music", "/music")
	if err != nil {
		t.Fatalf("CreateLibrary failed: %v", err)
	}

	// Case-distinct sibling directories, each with one audio file. SQLite's
	// ASCII-case-insensitive LIKE would attribute both files to both folders;
	// path identity must be binary.
	insertEntry(t, repo, "/music/Rock", "/music", "Rock", true)
	insertEntry(t, repo, "/music/rock", "/music", "rock", true)
	insertEntry(t, repo, "/music/Rock/a.mp3", "/music/Rock", "a.mp3", false)
	insertEntry(t, repo, "/music/rock/b.mp3", "/music/rock", "b.mp3", false)

	n, err := repo.ReplaceLibraryFolders(lib.ID, "/music")
	if err != nil {
		t.Fatalf("ReplaceLibraryFolders failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 folders, got %d", n)
	}

	folders, err := repo.ListLibraryFolders(lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryFolders failed: %v", err)
	}
	counts := map[string]int{}
	for _, f := range folders {
		counts[f.Path] = f.AudioFileCount
	}
	if counts["/music/Rock"] != 1 || counts["/music/rock"] != 1 {
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
