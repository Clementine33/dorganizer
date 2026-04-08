package grpc

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// TestPlanOperations_SummaryFields_LegacySourceFilesLosslessOnlyFailFast verifies
// explicit source_files slim:mode1 lossless-only input fails fast with
// SLIM_MODE1_LOSSLESS_ONLY and yields no operations.
func TestPlanOperations_SummaryFields_LegacySourceFilesLosslessOnlyFailFast(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-summary-allkeep-*")
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

	// Create a lossless-only file. Mode1 now treats lossless-only component as error.
	testFile := filepath.Join(tmpDir, "unique_song.flac")
	if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Insert entry into DB as lossless
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Call PlanOperations with slim:mode1 using source_files only.
	req := &pb.PlanOperationsRequest{
		SourceFiles:  []string{testFile},
		PlanType:     "slim",
		TargetFormat: "slim:mode1",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify persisted executable operations are empty.
	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 persisted plan items, got %d", len(items))
	}

	// Assert fail-fast summary and stable error code.
	if resp.GetActionableCount() != 0 {
		t.Errorf("expected actionable_count=0, got %d", resp.GetActionableCount())
	}
	if resp.GetTotalCount() != 0 {
		t.Errorf("expected total_count=0, got %d", resp.GetTotalCount())
	}
	if resp.GetSummaryReason() != "NO_MATCH" {
		t.Errorf("expected summary_reason=NO_MATCH, got %q", resp.GetSummaryReason())
	}

	found := false
	for _, pe := range resp.GetPlanErrors() {
		if pe.GetCode() == "SLIM_MODE1_LOSSLESS_ONLY" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected SLIM_MODE1_LOSSLESS_ONLY in plan_errors, got %+v", resp.GetPlanErrors())
	}
}

// TestPlanOperations_SummaryFields_MixedCase tests that when there are both
// actionable operations (delete/convert) and keep operations, the counters
// reflect the correct breakdown.
func TestPlanOperations_SummaryFields_MixedCase(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-summary-mixed-*")
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

	// Create files for mixed scenario:
	// 1. FLAC file with no MP3 counterpart -> keep (already slim)
	// 2. MP3 file with FLAC counterpart -> actionable (delete the MP3)
	keepFile := filepath.Join(tmpDir, "keep_only.flac")
	deleteMP3 := filepath.Join(tmpDir, "delete_me.mp3")
	deleteFLAC := filepath.Join(tmpDir, "delete_me.flac")

	for _, f := range []string{keepFile, deleteMP3, deleteFLAC} {
		if err := os.WriteFile(f, []byte("dummy audio"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}
	}

	// Insert entries into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(keepFile), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(deleteMP3), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 2000, 'audio/flac', 1, ?)
	`, filepath.ToSlash(deleteFLAC), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Call PlanOperations with slim mode
	req := &pb.PlanOperationsRequest{
		FolderPath: tmpDir,
		PlanType:   "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Count operation types from stored plan items
	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}

	var expectedActionable int
	for _, item := range items {
		switch item.OpType {
		case "delete", "convert", "convert_and_delete":
			expectedActionable++
		}
	}

	// Assert summary counters match actual counts.
	if resp.GetActionableCount() != int32(expectedActionable) {
		t.Errorf("expected actionable_count=%d, got %d", expectedActionable, resp.GetActionableCount())
	}
	if resp.GetTotalCount() != int32(len(items)) {
		t.Errorf("expected total_count=%d, got %d", len(items), resp.GetTotalCount())
	}
	// Verify invariant: total equals actionable because keep is no longer exposed.
	if resp.GetTotalCount() != resp.GetActionableCount() {
		t.Errorf("total_count should equal actionable_count: total=%d, actionable=%d",
			resp.GetTotalCount(), resp.GetActionableCount())
	}
}

// TestPlanOperations_SummaryFields_EmptyCase tests that when no files match
// the planning criteria, the summary counters reflect: all zeros.
func TestPlanOperations_SummaryFields_EmptyCase(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-summary-empty-*")
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

	// No files added to DB - empty scan

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Call PlanOperations with slim mode on empty folder
	req := &pb.PlanOperationsRequest{
		FolderPath: tmpDir,
		PlanType:   "slim",
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations failed: %v", err)
	}

	// Verify no operations
	items, err := repo.ListPlanItems(resp.PlanId)
	if err != nil {
		t.Fatalf("failed to list plan items: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 plan items for empty case, got %d", len(items))
	}

	// Assert summary counters are all zero for empty case.
	if resp.GetActionableCount() != 0 {
		t.Errorf("expected actionable_count=0 for empty case, got %d", resp.GetActionableCount())
	}
	if resp.GetTotalCount() != 0 {
		t.Errorf("expected total_count=0 for empty case, got %d", resp.GetTotalCount())
	}
	if resp.GetSummaryReason() != "NO_MATCH" {
		t.Errorf("expected summary_reason=NO_MATCH for empty case, got %q", resp.GetSummaryReason())
	}
}
