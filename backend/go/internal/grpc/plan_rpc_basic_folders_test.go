package grpc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// TestPlanOperations_SuccessfulFolders_ZeroOpSuccess tests that a folder with
// no actionable operations (0-op success) is included in successful_folders.
func TestPlanOperations_SuccessfulFolders_ZeroOpSuccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-successfulfolders-zeroop-*")
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

	// Create folder with single MP3 file (no FLAC counterpart = skip, 0 actionable ops)
	// This tests the "lossy only, no lossless source" branch which produces 0 ops
	zeroOpFolder := filepath.Join(tmpDir, "zero_op_folder")
	if err := os.MkdirAll(zeroOpFolder, 0755); err != nil {
		t.Fatalf("failed to create zero_op folder: %v", err)
	}

	mp3File := filepath.Join(zeroOpFolder, "unique_song.mp3")
	if err := os.WriteFile(mp3File, []byte("dummy mp3"), 0644); err != nil {
		t.Fatalf("failed to create mp3 file: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 2000, 'audio/mpeg', 1, ?, 320000)
	`, filepath.ToSlash(mp3File), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	req := &pb.PlanOperationsRequest{
		FolderPath: zeroOpFolder,
		PlanType:   "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify 0 actionable operations (MP3 with no FLAC = keep only, no delete)
	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 plan items (all keep), got %d", len(items))
	}

	// Assert folder is in successful_folders
	successFolders := resp.GetSuccessfulFolders()
	zeroOpFolderNorm := filepath.ToSlash(filepath.Clean(zeroOpFolder))
	found := false
	for _, sf := range successFolders {
		if sf == zeroOpFolderNorm {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected successful_folders to contain %q (0-op success), got %v", zeroOpFolderNorm, successFolders)
	}
}

// TestPlanOperations_CanonicalFolderPath tests that folder paths in
// plan_errors and successful_folders are canonical absolute first-level child
// slash-normalized.
func TestPlanOperations_CanonicalFolderPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-canonical-path-*")
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

	// Create nested folder structure
	musicFolder := filepath.Join(tmpDir, "Music")
	albumFolder := filepath.Join(musicFolder, "Album")
	if err := os.MkdirAll(albumFolder, 0755); err != nil {
		t.Fatalf("failed to create nested folders: %v", err)
	}

	// Create file in album folder
	audioFile := filepath.Join(albumFolder, "song.flac")
	if err := os.WriteFile(audioFile, []byte("dummy flac"), 0644); err != nil {
		t.Fatalf("failed to create audio file: %v", err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(audioFile), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Request with backslash path (Windows) - should be normalized
	reqPath := musicFolder
	req := &pb.PlanOperationsRequest{
		FolderPath: reqPath,
		PlanType:   "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify successful_folders contains canonical path
	successFolders := resp.GetSuccessfulFolders()
	expectedCanonical := filepath.ToSlash(filepath.Clean(musicFolder))

	if len(successFolders) == 0 {
		t.Fatal("expected at least one successful folder")
	}

	for _, sf := range successFolders {
		// Check it uses forward slashes
		if strings.Contains(sf, "\\") {
			t.Errorf("folder path should use forward slashes, got %q", sf)
		}
		// Check it's cleaned (no double slashes, no trailing slash except root)
		if sf != filepath.ToSlash(filepath.Clean(sf)) {
			t.Errorf("folder path should be cleaned, got %q", sf)
		}
	}

	// Verify first-level child semantics (should be musicFolder, not albumFolder)
	foundMusic := false
	for _, sf := range successFolders {
		if sf == expectedCanonical {
			foundMusic = true
			break
		}
	}
	if !foundMusic {
		t.Errorf("expected successful_folders to contain first-level child %q, got %v", expectedCanonical, successFolders)
	}
}

// TestPlanOperations_SlimMode1_LossyOnlyGroupSkipped verifies lossy-only folders are
// treated as successful with no operations and no plan_errors.
func TestPlanOperations_SlimMode1_LossyOnlyGroupSkipped(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-slim-mode1-lossy-only-*")
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

	folder := filepath.Join(tmpDir, "lossy_only")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}

	entries := []struct {
		path    string
		format  string
		size    int
		bitrate int
	}{
		{path: filepath.ToSlash(filepath.Join(folder, "song.mp3")), format: "audio/mpeg", size: 1000, bitrate: 128000},
		{path: filepath.ToSlash(filepath.Join(folder, "song.m4a")), format: "audio/mp4", size: 1100, bitrate: 0},
	}

	for _, e := range entries {
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, ?, ?, 1, ?, ?)
		`, e.path, filepath.ToSlash(tmpDir), e.size, e.format, 1234567890, e.bitrate)
		if err != nil {
			t.Fatal(err)
		}
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	req := &pb.PlanOperationsRequest{
		FolderPaths:  []string{folder},
		PlanType:     "slim",
		TargetFormat: "slim:mode1",
	}

	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	if len(resp.GetPlanErrors()) != 0 {
		t.Fatalf("expected no plan_errors, got: %+v", resp.GetPlanErrors())
	}

	if len(resp.GetOperations()) != 0 {
		t.Fatalf("expected no operations for lossy-only folder, got %d", len(resp.GetOperations()))
	}

	folderNorm := filepath.ToSlash(filepath.Clean(folder))
	foundSuccess := false
	for _, sf := range resp.GetSuccessfulFolders() {
		if sf == folderNorm {
			foundSuccess = true
		}
	}
	if !foundSuccess {
		t.Fatalf("expected lossy-only folder in successful_folders, got %v", resp.GetSuccessfulFolders())
	}
}
