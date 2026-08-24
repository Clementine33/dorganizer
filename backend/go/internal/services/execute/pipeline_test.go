package execute

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	errdomain "github.com/onsei/organizer/backend/internal/errors"
)

// getValidExecutablePath returns a valid executable path for testing
// This bypasses validateToolsConfig by using an existing executable
func getValidExecutablePath(t *testing.T) string {
	t.Helper()
	// Use the test executable itself as a valid path
	execPath, err := os.Executable()
	if err != nil {
		// Fallback to a common Windows executable
		execPath = `C:\Windows\System32\cmd.exe`
	}
	return execPath
}

// mockPipelineRunner is a mock tool runner for testing pipeline behavior
type mockPipelineRunner struct {
	mu sync.Mutex

	// Control behavior
	failOnConvertIndex int // -1 = never fail, 0+ = fail on Nth convert call
	convertDelay       time.Duration

	// Per-path delete failures (for testing delete barrier failures)
	deleteFailures map[string]error

	// Tracking
	convertCalls []string // tracks source files for each convert call
	deleteCalls  []string // tracks files deleted
	convertCount int32
	deleteCount  int32
}

func newMockPipelineRunner() *mockPipelineRunner {
	return &mockPipelineRunner{
		failOnConvertIndex: -1,
		deleteFailures:     map[string]error{},
	}
}

func (m *mockPipelineRunner) Convert(src, dst string) error {
	m.mu.Lock()
	m.convertCalls = append(m.convertCalls, src)
	idx := len(m.convertCalls) - 1
	m.mu.Unlock()

	atomic.AddInt32(&m.convertCount, 1)

	if m.convertDelay > 0 {
		time.Sleep(m.convertDelay)
	}

	m.mu.Lock()
	failIdx := m.failOnConvertIndex
	m.mu.Unlock()

	if failIdx >= 0 && idx == failIdx {
		return &ToolError{
			Code:    errdomain.TOOL_NOT_FOUND,
			Message: "mock conversion failure",
			Err:     errors.New("mock conversion failure"),
		}
	}

	// Simulate successful conversion by creating the dst file
	return os.WriteFile(dst, []byte("mock converted"), 0644)
}

func (m *mockPipelineRunner) Delete(path string, soft bool) error {
	m.mu.Lock()
	m.deleteCalls = append(m.deleteCalls, path)
	// Check for per-path delete failure
	if err, ok := m.deleteFailures[path]; ok {
		m.mu.Unlock()
		atomic.AddInt32(&m.deleteCount, 1)
		return err
	}
	m.mu.Unlock()

	atomic.AddInt32(&m.deleteCount, 1)
	return os.Remove(path)
}

func (m *mockPipelineRunner) getConvertCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.convertCalls))
	copy(result, m.convertCalls)
	return result
}

func (m *mockPipelineRunner) getDeleteCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.deleteCalls))
	copy(result, m.deleteCalls)
	return result
}

// TestExecutePlan_FailFast_StopsOnFirstError validates that when a conversion fails,
// the pipeline stops admitting new work after the first error is recorded.
//
// Worker-claim model semantics:
// - Workers claim next index atomically before processing
// - Once stop flag is set, no NEW index claims are allowed
// - In-flight already-claimed jobs may finish (that's acceptable)
//
// This means items claimed BEFORE the first failure may still complete,
// but items claimed AFTER will not be processed.
func TestExecutePlan_FailFast_StopsOnFirstError(t *testing.T) {
	tmp := t.TempDir()

	// Create 8 test files to have enough items
	var files []string
	for i := 0; i < 8; i++ {
		file := filepath.Join(tmp, fmt.Sprintf("song%d.wav", i))
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}

	// Create plan with 8 convert items
	var items []PlanItem
	for i, file := range files {
		items = append(items, PlanItem{
			Type:             ItemTypeConvert,
			SourcePath:       file,
			TargetPath:       filepath.Join(tmp, fmt.Sprintf("song%d.m4a", i)),
			PreconditionPath: file,
			PreconditionSize: 4,
		})
	}
	plan := &Plan{PlanID: "plan-failfast", Items: items}

	// Create service with valid tools config
	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	// Inject mock runner that fails on first convert
	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = 0 // first convert call fails
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)

	// Should fail
	if err == nil {
		t.Fatal("expected execution failure")
	}
	if result == nil {
		t.Fatal("expected result even on failure")
	}

	convertCalls := mockRunner.getConvertCalls()

	// ROBUST ASSERTION: With worker-claim model, SOME tail items should remain unprocessed
	// The exact count depends on machine parallelism (stage2Workers = min(4, max(1, NumCPU/2)))
	// We verify that not ALL items were processed - this is the core fail-fast invariant.
	if len(convertCalls) >= len(files) {
		t.Errorf("fail-fast should stop processing: all %d items were processed", len(convertCalls))
	}

	// ROBUST ASSERTION: At least one source file in the second half of the plan should remain
	// This verifies that fail-fast prevented processing of items claimed after the stop.
	// Using midpoint ensures we catch tail items regardless of parallelism (up to 4 workers).
	midpoint := len(files) / 2
	unprocessedTail := false
	for i := midpoint; i < len(files); i++ {
		found := false
		for _, call := range convertCalls {
			if call == files[i] {
				found = true
				break
			}
		}
		if !found {
			unprocessedTail = true
			break
		}
	}
	if !unprocessedTail {
		t.Errorf("fail-fast should leave some tail items unprocessed (midpoint=%d, total=%d, processed=%d)",
			midpoint, len(files), len(convertCalls))
	}

	// Verify: all source files should be preserved (not deleted) on failure
	for _, file := range files {
		if _, statErr := os.Stat(file); os.IsNotExist(statErr) {
			t.Errorf("source file %s was deleted - pipeline should preserve origSrc on failure", file)
		}
	}

	// Verify: for files never processed by Convert, destination must not exist.
	processed := make(map[string]struct{}, len(convertCalls))
	for _, call := range convertCalls {
		processed[call] = struct{}{}
	}
	for i, src := range files {
		if _, ok := processed[src]; ok {
			continue
		}
		dst := filepath.Join(tmp, fmt.Sprintf("song%d.m4a", i))
		if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
			t.Errorf("destination %s exists for unprocessed source %s", dst, src)
		}
	}
}

// TestExecutePlan_FailFast_StopsFeedingTasks validates that after first error,
// the producer stops feeding new tasks to the pipeline.
// Uses many files to observe "stop feeding" behavior with minimal in-flight.
func TestExecutePlan_FailFast_StopsFeedingTasks(t *testing.T) {
	tmp := t.TempDir()

	// Create many test files (20) to observe fail-fast behavior clearly
	const numFiles = 20
	var files []string
	for i := 0; i < numFiles; i++ {
		file := filepath.Join(tmp, fmt.Sprintf("track%d.wav", i))
		if err := os.WriteFile(file, []byte("audio"), 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}

	// Create plan with many convert items
	var items []PlanItem
	for i, file := range files {
		items = append(items, PlanItem{
			Type:             ItemTypeConvert,
			SourcePath:       file,
			TargetPath:       filepath.Join(tmp, fmt.Sprintf("track%d.m4a", i)),
			PreconditionPath: file,
			PreconditionSize: 5,
		})
	}
	plan := &Plan{PlanID: "plan-failfast-many", Items: items}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	// Inject mock runner that fails on 3rd convert (allows some to succeed first)
	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = 2 // fail on 3rd convert (index 2)
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)

	// Should fail
	if err == nil {
		t.Fatal("expected execution failure")
	}
	if result == nil {
		t.Fatal("expected result even on failure")
	}

	convertCalls := mockRunner.getConvertCalls()

	// ROBUST ASSERTION: Plan fails fast - not all items are processed
	// The exact count depends on NumCPU (which determines stage2Workers).
	// Key invariant: fail-fast prevents processing of ALL items.
	if len(convertCalls) >= numFiles {
		t.Errorf("fail-fast should stop feeding: all %d items were processed", len(convertCalls))
	}

	// ROBUST ASSERTION: Verify SOME tail items remain unprocessed
	// We use a threshold based on midpoint to avoid assuming specific parallelism.
	// This ensures fail-fast actually stopped admission at some point.
	midpoint := numFiles / 2
	unprocessedTail := false
	for i := midpoint; i < numFiles; i++ {
		found := false
		for _, call := range convertCalls {
			if call == files[i] {
				found = true
				break
			}
		}
		if !found {
			unprocessedTail = true
			break
		}
	}
	if !unprocessedTail {
		t.Errorf("fail-fast should leave some tail items unprocessed (midpoint=%d, total=%d, processed=%d)",
			midpoint, numFiles, len(convertCalls))
	}

	// Verify: files never processed by Convert remain present on disk.
	processed := make(map[string]struct{}, len(convertCalls))
	for _, call := range convertCalls {
		processed[call] = struct{}{}
	}
	for _, file := range files {
		if _, ok := processed[file]; ok {
			continue
		}
		if _, statErr := os.Stat(file); os.IsNotExist(statErr) {
			t.Errorf("unprocessed source file %s was deleted", file)
		}
	}

	// Verify scratch cleanup
	sessionID := result.SessionID
	scratchPath := svc.scratchRoot
	inPath := filepath.Join(scratchPath, "in", sessionID)
	outPath := filepath.Join(scratchPath, "out", sessionID)

	if _, statErr := os.Stat(inPath); !os.IsNotExist(statErr) {
		t.Errorf("scratch/in should be cleaned up: %s", inPath)
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Errorf("scratch/out should be cleaned up: %s", outPath)
	}
}

// TestExecutePlan_Stage3_OriginalSourcePreservedOnConvertFailure validates that
// when conversion fails, the original source file is NOT deleted.
func TestExecutePlan_Stage3_OriginalSourcePreservedOnConvertFailure(t *testing.T) {
	tmp := t.TempDir()

	srcFile := filepath.Join(tmp, "original.wav")
	if err := os.WriteFile(srcFile, []byte("original content"), 0644); err != nil {
		t.Fatal(err)
	}

	dstDir := filepath.Join(tmp, "output")
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-stage3-fail",
		Items: []PlanItem{{
			Type:             ItemTypeConvert,
			SourcePath:       srcFile,
			TargetPath:       filepath.Join(dstDir, "output.m4a"),
			PreconditionPath: srcFile,
			PreconditionSize: 16,
		}},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	// Inject mock runner that fails conversion
	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = 0
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)

	// Should fail
	if err == nil {
		t.Fatal("expected execution failure")
	}
	_ = result

	// Critical assertion: original source must NOT be deleted when conversion fails
	if _, statErr := os.Stat(srcFile); os.IsNotExist(statErr) {
		t.Error("FAIL: original source must NOT be deleted on conversion failure")
	}

	// Verify: no destination file created
	dst := filepath.Join(dstDir, "output.m4a")
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Error("destination file should not exist after conversion failure")
	}
}

// TestExecutePlan_Stage3_DeletesOriginalOnSuccess validates that
// when conversion succeeds and commits, the original source IS deleted.
func TestExecutePlan_Stage3_DeletesOriginalOnSuccess(t *testing.T) {
	tmp := t.TempDir()

	srcFile := filepath.Join(tmp, "original.wav")
	if err := os.WriteFile(srcFile, []byte("original content"), 0644); err != nil {
		t.Fatal(err)
	}

	dstDir := filepath.Join(tmp, "output")
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-stage3-success",
		Items: []PlanItem{{
			Type:             ItemTypeConvert,
			SourcePath:       srcFile,
			TargetPath:       filepath.Join(dstDir, "output.m4a"),
			PreconditionPath: srcFile,
			PreconditionSize: 16,
		}},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	// Inject mock runner that succeeds
	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = -1 // never fail
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)

	// Should succeed
	if err != nil {
		t.Fatalf("expected execution success, got: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed status, got: %s", result.Status)
	}

	// Critical assertion: original source IS deleted on successful commit
	if _, statErr := os.Stat(srcFile); !os.IsNotExist(statErr) {
		t.Error("original source MUST be deleted after successful conversion and commit")
	}

	// Verify: destination file exists
	dst := filepath.Join(dstDir, "output.m4a")
	if _, statErr := os.Stat(dst); os.IsNotExist(statErr) {
		t.Error("destination file should exist after successful conversion")
	}
}

// TestExecutePlan_Overlap_PartialSuccessThenFail validates that
// when some items succeed before failure, items after the failure point are not processed.
// In-flight items that complete before failure detection will commit.
func TestExecutePlan_Overlap_PartialSuccessThenFail(t *testing.T) {
	tmp := t.TempDir()

	// Use more files to clearly observe fail-fast stopping point
	const numFiles = 10
	var files []string
	for i := 0; i < numFiles; i++ {
		file := filepath.Join(tmp, fmt.Sprintf("track%d.wav", i))
		if err := os.WriteFile(file, []byte("audio"), 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}

	var items []PlanItem
	for i, file := range files {
		items = append(items, PlanItem{
			Type:             ItemTypeConvert,
			SourcePath:       file,
			TargetPath:       filepath.Join(tmp, fmt.Sprintf("track%d.m4a", i)),
			PreconditionPath: file,
			PreconditionSize: 5,
		})
	}
	plan := &Plan{PlanID: "plan-partial-fail", Items: items}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	// Inject mock runner that fails on 3rd convert (index 2)
	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = 2
	mockRunner.convertDelay = 5 * time.Millisecond
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)

	// Should fail
	if err == nil {
		t.Fatal("expected execution failure")
	}
	_ = result

	convertCalls := mockRunner.getConvertCalls()

	// ROBUST ASSERTION: Not all items were processed (fail-fast worked)
	if len(convertCalls) >= numFiles {
		t.Errorf("fail-fast should stop feeding: all %d items were processed", len(convertCalls))
	}

	// ROBUST ASSERTION: Verify SOME tail items remain unprocessed
	// Using midpoint to avoid assuming specific parallelism.
	midpoint := numFiles / 2
	unprocessedTail := false
	for i := midpoint; i < numFiles; i++ {
		found := false
		for _, call := range convertCalls {
			if call == files[i] {
				found = true
				break
			}
		}
		if !found {
			unprocessedTail = true
			break
		}
	}
	if !unprocessedTail {
		t.Errorf("fail-fast should leave some tail items unprocessed (midpoint=%d, total=%d, processed=%d)",
			midpoint, numFiles, len(convertCalls))
	}

	// Verify: The error indicates pipeline failure
	if !strings.Contains(err.Error(), "stage2 encode failed") {
		t.Errorf("expected stage2 encode failure, got: %v", err)
	}
}

// TestExecutePlan_Cleanup_RemovesScratchOnSuccess validates scratch cleanup on success.
func TestExecutePlan_Cleanup_RemovesScratchOnSuccess(t *testing.T) {
	tmp := t.TempDir()

	srcFile := filepath.Join(tmp, "source.wav")
	if err := os.WriteFile(srcFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-cleanup-success",
		Items: []PlanItem{{
			Type:             ItemTypeConvert,
			SourcePath:       srcFile,
			TargetPath:       filepath.Join(tmp, "output.m4a"),
			PreconditionPath: srcFile,
			PreconditionSize: 4,
		}},
	}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	mockRunner := newMockPipelineRunner()
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)

	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	// Verify: scratch directories cleaned up
	sessionID := result.SessionID
	scratchPath := svc.scratchRoot
	inPath := filepath.Join(scratchPath, "in", sessionID)
	outPath := filepath.Join(scratchPath, "out", sessionID)

	if _, statErr := os.Stat(inPath); !os.IsNotExist(statErr) {
		t.Errorf("scratch/in should be cleaned up: %s", inPath)
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Errorf("scratch/out should be cleaned up: %s", outPath)
	}

	// Verify: source file deleted on success
	if _, statErr := os.Stat(srcFile); !os.IsNotExist(statErr) {
		t.Error("source file should be deleted after successful conversion")
	}
}

// TestExecutePlan_Cleanup_RemovesScratchOnFailure validates scratch cleanup on failure.
func TestExecutePlan_Cleanup_RemovesScratchOnFailure(t *testing.T) {
	tmp := t.TempDir()

	srcFile := filepath.Join(tmp, "source.wav")
	if err := os.WriteFile(srcFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{
		PlanID: "plan-cleanup-fail",
		Items: []PlanItem{{
			Type:             ItemTypeConvert,
			SourcePath:       srcFile,
			TargetPath:       filepath.Join(tmp, "output.m4a"),
			PreconditionPath: srcFile,
			PreconditionSize: 4,
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

	result, err := svc.ExecutePlan(plan)

	if err == nil {
		t.Fatal("expected failure")
	}
	if result == nil {
		t.Fatal("expected result")
	}

	// Verify: scratch directories cleaned up even on failure
	sessionID := result.SessionID
	scratchPath := svc.scratchRoot
	inPath := filepath.Join(scratchPath, "in", sessionID)
	outPath := filepath.Join(scratchPath, "out", sessionID)

	if _, statErr := os.Stat(inPath); !os.IsNotExist(statErr) {
		t.Errorf("scratch/in should be cleaned up even on failure: %s", inPath)
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Errorf("scratch/out should be cleaned up even on failure: %s", outPath)
	}

	// Verify: source file preserved on failure
	if _, statErr := os.Stat(srcFile); os.IsNotExist(statErr) {
		t.Error("source file should be preserved when conversion fails")
	}
}

// TestExecutePlan_FailedConvertNotCommitted validates that
// failed conversions do NOT produce committed outputs for items never processed.
// Best-effort fail-fast permits in-flight items to finish before stop is observed.
func TestExecutePlan_FailedConvertNotCommitted(t *testing.T) {
	tmp := t.TempDir()

	// Create 3 files
	var files []string
	for i := 0; i < 3; i++ {
		file := filepath.Join(tmp, fmt.Sprintf("file%d.wav", i))
		if err := os.WriteFile(file, []byte("audio"), 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}

	var items []PlanItem
	for i, file := range files {
		items = append(items, PlanItem{
			Type:             ItemTypeConvert,
			SourcePath:       file,
			TargetPath:       filepath.Join(tmp, fmt.Sprintf("file%d.m4a", i)),
			PreconditionPath: file,
			PreconditionSize: 5,
		})
	}
	plan := &Plan{PlanID: "plan-failed-convert-not-committed", Items: items}

	toolsConfig := ToolsConfig{
		Encoder:  "qaac",
		QAACPath: getValidExecutablePath(t),
	}
	svc := NewExecuteService(nil, toolsConfig)

	// Mock runner fails on FIRST item (index 0)
	mockRunner := newMockPipelineRunner()
	mockRunner.failOnConvertIndex = 0
	svc.SetRunner(mockRunner)

	result, err := svc.ExecutePlan(plan)

	// Should fail
	if err == nil {
		t.Fatal("expected failure")
	}
	_ = result

	// All source files should be preserved on failed session.
	for i, file := range files {
		if _, statErr := os.Stat(file); os.IsNotExist(statErr) {
			t.Errorf("source file %d should be preserved (first failure, no commits): %s", i, file)
		}
	}

	// For sources never processed by Convert, destination must not exist.
	processed := make(map[string]struct{})
	for _, src := range mockRunner.getConvertCalls() {
		processed[src] = struct{}{}
	}
	for i, src := range files {
		if _, ok := processed[src]; ok {
			continue
		}
		dst := filepath.Join(tmp, fmt.Sprintf("file%d.m4a", i))
		if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
			t.Errorf("destination %s exists for unprocessed source %s", dst, src)
		}
	}

	// No source deletions should happen on failed convert batch.
	if count := atomic.LoadInt32(&mockRunner.deleteCount); count != 0 {
		t.Errorf("expected 0 source deletes on failed convert batch, got %d", count)
	}
}
