package grpc

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// TestPlanOperations_Prune_NoScope_RequireScopeTrue_ReturnsGlobalNoScope verifies that
// when require_scope=true (the default) and a prune request has no scope fields,
// the RPC returns GLOBAL_SHORT_CIRCUIT instead of performing a global scan.
func TestPlanOperations_Prune_NoScope_RequireScopeTrue_ReturnsGlobalNoScope(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	// Insert a DB entry that would produce operations if a global scan ran.
	// The test verifies that the no-scope short-circuit fires BEFORE
	// prune regex/config loading, so this entry is never actually queried.
	testFile := filepath.Join(tmpDir, "song.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Default config has plan.slim.require_scope=true, which is what we want.
	// No config.json needed – defaults apply.
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	req := &pb.PlanOperationsRequest{
		PlanType:     "prune",
		TargetFormat: "prune:both",
		// No scope fields: FolderPaths, FolderPath, SourceFiles all empty.
	}
	resp, err := server.PlanOperations(nil, req)
	if err != nil {
		t.Fatalf("PlanOperations returned unexpected error: %v", err)
	}

	if resp.SummaryReason != "GLOBAL_SHORT_CIRCUIT" {
		t.Errorf("expected SummaryReason GLOBAL_SHORT_CIRCUIT, got %q", resp.SummaryReason)
	}
	if resp.TotalCount != 0 {
		t.Errorf("expected zero TotalCount, got %d", resp.TotalCount)
	}
	if resp.ActionableCount != 0 {
		t.Errorf("expected zero ActionableCount, got %d", resp.ActionableCount)
	}
	if len(resp.Operations) != 0 {
		t.Errorf("expected zero Operations, got %d", len(resp.Operations))
	}
	if len(resp.PlanErrors) == 0 {
		t.Error("expected at least one PlanError, got none")
	} else {
		if resp.PlanErrors[0].Code != "GLOBAL_NO_SCOPE" {
			t.Errorf("expected PlanErrors[0].Code GLOBAL_NO_SCOPE, got %q", resp.PlanErrors[0].Code)
		}
	}
}
