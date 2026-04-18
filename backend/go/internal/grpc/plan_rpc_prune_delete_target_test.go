package grpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// TestPlanOperations_Prune_DeleteTarget_NoFsStatDuringPlan verifies that plan stage
// does not perform FS stat checks and computes delete target path purely via string calculation.
func TestPlanOperations_Prune_DeleteTarget_NoFsStatDuringPlan(t *testing.T) {
	t.Run("delete_target_path_semantics", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "onsei-test-prune-delete-target-semantics-*")
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

		// Create test file in a subdirectory (simulate existing file to be pruned)
		subDir := filepath.Join(tmpDir, "Music", "Album")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatalf("failed to create subdir: %v", err)
		}
		testFile := filepath.Join(subDir, "song.mp3")
		if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		// Insert entry into DB
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
		`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}

		configJSON := `{"prune": {"regex_pattern": "song"}}`
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		server := NewOnseiServer(repo, tmpDir, "ffmpeg")

		// Execute plan operation
		req := &pb.PlanOperationsRequest{
			PlanType:     "prune",
			TargetFormat: "prune:mp3aac",
			FolderPath:   tmpDir,
		}

		resp, err := server.PlanOperations(nil, req)
		if err != nil {
			t.Fatalf("PlanOperations failed: %v", err)
		}

		// Assert plan was created successfully
		if resp.PlanId == "" {
			t.Fatal("expected plan_id to be set")
		}

		// Get plan items to verify target_path is set correctly
		items, err := repo.ListPlanItems(resp.PlanId)
		if err != nil {
			t.Fatalf("failed to list plan items: %v", err)
		}

		if len(items) != 1 {
			t.Fatalf("expected 1 plan item, got %d", len(items))
		}

		// Verify target_path follows Delete/<relative_path_from_root> semantics
		// testFile is like: <tmpDir>/Music/Album/song.mp3
		// target should be: <tmpDir>/Delete/Music/Album/song.mp3
		expectedTargetPrefix := filepath.ToSlash(filepath.Join(tmpDir, "Delete"))
		if items[0].TargetPath == nil {
			t.Fatal("expected target_path to be set for delete operation")
		}
		if !strings.HasPrefix(*items[0].TargetPath, expectedTargetPrefix) {
			t.Errorf("expected target_path to start with %s, got %s", expectedTargetPrefix, *items[0].TargetPath)
		}

		// Verify the relative path is preserved after Delete/
		expectedRelativePath := "Music/Album/song.mp3"
		expectedSuffix := filepath.ToSlash(expectedRelativePath)
		if !strings.HasSuffix(*items[0].TargetPath, expectedSuffix) {
			t.Errorf("expected target_path to end with %s, got %s", expectedSuffix, *items[0].TargetPath)
		}
	})

	t.Run("no_fs_stat_during_plan", func(t *testing.T) {
		// Verify planning succeeds even if Delete folder already exists (simulating FS state)
		// This proves no os.Stat check prevented planning
		tmpDir, err := os.MkdirTemp("", "onsei-test-prune-delete-target-nostat-*")
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

		// Create test file
		subDir := filepath.Join(tmpDir, "Music", "Album")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatalf("failed to create subdir: %v", err)
		}
		testFile := filepath.Join(subDir, "song.mp3")
		if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		// Insert entry into DB
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
		`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert entry: %v", err)
		}

		// Pre-create Delete folder with conflicting file
		deleteDir := filepath.Join(tmpDir, "Delete")
		if err := os.MkdirAll(deleteDir, 0755); err != nil {
			t.Fatalf("failed to create Delete dir: %v", err)
		}
		// Create a conflicting file at the target path (this would cause os.Stat to find existing file)
		conflictPath := filepath.Join(deleteDir, "Music", "Album", "song.mp3")
		if err := os.MkdirAll(filepath.Dir(conflictPath), 0755); err != nil {
			t.Fatalf("failed to create conflict dir: %v", err)
		}
		if err := os.WriteFile(conflictPath, []byte("existing file"), 0644); err != nil {
			t.Fatalf("failed to create conflict file: %v", err)
		}

		configJSON := `{"prune": {"regex_pattern": "song"}}`
		if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
			t.Fatalf("failed to write config: %v", err)
		}

		server := NewOnseiServer(repo, tmpDir, "ffmpeg")

		// Planning should succeed (no FS stat during plan)
		req := &pb.PlanOperationsRequest{
			PlanType:     "prune",
			TargetFormat: "prune:mp3aac",
			FolderPath:   tmpDir,
		}

		resp, err := server.PlanOperations(nil, req)
		if err != nil {
			t.Fatalf("PlanOperations should succeed even with existing Delete folder: %v", err)
		}

		// Verify the plan has the correct target path
		items, err := repo.ListPlanItems(resp.PlanId)
		if err != nil {
			t.Fatalf("failed to list plan items: %v", err)
		}

		if len(items) != 1 {
			t.Fatalf("expected 1 plan item, got %d", len(items))
		}

		// The target path should follow the same semantics (no collision suffix in plan stage)
		expectedTargetPrefix := filepath.ToSlash(filepath.Join(tmpDir, "Delete"))
		if items[0].TargetPath == nil {
			t.Fatal("expected target_path to be set")
		}
		if !strings.HasPrefix(*items[0].TargetPath, expectedTargetPrefix) {
			t.Errorf("expected target_path to start with %s, got %s", expectedTargetPrefix, *items[0].TargetPath)
		}

		// Verify the relative path is preserved (no suffix added during plan)
		expectedRelativePath := "Music/Album/song.mp3"
		expectedSuffix := filepath.ToSlash(expectedRelativePath)
		if !strings.HasSuffix(*items[0].TargetPath, expectedSuffix) {
			t.Errorf("expected target_path to end with %s, got %s", expectedSuffix, *items[0].TargetPath)
		}
	})
}
