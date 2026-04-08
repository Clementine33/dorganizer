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

func TestExecutePlan_SoftDeleteTrue_MovesToDeleteFolder(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-soft-delete-*")
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

	// Create test audio file in a subdirectory
	subDir := filepath.Join(tmpDir, "music")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subDir, "test.mp3")
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
	planID := "plan-soft-delete-001"
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

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan with soft_delete=true
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: true}
	err = server.ExecutePlan(req, stream)

	// Assert no error
	if err != nil {
		t.Fatalf("ExecutePlan with soft_delete=true should succeed, got: %v", err)
	}

	// Assert original file is gone
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected original source file to be moved (not exist)")
	}

	// Assert file was moved to Delete/<relative_path>
	expectedDeleteDir := filepath.Join(tmpDir, "Delete", "music")
	expectedDeletedFile := filepath.Join(expectedDeleteDir, "test.mp3")
	if _, err := os.Stat(expectedDeletedFile); os.IsNotExist(err) {
		t.Errorf("expected file to be moved to %s, got error: %v", expectedDeletedFile, err)
	}
}

func TestExecutePlan_SoftDelete_UsesScanRootPathWhenScopeRootIsSubfolder(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-softdelete-scanroot-*")
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

	// source under scan root / scope subfolder
	scopeDir := filepath.Join(tmpDir, "music", "album")
	if err := os.MkdirAll(scopeDir, 0755); err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(scopeDir, "test.mp3")
	if err := os.WriteFile(sourceFile, []byte("dummy audio"), 0644); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(sourceFile)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(sourceFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	planID := "plan-softdelete-scanroot-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(scopeDir), filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatal(err)
	}

	persistedTargetPath := filepath.ToSlash(filepath.Join(tmpDir, "Delete", "music", "album", "test.mp3"))
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, ?, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(sourceFile), persistedTargetPath, filepath.ToSlash(sourceFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	server := NewOnseiServer(repo, tmpDir, "ffmpeg")
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: true}
	if err := server.ExecutePlan(req, stream); err != nil {
		t.Fatalf("ExecutePlan failed: %v", err)
	}

	// Must move to <scanroot>/Delete/<relPath-from-scanroot>
	expected := filepath.Join(tmpDir, "Delete", "music", "album", "test.mp3")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected file moved to scan-root delete path %s: %v", expected, err)
	}
	if _, err := os.Stat(sourceFile); !os.IsNotExist(err) {
		t.Fatalf("expected source file removed from original location")
	}
}

// TestExecutePlan_SoftDeleteFalse_HardDeletes validates soft_delete=false performs hard delete

func TestExecutePlan_SoftDeleteFalse_HardDeletes(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-hard-delete-*")
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
	planID := "plan-hard-delete-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with DELETE operation
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan with soft_delete=false (default)
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: false}
	err = server.ExecutePlan(req, stream)

	// Assert no error
	if err != nil {
		t.Fatalf("ExecutePlan with soft_delete=false should succeed, got: %v", err)
	}

	// Assert source file was hard deleted (gone completely)
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source file to be hard deleted")
	}

	// Assert Delete folder was NOT created
	deleteDir := filepath.Join(tmpDir, "Delete")
	if _, err := os.Stat(deleteDir); !os.IsNotExist(err) {
		t.Error("expected Delete folder to NOT be created for hard delete")
	}
}

// TestExecutePlan_SoftDeleteTrue_ConvertAndDeleteSource validates soft_delete=true for convert source deletion

func TestExecutePlan_SoftDeleteTrue_ConvertAndDeleteSource(t *testing.T) {
	// This test validates the soft_delete semantics for convert source deletion.
	// Uses deterministic fake encoder to ensure convert succeeds and source is moved to Delete folder.

	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-convert-soft-delete-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create deterministic fake encoder
	encoderPath := createFakeEncoder(t, tmpDir)

	// Create config with lame encoder and path to fake encoder
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
		"prune": { "regex_pattern": "^\\." },
		"tools": { "encoder": "lame", "lame_path": "` + strings.ReplaceAll(encoderPath, "\\", "\\\\") + `" }
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	// Create test source file
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

	// Create plan with convert_and_delete operation
	planID := "plan-convert-soft-delete-001"
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

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan with soft_delete=true
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: true}
	err = server.ExecutePlan(req, stream)

	// Assert convert succeeded (fake encoder always succeeds)
	if err != nil {
		t.Fatalf("ExecutePlan with fake encoder should succeed, got: %v", err)
	}

	// Assert source file is gone from original location
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source to be removed from original location after successful convert")
	}

	// Assert source file was moved to Delete folder (soft_delete=true)
	expectedDeletePath := filepath.Join(tmpDir, "Delete", "test.wav")
	if _, err := os.Stat(expectedDeletePath); os.IsNotExist(err) {
		t.Errorf("expected source to be moved to Delete folder at %s", expectedDeletePath)
	}
}

// TestExecutePlan_SoftDeleteFalse_ConvertAndDeleteSource validates soft_delete=false for convert source deletion

func TestExecutePlan_SoftDeleteFalse_ConvertAndDeleteSource(t *testing.T) {
	// This test validates hard delete semantics for convert source deletion.
	// Uses deterministic fake encoder to ensure convert succeeds and source is hard deleted.

	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-convert-hard-delete-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create deterministic fake encoder
	encoderPath := createFakeEncoder(t, tmpDir)

	// Create config with lame encoder and path to fake encoder
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
		"prune": { "regex_pattern": "^\\." },
		"tools": { "encoder": "lame", "lame_path": "` + strings.ReplaceAll(encoderPath, "\\", "\\\\") + `" }
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	// Create test source file
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

	// Create plan with convert_and_delete operation
	planID := "plan-convert-hard-delete-001"
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

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan with soft_delete=false (hard delete)
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: false}
	err = server.ExecutePlan(req, stream)

	// Assert convert succeeded (fake encoder always succeeds)
	if err != nil {
		t.Fatalf("ExecutePlan with fake encoder should succeed, got: %v", err)
	}

	// Assert source file was hard deleted (gone completely)
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source to be hard deleted from original location after successful convert")
	}

	// Assert Delete folder was NOT created (hard delete mode)
	deleteDir := filepath.Join(tmpDir, "Delete")
	if _, err := os.Stat(deleteDir); !os.IsNotExist(err) {
		t.Error("expected Delete folder to NOT be created for hard delete mode")
	}
}

// TestExecutePlan_SoftDeleteMoveFailure_ReturnsErrorAndPreservesSource validates that when soft delete move
// fails (e.g., because root/Delete exists as a file), it returns error and preserves source file.

func TestExecutePlan_SoftDeleteMoveFailure_ReturnsErrorAndPreservesSource(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-soft-delete-fallback-*")
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

	// Create test audio file in a subdirectory
	subDir := filepath.Join(tmpDir, "music")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(subDir, "test.mp3")
	if err := os.WriteFile(testFile, []byte("dummy audio content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Pre-create Delete as a FILE (not directory) to cause MkdirAll/Rename to fail
	deleteFile := filepath.Join(tmpDir, "Delete")
	if err := os.WriteFile(deleteFile, []byte("file pretending to be directory"), 0644); err != nil {
		t.Fatalf("failed to create Delete file: %v", err)
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
	planID := "plan-soft-delete-failure-001"
	now := time.Now()
	_, err = repo.DB().Exec(`
		INSERT INTO plans (plan_id, root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, 'single_delete', NULL, '', 'pending', ?)
	`, planID, filepath.ToSlash(tmpDir), now.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("failed to insert plan: %v", err)
	}

	// Create plan item with DELETE operation
	_, err = repo.DB().Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, 0, 'delete', ?, NULL, '', ?, 1, ?, ?)
	`, planID, filepath.ToSlash(testFile), filepath.ToSlash(testFile), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatalf("failed to insert plan item: %v", err)
	}

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan with soft_delete=true - should return failed precondition
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: true}
	err = server.ExecutePlan(req, stream)

	// Assert gRPC error is FailedPrecondition
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got: %v", st.Code())
	}

	// Assert source file still exists (no hard delete fallback)
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after soft delete failure (no fallback)")
	}

	// Verify the original "Delete" file still exists (it was not a directory)
	if _, err := os.Stat(deleteFile); os.IsNotExist(err) {
		t.Error("expected original Delete file to still exist")
	}
}

// TestExecutePlan_Attribution_PreconditionPath tests attribution uses precondition path.

func TestExecutePlan_ConvertFailure_PreservesSource(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "onsei-test-convert-failure-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create config with invalid encoder path (will fail at runtime, not preflight)
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
		"prune": { "regex_pattern": "^\\." },
		"tools": { "encoder": "lame", "lame_path": "nonexistent_lame_encoder" }
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	// Create test source file
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

	// Create plan with convert_and_delete operation
	planID := "plan-convert-failure-001"
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

	// Create server
	server := NewOnseiServer(repo, tmpDir, "ffmpeg")

	// Execute plan - convert will fail (encoder not found)
	stream := &mockServerStreamHelper{}
	req := &pb.ExecutePlanRequest{PlanId: planID, SoftDelete: true}
	err = server.ExecutePlan(req, stream)

	// Convert should fail (lame not found)
	if err == nil {
		t.Fatal("expected convert to fail with nonexistent encoder")
	}

	// Assert source file still exists (Task3 semantics: convert failure preserves source)
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after convert failure (Task3 semantics)")
	}
}

// =============================================================================
// Task 4: Execute Structured Error Events Tests
// =============================================================================

// TestExecutePlan_StructuredError_PreconditionFailed validates that precondition failure
// emits a structured error JobEvent with proper attribution fields.
