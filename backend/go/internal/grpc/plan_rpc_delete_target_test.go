package grpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/analyze"
)

// TestPlanOperations_DeleteTargetPath_Persistence tests that delete operations
// have their soft-delete target_path computed and persisted at plan time.
func TestPlanOperations_DeleteTargetPath_Persistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-delete-target-path-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	// Create folder with FLAC + MP3 (MP3 should be deleted)
	musicFolder := filepath.Join(tmpDir, "Music")
	if err := os.MkdirAll(musicFolder, 0755); err != nil {
		t.Fatal(err)
	}

	flacFile := filepath.Join(musicFolder, "song.flac")
	mp3File := filepath.Join(musicFolder, "song.mp3")
	if err := os.WriteFile(flacFile, []byte("dummy flac"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mp3File, []byte("dummy mp3"), 0644); err != nil {
		t.Fatal(err)
	}

	// Insert entries
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?, NULL)
	`, filepath.ToSlash(flacFile), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, 192000)
	`, filepath.ToSlash(mp3File), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatal(err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	req := &pb.PlanOperationsRequest{
		FolderPath:   musicFolder,
		PlanType:     "slim",
		TargetFormat: "slim:mode2",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Get plan items
	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	// Find delete item and verify target_path is set
	foundDelete := false
	for _, item := range items {
		if item.OpType == "delete" {
			foundDelete = true
			if item.TargetPath == nil || *item.TargetPath == "" {
				t.Errorf("delete item should have non-empty target_path (soft-delete destination), got nil or empty")
			} else {
				// Verify target_path follows expected pattern: <root>/Delete/<relPath>
				expectedPrefix := filepath.ToSlash(tmpDir) + "/Delete/"
				if !strings.HasPrefix(*item.TargetPath, expectedPrefix) {
					t.Errorf("expected target_path to start with %q, got %q", expectedPrefix, *item.TargetPath)
				}
				t.Logf("delete item target_path: %s", *item.TargetPath)
			}
		}
	}

	if !foundDelete {
		t.Fatal("expected at least one delete item in plan")
	}
}

func TestComputeDeleteTargetPaths_UNC_TruncatesRelativeDirComponents(t *testing.T) {
	server := NewOnseiServer(nil, t.TempDir(), "ffmpeg")
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

	server.computeDeleteTargetPaths(plan, rootPath)

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
	server := NewOnseiServer(nil, t.TempDir(), "ffmpeg")
	plan := &analyze.Plan{
		Operations: []analyze.Operation{{
			Type:       analyze.OpTypeDelete,
			SourcePath: filepath.ToSlash(filepath.Join(string(filepath.Separator), "tmp", "a.mp3")),
		}},
	}

	server.computeDeleteTargetPaths(plan, "relative-root")

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

// ============================================================================
// Task 1 Tests: Prune scope merge query with chunked union collector
// ============================================================================

// TestPlanOperations_Prune_MixedScopes_UnionDedup verifies that folder_path,
// folder_paths, and source_files are combined as a union with proper deduplication.
