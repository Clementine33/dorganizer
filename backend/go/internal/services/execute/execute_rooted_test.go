package execute

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestExecutePlan_RootedConvertFailure_NotCompleted verifies rooted Task4-mode
// execution does not report overall completed when convert pipeline fails.
func TestExecutePlan_RootedConvertFailure_NotCompleted(t *testing.T) {
	tmp := t.TempDir()

	folderA := filepath.Join(tmp, "AlbumA")
	folderB := filepath.Join(tmp, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	fileA := filepath.Join(folderA, "a.wav")
	fileB := filepath.Join(folderB, "b.wav")
	if err := os.WriteFile(fileA, []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("bbbb"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID:   "plan-rooted-convert-fail",
		RootPath: filepath.ToSlash(tmp),
		Items: []PlanItem{
			{Type: ItemTypeConvert, SourcePath: fileA, TargetPath: filepath.Join(folderA, "a.m4a"), PreconditionPath: fileA, PreconditionSize: 4},
			{Type: ItemTypeConvert, SourcePath: fileB, TargetPath: filepath.Join(folderB, "b.m4a"), PreconditionPath: fileB, PreconditionSize: 4},
		},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = 0
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)
	if err == nil {
		t.Fatal("expected rooted convert failure to return error")
	}
	if result == nil {
		t.Fatal("expected non-nil result on failure")
	}
	if result.Status == "completed" {
		t.Fatalf("expected non-completed status on rooted convert failure, got %q", result.Status)
	}
}

// TestExecutePlan_RootedMixedFailure_ContinuesOtherFolder verifies rooted Task4-mode
// execution continues other folders after a folder-attributed convert failure,
// while overall result still reports failure (not completed).
func TestExecutePlan_RootedMixedFailure_ContinuesOtherFolder(t *testing.T) {
	tmp := t.TempDir()

	folderA := filepath.Join(tmp, "AlbumA")
	folderB := filepath.Join(tmp, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	fileA := filepath.Join(folderA, "a.wav")
	fileB := filepath.Join(folderB, "b.mp3")
	if err := os.WriteFile(fileA, []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("bbbb"), 0644); err != nil {
		t.Fatal(err)
	}
	infoB, err := os.Stat(fileB)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID:   "plan-rooted-mixed-fail",
		RootPath: filepath.ToSlash(tmp),
		Items: []PlanItem{
			{Type: ItemTypeConvert, SourcePath: fileA, TargetPath: filepath.Join(folderA, "a.m4a"), PreconditionPath: fileA, PreconditionSize: 4},
			{Type: ItemTypeDelete, SourcePath: fileB, PreconditionPath: fileB, PreconditionSize: infoB.Size(), PreconditionMtime: infoB.ModTime().Unix()},
		},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = 0
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)
	if err == nil {
		t.Fatal("expected rooted mixed execution to return error")
	}
	if result == nil {
		t.Fatal("expected non-nil result on mixed failure")
	}
	if result.Status == "completed" {
		t.Fatalf("expected non-completed status on mixed rooted failure, got %q", result.Status)
	}

	if _, statErr := os.Stat(fileB); !os.IsNotExist(statErr) {
		t.Fatalf("expected folder B delete item to be processed (deleted), statErr=%v", statErr)
	}
}

// TestExecutePlan_RootedConsecutiveConvertFailure_ContinuesOtherFolder verifies
// rooted Task4-mode consecutive converts across folders do not share one batch:
// folder A convert failure must not block folder B convert processing.
func TestExecutePlan_RootedConsecutiveConvertFailure_ContinuesOtherFolder(t *testing.T) {
	tmp := t.TempDir()

	folderA := filepath.Join(tmp, "AlbumA")
	folderB := filepath.Join(tmp, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	fileA := filepath.Join(folderA, "a.wav")
	fileB := filepath.Join(folderB, "b.wav")
	if err := os.WriteFile(fileA, []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("bbbb"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID:   "plan-rooted-consecutive-convert-fail",
		RootPath: filepath.ToSlash(tmp),
		Items: []PlanItem{
			{Type: ItemTypeConvert, SourcePath: fileA, TargetPath: filepath.Join(folderA, "a.m4a"), PreconditionPath: fileA, PreconditionSize: 4},
			{Type: ItemTypeConvert, SourcePath: fileB, TargetPath: filepath.Join(folderB, "b.m4a"), PreconditionPath: fileB, PreconditionSize: 4},
		},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	mockRunner := newMockBatchBarrierRunner()
	mockRunner.convertFailures[fileA] = errors.New("forced folder A convert failure")
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)
	if err == nil {
		t.Fatal("expected rooted consecutive convert failure to return error")
	}
	if result == nil {
		t.Fatal("expected non-nil result on failure")
	}
	if result.Status == "completed" {
		t.Fatalf("expected non-completed status on rooted consecutive failure, got %q", result.Status)
	}

	if _, statErr := os.Stat(filepath.Join(folderB, "b.m4a")); os.IsNotExist(statErr) {
		t.Fatalf("expected folder B convert to still execute and create target, statErr=%v", statErr)
	}
	if _, statErr := os.Stat(fileB); !os.IsNotExist(statErr) {
		t.Fatalf("expected folder B source to be deleted after successful convert, statErr=%v", statErr)
	}
}

func TestExecutePlan_RootedExplicitDeleteSkippedAfterFlushMarksFolderFailed(t *testing.T) {
	tmp := t.TempDir()

	folderA := filepath.Join(tmp, "AlbumA")
	folderB := filepath.Join(tmp, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	aConvert := filepath.Join(folderA, "a.wav")
	aDelete := filepath.Join(folderA, "a-delete.wav")
	bConvert := filepath.Join(folderB, "b.wav")
	if err := os.WriteFile(aConvert, []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aDelete, []byte("deleteme"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bConvert, []byte("bbbb"), 0644); err != nil {
		t.Fatal(err)
	}
	deleteInfo, err := os.Stat(aDelete)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID:   "plan-rooted-explicit-delete-recheck",
		RootPath: filepath.ToSlash(tmp),
		Items: []PlanItem{
			{Type: ItemTypeConvert, SourcePath: aConvert, TargetPath: filepath.Join(folderA, "a.m4a"), PreconditionPath: aConvert, PreconditionSize: 4},
			{Type: ItemTypeConvert, SourcePath: bConvert, TargetPath: filepath.Join(folderB, "b.m4a"), PreconditionPath: bConvert, PreconditionSize: 4},
			{Type: ItemTypeDelete, SourcePath: aDelete, PreconditionPath: aDelete, PreconditionSize: deleteInfo.Size(), PreconditionMtime: deleteInfo.ModTime().Unix()},
		},
	}

	svc := NewExecuteService(nil, ToolsConfig{Encoder: "qaac", QAACPath: getValidExecutablePath(t)})
	runner := newMockBatchBarrierRunner()
	runner.convertFailures[aConvert] = errors.New("forced folder A convert failure")
	svc.SetRunner(runner)

	result, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected rooted execution to return error")
	}
	if result == nil || result.Status != "failed" {
		t.Fatalf("expected failed result, got %+v", result)
	}

	if _, statErr := os.Stat(aDelete); statErr != nil {
		t.Fatalf("expected explicit delete in failed folder to be skipped and source preserved, statErr=%v", statErr)
	}

	deleteCalls := runner.getDeleteCalls()
	for _, call := range deleteCalls {
		if call == aDelete {
			t.Fatalf("explicit delete in failed folder should be skipped after flush, deleteCalls=%v", deleteCalls)
		}
	}
}

func TestExecutePlan_RootedMixedResultBatch_EmitsCompletedForSuccessfulFolderOnly(t *testing.T) {
	tmp := t.TempDir()

	folderA := filepath.Join(tmp, "AlbumA")
	folderB := filepath.Join(tmp, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	a := filepath.Join(folderA, "a.wav")
	b := filepath.Join(folderB, "b.wav")
	if err := os.WriteFile(a, []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("bbbb"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID:   "plan-rooted-mixed-batch-folder-completed",
		RootPath: filepath.ToSlash(tmp),
		Items: []PlanItem{
			{Type: ItemTypeConvert, SourcePath: a, TargetPath: filepath.Join(folderA, "a.m4a"), PreconditionPath: a, PreconditionSize: 4},
			{Type: ItemTypeConvert, SourcePath: b, TargetPath: filepath.Join(folderB, "b.m4a"), PreconditionPath: b, PreconditionSize: 4},
		},
	}

	svc := NewExecuteService(nil, ToolsConfig{Encoder: "qaac", QAACPath: getValidExecutablePath(t)})
	runner := newMockBatchBarrierRunner()
	runner.convertFailures[a] = errors.New("forced folder A convert failure")
	svc.SetRunner(runner)
	handler := newMockEventHandler()
	svc.SetEventHandler(handler)

	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected rooted mixed-result execution to fail")
	}

	completed := handler.getFolderCompletedCalls()
	folderAPath := getFolderForItem(plan.RootPath, plan.Items[0])
	folderBPath := getFolderForItem(plan.RootPath, plan.Items[1])

	hasA := false
	hasB := false
	for _, folder := range completed {
		if folder == folderAPath {
			hasA = true
		}
		if folder == folderBPath {
			hasB = true
		}
	}
	if hasA {
		t.Fatalf("did not expect OnFolderCompleted for failed folder A, calls=%v", completed)
	}
	if !hasB {
		t.Fatalf("expected OnFolderCompleted for successful folder B, calls=%v", completed)
	}
}

// TestExecutePlan_RootedDeleteBarrierFailure_ContinuesOtherFolder validates that
// in rooted mode (RootPath != ""), when a converted-source delete fails for one
// folder during the delete-barrier phase, other folders continue to be processed.
// This is the per-folder fail-fast policy for delete operations.
// The test uses ItemTypeConvert items so the delete is a converted-source delete
// barrier failure (after convert success), not an explicit delete-item failure.
func TestExecutePlan_RootedDeleteBarrierFailure_ContinuesOtherFolder(t *testing.T) {
	tmp := t.TempDir()

	folderA := filepath.Join(tmp, "AlbumA")
	folderB := filepath.Join(tmp, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	// Create source files in both folders
	srcA := filepath.Join(folderA, "a.wav")
	srcB := filepath.Join(folderB, "b.wav")
	if err := os.WriteFile(srcA, []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcB, []byte("bbbb"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use ItemTypeConvert items - source deletes are delete-barrier failures after convert success
	plan := &Plan{
		PlanID:   "plan-rooted-delete-barrier-fail-continue",
		RootPath: filepath.ToSlash(tmp),
		Items: []PlanItem{
			{Type: ItemTypeConvert, SourcePath: srcA, TargetPath: filepath.Join(folderA, "a.m4a"), PreconditionPath: srcA, PreconditionSize: 4},
			{Type: ItemTypeConvert, SourcePath: srcB, TargetPath: filepath.Join(folderB, "b.m4a"), PreconditionPath: srcB, PreconditionSize: 4},
		},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	runner := newMockBatchBarrierRunner()
	// Make delete fail for srcA (AlbumA) - this is a converted-source delete barrier failure
	runner.deleteFailures[srcA] = errors.New("simulated delete barrier failure for AlbumA")
	svc.SetRunner(runner)

	mockHandler := newMockEventHandler()
	svc.SetEventHandler(mockHandler)

	result, err := svc.ExecutePlan(plan)

	// In rooted mode, delete barrier failure should NOT stop entire execution
	// Result should indicate failure overall, but folder B should be processed
	if err == nil {
		t.Fatal("expected error due to delete barrier failure")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != "failed" {
		t.Fatalf("expected failed status in rooted mode with delete barrier failure, got %q", result.Status)
	}

	// CRITICAL ASSERTION: Folder A source should remain (delete barrier failed)
	if _, statErr := os.Stat(srcA); os.IsNotExist(statErr) {
		t.Error("FAIL: srcA should remain when delete barrier fails, but it was deleted")
	}

	// CRITICAL ASSERTION: srcB should be deleted (folder B continued processing after folder A delete barrier failure)
	if _, statErr := os.Stat(srcB); !os.IsNotExist(statErr) {
		t.Fatalf("FAIL: srcB should be deleted (folder B continued after folder A delete barrier failure), but statErr=%v", statErr)
	}

	// Verify OnDeleteFailed was called for item index 0 (srcA)
	deleteFailedCalls := mockHandler.getDeleteFailedCalls()
	if len(deleteFailedCalls) != 1 || deleteFailedCalls[0] != 0 {
		t.Errorf("OnDeleteFailed should be called exactly once for item index 0, got: %v", deleteFailedCalls)
	}

	t.Logf("VERIFIED: Rooted delete-barrier failure continued to other folder - srcA preserved, srcB deleted")
}

// TestExecutePlan_RootedConcurrentPrecheck_PerFolderFailFast_ContinuesOtherFolders
// verifies concurrent precheck mode still preserves rooted per-folder fail-fast:
// folder A precheck failure stops folder A only, while folder B continues.
func TestExecutePlan_RootedConcurrentPrecheck_PerFolderFailFast_ContinuesOtherFolders(t *testing.T) {
	tmp := t.TempDir()

	folderA := filepath.Join(tmp, "AlbumA")
	folderB := filepath.Join(tmp, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	fileA1 := filepath.Join(folderA, "a1.wav")
	fileA2 := filepath.Join(folderA, "a2.wav")
	fileB1 := filepath.Join(folderB, "b1.wav")
	if err := os.WriteFile(fileA1, []byte("a1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileA2, []byte("a2"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB1, []byte("b1"), 0644); err != nil {
		t.Fatal(err)
	}

	infoA1, err := os.Stat(fileA1)
	if err != nil {
		t.Fatal(err)
	}
	infoA2, err := os.Stat(fileA2)
	if err != nil {
		t.Fatal(err)
	}
	infoB1, err := os.Stat(fileB1)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID:   "plan-rooted-concurrent-precheck-per-folder",
		RootPath: filepath.ToSlash(tmp),
		Items: []PlanItem{
			// Folder A: first item has stale size precondition -> folder A fail-fast
			{Type: ItemTypeDelete, SourcePath: fileA1, PreconditionPath: fileA1, PreconditionSize: infoA1.Size() + 1, PreconditionMtime: infoA1.ModTime().Unix()},
			// Folder A second item should be skipped due to folder-level fail-fast
			{Type: ItemTypeDelete, SourcePath: fileA2, PreconditionPath: fileA2, PreconditionSize: infoA2.Size(), PreconditionMtime: infoA2.ModTime().Unix()},
			// Folder B should still continue and execute
			{Type: ItemTypeDelete, SourcePath: fileB1, PreconditionPath: fileB1, PreconditionSize: infoB1.Size(), PreconditionMtime: infoB1.ModTime().Unix()},
		},
	}

	svc := NewExecuteService(nil, ToolsConfig{})
	svc.SetExecuteConfig(ExecuteConfig{MaxIOWorkers: 4, PrecheckConcurrentStat: true})
	svc.SetEventHandler(newMockEventHandler())

	_, _ = svc.ExecutePlan(plan)

	if _, err := os.Stat(fileA1); os.IsNotExist(err) {
		t.Fatal("fileA1 should remain (folder A precheck failure)")
	}
	if _, err := os.Stat(fileA2); os.IsNotExist(err) {
		t.Fatal("fileA2 should remain (folder A fail-fast skip)")
	}
	if _, err := os.Stat(fileB1); !os.IsNotExist(err) {
		t.Fatal("fileB1 should be deleted (folder B should continue)")
	}
}
