package execute

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExecutePlan_DeleteOperation(t *testing.T) {
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-001",
		Items: []PlanItem{{
			Type:              ItemTypeDelete,
			SourcePath:        testFile,
			PreconditionPath:  testFile,
			PreconditionSize:  info.Size(),
			PreconditionMtime: info.ModTime().Unix(),
		}},
	}

	svc := NewExecuteService(nil, ToolsConfig{})
	result, execErr := svc.ExecutePlan(plan)
	if execErr != nil {
		t.Fatalf("ExecutePlan failed: %v", execErr)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed, got %s", result.Status)
	}
	if _, err := os.Stat(testFile); err == nil {
		t.Fatal("expected file to be deleted")
	}
}

// TestExecutePlan_MixedOrdering_ConvertFailStopsDelete validates that
// when a convert batch fails, subsequent delete operations are NOT executed.
func TestExecutePlan_MixedOrdering_ConvertFailStopsDelete(t *testing.T) {
	tmp := t.TempDir()

	// Create files for convert
	convertFile := filepath.Join(tmp, "convert.wav")
	if err := os.WriteFile(convertFile, []byte("convert"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create file for delete
	deleteFile := filepath.Join(tmp, "delete.wav")
	if err := os.WriteFile(deleteFile, []byte("delete"), 0644); err != nil {
		t.Fatal(err)
	}

	// Plan: convert first, then delete
	// If convert fails, delete should NOT execute
	plan := &Plan{
		PlanID: "plan-mixed-order",
		Items: []PlanItem{
			{
				Type:             ItemTypeConvert,
				SourcePath:       convertFile,
				TargetPath:       filepath.Join(tmp, "convert.m4a"),
				PreconditionPath: convertFile,
				PreconditionSize: 7,
			},
			{
				Type:             ItemTypeDelete,
				SourcePath:       deleteFile,
				PreconditionPath: deleteFile,
				PreconditionSize: 6,
			},
		},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	// Inject mock runner that fails conversion
	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = 0 // fail on first convert
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)

	// Should fail
	if err == nil {
		t.Fatal("expected execution failure due to convert failure")
	}
	_ = result

	// Critical assertion: delete should NOT have been executed
	// (ordering semantics: if convert batch fails, subsequent delete is skipped)
	if _, statErr := os.Stat(deleteFile); os.IsNotExist(statErr) {
		t.Error("delete file was deleted - ordering semantics violated: convert failure should stop subsequent delete")
	}

	// Verify: convert source preserved
	if _, statErr := os.Stat(convertFile); os.IsNotExist(statErr) {
		t.Error("convert source file should be preserved on failure")
	}
}

// TestExecutePlan_MixedOrdering_ConvertSuccessThenDelete validates ordering
// when convert succeeds, subsequent delete is executed.
func TestExecutePlan_MixedOrdering_ConvertSuccessThenDelete(t *testing.T) {
	tmp := t.TempDir()

	convertFile := filepath.Join(tmp, "convert.wav")
	if err := os.WriteFile(convertFile, []byte("convert"), 0644); err != nil {
		t.Fatal(err)
	}

	deleteFile := filepath.Join(tmp, "delete.wav")
	if err := os.WriteFile(deleteFile, []byte("delete"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-mixed-success",
		Items: []PlanItem{
			{
				Type:             ItemTypeConvert,
				SourcePath:       convertFile,
				TargetPath:       filepath.Join(tmp, "convert.m4a"),
				PreconditionPath: convertFile,
				PreconditionSize: 7,
			},
			{
				Type:             ItemTypeDelete,
				SourcePath:       deleteFile,
				PreconditionPath: deleteFile,
				PreconditionSize: 6,
			},
		},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	// Inject mock runner that succeeds
	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = -1
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)

	// Should succeed
	if err != nil {
		t.Fatalf("expected execution success, got: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed, got: %s", result.Status)
	}

	// Verify: convert source deleted (committed)
	if _, statErr := os.Stat(convertFile); !os.IsNotExist(statErr) {
		t.Error("convert source should be deleted after successful commit")
	}

	// Verify: delete file deleted
	if _, statErr := os.Stat(deleteFile); !os.IsNotExist(statErr) {
		t.Error("delete file should be deleted")
	}

	// Verify: destination exists
	dst := filepath.Join(tmp, "convert.m4a")
	if _, statErr := os.Stat(dst); os.IsNotExist(statErr) {
		t.Error("destination file should exist")
	}
}

func TestExecutePlan_BatchFailure_SkipsConvertedSourceDelete(t *testing.T) {
	tmp := t.TempDir()

	srcA := filepath.Join(tmp, "a.wav")
	srcB := filepath.Join(tmp, "b.wav")
	if err := os.WriteFile(srcA, []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcB, []byte("bbbb"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{PlanID: "plan-batch-fail-barrier", Items: []PlanItem{
		{Type: ItemTypeConvert, SourcePath: srcA, TargetPath: filepath.Join(tmp, "a.m4a"), PreconditionPath: srcA, PreconditionSize: 4},
		{Type: ItemTypeConvert, SourcePath: srcB, TargetPath: filepath.Join(tmp, "b.m4a"), PreconditionPath: srcB, PreconditionSize: 4},
	}}

	svc := NewExecuteService(nil, ToolsConfig{Encoder: "qaac", QAACPath: getValidExecutablePath(t)})
	runner := newMockBatchBarrierRunner()
	runner.convertFailures[srcB] = errors.New("forced convert failure")
	svc.SetRunner(runner)

	_, err := svc.ExecutePlan(plan)
	if err == nil {
		t.Fatal("expected batch convert failure")
	}

	if _, statErr := os.Stat(srcA); os.IsNotExist(statErr) {
		t.Fatal("converted source from successful item should not be deleted when batch fails")
	}
	if _, statErr := os.Stat(srcB); os.IsNotExist(statErr) {
		t.Fatal("failed source should remain")
	}
	if len(runner.getDeleteCalls()) != 0 {
		t.Fatalf("expected no source deletes on failed convert batch, got %v", runner.getDeleteCalls())
	}
}

func TestExecutePlan_BatchSuccess_ConvertedSourcesDeletedBeforeExplicitDelete(t *testing.T) {
	tmp := t.TempDir()

	srcA := filepath.Join(tmp, "a.wav")
	srcB := filepath.Join(tmp, "b.wav")
	explicitDelete := filepath.Join(tmp, "delete.wav")
	if err := os.WriteFile(srcA, []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcB, []byte("bbbb"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(explicitDelete, []byte("cccc"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{PlanID: "plan-batch-success-barrier", Items: []PlanItem{
		{Type: ItemTypeConvert, SourcePath: srcA, TargetPath: filepath.Join(tmp, "a.m4a"), PreconditionPath: srcA, PreconditionSize: 4},
		{Type: ItemTypeConvert, SourcePath: srcB, TargetPath: filepath.Join(tmp, "b.m4a"), PreconditionPath: srcB, PreconditionSize: 4},
		{Type: ItemTypeDelete, SourcePath: explicitDelete, PreconditionPath: explicitDelete, PreconditionSize: 4},
	}}

	svc := NewExecuteService(nil, ToolsConfig{Encoder: "qaac", QAACPath: getValidExecutablePath(t)})
	runner := newMockBatchBarrierRunner()
	svc.SetRunner(runner)

	result, err := svc.ExecutePlan(plan)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if result == nil || result.Status != "completed" {
		t.Fatalf("expected completed result, got %+v", result)
	}

	deleteCalls := runner.getDeleteCalls()
	if len(deleteCalls) != 3 {
		t.Fatalf("expected 3 delete calls (2 converted sources + 1 explicit), got %v", deleteCalls)
	}

	positions := map[string]int{}
	for i, p := range deleteCalls {
		if _, seen := positions[p]; !seen {
			positions[p] = i
		}
	}

	idxExplicit, okExplicit := positions[explicitDelete]
	idxA, okA := positions[srcA]
	idxB, okB := positions[srcB]
	if !okExplicit || !okA || !okB {
		t.Fatalf("missing expected delete call(s), got %v", deleteCalls)
	}
	if idxExplicit < idxA || idxExplicit < idxB {
		t.Fatalf("explicit delete executed before converted-source barrier deletes: %v", deleteCalls)
	}
}

// TestExecutePlan_MissingSourceRoutesToStage1Callback verifies that source-open
// failures during convert execution are reported as stage1 copy failures
// (compatibility behavior), not stage2 encode failures.
func TestExecutePlan_MissingSourceRoutesToStage1Callback(t *testing.T) {
	tmp := t.TempDir()

	preconditionFile := filepath.Join(tmp, "precondition.wav")
	if err := os.WriteFile(preconditionFile, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	missingSource := filepath.Join(tmp, "missing-source.wav")

	plan := &Plan{PlanID: "plan-missing-source-stage1", Items: []PlanItem{{
		Type:             ItemTypeConvert,
		SourcePath:       missingSource,
		TargetPath:       filepath.Join(tmp, "out.m4a"),
		PreconditionPath: preconditionFile,
		PreconditionSize: int64(len("audio")),
	}}}

	svc := NewExecuteService(nil, ToolsConfig{Encoder: "qaac", QAACPath: getValidExecutablePath(t)})
	h := newMockEventHandler()
	svc.SetEventHandler(h)

	_, err := svc.ExecutePlan(plan)
	if err == nil {
		t.Fatal("expected execution failure for missing source")
	}

	stage1 := h.getStage1CopyFailedCalls()
	if len(stage1) != 1 || stage1[0] != 0 {
		t.Fatalf("expected OnStage1CopyFailed once for item 0, got %v", stage1)
	}
	if stage2 := h.getStage2EncodeFailedCalls(); len(stage2) != 0 {
		t.Fatalf("expected no OnStage2EncodeFailed callbacks for source-open failure, got %v", stage2)
	}
}

// TestExecutePlan_DeleteBarrierFailure_RoutesToOnDeleteFailed verifies that
// when a converted-source delete fails during the batch delete barrier phase,
// the failure routes through OnDeleteFailed callback (NOT OnStage3CommitFailed).
// This is a critical callback routing invariant.
func TestExecutePlan_DeleteBarrierFailure_RoutesToOnDeleteFailed(t *testing.T) {
	tmp := t.TempDir()

	srcA := filepath.Join(tmp, "a.wav")
	srcB := filepath.Join(tmp, "b.wav")
	if err := os.WriteFile(srcA, []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcB, []byte("bbbb"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{PlanID: "plan-delete-barrier-fail", Items: []PlanItem{
		{Type: ItemTypeConvert, SourcePath: srcA, TargetPath: filepath.Join(tmp, "a.m4a"), PreconditionPath: srcA, PreconditionSize: 4},
		{Type: ItemTypeConvert, SourcePath: srcB, TargetPath: filepath.Join(tmp, "b.m4a"), PreconditionPath: srcB, PreconditionSize: 4},
	}}

	svc := NewExecuteService(nil, ToolsConfig{Encoder: "qaac", QAACPath: getValidExecutablePath(t)})
	runner := newMockBatchBarrierRunner()
	// Make delete fail for source A - this tests delete-barrier failure routing
	runner.deleteFailures[srcA] = errors.New("simulated delete failure for source A")
	svc.SetRunner(runner)

	mockHandler := newMockEventHandler()
	svc.SetEventHandler(mockHandler)

	result, err := svc.ExecutePlan(plan)

	// Should fail due to delete barrier failure
	if err == nil {
		t.Fatal("expected execution failure due to delete barrier failure")
	}

	// CRITICAL ASSERTION: OnDeleteFailed should be called (not OnStage3CommitFailed)
	deleteFailedCalls := mockHandler.getDeleteFailedCalls()
	stage3CommitFailedCalls := mockHandler.getStage3CommitFailedCalls()
	stage2EncodeFailedCalls := mockHandler.getStage2EncodeFailedCalls()

	if len(deleteFailedCalls) == 0 {
		t.Error("FAIL: OnDeleteFailed was NOT called - delete-barrier failure did not route correctly")
	}
	if len(stage3CommitFailedCalls) > 0 {
		t.Errorf("FAIL: OnStage3CommitFailed was called (count=%d) - should NOT be called for delete failures", len(stage3CommitFailedCalls))
	}
	if len(stage2EncodeFailedCalls) > 0 {
		t.Errorf("FAIL: OnStage2EncodeFailed was called (count=%d) - should NOT be called for delete failures", len(stage2EncodeFailedCalls))
	}

	// Verify OnDeleteFailed was called for item index 0 (srcA)
	if len(deleteFailedCalls) != 1 || deleteFailedCalls[0] != 0 {
		t.Errorf("OnDeleteFailed should be called for item index 0, got: %v", deleteFailedCalls)
	}

	// Verify result status indicates failure
	if result == nil || result.Status == "completed" {
		t.Errorf("expected failed result, got status=%v", result)
	}

	// Verify source A was NOT deleted (delete failed, so preserve original)
	if _, statErr := os.Stat(srcA); os.IsNotExist(statErr) {
		t.Errorf("source A should remain when delete fails, but it was deleted")
	}

	t.Logf("VERIFIED: delete-barrier failure routes to OnDeleteFailed (calls=%v), NOT OnStage3CommitFailed (calls=%v)",
		deleteFailedCalls, stage3CommitFailedCalls)
}

// TestExecutePlan_NonRootedDeleteBarrierFailure_StopsExecution validates that
// in non-rooted mode (RootPath == ""), when a converted-source delete fails
// during the delete-barrier phase, the entire execution stops immediately.
// This is the fail-fast policy for non-rooted mode.
// The test uses ItemTypeConvert items so the delete is a converted-source delete
// barrier failure (after convert success), not an explicit delete-item failure.
func TestExecutePlan_NonRootedDeleteBarrierFailure_StopsExecution(t *testing.T) {
	tmp := t.TempDir()

	// Create two source files without any folder structure (non-rooted mode)
	srcA := filepath.Join(tmp, "a.wav")
	srcB := filepath.Join(tmp, "b.wav")
	if err := os.WriteFile(srcA, []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcB, []byte("bbbb"), 0644); err != nil {
		t.Fatal(err)
	}

	// Use ItemTypeConvert items - source deletes are delete-barrier failures after convert success
	// Note: RootPath is empty - this is non-rooted mode
	plan := &Plan{
		PlanID:   "plan-nonrooted-delete-barrier-fail-stop",
		RootPath: "", // Non-rooted mode
		Items: []PlanItem{
			{Type: ItemTypeConvert, SourcePath: srcA, TargetPath: filepath.Join(tmp, "a.m4a"), PreconditionPath: srcA, PreconditionSize: 4},
			{Type: ItemTypeConvert, SourcePath: srcB, TargetPath: filepath.Join(tmp, "b.m4a"), PreconditionPath: srcB, PreconditionSize: 4},
		},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	runner := newMockBatchBarrierRunner()
	// Make delete fail for srcA - this is a converted-source delete barrier failure
	runner.deleteFailures[srcA] = errors.New("simulated delete barrier failure for srcA")
	svc.SetRunner(runner)

	mockHandler := newMockEventHandler()
	svc.SetEventHandler(mockHandler)

	result, err := svc.ExecutePlan(plan)

	// In non-rooted mode, delete barrier failure should stop execution immediately
	if err == nil {
		t.Fatal("expected error due to delete barrier failure in non-rooted mode")
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != "failed" {
		t.Fatalf("expected failed status in non-rooted mode with delete barrier failure, got %q", result.Status)
	}

	// CRITICAL ASSERTION: srcA should remain (delete barrier failed)
	if _, statErr := os.Stat(srcA); os.IsNotExist(statErr) {
		t.Error("FAIL: srcA should remain when delete barrier fails, but it was deleted")
	}

	// CRITICAL ASSERTION: srcB should also remain (execution stopped before processing it)
	if _, statErr := os.Stat(srcB); os.IsNotExist(statErr) {
		t.Error("FAIL: srcB should remain (non-rooted mode should stop execution after first delete barrier failure), but it was deleted")
	}

	// Verify OnDeleteFailed was called for item index 0
	deleteFailedCalls := mockHandler.getDeleteFailedCalls()
	if len(deleteFailedCalls) != 1 || deleteFailedCalls[0] != 0 {
		t.Errorf("OnDeleteFailed should be called exactly once for item index 0, got: %v", deleteFailedCalls)
	}

	t.Logf("VERIFIED: Non-rooted delete barrier failure stopped execution - both sources preserved")
}

// TestConvertFailure_DoesNotDeleteSourceFile validates that conversion failure does NOT delete source file
func TestConvertFailure_DoesNotDeleteSourceFile(t *testing.T) {
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-convert-fail",
		Items: []PlanItem{{
			Type:                   ItemTypeConvert,
			SourcePath:             testFile,
			TargetPath:             filepath.Join(tmp, "song.m4a"),
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 0,
		}},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = 0
	svc.SetRunner(mockRunner)

	_, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected conversion error, got nil")
	}

	// Assert source file still exists (NOT deleted)
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Error("expected source file to NOT be deleted after conversion failure")
	}
}
