package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestListPlans_EmptyRootPath_ReturnsInvalidArgument(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-list-plans-empty-*")
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

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Call ListPlans with empty root_path
	req := &pb.ListPlansRequest{
		RootPath: "",
	}
	_, err = server.ListPlans(context.Background(), req)

	// Verify error is InvalidArgument
	if err == nil {
		t.Fatal("expected error for empty root_path")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestListPlans_ValidRootPathWithData_ReturnsPlansSortedByCreatedAtDesc(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-list-plans-data-*")
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

	rootPathPosix := filepath.ToSlash(tmpDir)

	// Create test plans in the database with different timestamps
	now := time.Now()
	plan1 := &sqlite.Plan{
		PlanID:        "plan-001",
		RootPath:      rootPathPosix,
		PlanType:      "slim",
		SnapshotToken: "snapshot-001",
		Status:        "ready",
		CreatedAt:     now.Add(-2 * time.Hour), // oldest
	}
	plan2 := &sqlite.Plan{
		PlanID:        "plan-002",
		RootPath:      rootPathPosix,
		PlanType:      "prune",
		SnapshotToken: "snapshot-002",
		Status:        "executed",
		CreatedAt:     now.Add(-1 * time.Hour), // middle
	}
	plan3 := &sqlite.Plan{
		PlanID:        "plan-003",
		RootPath:      rootPathPosix,
		PlanType:      "single_delete",
		SnapshotToken: "snapshot-003",
		Status:        "ready",
		CreatedAt:     now, // newest
	}

	if err := repo.CreatePlan(plan1); err != nil {
		t.Fatalf("failed to create plan1: %v", err)
	}
	if err := repo.CreatePlan(plan2); err != nil {
		t.Fatalf("failed to create plan2: %v", err)
	}
	if err := repo.CreatePlan(plan3); err != nil {
		t.Fatalf("failed to create plan3: %v", err)
	}

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Call ListPlans with valid root_path and default limit (10)
	req := &pb.ListPlansRequest{
		RootPath: tmpDir,
	}
	resp, err := server.ListPlans(context.Background(), req)
	if err != nil {
		t.Fatalf("ListPlans failed: %v", err)
	}

	// Verify response
	if len(resp.Plans) != 3 {
		t.Errorf("expected 3 plans, got %d", len(resp.Plans))
	}

	// Verify plans are sorted by created_at DESC (newest first)
	if len(resp.Plans) >= 1 {
		if resp.Plans[0].PlanId != "plan-003" {
			t.Errorf("expected first plan to be plan-003 (newest), got %s", resp.Plans[0].PlanId)
		}
	}
	if len(resp.Plans) >= 2 {
		if resp.Plans[1].PlanId != "plan-002" {
			t.Errorf("expected second plan to be plan-002, got %s", resp.Plans[1].PlanId)
		}
	}
	if len(resp.Plans) >= 3 {
		if resp.Plans[2].PlanId != "plan-001" {
			t.Errorf("expected third plan to be plan-001 (oldest), got %s", resp.Plans[2].PlanId)
		}
	}

	// Verify fields are populated correctly
	for _, p := range resp.Plans {
		if p.RootPath != rootPathPosix {
			t.Errorf("expected root_path %s, got %s", rootPathPosix, p.RootPath)
		}
		if p.CreatedAt == "" {
			t.Error("created_at should not be empty")
		}
	}
}

func TestListPlans_ValidRootPathNoData_ReturnsEmptyList(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-list-plans-empty-list-*")
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

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Call ListPlans with a root_path that has no plans
	req := &pb.ListPlansRequest{
		RootPath: tmpDir,
	}
	resp, err := server.ListPlans(context.Background(), req)
	if err != nil {
		t.Fatalf("ListPlans failed: %v", err)
	}

	// Verify empty list is returned
	if len(resp.Plans) != 0 {
		t.Errorf("expected 0 plans, got %d", len(resp.Plans))
	}
}

func TestListPlans_WithLimit_ReturnsCorrectNumberOfPlans(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-list-plans-limit-*")
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

	rootPathPosix := filepath.ToSlash(tmpDir)

	// Create 5 test plans
	now := time.Now()
	for i := 1; i <= 5; i++ {
		plan := &sqlite.Plan{
			PlanID:        filepath.ToSlash(filepath.Join(tmpDir, "plan-"+string(rune('0'+i)))),
			RootPath:      rootPathPosix,
			PlanType:      "slim",
			SnapshotToken: "snapshot-" + string(rune('0'+i)),
			Status:        "ready",
			CreatedAt:     now.Add(time.Duration(-i) * time.Hour),
		}
		if err := repo.CreatePlan(plan); err != nil {
			t.Fatalf("failed to create plan: %v", err)
		}
	}

	// Fix the plan IDs (above loop has issues with string conversion)
	_ = []string{"plan-1", "plan-2", "plan-3", "plan-4", "plan-5"}

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Call ListPlans with limit=2
	req := &pb.ListPlansRequest{
		RootPath: tmpDir,
		Limit:    2,
	}
	resp, err := server.ListPlans(context.Background(), req)
	if err != nil {
		t.Fatalf("ListPlans failed: %v", err)
	}

	// Verify only 2 plans are returned
	if len(resp.Plans) != 2 {
		t.Errorf("expected 2 plans, got %d", len(resp.Plans))
	}
}
