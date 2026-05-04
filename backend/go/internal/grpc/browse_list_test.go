package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func TestListFolders_NaturalSort(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"folder10", "folder2", "folder1"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", name, err)
		}
	}

	s := &OnseiServer{}
	resp, err := s.ListFolders(context.Background(), &pb.ListFoldersRequest{ParentPath: root})
	if err != nil {
		t.Fatalf("ListFolders returned error: %v", err)
	}

	got := make([]string, 0, len(resp.Folders))
	for _, abs := range resp.Folders {
		got = append(got, filepath.Base(abs))
	}

	want := []string{"folder1", "folder2", "folder10"}
	if len(got) != len(want) {
		t.Fatalf("unexpected folder count: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected order at %d: got %q want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestListFiles_DepthThenNaturalSortOnRelativePath(t *testing.T) {
	root := t.TempDir()

	mustMkdirAll := func(rel string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", rel, err)
		}
	}
	mustWriteFile := func(rel string) {
		t.Helper()
		full := filepath.Join(root, rel)
		mustMkdirAll(filepath.Dir(rel))
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}

	// Depth 0
	mustWriteFile("10.wav")
	mustWriteFile("2.wav")
	mustWriteFile("1.wav")
	// Depth 1
	mustWriteFile("1-mp3/10.mp3")
	mustWriteFile("1-mp3/2.mp3")
	mustWriteFile("1-mp3/1.mp3")
	mustWriteFile("2-mp3/1.mp3")
	// Depth 2
	mustWriteFile("album/disc10/1.flac")
	mustWriteFile("album/disc2/1.flac")

	s := &OnseiServer{}
	resp, err := s.ListFiles(context.Background(), &pb.ListFilesRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ListFiles returned error: %v", err)
	}

	got := make([]string, 0, len(resp.Files))
	for _, abs := range resp.Files {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			t.Fatalf("filepath.Rel(%q, %q): %v", root, abs, err)
		}
		got = append(got, filepath.ToSlash(rel))
	}

	// DFS tree order: root files first (natural sort), then recurse into
	// subdirectories in natural order. Within each subdirectory the same
	// rule applies recursively.
	want := []string{
		// depth-0 files (root), natural sort
		"1.wav",
		"2.wav",
		"10.wav",
		// subtree rooted at "1-mp3" (natural sort: 1-mp3 < 2-mp3 < album)
		"1-mp3/1.mp3",
		"1-mp3/2.mp3",
		"1-mp3/10.mp3",
		// subtree rooted at "2-mp3"
		"2-mp3/1.mp3",
		// subtree rooted at "album" -> recurse: disc2 < disc10
		"album/disc2/1.flac",
		"album/disc10/1.flac",
	}

	if len(got) != len(want) {
		t.Fatalf("unexpected file count: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected order at %d: got %q want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestListFiles_ReturnsBitrateEntriesForMP3(t *testing.T) {
	root := t.TempDir()

	// Create two .mp3 files at depth 0.
	mustWriteFile := func(rel string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %q: %v", rel, err)
		}
	}
	mustWriteFile("1.mp3")
	mustWriteFile("2.mp3")

	// Create a SQLite repo and pre-populate bitrate for known paths.
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	// Insert entries with bitrate for the two files (using POSIX-normalized paths).
	now := "2025-01-01T00:00:00Z"
	paths := []struct {
		abs     string
		bitrate int32
	}{
		{filepath.Join(root, "1.mp3"), 128000},
		{filepath.Join(root, "2.mp3"), 256000},
	}
	for _, p := range paths {
		_, err := repo.DB().Exec(`
			INSERT OR REPLACE INTO entries (path, root_path, parent_path, name,
				is_dir, size, mtime, scan_id, content_rev, bitrate, updated_at)
			VALUES (?, ?, ?, ?, 0, 1000, 1, 'scan-1', 1, ?, ?)
		`, filepath.ToSlash(p.abs), filepath.ToSlash(root), filepath.ToSlash(root), filepath.Base(p.abs), p.bitrate, now)
		if err != nil {
			t.Fatalf("insert entry %q: %v", p.abs, err)
		}
	}

	s := NewOnseiServer(repo, t.TempDir(), "ffmpeg")
	resp, err := s.ListFiles(context.Background(), &pb.ListFilesRequest{FolderPath: root})
	if err != nil {
		t.Fatalf("ListFiles returned error: %v", err)
	}

	// Legacy files field must still be populated.
	if len(resp.Files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(resp.Files), resp.Files)
	}

	// Entries must contain path + bitrate for each file, in the same order.
	if len(resp.Entries) != len(resp.Files) {
		t.Fatalf("entries length %d != files length %d", len(resp.Entries), len(resp.Files))
	}
	for i := range resp.Files {
		if resp.Entries[i].Path != resp.Files[i] {
			t.Errorf("entry[%d] path mismatch: entries=%q files=%q",
				i, resp.Entries[i].Path, resp.Files[i])
		}
	}

	// Bitrate assertions (bps).
	entryByRel := make(map[string]*pb.FileListEntry)
	for _, e := range resp.Entries {
		rel, err := filepath.Rel(root, e.Path)
		if err != nil {
			t.Fatalf("filepath.Rel(%q, %q): %v", root, e.Path, err)
		}
		entryByRel[filepath.ToSlash(rel)] = e
	}

	if e, ok := entryByRel["1.mp3"]; !ok {
		t.Fatal("missing entry for 1.mp3")
	} else if e.Bitrate != 128000 {
		t.Fatalf("expected bitrate 128000 (bps) for 1.mp3, got %d", e.Bitrate)
	}

	if e, ok := entryByRel["2.mp3"]; !ok {
		t.Fatal("missing entry for 2.mp3")
	} else if e.Bitrate != 256000 {
		t.Fatalf("expected bitrate 256000 (bps) for 2.mp3, got %d", e.Bitrate)
	}
}
