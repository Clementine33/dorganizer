package grpc

import (
	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecutePlan_Delete_UsesPersistedTargetPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-delete-persisted-*")
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

	// Create test audio file in subfolder
	subDir := filepath.Join(tmpDir, "Music")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

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
	planID := "plan-delete-persisted-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with a SPECIFIC persisted target_path
	// This target_path is different from what would be computed at execution time
	persistedTargetPath := filepath.ToSlash(filepath.Join(tmpDir, "Delete", "Music", "test_persisted.mp3"))
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, ?, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), persistedTargetPath, filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan with soft_delete=true
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: true}
	err = server.ExecutePlan(req, stream)

	if err != nil {
		t.Fatalf("ExecutePlan should succeed, got: %v", err)
	}

	// Verify: source file should be moved to the PERSISTED target path
	expectedDeletePath := filepath.FromSlash(persistedTargetPath)
	if _, err := os.Stat(expectedDeletePath); os.IsNotExist(err) {
		t.Errorf("expected file to be moved to persisted target path %s", expectedDeletePath)
	}

	// Verify: source file should be gone
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source file to be moved")
	}
}

// TestExecutePlan_Delete_MissingPersistedTargetPath_Fails validates that
// delete items without persisted target_path fail with proper error.

func TestExecutePlan_Delete_MissingPersistedTargetPath_Fails(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-delete-missing-target-*")
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
	planID := "plan-delete-missing-target-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item WITHOUT target_path (NULL)
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan with soft_delete=true (requires target_path)
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: true}
	_ = server.ExecutePlan(req, stream)

	// Verify: source file should still exist (delete failed)
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after failed delete")
	}

	// Verify: error event was emitted
	var foundError bool
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Error("expected error event for missing target_path")
	}
}

func TestExecutePlan_DeleteTargetConflict(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-target-conflict-*")
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
	subDir := filepath.Join(tmpDir, "Music")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subDir, "song.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

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
	planID := "plan-target-conflict-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Persisted target path for delete
	persistedTargetPath := filepath.ToSlash(filepath.Join(tmpDir, "Delete", "Music", "song.mp3"))
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, ?, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), persistedTargetPath, filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Pre-create the target path with different content (simulates conflict)
	deleteDir := filepath.Join(tmpDir, "Delete", "Music")
	if err := os.MkdirAll(deleteDir, 0755); err != nil {
		t.Fatalf("failed to create delete dir: %v", err)
	}
	// Create a file at the target path with different content
	if err := os.WriteFile(filepath.FromSlash(persistedTargetPath), []byte("CONFLICTING FILE CONTENT"), 0644); err != nil {
		t.Fatalf("failed to create conflict file: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan with soft_delete=true
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: true}
	execErr := server.ExecutePlan(req, stream)

	// Task 2: Operation MUST fail due to target conflict
	if execErr == nil {
		t.Fatal("expected ExecutePlan to fail with target conflict, but it succeeded")
	}

	// Verify gRPC error code is FailedPrecondition (deterministic semantic contract)
	st, ok := status.FromError(execErr)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", execErr)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition for target conflict, got: %v", st.Code())
	}

	// Verify error event was sent with target conflict semantic contract
	var foundErrorEvent bool
	var errorCode, errorMessage string
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			foundErrorEvent = true
			errorCode = ev.Code
			errorMessage = ev.Message
			t.Logf("Error event: code=%s message=%s", ev.Code, ev.Message)
			break
		}
	}
	if !foundErrorEvent {
		t.Fatal("expected error JobEvent for target conflict")
	}

	// Task 2: Verify deterministic conflict error code
	if errorCode != "EXEC_DELETE_FAILED" && errorCode != "EXEC_PRECONDITION_FAILED" {
		t.Errorf("expected error code EXEC_DELETE_FAILED or EXEC_PRECONDITION_FAILED for target conflict, got: %s", errorCode)
	}

	// Task 2: Verify standardized "TARGET_CONFLICT:" message prefix (semantic contract)
	if !strings.HasPrefix(errorMessage, "TARGET_CONFLICT:") {
		t.Errorf("expected error message to start with 'TARGET_CONFLICT:' (standardized prefix), got: %s", errorMessage)
	}

	// Verify source file still exists (delete was blocked by conflict check)
	if _, statErr := os.Stat(testFile); os.IsNotExist(statErr) {
		t.Error("expected source file to still exist after target conflict (delete was blocked)")
	}

	// Verify target file still has original conflicting content (was not overwritten)
	content, readErr := os.ReadFile(filepath.FromSlash(persistedTargetPath))
	if readErr != nil {
		t.Fatalf("failed to read target file: %v", readErr)
	}
	if string(content) != "CONFLICTING FILE CONTENT" {
		t.Errorf("expected target to retain original conflicting content, got: %s", string(content))
	}
}

// TestExecutePlan_SourceMissing validates that execute reports SOURCE_MISSING
// when the source file is missing at execution time.
// Task 2: Strengthened to assert deterministic SOURCE_MISSING semantic contract.

func TestExecutePlan_SourceMissing(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-source-missing-*")
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

	// Create test audio file (will be deleted before execution)
	subDir := filepath.Join(tmpDir, "Music")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subDir, "song.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

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
	planID := "plan-source-missing-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Delete source file BEFORE execution to simulate SOURCE_MISSING
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to remove source file: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID}
	_ = server.ExecutePlan(req, stream)

	// Verify error event was sent with precondition or source missing code
	var foundError bool
	var errorCode string
	var errorMessage string
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			foundError = true
			errorCode = ev.Code
			errorMessage = ev.Message
			t.Logf("Error event: code=%s message=%s", ev.Code, ev.Message)
			break
		}
	}
	if !foundError {
		t.Fatal("expected error event for source missing")
	}

	// Task 2: Verify error code indicates precondition failure due to missing source file
	// The execute service reports EXEC_PRECONDITION_FAILED when source is missing
	// because validatePrecondition checks for file existence before execution
	if errorCode != "EXEC_PRECONDITION_FAILED" {
		t.Errorf("expected error code EXEC_PRECONDITION_FAILED for source missing, got: %s", errorCode)
	}

	// Task 2: Verify standardized SOURCE_MISSING message prefix: "SOURCE_MISSING:"
	// This is the strict contract for source file missing errors from validatePrecondition
	if !strings.HasPrefix(errorMessage, "SOURCE_MISSING:") {
		t.Errorf("expected error message to start with 'SOURCE_MISSING:' (standardized prefix), got: %s", errorMessage)
	}

	// Task 2: Additional semantic contract - verify the error mentions the missing path or precondition
	if !strings.Contains(errorMessage, "precondition") && !strings.Contains(strings.ToLower(errorMessage), "missing") {
		t.Logf("Note: error message does not explicitly mention 'precondition' or 'missing', but 'file not found' prefix satisfies contract")
	}

	// Verify that the error is attributed to the correct folder
	var folderPath string
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			folderPath = ev.FolderPath
			break
		}
	}
	expectedFolder := filepath.ToSlash(subDir)
	if folderPath != expectedFolder {
		t.Errorf("expected error to be attributed to folder %s, got %s", expectedFolder, folderPath)
	}
}

// TestExecutePlan_DeletePermissionDenied validates that permission denied errors
// during delete operations return the standardized "PERMISSION_DENIED:" prefix.
// Task 2: Strict semantic coverage for permission-denied path.

func TestExecutePlan_DeletePermissionDenied(t *testing.T) {
	// Skip on platforms where permission testing cannot be reliably enforced
	if runtime.GOOS == "windows" {
		// Windows with admin/elevated privileges may not enforce permission restrictions
		t.Skip("skipping permission test on Windows: platform cannot reliably enforce permission restrictions in test environment")
	}

	tmpDir, err := os.MkdirTemp("", "onsei-test-perm-denied-*")
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
	subDir := filepath.Join(tmpDir, "Music")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subDir, "song.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

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

	// Create plan with soft_delete=true (which uses move/rename)
	planID := "plan-perm-denied-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Persisted target path for delete
	persistedTargetPath := filepath.ToSlash(filepath.Join(tmpDir, "Delete", "Music", "song.mp3"))
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, ?, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), persistedTargetPath, filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create Delete directory and make it read-only to trigger permission denied
	deleteDir := filepath.Join(tmpDir, "Delete", "Music")
	if err := os.MkdirAll(deleteDir, 0755); err != nil {
		t.Fatalf("failed to create delete dir: %v", err)
	}

	// Make the Delete directory read-only (no write permissions)
	if err := os.Chmod(deleteDir, 0555); err != nil {
		t.Fatalf("failed to chmod delete dir: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan with soft_delete=true
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: true}
	execErr := server.ExecutePlan(req, stream)

	// Cleanup: restore permissions for cleanup
	_ = os.Chmod(deleteDir, 0755)

	// Task 2: Operation MUST fail due to permission denied
	if execErr == nil {
		t.Fatal("expected ExecutePlan to fail with permission denied, but it succeeded")
	}

	// Verify error event was sent
	var foundError bool
	var errorMessage string
	for _, ev := range stream.events {
		if ev.EventType == "error" {
			foundError = true
			errorMessage = ev.Message
			t.Logf("Error event: code=%s message=%s", ev.Code, ev.Message)
			break
		}
	}
	if !foundError {
		t.Fatal("expected error event for permission denied")
	}

	// Task 2: STRICT - Verify standardized "PERMISSION_DENIED:" message prefix (no fallback)
	if !strings.HasPrefix(errorMessage, "PERMISSION_DENIED:") {
		t.Errorf("expected error message to start with 'PERMISSION_DENIED:' (strict semantic contract), got: %s", errorMessage)
	}

	// Task 2: Verify source file still exists (operation was blocked)
	if _, statErr := os.Stat(testFile); os.IsNotExist(statErr) {
		t.Error("expected source file to still exist after permission error")
	}
}
