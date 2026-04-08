package grpc

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func writeMinimalMP3Frame(t *testing.T, path string) {
	t.Helper()
	// Minimal MPEG1 Layer3 frame header (128kbps, 44100Hz) + payload bytes.
	data := append([]byte{0xFF, 0xFB, 0x90, 0x64}, make([]byte, 1024)...)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write minimal mp3 %s: %v", path, err)
	}
}

func TestDetermineRootPath_Chunk(t *testing.T) {
	buildPaths := func(n int) []string {
		paths := make([]string, 0, n)
		for i := 0; i < n; i++ {
			paths = append(paths, fmt.Sprintf("/music/file-%04d.mp3", i))
		}
		return paths
	}

	tests := []struct {
		name      string
		total     int
		expected  []int
		chunkSize int
	}{
		{name: "exact_500", total: 500, expected: []int{500}, chunkSize: determineRootPathBatchChunkSize},
		{name: "boundary_501", total: 501, expected: []int{500, 1}, chunkSize: determineRootPathBatchChunkSize},
		{name: "boundary_999", total: 999, expected: []int{500, 499}, chunkSize: determineRootPathBatchChunkSize},
		{name: "boundary_1000", total: 1000, expected: []int{500, 500}, chunkSize: determineRootPathBatchChunkSize},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := chunkRootResolvePaths(buildPaths(tc.total), tc.chunkSize)
			got := make([]int, 0, len(chunks))
			for _, chunk := range chunks {
				got = append(got, len(chunk))
			}
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("unexpected chunk boundaries for total=%d: got=%v want=%v", tc.total, got, tc.expected)
			}
		})
	}
}

func TestDetermineRootPath_UsesChunkedBatchQuery(t *testing.T) {
	const total = 1000
	paths := make([]string, 0, total)
	for i := 0; i < total; i++ {
		paths = append(paths, fmt.Sprintf("/music/file-%04d.mp3", i))
	}

	expectedRoot := "/scan/root"
	needle := paths[750]
	queryCalls := 0
	chunkSizes := make([]int, 0, 2)

	resolved, err := resolveRootPathFromExactMatches(paths, func(chunk []string) (map[string]string, error) {
		queryCalls++
		chunkSizes = append(chunkSizes, len(chunk))
		if len(chunk) > determineRootPathBatchChunkSize {
			t.Fatalf("chunk exceeded max size: got=%d max=%d", len(chunk), determineRootPathBatchChunkSize)
		}

		roots := make(map[string]string)
		for _, p := range chunk {
			if p == needle {
				roots[p] = expectedRoot
				break
			}
		}
		return roots, nil
	})
	if err != nil {
		t.Fatalf("resolveRootPathFromExactMatches returned error: %v", err)
	}

	if resolved != expectedRoot {
		t.Fatalf("expected resolved root %q, got %q", expectedRoot, resolved)
	}

	if queryCalls != 2 {
		t.Fatalf("expected 2 chunked batch queries, got %d", queryCalls)
	}

	if !reflect.DeepEqual(chunkSizes, []int{500, 500}) {
		t.Fatalf("unexpected chunk sizes: got=%v want=%v", chunkSizes, []int{500, 500})
	}
}

func TestDetermineRootPath_SourceCandidateLikeFallbackParity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-root-like-fallback-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	scanRoot := filepath.Join(tmpDir, "scan-root")
	sourceFolder := filepath.Join(scanRoot, "music", "album")
	if err := os.MkdirAll(sourceFolder, 0755); err != nil {
		t.Fatalf("failed to create source folder: %v", err)
	}

	entryPath := filepath.Join(sourceFolder, "song.mp3")
	if err := os.WriteFile(entryPath, []byte("dummy mp3"), 0644); err != nil {
		t.Fatalf("failed to create test entry file: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(entryPath), filepath.ToSlash(scanRoot), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	root := server.determineRootPath(&pb.PlanOperationsRequest{SourceFiles: []string{sourceFolder}}, nil, true)

	if root != filepath.ToSlash(scanRoot) {
		t.Fatalf("expected LIKE fallback root %q, got %q", filepath.ToSlash(scanRoot), root)
	}
}
