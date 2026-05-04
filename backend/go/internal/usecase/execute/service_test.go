package execute

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// Success path tests
// =============================================================================

// TestExecute_SimpleDelete_Success validates a basic delete execution end-to-end:
// fresh preconditions, file deleted, events emitted, folder_completed sent.
func TestExecute_SimpleDelete_Success(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-simple-delete-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	// Create test audio file
	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-simple-delete-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	result, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr != nil {
		t.Fatalf("Execute returned error: %v", execErr)
	}

	if result.Status != "completed" {
		t.Errorf("expected status=completed, got %q", result.Status)
	}
	if result.PlanID != planID {
		t.Errorf("expected plan_id=%s, got %s", planID, result.PlanID)
	}

	// Verify file was deleted
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source file to be deleted")
	}

	// Verify events
	types := eventTypes(sink.events)
	assertContains(t, types, "started", "expected started event")
	assertContains(t, types, "folder_completed", "expected folder_completed event")
	assertContains(t, types, "completed", "expected completed event")
}

// TestExecute_SoftDelete_MovesToDeleteFolder validates that soft_delete=true
// moves the file to the Delete directory using the persisted target_path.
func TestExecute_SoftDelete_MovesToDeleteFolder(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-soft-delete-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	subDir := filepath.Join(tmpDir, "music")
	testFile := filepath.Join(subDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-soft-delete-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")

	persistedTargetPath := filepath.ToSlash(filepath.Join(tmpDir, "Delete", "music", "test.mp3"))
	seedDeleteItem(t, repo, planID, 0, testFile, persistedTargetPath, testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: true}, sink)
	if execErr != nil {
		t.Fatalf("Execute returned error: %v", execErr)
	}

	// Source should be gone
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source file to be moved")
	}

	// File should be at persisted target path
	if _, err := os.Stat(filepath.FromSlash(persistedTargetPath)); os.IsNotExist(err) {
		t.Errorf("expected file moved to %s", persistedTargetPath)
	}
}

// TestExecute_HardDelete_RemovesFile validates soft_delete=false performs hard delete.
func TestExecute_HardDelete_RemovesFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-hard-delete-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-hard-delete-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr != nil {
		t.Fatalf("Execute returned error: %v", execErr)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source file to be hard deleted")
	}

	deleteDir := filepath.Join(tmpDir, "Delete")
	if _, err := os.Stat(deleteDir); !os.IsNotExist(err) {
		t.Error("expected Delete folder to NOT be created for hard delete")
	}
}

// =============================================================================
// Precondition failure tests
// =============================================================================

// TestExecute_StaleContentRev_FailsWithError validates that stale content_rev
// causes precondition failure, error event, and file preservation.
func TestExecute_StaleContentRev_FailsWithError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-stale-rev-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-stale-rev-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	// Wrong content_rev (999 instead of 1)
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 999, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr == nil {
		t.Fatal("expected error for stale precondition")
	}

	// File should still exist
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after stale precondition")
	}

	// Should have error event
	assertHasErrorEvent(t, sink.events, "EXEC_PRECONDITION_FAILED")
}

// TestExecute_StaleSize_FailsWithError validates size mismatch precondition.
func TestExecute_StaleSize_FailsWithError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-stale-size-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-stale-size-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	// Wrong size
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size()+1000, info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr == nil {
		t.Fatal("expected error for stale size precondition")
	}

	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist")
	}
	assertHasErrorEvent(t, sink.events, "EXEC_PRECONDITION_FAILED")
}

// TestExecute_SourceFileDeleted_FailsWithError validates precondition failure
// when source file was deleted after plan creation.
func TestExecute_SourceFileDeleted_FailsWithError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-file-gone-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-file-gone-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix())

	// Delete source file before execution
	if err := os.Remove(testFile); err != nil {
		t.Fatal(err)
	}

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr == nil {
		t.Fatal("expected error for missing source file")
	}

	// Should have SOURCE_MISSING in error
	found := false
	for _, ev := range sink.events {
		if ev.Type == "error" && strings.Contains(ev.Message, "SOURCE_MISSING") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SOURCE_MISSING error event")
	}
}

// TestExecute_FreshMtimeOnly_Succeeds validates that precondition using only mtime
// (content_rev=0) succeeds when mtime matches.
func TestExecute_FreshMtimeOnly_Succeeds(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-fresh-mtime-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	// Insert entry with content_rev=0
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 0, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	planID := "plan-fresh-mtime-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	// content_rev=0 (skip content check), matching mtime
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 0, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr != nil {
		t.Fatalf("Execute returned error: %v", execErr)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source file to be deleted")
	}
}

// =============================================================================
// Validation error tests
// =============================================================================

// TestExecute_InvalidPlanID_ReturnsError validates empty plan_id error with event.
func TestExecute_InvalidPlanID_ReturnsError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-invalid-plan-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)
	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: "", SoftDelete: false}, sink)
	if execErr == nil {
		t.Fatal("expected error for empty plan ID")
	}

	useErr, ok := AsError(execErr)
	if !ok {
		t.Fatalf("expected usecase.Error, got %T: %v", execErr, execErr)
	}
	if useErr.Kind != ErrKindInvalidArgument {
		t.Errorf("expected kind=%s, got %q", ErrKindInvalidArgument, useErr.Kind)
	}

	assertHasErrorEvent(t, sink.events, "INVALID_PLAN_ID")
}

// TestExecute_PlanNotFound_ReturnsError validates non-existent plan handling.
func TestExecute_PlanNotFound_ReturnsError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-notfound-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)
	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: "nonexistent-plan", SoftDelete: false}, sink)
	if execErr == nil {
		t.Fatal("expected error for non-existent plan")
	}

	useErr, ok := AsError(execErr)
	if !ok {
		t.Fatalf("expected usecase.Error, got %T: %v", execErr, execErr)
	}
	if useErr.Kind != ErrKindNotFound {
		t.Errorf("expected kind=%s, got %q", ErrKindNotFound, useErr.Kind)
	}

	assertHasErrorEvent(t, sink.events, "PLAN_NOT_FOUND")
}

// =============================================================================
// Delete error tests
// =============================================================================

// TestExecute_DeleteTargetConflict_Fails validates that target conflict
// blocks deletion, emits error, and preserves both files.
func TestExecute_DeleteTargetConflict_Fails(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-target-conflict-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	subDir := filepath.Join(tmpDir, "Music")
	testFile := filepath.Join(subDir, "song.mp3")
	info := writeTestFile(t, testFile, "dummy audio")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-target-conflict-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")

	persistedTargetPath := filepath.ToSlash(filepath.Join(tmpDir, "Delete", "Music", "song.mp3"))
	seedDeleteItem(t, repo, planID, 0, testFile, persistedTargetPath, testFile, 1, info.Size(), info.ModTime().Unix())

	// Pre-create conflicting file at target path
	deleteDir := filepath.Join(tmpDir, "Delete", "Music")
	if err := os.MkdirAll(deleteDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.FromSlash(persistedTargetPath), []byte("CONFLICTING CONTENT"), 0644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: true}, sink)
	if execErr == nil {
		t.Fatal("expected error for target conflict")
	}

	// Source should still exist
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after target conflict")
	}

	// Target should retain original content
	content, _ := os.ReadFile(filepath.FromSlash(persistedTargetPath))
	if string(content) != "CONFLICTING CONTENT" {
		t.Errorf("expected target to retain original content, got: %s", string(content))
	}

	// Should have TARGET_CONFLICT error
	found := false
	for _, ev := range sink.events {
		if ev.Type == "error" && strings.Contains(ev.Message, "TARGET_CONFLICT") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected TARGET_CONFLICT error event")
	}
}

// TestExecute_SourceMissing_EmitsError validates that missing source file
// at execution time produces SOURCE_MISSING error.
func TestExecute_SourceMissing_EmitsError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-source-missing-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	subDir := filepath.Join(tmpDir, "Music")
	testFile := filepath.Join(subDir, "song.mp3")
	info := writeTestFile(t, testFile, "dummy audio")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-source-missing-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix())

	// Delete source before execution
	if err := os.Remove(testFile); err != nil {
		t.Fatal(err)
	}

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr == nil {
		t.Fatal("expected error for source missing")
	}

	// Should have SOURCE_MISSING error
	found := false
	for _, ev := range sink.events {
		if ev.Type == "error" && strings.HasPrefix(ev.Message, "SOURCE_MISSING:") {
			found = true
			// Verify folder attribution
			expectedFolder := filepath.ToSlash(subDir)
			if ev.FolderPath != expectedFolder {
				t.Errorf("expected folder %s, got %s", expectedFolder, ev.FolderPath)
			}
			break
		}
	}
	if !found {
		t.Error("expected SOURCE_MISSING error event")
	}
}

// TestExecute_DeleteMissingTargetPath_Fails validates that delete items
// without persisted target_path fail when soft_delete=true.
func TestExecute_DeleteMissingTargetPath_Fails(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-missing-target-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-missing-target-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	// No target_path (empty string)
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: true}, sink)

	// Source should still exist
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after failed delete")
	}

	// Should have error event
	found := false
	for _, ev := range sink.events {
		if ev.Type == "error" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected error event for missing target path")
	}
}

// =============================================================================
// Config validation tests
// =============================================================================

// TestExecute_ConvertPlanWithoutToolsConfig_Fails validates that convert plans
// fail when tools config is missing.
func TestExecute_ConvertPlanWithoutToolsConfig_Fails(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-convert-no-tools-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	// Create config.json with empty tools config
	configJSON := `{"prune": {"regex_pattern": "^\\."}, "tools": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(tmpDir, "test.wav")
	info := writeTestFile(t, testFile, "dummy audio content")

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/wav', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	planID := "plan-convert-no-tools-001"
	seedPlan(t, repo, planID, tmpDir, "single_convert")
	seedConvertItem(t, repo, planID, 0, testFile, filepath.Join(tmpDir, "test.mp3"), testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr == nil {
		t.Fatal("expected error for missing encoder config")
	}

	useErr, ok := AsError(execErr)
	if !ok {
		t.Fatalf("expected usecase.Error, got %T", execErr)
	}
	if useErr.Kind != ErrKindFailedPrecondition {
		t.Errorf("expected kind=%s, got %q", ErrKindFailedPrecondition, useErr.Kind)
	}

	// CONFIG_INVALID error event should be emitted by lower-level service
	assertHasErrorEvent(t, sink.events, "CONFIG_INVALID")

	// File should still exist
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after preflight failure")
	}
}

// TestExecute_DeleteOnlyPlan_SkipsToolsConfig validates that delete-only plans
// succeed even without tools config.
func TestExecute_DeleteOnlyPlan_SkipsToolsConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-delete-no-tools-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	// Empty tools config
	configJSON := `{"prune": {"regex_pattern": "^\\."}, "tools": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-delete-no-tools-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr != nil {
		t.Fatalf("delete-only plan should succeed without tools config, got: %v", execErr)
	}

	assertContains(t, eventTypes(sink.events), "completed", "expected completed event")
}

// TestExecute_DeleteOnlyPlan_IgnoresMalformedToolsConfig validates delete-only
// plans succeed even when tools config is malformed.
func TestExecute_DeleteOnlyPlan_IgnoresMalformedToolsConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-delete-bad-config-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	// Malformed JSON in tools section
	configJSON := `{"prune": {"regex_pattern": "^\\."}, "tools": {"encoder": "lame", "lame_path": "C:/tools/lame.exe"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-delete-bad-config-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr != nil {
		t.Fatalf("delete-only plan should succeed with malformed tools config, got: %v", execErr)
	}

	assertContains(t, eventTypes(sink.events), "completed", "expected completed event")
}

// =============================================================================
// Convert operation tests
// =============================================================================

// TestExecute_ConvertWithFakeEncoder_SoftDelete validates convert + soft_delete
// using a deterministic fake encoder.
func TestExecute_ConvertWithFakeEncoder_SoftDelete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-convert-soft-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create deterministic fake encoder
	encoderPath := createFakeEncoder(t, tmpDir)

	configJSON := `{"prune": {"regex_pattern": "^\\."}, "tools": {"encoder": "lame", "lame_path": "` + strings.ReplaceAll(encoderPath, "\\", "\\\\") + `"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.wav")
	info := writeTestFile(t, testFile, "dummy audio content")

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/wav', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	planID := "plan-convert-soft-001"
	seedPlan(t, repo, planID, tmpDir, "single_convert")
	seedConvertItem(t, repo, planID, 0, testFile, filepath.Join(tmpDir, "test.mp3"), testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: true}, sink)
	if execErr != nil {
		t.Fatalf("Execute with fake encoder should succeed, got: %v", execErr)
	}

	// Source should be gone
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source to be removed")
	}

	// Source should be in Delete folder
	expectedDeletePath := filepath.Join(tmpDir, "Delete", "test.wav")
	if _, err := os.Stat(expectedDeletePath); os.IsNotExist(err) {
		t.Errorf("expected source moved to Delete folder at %s", expectedDeletePath)
	}
}

// TestExecute_ConvertWithFakeEncoder_HardDelete validates convert + hard delete.
func TestExecute_ConvertWithFakeEncoder_HardDelete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-convert-hard-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	encoderPath := createFakeEncoder(t, tmpDir)
	configJSON := `{"prune": {"regex_pattern": "^\\."}, "tools": {"encoder": "lame", "lame_path": "` + strings.ReplaceAll(encoderPath, "\\", "\\\\") + `"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.wav")
	info := writeTestFile(t, testFile, "dummy audio content")

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/wav', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	planID := "plan-convert-hard-001"
	seedPlan(t, repo, planID, tmpDir, "single_convert")
	seedConvertItem(t, repo, planID, 0, testFile, filepath.Join(tmpDir, "test.mp3"), testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr != nil {
		t.Fatalf("Execute with fake encoder should succeed, got: %v", execErr)
	}

	// Source should be hard deleted
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected source to be hard deleted")
	}

	// Delete folder should NOT exist
	deleteDir := filepath.Join(tmpDir, "Delete")
	if _, err := os.Stat(deleteDir); !os.IsNotExist(err) {
		t.Error("expected Delete folder to NOT be created")
	}
}

// TestExecute_ConvertFailure_PreservesSource validates that convert failure
// does NOT delete the source file.
func TestExecute_ConvertFailure_PreservesSource(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-convert-fail-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Config with nonexistent encoder
	configJSON := `{"prune": {"regex_pattern": "^\\."}, "tools": {"encoder": "lame", "lame_path": "nonexistent_lame_encoder"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.wav")
	info := writeTestFile(t, testFile, "dummy audio content")

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/wav', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	planID := "plan-convert-fail-001"
	seedPlan(t, repo, planID, tmpDir, "single_convert")
	seedConvertItem(t, repo, planID, 0, testFile, filepath.Join(tmpDir, "test.mp3"), testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: true}, sink)
	if execErr == nil {
		t.Fatal("expected convert to fail with nonexistent encoder")
	}

	// Source file should still exist
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after convert failure")
	}
}

// =============================================================================
// Soft delete move failure test
// =============================================================================

// TestExecute_SoftDeleteMoveFailure_ReturnsError validates that when soft delete
// move fails (e.g., root/Delete exists as a file), it returns error and
// preserves source.
func TestExecute_SoftDeleteMoveFailure_ReturnsError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-soft-fail-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	subDir := filepath.Join(tmpDir, "music")
	testFile := filepath.Join(subDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-soft-fail-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix())

	// Pre-create Delete as a FILE (not directory) to cause move failure
	deleteFile := filepath.Join(tmpDir, "Delete")
	if err := os.WriteFile(deleteFile, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: true}, sink)
	if execErr == nil {
		t.Fatal("expected error for soft delete move failure")
	}

	// Source should still exist
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist after move failure")
	}

	// The Delete file should still exist
	if _, err := os.Stat(deleteFile); os.IsNotExist(err) {
		t.Error("expected original Delete file to still exist")
	}
}

// =============================================================================
// Error persistence tests
// =============================================================================

// TestExecute_ErrorPersisted_PreconditionFailed validates that execute-stage
// errors are persisted into the error_events table.
func TestExecute_ErrorPersisted_PreconditionFailed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-persist-precond-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	subDir := filepath.Join(tmpDir, "Album")
	testFile := filepath.Join(subDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-persist-precond-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 999, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)

	// Check error_events table
	ev := findErrorEventByCode(t, repo, "EXEC_PRECONDITION_FAILED")
	if ev == nil {
		t.Fatal("expected EXEC_PRECONDITION_FAILED in error_events")
	}
	if ev.Scope != "execute" {
		t.Errorf("expected scope='execute', got %q", ev.Scope)
	}
	if ev.RootPath == "" {
		t.Error("expected non-empty root_path")
	}
	if ev.Path == nil || *ev.Path == "" {
		t.Error("expected non-empty path for folder-attributed execute error")
	}
	if ev.Message == "" {
		t.Error("expected non-empty message")
	}
	if ev.Retryable {
		t.Error("expected retryable=false")
	}
}

// TestExecute_ErrorPersisted_InvalidPlanID validates INVALID_PLAN_ID persistence.
func TestExecute_ErrorPersisted_InvalidPlanID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-persist-invalid-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)
	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: ""}, sink)

	ev := findErrorEventByCode(t, repo, "INVALID_PLAN_ID")
	if ev == nil {
		t.Fatal("expected INVALID_PLAN_ID in error_events")
	}
	if ev.Scope != "execute" {
		t.Errorf("expected scope='execute', got %q", ev.Scope)
	}
	if ev.Message == "" {
		t.Error("expected non-empty message")
	}
}

// TestExecute_ErrorPersisted_PlanNotFound validates PLAN_NOT_FOUND persistence.
func TestExecute_ErrorPersisted_PlanNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-persist-notfound-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)
	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: "nonexistent-plan-id"}, sink)

	ev := findErrorEventByCode(t, repo, "PLAN_NOT_FOUND")
	if ev == nil {
		t.Fatal("expected PLAN_NOT_FOUND in error_events")
	}
	if ev.Scope != "execute" {
		t.Errorf("expected scope='execute', got %q", ev.Scope)
	}
}

// =============================================================================
// Event sequencing tests
// =============================================================================

// TestExecute_EmitStartedAndCompletedEvents validates that started and completed
// events are emitted in order.
func TestExecute_EmitStartedAndCompletedEvents(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-events-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-events-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr != nil {
		t.Fatalf("Execute returned error: %v", execErr)
	}

	types := eventTypes(sink.events)
	if len(types) < 2 {
		t.Fatalf("expected at least 2 events, got %d: %v", len(types), types)
	}
	if types[0] != "started" {
		t.Errorf("expected first event 'started', got %q", types[0])
	}
	if types[len(types)-1] != "completed" {
		t.Errorf("expected last event 'completed', got %q", types[len(types)-1])
	}
}

// TestExecute_EventIDs_Unique validates that all events have unique event IDs.
func TestExecute_EventIDs_Unique(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-unique-ids-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	folderA := filepath.Join(tmpDir, "AlbumA")
	folderB := filepath.Join(tmpDir, "AlbumB")
	fileA := filepath.Join(folderA, "test.mp3")
	fileB := filepath.Join(folderB, "test.mp3")

	infoA := writeTestFile(t, fileA, "dummy a")
	infoB := writeTestFile(t, fileB, "dummy b")

	seedEntry(t, repo, fileA, tmpDir, infoA)
	seedEntry(t, repo, fileB, tmpDir, infoB)

	planID := "plan-unique-ids-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, fileA, "", fileA, 999, infoA.Size(), infoA.ModTime().Unix())
	seedDeleteItem(t, repo, planID, 1, fileB, "", fileB, 999, infoB.Size(), infoB.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)

	seenIDs := make(map[string]bool)
	for _, ev := range sink.events {
		if ev.EventID == "" {
			continue
		}
		if seenIDs[ev.EventID] {
			t.Errorf("duplicate event_id %q", ev.EventID)
		}
		seenIDs[ev.EventID] = true
	}
}

// TestExecute_FolderCompletedEmittedOnSuccess validates folder_completed event structure.
func TestExecute_FolderCompletedEmittedOnSuccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-folder-ok-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	subDir := filepath.Join(tmpDir, "Album")
	testFile := filepath.Join(subDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-folder-ok-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr != nil {
		t.Fatalf("Execute returned error: %v", execErr)
	}

	// Find folder_completed event
	var fc *Event
	for i := range sink.events {
		if sink.events[i].Type == "folder_completed" {
			fc = &sink.events[i]
			break
		}
	}
	if fc == nil {
		t.Fatal("expected folder_completed event")
	}

	if fc.Stage != "execute" {
		t.Errorf("expected stage=execute, got %q", fc.Stage)
	}
	if fc.FolderPath == "" {
		t.Error("expected non-empty folder_path")
	}
	if fc.PlanID != planID {
		t.Errorf("expected plan_id=%s, got %s", planID, fc.PlanID)
	}
	if fc.RootPath == "" {
		t.Error("expected non-empty root_path")
	}
	if fc.EventID == "" {
		t.Error("expected non-empty event_id")
	}
	if fc.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	expectedMsg := "Scope " + fc.FolderPath + " completed"
	if fc.Message != expectedMsg {
		t.Errorf("expected message %q, got %q", expectedMsg, fc.Message)
	}
}

// TestExecute_FolderFailedEmittedOnFailure validates folder_failed event structure.
func TestExecute_FolderFailedEmittedOnFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-folder-fail-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	subDir := filepath.Join(tmpDir, "Album")
	testFile := filepath.Join(subDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-folder-fail-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	// Stale precondition
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 999, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)

	var ff *Event
	for i := range sink.events {
		if sink.events[i].Type == "folder_failed" {
			ff = &sink.events[i]
			break
		}
	}
	if ff == nil {
		t.Fatal("expected folder_failed event")
	}

	if ff.Stage != "execute" {
		t.Errorf("expected stage=execute, got %q", ff.Stage)
	}
	if ff.FolderPath == "" {
		t.Error("expected non-empty folder_path")
	}
	if ff.PlanID != planID {
		t.Errorf("expected plan_id=%s, got %s", planID, ff.PlanID)
	}
	if ff.RootPath == "" {
		t.Error("expected non-empty root_path")
	}
	if ff.EventID == "" {
		t.Error("expected non-empty event_id")
	}
	expectedMsg := "Scope " + ff.FolderPath + " failed"
	if ff.Message != expectedMsg {
		t.Errorf("expected message %q, got %q", expectedMsg, ff.Message)
	}
}

// TestExecute_FolderCompletedBeforeLaterFolderFailure validates ordering:
// successful folder_completed emitted before later folder's error.
func TestExecute_FolderCompletedBeforeLaterFolderFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-folder-order-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	folderA := filepath.Join(tmpDir, "AlbumA")
	folderB := filepath.Join(tmpDir, "AlbumB")
	fileA := filepath.Join(folderA, "a.mp3")
	fileB := filepath.Join(folderB, "b.mp3")

	infoA := writeTestFile(t, fileA, "a")
	infoB := writeTestFile(t, fileB, "b")

	seedEntry(t, repo, fileA, tmpDir, infoA)
	seedEntry(t, repo, fileB, tmpDir, infoB)

	planID := "plan-folder-order-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	// Folder A succeeds
	seedDeleteItem(t, repo, planID, 0, fileA, "", fileA, 1, infoA.Size(), infoA.ModTime().Unix())
	// Folder B fails precondition
	seedDeleteItem(t, repo, planID, 1, fileB, "", fileB, 999, infoB.Size(), infoB.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)

	folderCompletedIdx := -1
	errorForFolderBIdx := -1
	for i, ev := range sink.events {
		if ev.Type == "folder_completed" && strings.Contains(ev.FolderPath, "AlbumA") {
			folderCompletedIdx = i
		}
		if ev.Type == "error" && strings.Contains(ev.FolderPath, "AlbumB") {
			errorForFolderBIdx = i
		}
	}

	if folderCompletedIdx == -1 {
		t.Fatal("expected folder_completed for AlbumA")
	}
	if errorForFolderBIdx == -1 {
		t.Fatal("expected error for AlbumB")
	}
	if folderCompletedIdx > errorForFolderBIdx {
		t.Fatalf("expected folder_completed(before %d) error(%d)", folderCompletedIdx, errorForFolderBIdx)
	}
}

// =============================================================================
// Per-folder fail-fast test
// =============================================================================

// TestExecute_PerFolderFailFast validates that the first error in a folder
// stops processing of remaining items in that folder, but other folders continue.
func TestExecute_PerFolderFailFast(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-failfast-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	folderA := filepath.Join(tmpDir, "AlbumA")
	folderB := filepath.Join(tmpDir, "AlbumB")
	fileA1 := filepath.Join(folderA, "song1.mp3")
	fileA2 := filepath.Join(folderA, "song2.mp3")
	fileB1 := filepath.Join(folderB, "song1.mp3")

	infoA1 := writeTestFile(t, fileA1, "a1")
	infoA2 := writeTestFile(t, fileA2, "a2")
	infoB1 := writeTestFile(t, fileB1, "b1")

	seedEntry(t, repo, fileA1, tmpDir, infoA1)
	seedEntry(t, repo, fileA2, tmpDir, infoA2)
	seedEntry(t, repo, fileB1, tmpDir, infoB1)

	planID := "plan-failfast-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	// A1: stale precondition (will fail)
	seedDeleteItem(t, repo, planID, 0, fileA1, "", fileA1, 999, infoA1.Size(), infoA1.ModTime().Unix())
	// A2: valid precondition (should NOT be processed due to fail-fast)
	seedDeleteItem(t, repo, planID, 1, fileA2, "", fileA2, 1, infoA2.Size(), infoA2.ModTime().Unix())
	// B1: valid precondition (should be processed)
	seedDeleteItem(t, repo, planID, 2, fileB1, "", fileB1, 1, infoB1.Size(), infoB1.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)

	// A1 should still exist (precondition blocked)
	if _, err := os.Stat(fileA1); os.IsNotExist(err) {
		t.Error("A1 should still exist (precondition failure blocks mutation)")
	}
	// A2 should still exist (fail-fast stopped folder A)
	if _, err := os.Stat(fileA2); os.IsNotExist(err) {
		t.Error("A2 should still exist (fail-fast should have stopped folder A)")
	}
	// B1 should be deleted (folder B continues)
	if _, err := os.Stat(fileB1); !os.IsNotExist(err) {
		t.Error("B1 should be deleted (folder B should continue)")
	}

	// folder_completed should only be for AlbumB
	folderCompletedCount := 0
	for _, ev := range sink.events {
		if ev.Type == "folder_completed" {
			folderCompletedCount++
			if !strings.Contains(ev.FolderPath, "AlbumB") {
				t.Errorf("folder_completed should only be for AlbumB, got %q", ev.FolderPath)
			}
		}
	}
	if folderCompletedCount != 1 {
		t.Errorf("expected 1 folder_completed event (for AlbumB), got %d", folderCompletedCount)
	}
}

// =============================================================================
// Global vs folder-attributed error tests
// =============================================================================

// TestExecute_ConfigInvalid_RemainsGlobal validates that CONFIG_INVALID
// errors remain global (no folder attribution).
func TestExecute_ConfigInvalid_RemainsGlobal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-config-global-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	// No tools config
	configJSON := `{"prune": {"regex_pattern": "^\\."}, "tools": {}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(tmpDir, "test.wav")
	info := writeTestFile(t, testFile, "dummy audio content")

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/wav', 1, ?)
	`, filepath.ToSlash(testFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	planID := "plan-config-global-001"
	seedPlan(t, repo, planID, tmpDir, "single_convert")
	seedConvertItem(t, repo, planID, 0, testFile, filepath.Join(tmpDir, "test.mp3"), testFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)

	for _, ev := range sink.events {
		if ev.Type == "error" && ev.Code == "CONFIG_INVALID" && ev.FolderPath != "" {
			t.Errorf("CONFIG_INVALID error should be global (folder_path=''), got %q", ev.FolderPath)
		}
	}
}

// TestExecute_InvalidPlanID_RemainsGlobal validates that invalid plan_id errors are global.
func TestExecute_InvalidPlanID_RemainsGlobal(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-invalid-global-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)
	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: ""}, sink)

	for _, ev := range sink.events {
		if ev.Type == "error" && ev.FolderPath != "" {
			t.Errorf("invalid plan_id error should be global (folder_path=''), got %q", ev.FolderPath)
		}
	}
}

// =============================================================================
// Scan root path tests
// =============================================================================

// TestExecute_SoftDelete_UsesScanRootPath validates that soft delete uses the
// scan_root_path when scope root is a subfolder.
func TestExecute_SoftDelete_UsesScanRootPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-scanroot-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	scopeDir := filepath.Join(tmpDir, "music", "album")
	sourceFile := filepath.Join(scopeDir, "test.mp3")
	info := writeTestFile(t, sourceFile, "dummy audio")

	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime)
		VALUES (?, ?, 0, ?, 'audio/mpeg', 1, ?)
	`, filepath.ToSlash(sourceFile), filepath.ToSlash(tmpDir), info.Size(), info.ModTime().Unix())
	if err != nil {
		t.Fatal(err)
	}

	planID := "plan-scanroot-001"
	seedPlanWithScanRoot(t, repo, planID, filepath.ToSlash(scopeDir), filepath.ToSlash(tmpDir), "single_delete")

	persistedTargetPath := filepath.ToSlash(filepath.Join(tmpDir, "Delete", "music", "album", "test.mp3"))
	seedDeleteItem(t, repo, planID, 0, sourceFile, persistedTargetPath, sourceFile, 1, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: true}, sink)
	if execErr != nil {
		t.Fatalf("Execute failed: %v", execErr)
	}

	// Should be at scan-root relative delete path
	expected := filepath.Join(tmpDir, "Delete", "music", "album", "test.mp3")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected file at scan-root delete path %s: %v", expected, err)
	}
}

// TestExecute_StalePrecondition_PreservesErrorCodeThroughUsecase verifies
// that rooted stale precondition errors from the lower service are not
// collapsed to a generic EXECUTE_FAILED by the usecase. The usecase must
// propagate the lower-layer EXEC_PRECONDITION_FAILED semantics.
func TestExecute_StalePrecondition_PreservesErrorCodeThroughUsecase(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-preserve-code-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-preserve-code-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	// Stale content_rev (999 instead of 1) triggers precondition failure.
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 999, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)

	useErr, ok := AsError(execErr)
	if !ok {
		t.Fatalf("expected usecase.Error, got %T: %v", execErr, execErr)
	}
	// The usecase must surface EXEC_PRECONDITION_FAILED from the lower service,
	// not collapse it to the generic EXECUTE_FAILED.
	if useErr.Code != "EXEC_PRECONDITION_FAILED" {
		t.Errorf("expected usecase error code EXEC_PRECONDITION_FAILED, got %q", useErr.Code)
	}
}

// =============================================================================
// All folders failed → overall failure test
// =============================================================================

// TestExecute_AllFoldersFailed_OverallFailure validates that when every folder
// fails, the overall result is a failure.
func TestExecute_AllFoldersFailed_OverallFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-all-fail-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	folderA := filepath.Join(tmpDir, "AlbumA")
	folderB := filepath.Join(tmpDir, "AlbumB")
	fileA := filepath.Join(folderA, "a.mp3")
	fileB := filepath.Join(folderB, "b.mp3")

	infoA := writeTestFile(t, fileA, "a")
	infoB := writeTestFile(t, fileB, "b")

	seedEntry(t, repo, fileA, tmpDir, infoA)
	seedEntry(t, repo, fileB, tmpDir, infoB)

	planID := "plan-all-fail-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	// Both folders have stale preconditions
	seedDeleteItem(t, repo, planID, 0, fileA, "", fileA, 999, infoA.Size(), infoA.ModTime().Unix())
	seedDeleteItem(t, repo, planID, 1, fileB, "", fileB, 999, infoB.Size(), infoB.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	result, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr == nil {
		t.Fatal("expected overall failure when all folders fail")
	}

	if result.Status != "failed" {
		t.Errorf("expected status=failed, got %q", result.Status)
	}

	useErr, ok := AsError(execErr)
	if !ok {
		t.Fatalf("expected usecase.Error, got %T", execErr)
	}
	// The lower service returns precondition_failed for stale items.
	// The usecase propagates EXEC_PRECONDITION_FAILED through its error object.
	if useErr.Code != "EXEC_PRECONDITION_FAILED" {
		t.Errorf("expected EXEC_PRECONDITION_FAILED, got %q", useErr.Code)
	}
}

// =============================================================================
// Stale mtime test
// =============================================================================

// TestExecute_StaleMtime_FailsWithError validates mtime precondition failure.
func TestExecute_StaleMtime_FailsWithError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-stale-mtime-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	testFile := filepath.Join(tmpDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-stale-mtime-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	// Wrong mtime (future)
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 1, info.Size(), info.ModTime().Unix()+3600)

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, execErr := svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)
	if execErr == nil {
		t.Fatal("expected error for stale mtime")
	}

	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to still exist")
	}
	assertHasErrorEvent(t, sink.events, "EXEC_PRECONDITION_FAILED")
}

// =============================================================================
// Structured error event tests
// =============================================================================

// TestExecute_StructuredError_PreconditionFailed validates that precondition
// failure errors have proper structure (stage, code, folder, event_id, etc.).
func TestExecute_StructuredError_PreconditionFailed(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-usecase-structured-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo := newTestRepo(t, tmpDir)

	subDir := filepath.Join(tmpDir, "Album")
	testFile := filepath.Join(subDir, "test.mp3")
	info := writeTestFile(t, testFile, "dummy audio content")

	seedEntry(t, repo, testFile, tmpDir, info)
	planID := "plan-structured-001"
	seedPlan(t, repo, planID, tmpDir, "single_delete")
	seedDeleteItem(t, repo, planID, 0, testFile, "", testFile, 999, info.Size(), info.ModTime().Unix())

	svc := NewService(repo, tmpDir)
	sink := &testEventSink{}

	_, _ = svc.Execute(context.Background(), Request{PlanID: planID, SoftDelete: false}, sink)

	var errorEvent *Event
	for i := range sink.events {
		if sink.events[i].Type == "error" {
			errorEvent = &sink.events[i]
			break
		}
	}
	if errorEvent == nil {
		t.Fatal("expected error event")
	}

	if errorEvent.Stage != "execute" {
		t.Errorf("expected stage=execute, got %q", errorEvent.Stage)
	}
	if errorEvent.Code != "EXEC_PRECONDITION_FAILED" {
		t.Errorf("expected code=EXEC_PRECONDITION_FAILED, got %q", errorEvent.Code)
	}
	if errorEvent.PlanID != planID {
		t.Errorf("expected plan_id=%s, got %s", planID, errorEvent.PlanID)
	}
	if errorEvent.EventID == "" {
		t.Error("expected non-empty event_id")
	}
	if errorEvent.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	if errorEvent.RootPath == "" {
		t.Error("expected non-empty root_path")
	}
	if errorEvent.FolderPath == "" {
		t.Error("expected non-empty folder_path for precondition failure")
	}
	if errorEvent.ItemSourcePath == "" {
		t.Error("expected non-empty item_source_path")
	}
}

// =============================================================================
// Helpers
// =============================================================================

func assertContains(t *testing.T, slice []string, want string, msg string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("%s: %q not found in %v", msg, want, slice)
}

func assertHasErrorEvent(t *testing.T, events []Event, code string) {
	t.Helper()
	for _, ev := range events {
		if ev.Type == "error" && ev.Code == code {
			return
		}
	}
	var codes []string
	for _, ev := range events {
		if ev.Type == "error" {
			codes = append(codes, ev.Code)
		}
	}
	t.Errorf("expected error event with code %q among %v in %d total events", code, codes, len(events))
}
