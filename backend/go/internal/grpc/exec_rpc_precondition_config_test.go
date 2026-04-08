package grpc

import (
	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecutePlan_StalePrecondition_YieldsFailedPrecondition(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-stale-*")
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

	// Create test audio file
	testFile := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Get actual file info
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB with a specific content_rev
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan
	planID := "plan-stale-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with STALE precondition (wrong content_rev: expected 999, actual is 1)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, ?, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), 999 /* wrong content_rev */, info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	err = server.ExecutePlan(req, stream)

	// Assert gRPC error is FailedPrecondition
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got: %v", st.Code())
	}

	// Assert error JobEvent was sent
	var foundErrorEvent bool
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			foundErrorEvent = true
			t.Logf("Error event message: %s", ev.Message)
			break
		}
	}
	if !foundErrorEvent {
		t.Error("expected error JobEvent to be sent")
	}

	// Assert source file still exists (mutation was blocked)
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after stale precondition")
	}
}

func TestExecutePlan_StalePreconditionBySize_YieldsFailedPrecondition(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-stale-size-*")
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

	// Create test audio file
	testFile := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Get actual file info
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan
	planID := "plan-stale-size-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with STALE precondition (wrong size)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size()+1000 /* wrong size */, info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	err = server.ExecutePlan(req, stream)

	// Assert gRPC error is FailedPrecondition
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got: %v", st.Code())
	}

	// Assert source file still exists
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after stale precondition")
	}
}

func TestExecutePlan_FreshPreconditions_ExecutesSuccessfully(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-fresh-*")
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

	// Create test audio file
	testFile := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Get actual file info
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB with matching content_rev (1)
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan
	planID := "plan-fresh-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with FRESH preconditions (matching content_rev=1, size, mtime)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	err = server.ExecutePlan(req, stream)

	// Assert no error (execution succeeded)
	if err != nil {
		t.Fatalf("ExecutePlan should succeed with fresh preconditions, got: %v", err)
	}

	// Assert completed JobEvent was sent
	var foundCompletedEvent bool
	for _, ev := range stream.events {
		if ev.EventType == "completed" {
			foundCompletedEvent = true
			t.Logf("Completed event message: %s, progress: %d", ev.Message, ev.ProgressPercent)
			break
		}
	}
	if !foundCompletedEvent {
		t.Error("expected completed JobEvent to be sent")
	}

	// Assert source file was removed
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source file to be removed after successful execution")
	}
}

func TestExecutePlan_FreshPreconditionsUsingMtimeOnly_ExecutesSuccessfully(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-fresh-mtime-*")
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

	// Create test audio file
	testFile := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Get actual file info
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB (content_rev will be 0 since not specified)
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 0, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan
	planID := "plan-fresh-mtime-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with fresh preconditions using only mtime (no content_rev check)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 0, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	err = server.ExecutePlan(req, stream)

	// Assert no error (execution succeeded)
	if err != nil {
		t.Fatalf("ExecutePlan should succeed with fresh preconditions, got: %v", err)
	}

	// Assert source file was removed
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source file to be removed after successful execution")
	}
}

func TestExecutePlan_EmptyPlanID_YieldsInvalidArgument(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-empty-planid-*")
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

	// Execute plan with empty plan_id
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: ""}
	err = server.ExecutePlan(req, stream)

	// Assert gRPC error is InvalidArgument
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got: %v", st.Code())
	}

	// Assert error JobEvent was sent
	var foundErrorEvent bool
	var errorMessage string
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			foundErrorEvent = true
			errorMessage = ev.Message
			break
		}
	}
	if !foundErrorEvent {
		t.Error("expected error JobEvent to be sent")
	}
	if !strings.Contains(errorMessage, "plan_id is required") {
		t.Errorf("expected error message to contain 'plan_id is required', got: %s", errorMessage)
	}
}

func TestExecutePlan_NonExistentPlanID_YieldsNotFound(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-nonexistent-planid-*")
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

	// Execute plan with non-existent plan_id
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: "non-existent-plan-id"}
	err = server.ExecutePlan(req, stream)

	// Assert gRPC error is NotFound
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Fatalf("expected NotFound, got: %v", st.Code())
	}

	// Assert error JobEvent was sent
	var foundErrorEvent bool
	var errorMessage string
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			foundErrorEvent = true
			errorMessage = ev.Message
			break
		}
	}
	if !foundErrorEvent {
		t.Error("expected error JobEvent to be sent")
	}
	if !strings.Contains(errorMessage, "Plan not found") {
		t.Errorf("expected error message to contain 'Plan not found', got: %s", errorMessage)
	}
}

func TestExecutePlan_StaleSourceFileDeleted_YieldsFailedPrecondition(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-file-deleted-*")
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

	// Create test audio file
	testFile := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Get actual file info
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB with matching content_rev
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan
	planID := "plan-file-deleted-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with FRESH preconditions (matching content_rev=1, size, mtime)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Delete source file AFTER plan_item is persisted
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to delete source file: %v", err)
	}

	// Create server
	configDir := tmpDir
	server := NewOnseiServer(repo, configDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	err = server.ExecutePlan(req, stream)

	// Assert gRPC error is FailedPrecondition
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got: %v", st.Code())
	}

	// Assert error JobEvent was sent and message mentions missing file / precondition
	var foundErrorEvent bool
	var errorMessage string
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			foundErrorEvent = true
			errorMessage = ev.Message
			t.Logf("Error event message: %s", ev.Message)
			break
		}
	}
	if !foundErrorEvent {
		t.Error("expected error JobEvent was sent")
	}
	if !strings.Contains(errorMessage, "missing") && !strings.Contains(errorMessage, "precondition") && !strings.Contains(errorMessage, "failed") {
		t.Errorf("expected error message to mention 'missing', 'precondition', or 'failed', got: %s", errorMessage)
	}
}

// TestExecutePlan_DeleteOnlyPlan_SkipsToolsConfigValidation validates delete-only plans don't require tools config

func TestExecutePlan_DeleteOnlyPlan_SkipsToolsConfigValidation(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-delete-only-*")
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

	// Create config.json with NO tools config (empty encoder)
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
		"prune": {
			"regex_pattern": "^\\."
		},
		"tools": {}
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test audio file
	testFile := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Get actual file info
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan with DELETE operation (no convert)
	planID := "plan-delete-only-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with DELETE operation (with persisted target_path)
	persistedTargetPath := filepath.ToSlash(filepath.Join(tmpDir, "Delete", "music", "test.mp3"))
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, ?, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), persistedTargetPath, filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server with config that has no tools config
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan - should succeed even without tools config
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	err = server.ExecutePlan(req, stream)

	// Assert no error (execution should succeed)
	if err != nil {
		t.Fatalf("delete-only plan should succeed without tools config, got: %v", err)
	}

	// Assert completed JobEvent was sent
	var foundCompletedEvent bool
	for _, ev := range stream.events {
		if ev.EventType == "completed" {
			foundCompletedEvent = true
			break
		}
	}
	if !foundCompletedEvent {
		t.Error("expected completed JobEvent to be sent")
	}
}

// TestExecutePlan_DeleteOnlyPlan_IgnoresMalformedToolsConfig validates that delete-only
// plans succeed even when tools config is malformed (regression test for B6).

func TestExecutePlan_DeleteOnlyPlan_IgnoresMalformedToolsConfig(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-delete-malformed-config-*")
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

	// Create config.json with TRULY malformed tools config
	// The tools section has invalid JSON (missing closing bracket)
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
		"prune": {
			"regex_pattern": "^\\."
		},
		"tools": {
			"encoder": "lame",
			"lame_path": "C:/tools/lame.exe"
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test audio file
	testFile := filepath.Join(tmpDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Get actual file info
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan with DELETE operation only
	planID := "plan-delete-malformed-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with DELETE operation (persisted target_path)
	persistedTargetPath := filepath.ToSlash(filepath.Join(tmpDir, "Delete", "music", "test.mp3"))
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, ?, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), persistedTargetPath, filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server with malformed config
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan - should succeed because we skip tools config for delete-only
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	err = server.ExecutePlan(req, stream)

	// Assert no error (delete-only should skip tools config parsing)
	if err != nil {
		t.Fatalf("delete-only plan should succeed even with malformed tools config, got: %v", err)
	}

	// Assert completed JobEvent was sent
	var foundCompletedEvent bool
	for _, ev := range stream.events {
		if ev.EventType == "completed" {
			foundCompletedEvent = true
			break
		}
	}
	if !foundCompletedEvent {
		t.Error("expected completed JobEvent to be sent")
	}
}

// TestExecutePlan_ConvertPlan_RequiresToolsConfig validates convert plans require tools config

func TestExecutePlan_ConvertPlan_RequiresToolsConfig(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-convert-no-config-*")
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

	// Create config.json with NO tools config (empty encoder)
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
		"prune": {
			"regex_pattern": "^\\."
		},
		"tools": {}
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test audio file
	testFile := filepath.Join(tmpDir, "test.wav")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Get actual file info
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	// Insert entry into DB
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/wav', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert entry: %v", err)
	}

	// Create plan with CONVERT operation
	planID := "plan-convert-no-config-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_convert', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with convert_and_delete operation
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'convert_and_delete', ?, ?, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(filepath.Join(tmpDir, "test.mp3")), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server with config that has no tools config
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan - should fail because encoder is not configured
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	err = server.ExecutePlan(req, stream)

	// Assert gRPC error is FailedPrecondition (preflight failure)
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for missing encoder, got: %v", st.Code())
	}

	// Assert error message mentions encoder
	if !strings.Contains(st.Message(), "encoder") {
		t.Errorf("expected error message to mention 'encoder', got: %s", st.Message())
	}

	// Assert source file still exists (preflight failure blocks mutation)
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after preflight failure")
	}
}

// TestExecutePlan_SoftDeleteTrue_MovesToDeleteFolder validates soft_delete=true moves file to Delete folder
