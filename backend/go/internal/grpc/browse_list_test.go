package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
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
