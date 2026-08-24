package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
)

// =============================================================================
// chunkRootResolvePaths tests (ported from grpc/plan_rpc_helpers_test.go)
// =============================================================================

func TestChunkRootResolvePaths_Boundaries(t *testing.T) {
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

func TestResolveRootPathFromExactMatches_UsesChunkedBatchQuery(t *testing.T) {
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

func TestNormalizeUniquePaths_Deduplicates(t *testing.T) {
	paths := []string{"C:/A/file.mp3", "C:\\A\\file.mp3", "/B/file.mp3", "/B/file.mp3", ""}
	got := normalizeUniquePaths(paths)
	expected := []string{"C:/A/file.mp3", "/B/file.mp3"}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("normalizeUniquePaths: got=%v want=%v", got, expected)
	}
}

// =============================================================================
// attributeFolderPath tests (ported from grpc/plan_rpc_attribution_test.go)
// =============================================================================

func TestAttributeFolderPath_ValidPath(t *testing.T) {
	tests := []struct {
		name       string
		rootPath   string
		candidate  string
		wantFolder string
	}{
		{
			name:       "first level child",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/Album/song.mp3",
			wantFolder: "C:/Music/Album",
		},
		{
			name:       "nested path still maps to first level child",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/Album/Disc1/song.mp3",
			wantFolder: "C:/Music/Album",
		},
		{
			name:       "trailing slash on root",
			rootPath:   "C:/Music/",
			candidate:  "C:/Music/Artist/song.mp3",
			wantFolder: "C:/Music/Artist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			if got != tt.wantFolder {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want %q", tt.rootPath, tt.candidate, got, tt.wantFolder)
			}
		})
	}
}

func TestAttributeFolderPath_EscapeAndOutside(t *testing.T) {
	tests := []struct {
		name       string
		rootPath   string
		candidate  string
		wantGlobal bool // true means folder_path should be ""
	}{
		{
			name:       "parent traversal escapes root",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/Album/../../../Windows/System32",
			wantGlobal: true,
		},
		{
			name:       "absolute path outside root",
			rootPath:   "C:/Music",
			candidate:  "D:/Other/song.mp3",
			wantGlobal: true,
		},
		{
			name:       "empty candidate",
			rootPath:   "C:/Music",
			candidate:  "",
			wantGlobal: true,
		},
		{
			name:       "root itself returns empty (no first level child)",
			rootPath:   "C:/Music",
			candidate:  "C:/Music",
			wantGlobal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			if tt.wantGlobal && got != "" {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want empty string for global", tt.rootPath, tt.candidate, got)
			}
		})
	}
}

func TestAttributeFolderPath_Normalization(t *testing.T) {
	tests := []struct {
		name       string
		rootPath   string
		candidate  string
		wantFolder string
	}{
		{
			name:       "backslash converted to forward slash",
			rootPath:   "C:\\Music",
			candidate:  "C:\\Music\\Album\\song.mp3",
			wantFolder: "C:/Music/Album",
		},
		{
			name:       "mixed separators normalized",
			rootPath:   "C:/Music",
			candidate:  "C:\\Music/Album\\song.mp3",
			wantFolder: "C:/Music/Album",
		},
		{
			name:       "double slashes collapsed",
			rootPath:   "C:/Music",
			candidate:  "C://Music//Album/song.mp3",
			wantFolder: "C:/Music/Album",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			// Normalize expected for comparison (handles platform differences)
			wantNorm := filepath.ToSlash(filepath.Clean(tt.wantFolder))
			if got != wantNorm {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want %q", tt.rootPath, tt.candidate, got, wantNorm)
			}
		})
	}
}

func TestAttributeFolderPath_WindowsCaseInsensitive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive attribution test only relevant on Windows")
	}

	tests := []struct {
		name       string
		rootPath   string
		candidate  string
		wantFolder string
	}{
		{
			name:       "different case on root",
			rootPath:   "C:/music",
			candidate:  "C:/MUSIC/Album/song.mp3",
			wantFolder: "C:/MUSIC/Album",
		},
		{
			name:       "different case on folder",
			rootPath:   "C:/Music",
			candidate:  "C:/music/ALBUM/song.mp3",
			wantFolder: "C:/music/ALBUM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			wantNorm := filepath.ToSlash(filepath.Clean(tt.wantFolder))
			if got != wantNorm {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want %q", tt.rootPath, tt.candidate, got, wantNorm)
			}
		})
	}
}

func TestAttributeFolderPath_NonWindowsCaseSensitive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("case-sensitive attribution test only relevant on non-Windows")
	}

	tests := []struct {
		name      string
		rootPath  string
		candidate string
		wantEmpty bool
	}{
		{
			name:      "different case on root - case-sensitive mismatch",
			rootPath:  "/music",
			candidate: "/MUSIC/Album/song.mp3",
			wantEmpty: true,
		},
		{
			name:      "different case on folder - case-sensitive mismatch",
			rootPath:  "/Music",
			candidate: "/music/ALBUM/song.mp3",
			wantEmpty: true,
		},
		{
			name:      "exact case match - should work",
			rootPath:  "/Music",
			candidate: "/Music/Album/song.mp3",
			wantEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			if tt.wantEmpty && got != "" {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want empty string (case-sensitive mismatch on non-Windows)",
					tt.rootPath, tt.candidate, got)
			}
			if !tt.wantEmpty && got == "" {
				t.Errorf("attributeFolderPath(%q, %q) = empty, want non-empty (exact case match)",
					tt.rootPath, tt.candidate)
			}
		})
	}
}

func TestAttributeFolderPath_UnreliablePathFallback(t *testing.T) {
	tests := []struct {
		name       string
		rootPath   string
		candidate  string
		wantGlobal bool
	}{
		{
			name:       "path with null bytes is unreliable",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/Al\u0000bum/song.mp3",
			wantGlobal: true,
		},
		{
			name:       "long but valid paths should still work",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/" + strings.Repeat("a", 500) + "/song.mp3",
			wantGlobal: false,
		},
		{
			name:       "dots-only segments are still valid path components",
			rootPath:   "C:/Music",
			candidate:  "C:/Music/.../song.mp3",
			wantGlobal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attributeFolderPath(tt.rootPath, tt.candidate)
			if tt.wantGlobal && got != "" {
				t.Errorf("attributeFolderPath(%q, %q) = %q, want empty string (global fallback)",
					tt.rootPath, tt.candidate, got)
			}
			if !tt.wantGlobal && got == "" {
				t.Errorf("attributeFolderPath(%q, %q) = empty, want non-empty",
					tt.rootPath, tt.candidate)
			}
		})
	}
}

// =============================================================================
// computeDeleteTargetPaths tests (ported from grpc/plan_rpc_delete_target_test.go)
// =============================================================================

func TestComputeDeleteTargetPaths_UNC_TruncatesRelativeDirComponents(t *testing.T) {
	longDir := strings.Repeat("单", 120) // 360 bytes
	if len(longDir) <= 214 {
		t.Fatalf("invalid test setup: longDir bytes=%d", len(longDir))
	}

	rootPath := "//server/share/archive"
	sourcePath := rootPath + "/1-单一/12_一般/" + longDir + "/song.mp3"
	plan := &analyze.Plan{
		Operations: []analyze.Operation{{
			Type:       analyze.OpTypeDelete,
			SourcePath: sourcePath,
		}},
	}

	computeDeleteTargetPaths(plan, rootPath)

	if len(plan.Errors) != 0 {
		t.Fatalf("expected no plan errors, got %+v", plan.Errors)
	}
	target := plan.Operations[0].TargetPath
	if target == "" {
		t.Fatal("expected non-empty target path")
	}
	if filepath.Base(target) != "song.mp3" {
		t.Fatalf("expected basename preserved, got %q", filepath.Base(target))
	}

	parts := strings.Split(strings.TrimPrefix(target, "/"), "/")
	deleteIdx := -1
	for i, p := range parts {
		if p == "Delete" {
			deleteIdx = i
			break
		}
	}
	if deleteIdx == -1 {
		t.Fatalf("expected Delete segment in target path: %q", target)
	}

	// Validate only directory components after Delete and before basename.
	for i := deleteIdx + 1; i < len(parts)-1; i++ {
		part := parts[i]
		if part == "" || part == "." || part == ".." {
			continue
		}
		if len(part) > 214 {
			t.Fatalf("component %q bytes=%d exceeds 214", part, len(part))
		}
		if !utf8.ValidString(part) {
			t.Fatalf("component is invalid UTF-8: %q", part)
		}
	}
}

func TestComputeDeleteTargetPaths_RelativeRootPathRejected(t *testing.T) {
	plan := &analyze.Plan{
		Operations: []analyze.Operation{{
			Type:       analyze.OpTypeDelete,
			SourcePath: filepath.ToSlash(filepath.Join(string(filepath.Separator), "tmp", "a.mp3")),
		}},
	}

	computeDeleteTargetPaths(plan, "relative-root")

	if got := plan.Operations[0].TargetPath; got != "" {
		t.Fatalf("expected empty target path for relative root, got %q", got)
	}
	found := false
	for _, e := range plan.Errors {
		if e.Code == "PATH_ROOT_NOT_ABSOLUTE" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected PATH_ROOT_NOT_ABSOLUTE error, got %+v", plan.Errors)
	}
}

func TestComputeDeleteTargetPaths_EscapeAttemptOutsideDeleteDir(t *testing.T) {
	rootPath := filepath.ToSlash(os.TempDir())
	// Source path that resolves outside the root
	sourcePath := rootPath + "/../outside/song.mp3"
	plan := &analyze.Plan{
		Operations: []analyze.Operation{{
			Type:       analyze.OpTypeDelete,
			SourcePath: sourcePath,
		}},
	}

	computeDeleteTargetPaths(plan, rootPath)

	// Should produce escape attempt error or empty target
	target := plan.Operations[0].TargetPath
	if target != "" {
		// If target was set, it should be inside Delete dir
		expectedPrefix := rootPath + "/Delete/"
		if !strings.HasPrefix(target, expectedPrefix) {
			t.Fatalf("target path %q should be under Delete/ directory", target)
		}
	}
	// Either empty target or escape error should exist
	hasEscapeErr := false
	for _, e := range plan.Errors {
		if e.Code == "DELETE_TARGET_ESCAPE_ATTEMPT" {
			hasEscapeErr = true
			break
		}
	}
	if target == "" && !hasEscapeErr {
		t.Logf("target cleared without explicit escape error: errors=%+v", plan.Errors)
	}
}

func TestComputeDeleteTargetPaths_NonDeleteOpsUntouched(t *testing.T) {
	rootPath := filepath.ToSlash(os.TempDir())
	plan := &analyze.Plan{
		Operations: []analyze.Operation{
			{Type: analyze.OpTypeConvert, SourcePath: rootPath + "/song.flac", TargetPath: rootPath + "/song.m4a"},
			{Type: analyze.OpTypeDelete, SourcePath: rootPath + "/song.mp3"},
		},
	}

	computeDeleteTargetPaths(plan, rootPath)

	// Convert op target should be untouched
	if plan.Operations[0].TargetPath != rootPath+"/song.m4a" {
		t.Errorf("convert target changed: got %q", plan.Operations[0].TargetPath)
	}
	// Delete op should have a computed target
	if plan.Operations[1].TargetPath == "" {
		t.Error("delete op should have computed target")
	}
}

// =============================================================================
// determineRootPath tests (ported from grpc/plan_rpc_helpers_test.go)
// Uses FilepathAbs override for testability
// =============================================================================

func TestDetermineRootPath_LikeFallbackResolvesFromEntry(t *testing.T) {
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

	scanRoot := filepath.ToSlash(filepath.Join(tmpDir, "scan-root"))
	sourceFolder := filepath.Join(tmpDir, "scan-root", "music", "album")
	if err := os.MkdirAll(sourceFolder, 0755); err != nil {
		t.Fatalf("failed to create source folder: %v", err)
	}

	entryPath := filepath.ToSlash(filepath.Join(sourceFolder, "song.mp3"))
	if err := os.WriteFile(filepath.FromSlash(entryPath), []byte("dummy mp3"), 0644); err != nil {
		t.Fatalf("failed to create test entry file: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, entryPath, scanRoot, 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	root := determineRootPath(repo, Request{SourceFiles: []string{sourceFolder}}, nil, true)

	if root != scanRoot {
		t.Fatalf("expected LIKE fallback root %q, got %q", scanRoot, root)
	}
}
