package execute

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	errdomain "github.com/onsei/organizer/backend/internal/errors"
)

// =============================================================================
// Soft Delete Retry/Failure Strategy Tests
// =============================================================================

// mockRenameLockedBusy simulates Locked/Busy error on every rename call
type mockRenameLockedBusy struct {
	mu        sync.Mutex
	callCount int32
}

func (m *mockRenameLockedBusy) rename(oldpath, newpath string) error {
	atomic.AddInt32(&m.callCount, 1)
	// Simulate Windows ERROR_LOCK_VIOLATION (33) or similar locked/busy condition
	return &os.PathError{
		Op:   "rename",
		Path: oldpath,
		Err:  errors.New("file is locked by another process"),
	}
}

func (m *mockRenameLockedBusy) getCallCount() int {
	return int(atomic.LoadInt32(&m.callCount))
}

// mockRenamePermissionDenied simulates Permission Denied error
type mockRenamePermissionDenied struct {
	mu        sync.Mutex
	callCount int32
}

func (m *mockRenamePermissionDenied) rename(oldpath, newpath string) error {
	atomic.AddInt32(&m.callCount, 1)
	// Simulate Permission Denied
	return &os.PathError{
		Op:   "rename",
		Path: oldpath,
		Err:  os.ErrPermission,
	}
}

func (m *mockRenamePermissionDenied) getCallCount() int {
	return int(atomic.LoadInt32(&m.callCount))
}

// TestDelete_SoftDelete_LockedBusy_RetriesUpToMax tests that when os.Rename
// fails with Locked/Busy errors, it retries up to max attempts (3) before
// returning an error WITHOUT falling back to hard delete.
//
// Expected behavior (TDD RED - not yet implemented):
// - Rename fails with Locked/Busy -> retry with exponential backoff
// - After 3 failed attempts, return error
// - Do NOT fall back to hard delete
func TestDelete_SoftDelete_LockedBusy_RetriesUpToMax(t *testing.T) {
	// Save original rename function and restore after test
	originalRename := renameFunc
	defer func() { renameFunc = originalRename }()

	// Setup mock that always fails with locked/busy
	mock := &mockRenameLockedBusy{}
	renameFunc = mock.rename

	// Create temp directory structure
	tmp := t.TempDir()
	rootPath := tmp
	sourceFile := filepath.Join(rootPath, "test.wav")
	if err := os.WriteFile(sourceFile, []byte("test content"), 0644); err != nil {
		t.Fatal(err)
	}

	runner := NewToolRunnerWithRoot(ToolsConfig{}, rootPath)

	// Execute soft delete - should fail after retries
	err := runner.Delete(sourceFile, true)

	// EXPECTED: Error should be returned (not success)
	// Current behavior: falls back to hard delete, no error
	if err == nil {
		t.Error("Expected error after retry exhaustion, got nil")
	}

	// EXPECTED: Rename should have been called 3 times (max retries)
	// Current behavior: called once, then falls back to hard delete
	callCount := mock.getCallCount()
	if callCount != 3 {
		t.Errorf("Expected 3 rename attempts (max retries), got %d", callCount)
	}

	// EXPECTED: Source file should still exist (not deleted)
	// Current behavior: file is hard deleted as fallback
	if _, statErr := os.Stat(sourceFile); os.IsNotExist(statErr) {
		t.Error("Source file should NOT be deleted - error should be returned instead of fallback to hard delete")
	}
}

// TestDelete_SoftDelete_PermissionDenied_NoRetry tests that when os.Rename
// fails with Permission Denied, it does NOT retry and immediately returns error.
//
// Expected behavior (TDD RED - not yet implemented):
// - Rename fails with Permission Denied -> no retry, immediate failure
// - Return error without falling back to hard delete
func TestDelete_SoftDelete_PermissionDenied_NoRetry(t *testing.T) {
	// Save original rename function and restore after test
	originalRename := renameFunc
	defer func() { renameFunc = originalRename }()

	// Setup mock that fails with permission denied
	mock := &mockRenamePermissionDenied{}
	renameFunc = mock.rename

	// Create temp directory structure
	tmp := t.TempDir()
	rootPath := tmp
	sourceFile := filepath.Join(rootPath, "test.wav")
	if err := os.WriteFile(sourceFile, []byte("test content"), 0644); err != nil {
		t.Fatal(err)
	}

	runner := NewToolRunnerWithRoot(ToolsConfig{}, rootPath)

	// Execute soft delete - should fail immediately without retry
	err := runner.Delete(sourceFile, true)

	// EXPECTED: Error should be returned (not success)
	// Current behavior: falls back to hard delete, no error
	if err == nil {
		t.Error("Expected error for permission denied, got nil")
	}

	// EXPECTED: Rename should have been called exactly once (no retry)
	// Current behavior: called once, then falls back to hard delete
	callCount := mock.getCallCount()
	if callCount != 1 {
		t.Errorf("Expected 1 rename attempt (no retry for permission denied), got %d", callCount)
	}

	// EXPECTED: Source file should still exist (not deleted)
	// Current behavior: file is hard deleted as fallback
	if _, statErr := os.Stat(sourceFile); os.IsNotExist(statErr) {
		t.Error("Source file should NOT be deleted - error should be returned instead of fallback to hard delete")
	}

	// EXPECTED: Error should be a ToolError with appropriate code
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		if toolErr.Code != errdomain.FILE_LOCKED {
			t.Errorf("Expected error code FILE_LOCKED for permission denied, got %v", toolErr.Code)
		}
	} else {
		t.Errorf("Expected ToolError, got %T", err)
	}
}

// TestDelete_SoftDelete_Success tests normal successful soft delete behavior
// This should continue to work after retry logic is added.
func TestDelete_SoftDelete_Success(t *testing.T) {
	// Save original rename function and restore after test
	originalRename := renameFunc
	defer func() { renameFunc = originalRename }()

	// Use real os.Rename for success case
	renameFunc = os.Rename

	// Create temp directory structure
	tmp := t.TempDir()
	rootPath := tmp
	subDir := filepath.Join(rootPath, "RJ 123456", "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(subDir, "test.wav")
	if err := os.WriteFile(sourceFile, []byte("test content"), 0644); err != nil {
		t.Fatal(err)
	}

	runner := NewToolRunnerWithRoot(ToolsConfig{}, rootPath)

	// Execute soft delete - should succeed
	err := runner.Delete(sourceFile, true)

	// Should succeed
	if err != nil {
		t.Fatalf("Expected successful soft delete, got error: %v", err)
	}

	// Source file should NOT exist (moved to Delete folder)
	if _, statErr := os.Stat(sourceFile); !os.IsNotExist(statErr) {
		t.Error("Source file should be moved (not exist at original location)")
	}

	// File should exist in Delete folder with relative path preserved
	expectedDest := filepath.Join(rootPath, "Delete", "RJ 123456", "subdir", "test.wav")
	if _, statErr := os.Stat(expectedDest); os.IsNotExist(statErr) {
		t.Errorf("File should exist at %s", expectedDest)
	}
}
func TestDelete_SoftDelete_RelativePath_ReturnsError(t *testing.T) {
	tmp := t.TempDir()
	runner := NewToolRunnerWithRoot(ToolsConfig{}, tmp)

	err := runner.Delete("relative/test.wav", true)
	if err == nil {
		t.Fatal("expected error for relative delete path, got nil")
	}

	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected ToolError, got %T", err)
	}
	if toolErr.Code != errdomain.FILE_LOCKED {
		t.Fatalf("expected FILE_LOCKED, got %v", toolErr.Code)
	}
}

func TestConvert_RelativePaths_ReturnsError(t *testing.T) {
	runner := NewToolRunner(ToolsConfig{Encoder: "qaac", QAACPath: "qaac"})

	err := runner.Convert("relative/in.wav", "relative/out.m4a")
	if err == nil {
		t.Fatal("expected error for relative convert paths, got nil")
	}

	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected ToolError, got %T", err)
	}
	if toolErr.Code != errdomain.FILE_LOCKED {
		t.Fatalf("expected FILE_LOCKED, got %v", toolErr.Code)
	}
}

func TestDelete_SoftDelete_RelativeRoot_ReturnsError(t *testing.T) {
	runner := NewToolRunnerWithRoot(ToolsConfig{}, "relative-root")

	err := runner.Delete(filepath.Join(string(filepath.Separator), "tmp", "x.wav"), true)
	if err == nil {
		t.Fatal("expected error for relative root path, got nil")
	}

	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected ToolError, got %T", err)
	}
	if toolErr.Code != errdomain.FILE_LOCKED {
		t.Fatalf("expected FILE_LOCKED, got %v", toolErr.Code)
	}
}
