package execute

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockExecuteRepo implements ExecuteRepository and contentRevProvider for testing
type mockExecuteRepo struct {
	mu                 sync.Mutex
	revByPath          map[string]int
	contentRevCalls    int
	contentRevCallback func(path string)
}

func (m *mockExecuteRepo) CreateExecuteSession(_, _, _, _ string) error       { return nil }
func (m *mockExecuteRepo) UpdateExecuteSessionStatus(_, _, _, _ string) error { return nil }
func (m *mockExecuteRepo) GetEntryContentRev(path string) (int, error) {
	m.mu.Lock()
	m.contentRevCalls++
	cb := m.contentRevCallback
	m.mu.Unlock()
	if cb != nil {
		cb(path)
	}

	if rev, ok := m.revByPath[path]; ok {
		return rev, nil
	}
	return 0, os.ErrNotExist
}

func (m *mockExecuteRepo) getContentRevCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.contentRevCalls
}

func TestExecutePlan_ValidatesPreconditions(t *testing.T) {
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
			Type:                   ItemTypeDelete,
			SourcePath:             testFile,
			PreconditionPath:       testFile,
			PreconditionSize:       info.Size(),
			PreconditionMtime:      info.ModTime().Unix(),
			PreconditionContentRev: 999,
		}},
	}

	svc := NewExecuteService(&mockExecuteRepo{revByPath: map[string]int{testFile: 1}}, ToolsConfig{})
	result, execErr := svc.ExecutePlan(plan)
	if execErr == nil {
		t.Fatal("expected precondition error, got nil")
	}
	if !strings.Contains(execErr.Error(), "content_rev mismatch") {
		t.Fatalf("expected content_rev mismatch, got %v", execErr)
	}
	if result == nil || result.Status != "precondition_failed" {
		t.Fatalf("expected status precondition_failed, got %+v", result)
	}
}

func TestExecutePlan_PrevalidatesAllItemsBeforeMutation(t *testing.T) {
	tmp := t.TempDir()
	file1 := filepath.Join(tmp, "a.wav")
	file2 := filepath.Join(tmp, "b.wav")
	if err := os.WriteFile(file1, []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	info1, _ := os.Stat(file1)
	info2, _ := os.Stat(file2)

	plan := &Plan{PlanID: "plan-003", Items: []PlanItem{
		{Type: ItemTypeDelete, SourcePath: file1, PreconditionPath: file1, PreconditionSize: info1.Size(), PreconditionMtime: info1.ModTime().Unix()},
		{Type: ItemTypeDelete, SourcePath: file2, PreconditionPath: file2, PreconditionSize: info2.Size() + 1, PreconditionMtime: info2.ModTime().Unix()},
	}}

	svc := NewExecuteService(nil, ToolsConfig{})
	svc.SetExecuteConfig(ExecuteConfig{MaxIOWorkers: 4, PrecheckConcurrentStat: true})
	_, err := svc.ExecutePlan(plan)
	if err == nil {
		t.Fatal("expected precondition failure")
	}
	if _, err := os.Stat(file1); err != nil {
		t.Fatalf("expected first file to remain untouched, got %v", err)
	}
}

func TestIsPlanStale(t *testing.T) {
	tmp := t.TempDir()
	testFile := filepath.Join(tmp, "song.wav")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatal(err)
	}

	validPlan := &Plan{
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
	stale, err := svc.IsPlanStale(validPlan)
	if stale || err != nil {
		t.Fatalf("expected valid plan, got stale=%v err=%v", stale, err)
	}

	invalidPlan := &Plan{
		PlanID: "plan-002",
		Items: []PlanItem{{
			Type:              ItemTypeDelete,
			SourcePath:        testFile,
			PreconditionPath:  testFile,
			PreconditionSize:  info.Size() + 100,
			PreconditionMtime: info.ModTime().Unix(),
		}},
	}

	stale, _ = svc.IsPlanStale(invalidPlan)
	if !stale {
		t.Fatal("expected stale plan")
	}
}

func TestExecutePlan_PrecheckConcurrent_FirstHardErrorCancelsNewWork(t *testing.T) {
	originalStatPath := statPath
	defer func() { statPath = originalStatPath }()

	tmp := t.TempDir()
	goodFile := filepath.Join(tmp, "good.wav")
	if err := os.WriteFile(goodFile, []byte("good"), 0644); err != nil {
		t.Fatal(err)
	}

	failPath := filepath.Join(tmp, "missing.wav")
	slowPath := filepath.Join(tmp, "slow.wav")
	if err := os.WriteFile(slowPath, []byte("slow"), 0644); err != nil {
		t.Fatal(err)
	}
	extraPath := filepath.Join(tmp, "extra.wav")
	if err := os.WriteFile(extraPath, []byte("extra"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{PlanID: "plan-precheck-concurrent", Items: []PlanItem{
		{Type: ItemTypeDelete, SourcePath: slowPath, PreconditionPath: slowPath},
		{Type: ItemTypeDelete, SourcePath: failPath, PreconditionPath: failPath},
		{Type: ItemTypeDelete, SourcePath: extraPath, PreconditionPath: extraPath},
		{Type: ItemTypeDelete, SourcePath: goodFile, PreconditionPath: goodFile},
	}}

	var started int32
	slowStarted := make(chan struct{}, 1)
	failCalled := make(chan struct{}, 1)
	failRelease := make(chan struct{})
	failDone := make(chan struct{}, 1)
	releaseSlow := make(chan struct{})

	statPath = func(path string) (os.FileInfo, error) {
		atomic.AddInt32(&started, 1)
		if path == slowPath {
			slowStarted <- struct{}{}
			<-releaseSlow
		}
		if path == failPath {
			failCalled <- struct{}{}
			<-failRelease
			failDone <- struct{}{}
		}
		return originalStatPath(path)
	}

	svc := NewExecuteService(nil, ToolsConfig{})
	svc.SetExecuteConfig(ExecuteConfig{MaxIOWorkers: 2, PrecheckConcurrentStat: true})
	if got := svc.maxIOWorkers(); got != 2 {
		t.Fatalf("expected maxIOWorkers=2, got %d", got)
	}

	done := make(chan error, 1)
	go func() {
		_, err := svc.ExecutePlan(plan)
		done <- err
	}()

	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow in-flight precheck did not start")
	}
	select {
	case <-failCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("failing precheck did not start")
	}
	close(failRelease)
	select {
	case <-failDone:
	case <-time.After(2 * time.Second):
		t.Fatal("failing precheck did not complete")
	}

	close(releaseSlow)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected precheck hard error, got nil")
		}
		if !strings.Contains(err.Error(), "SOURCE_MISSING") {
			t.Fatalf("expected SOURCE_MISSING, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ExecutePlan deadlocked waiting for in-flight prechecks")
	}

	if got := atomic.LoadInt32(&started); got < 2 {
		t.Fatalf("expected at least failing and in-flight checks to run, started=%d", got)
	}
}

func TestExecutePlan_Phase1Fail_CancelsNewWork(t *testing.T) {
	originalStatPath := statPath
	defer func() { statPath = originalStatPath }()

	tmp := t.TempDir()
	first := filepath.Join(tmp, "first.wav")
	second := filepath.Join(tmp, "second.wav")
	third := filepath.Join(tmp, "third.wav")
	if err := os.WriteFile(first, []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("2"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(third, []byte("3"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{PlanID: "plan-phase1-cancel", Items: []PlanItem{
		{Type: ItemTypeDelete, SourcePath: first, PreconditionPath: first},
		{Type: ItemTypeDelete, SourcePath: second, PreconditionPath: second},
		{Type: ItemTypeDelete, SourcePath: third, PreconditionPath: third},
	}}

	var started int32
	statPath = func(path string) (os.FileInfo, error) {
		if atomic.AddInt32(&started, 1) == 1 {
			return nil, os.ErrNotExist
		}
		return originalStatPath(path)
	}

	svc := NewExecuteService(nil, ToolsConfig{})
	svc.SetExecuteConfig(ExecuteConfig{MaxIOWorkers: 1, PrecheckConcurrentStat: true})

	_, err := svc.ExecutePlan(plan)
	if err == nil {
		t.Fatal("expected phase1 hard error")
	}
	if got := atomic.LoadInt32(&started); got != 1 {
		t.Fatalf("expected cancellation to block new work admission, started=%d", got)
	}
}

func TestExecutePlan_FirstHardErrorReturned(t *testing.T) {
	originalStatPath := statPath
	defer func() { statPath = originalStatPath }()

	tmp := t.TempDir()
	fileA := filepath.Join(tmp, "a.wav")
	fileB := filepath.Join(tmp, "b.wav")
	if err := os.WriteFile(fileA, []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	firstErr := errors.New("first hard error")
	laterErr := errors.New("later hard error")

	var calls int32
	statPath = func(path string) (os.FileInfo, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return nil, firstErr
		}
		return nil, laterErr
	}

	plan := &Plan{PlanID: "plan-first-hard-error", Items: []PlanItem{
		{Type: ItemTypeDelete, SourcePath: fileA, PreconditionPath: fileA},
		{Type: ItemTypeDelete, SourcePath: fileB, PreconditionPath: fileB},
	}}

	svc := NewExecuteService(nil, ToolsConfig{})
	svc.SetExecuteConfig(ExecuteConfig{MaxIOWorkers: 1, PrecheckConcurrentStat: true})

	_, err := svc.ExecutePlan(plan)
	if err == nil {
		t.Fatal("expected hard precheck error, got nil")
	}
	if !errors.Is(err, firstErr) {
		t.Fatalf("expected first hard error to be returned, got %v", err)
	}
	if strings.Contains(err.Error(), laterErr.Error()) {
		t.Fatalf("later error must not override first error, got %v", err)
	}
}

func TestExecutePlan_Phase1Fail_SkipsPhase2(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "missing.wav")

	plan := &Plan{PlanID: "plan-phase1-fail", Items: []PlanItem{{
		Type:                   ItemTypeDelete,
		SourcePath:             missing,
		PreconditionPath:       missing,
		PreconditionContentRev: 123,
	}}}

	repo := &mockExecuteRepo{revByPath: map[string]int{missing: 123}}
	svc := NewExecuteService(repo, ToolsConfig{})

	_, err := svc.ExecutePlan(plan)
	if err == nil {
		t.Fatal("expected phase1 precheck failure")
	}
	if got := repo.getContentRevCalls(); got != 0 {
		t.Fatalf("expected phase2 content_rev checks to be skipped, calls=%d", got)
	}
}

func TestExecuteConfig_PrecheckConcurrentStat_DefaultsFalse(t *testing.T) {
	svc := NewExecuteService(nil, ToolsConfig{})
	if svc.config.PrecheckConcurrentStat {
		t.Fatal("expected default PrecheckConcurrentStat=false")
	}
}

func TestExecuteConfig_PrecheckConcurrentStat_OverrideTrue(t *testing.T) {
	svc := NewExecuteService(nil, ToolsConfig{})
	svc.SetExecuteConfig(ExecuteConfig{MaxIOWorkers: 4, PrecheckConcurrentStat: true})
	if !svc.config.PrecheckConcurrentStat {
		t.Fatal("expected PrecheckConcurrentStat=true after override")
	}
}
