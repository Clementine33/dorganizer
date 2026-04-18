package execute

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMaxIOWorkers_UsesConfiguredValue(t *testing.T) {
	svc := NewExecuteService(nil, ToolsConfig{})
	want := runtime.NumCPU() + 10
	svc.SetExecuteConfig(ExecuteConfig{MaxIOWorkers: want})

	got := svc.maxIOWorkers()
	if got != want {
		t.Fatalf("expected configured maxIOWorkers=%d, got %d", want, got)
	}
}

func TestMaxCPUWorkers_EqualsNumCPU(t *testing.T) {
	got := maxCPUWorkers()
	want := runtime.NumCPU()
	if want < 1 {
		want = 1
	}
	if got != want {
		t.Fatalf("expected maxCPUWorkers=%d, got %d", want, got)
	}
}

// instrumentedSem is a semaphore wrapper for tests.
// It tracks current, peak, and over-limit breach counts.
type instrumentedSem struct {
	mu       sync.Mutex
	tokens   chan struct{}
	current  int32
	peak     int32
	breaches int32
}

func newInstrumentedSem(limit int) *instrumentedSem {
	if limit < 1 {
		limit = 1
	}
	return &instrumentedSem{tokens: make(chan struct{}, limit)}
}

func (s *instrumentedSem) Acquire() {
	s.tokens <- struct{}{}
	cur := atomic.AddInt32(&s.current, 1)

	s.mu.Lock()
	if cur > s.peak {
		s.peak = cur
	}
	if int(cur) > cap(s.tokens) {
		s.breaches++
	}
	s.mu.Unlock()
}

func (s *instrumentedSem) Release() {
	if cur := atomic.AddInt32(&s.current, -1); cur < 0 {
		atomic.StoreInt32(&s.current, 0)
	}
	<-s.tokens
}

func (s *instrumentedSem) Peak() int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}

func (s *instrumentedSem) BreachCount() int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.breaches
}

// TestExecuteConvertBatchWithPool_SemaphorePeaksAndNoReentry validates that
// io/cpu semaphores enforce configured concurrency bounds under pool execution.
//
// Scenario: maxIOWorkers=1, batch>=2, with delayed hooks to ensure
// temporal separation between encode and commit phases.
func TestExecuteConvertBatchWithPool_SemaphorePeaksAndNoReentry(t *testing.T) {
	tmp := t.TempDir()
	svc := NewExecuteService(nil, ToolsConfig{})
	svc.scratchRoot = filepath.Join(tmp, "scratch")

	// Create batch of 4 items to observe semaphore behavior
	const batchSize = 4
	items := make([]PlanItem, 0, batchSize)
	indices := make([]int, 0, batchSize)
	for i := 0; i < batchSize; i++ {
		src := filepath.Join(tmp, fmt.Sprintf("%02d.wav", i))
		dst := filepath.Join(tmp, fmt.Sprintf("%02d.m4a", i))
		if err := os.WriteFile(src, []byte("audio"), 0644); err != nil {
			t.Fatal(err)
		}
		items = append(items, PlanItem{SourcePath: src, TargetPath: dst})
		indices = append(indices, 100+i)
	}

	// Instrumented sem that properly tracks concurrency
	ioSem := newInstrumentedSem(1)
	cpuSem := newInstrumentedSem(1)

	var encodeCalled int32

	runtime := poolRuntime{
		ioSem:  ioSem,
		cpuSem: cpuSem,
		runEncoderToTmpFn: func(src, tmpOut string, rt poolRuntime) error {
			rt.ioSem.Acquire()
			defer rt.ioSem.Release()
			rt.cpuSem.Acquire()
			defer rt.cpuSem.Release()
			atomic.AddInt32(&encodeCalled, 1)
			// Small delay to allow commit phase to observe semaphore state
			time.Sleep(50 * time.Millisecond)
			if err := os.MkdirAll(filepath.Dir(tmpOut), 0755); err != nil {
				return err
			}
			return os.WriteFile(tmpOut, []byte("encoded"), 0644)
		},
		commitReplaceFn: func(tmpOut, dst string) error {
			// Commit holds ioSem for its duration
			// With maxIOWorkers=1, only one commit at a time
			time.Sleep(20 * time.Millisecond)
			return os.Rename(tmpOut, dst)
		},
	}

	resultCh := make(chan *batchOutcome, 1)
	go func() {
		resultCh <- svc.executeConvertBatchWithPool(
			&Plan{PlanID: "pool-semaphore-test"},
			"session-semaphore",
			items,
			indices,
			make([]string, len(items)),
			true,
			runtime,
		)
	}()

	var outcome *batchOutcome
	select {
	case outcome = <-resultCh:
	case <-time.After(3 * time.Second):
		t.Fatal("batch execution timed out (possible semaphore nested-acquire stall)")
	}

	if outcome == nil {
		t.Fatal("expected non-nil outcome")
	}
	if outcome.firstFailure != nil {
		t.Fatalf("expected no failures, got firstFailure: %v", outcome.firstFailure)
	}

	// Verify semaphore behavior
	ioPeak := ioSem.Peak()
	ioBreaches := ioSem.BreachCount()
	cpuPeak := cpuSem.Peak()
	cpuBreaches := cpuSem.BreachCount()

	// Peak should be 1 (maxIOWorkers=1 means max 1 concurrent IO operation)
	if ioPeak != 1 {
		t.Errorf("expected io peak=1 (maxIOWorkers=1), got io peak=%d", ioPeak)
	}

	// Breaches should be 0: no observed over-limit concurrency.
	if ioBreaches != 0 {
		t.Errorf("expected io over-limit breaches=0, got io breaches=%d", ioBreaches)
	}
	if cpuPeak != 1 {
		t.Errorf("expected cpu peak=1, got cpu peak=%d", cpuPeak)
	}
	if cpuBreaches != 0 {
		t.Errorf("expected cpu over-limit breaches=0, got cpu breaches=%d", cpuBreaches)
	}

	// Verify all encodes were called
	if got := atomic.LoadInt32(&encodeCalled); got != batchSize {
		t.Errorf("expected %d encode calls, got %d", batchSize, got)
	}

	t.Logf("Semaphore verified: io(peak=%d,breaches=%d) cpu(peak=%d,breaches=%d) encodeCalls=%d",
		ioPeak, ioBreaches, cpuPeak, cpuBreaches, encodeCalled)
}

type testNoopSem struct{}

func (testNoopSem) Acquire() {}
func (testNoopSem) Release() {}

func TestExecutePlan_NoScratchInArtifactsAfterConvert(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "a.wav")
	_ = os.WriteFile(src, []byte("audio"), 0644)

	plan := &Plan{PlanID: "pool-path", Items: []PlanItem{{
		Type:             ItemTypeConvert,
		SourcePath:       src,
		TargetPath:       filepath.Join(tmp, "a.m4a"),
		PreconditionPath: src,
		PreconditionSize: 5,
	}}}

	svc := NewExecuteService(nil, ToolsConfig{Encoder: "qaac", QAACPath: getValidExecutablePath(t)})
	svc.scratchRoot = filepath.Join(tmp, "scratch")
	svc.SetRunner(newMockPipelineRunner())
	var copyObserved atomic.Bool
	svc.copyCallback = func(_ int, _, _ string) {
		copyObserved.Store(true)
	}
	result, err := svc.ExecutePlan(plan)
	if err != nil {
		t.Fatalf("expected successful convert execution, got error: %v", err)
	}

	if result == nil || result.SessionID == "" {
		t.Fatal("expected non-empty sessionID")
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed status, got %q", result.Status)
	}
	if _, err := os.Stat(plan.Items[0].TargetPath); err != nil {
		t.Fatalf("expected converted artifact at target path, stat error: %v", err)
	}
	inPath := filepath.Join(svc.scratchRoot, "in", result.SessionID)
	if _, err := os.Stat(inPath); !os.IsNotExist(err) {
		t.Fatal("unexpected scratch/in usage; old stage1 copy path still active")
	}
	outRoot := filepath.Join(svc.scratchRoot, "out")
	if entries, err := os.ReadDir(outRoot); err != nil {
		if !os.IsNotExist(err) {
			t.Fatalf("unexpected read dir error for scratch/out: %v", err)
		}
	} else if len(entries) != 0 {
		t.Fatalf("expected no residual scratch/out entries, found %d", len(entries))
	}
	if copyObserved.Load() {
		t.Fatal("unexpected stage1 copy callback usage; old stage1 copy path still active")
	}
}

func TestProcessConvertJob_CommitFailure_RemovesTmp(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.wav")
	if err := os.WriteFile(src, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(tmp, "dst.m4a")
	svc := NewExecuteService(nil, ToolsConfig{})
	svc.scratchRoot = filepath.Join(tmp, "scratch")

	item := PlanItem{SourcePath: src, TargetPath: dst}
	runtime := poolRuntime{
		ioSem:  testNoopSem{},
		cpuSem: testNoopSem{},
		runEncoderToTmpFn: func(_, tmpOut string, _ poolRuntime) error {
			return os.WriteFile(tmpOut, []byte("encoded"), 0644)
		},
		commitReplaceFn: func(_, _ string) error {
			return errors.New("commit failed")
		},
	}

	err := svc.processConvertJob(&Plan{}, "session-a", item, 0, runtime)
	if err == nil {
		t.Fatal("expected commit failure")
	}

	outDir := filepath.Join(svc.scratchRoot, "out", "session-a")
	entries, readErr := os.ReadDir(outDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("unexpected read dir error: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected tmp output to be removed, found %d entries", len(entries))
	}
}

func TestProcessConvertJob_CreatesDstParentBeforeCommit(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.wav")
	if err := os.WriteFile(src, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(tmp, "nested", "missing", "dst.m4a")
	svc := NewExecuteService(nil, ToolsConfig{})
	svc.scratchRoot = filepath.Join(tmp, "scratch")

	item := PlanItem{SourcePath: src, TargetPath: dst}
	runtime := poolRuntime{
		ioSem:  testNoopSem{},
		cpuSem: testNoopSem{},
		runEncoderToTmpFn: func(_, tmpOut string, _ poolRuntime) error {
			return os.WriteFile(tmpOut, []byte("encoded"), 0644)
		},
		commitReplaceFn: func(tmpOut, commitDst string) error {
			if _, err := os.Stat(filepath.Dir(commitDst)); err != nil {
				return err
			}
			return os.Rename(tmpOut, commitDst)
		},
	}

	if err := svc.processConvertJob(&Plan{}, "session-b", item, 1, runtime); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("expected destination file to exist, got %v", err)
	}
}

func TestExecuteConvertBatchWithPool_RecordsFirstAndAllFailures(t *testing.T) {
	if maxCPUWorkers() < 2 {
		t.Skip("requires at least 2 workers to validate started-job failure aggregation")
	}

	tmp := t.TempDir()
	svc := NewExecuteService(nil, ToolsConfig{})
	svc.scratchRoot = filepath.Join(tmp, "scratch")

	const totalItems = 20
	items := make([]PlanItem, 0, totalItems)
	indices := make([]int, 0, totalItems)
	for i := 0; i < totalItems; i++ {
		src := filepath.Join(tmp, fmt.Sprintf("%02d.wav", i))
		dst := filepath.Join(tmp, fmt.Sprintf("%02d.m4a", i))
		if err := os.WriteFile(src, []byte("audio"), 0644); err != nil {
			t.Fatal(err)
		}
		items = append(items, PlanItem{SourcePath: src, TargetPath: dst})
		indices = append(indices, 100+i)
	}

	var started atomic.Int32
	readyCh := make(chan struct{})
	var readyOnce sync.Once

	runtime := poolRuntime{
		ioSem:  testNoopSem{},
		cpuSem: testNoopSem{},
		runEncoderToTmpFn: func(src, tmpOut string, _ poolRuntime) error {
			n := started.Add(1)
			if n == 2 {
				readyOnce.Do(func() { close(readyCh) })
			}
			if n <= 2 {
				<-readyCh
			}
			return errors.New("forced encode failure: " + filepath.Base(src))
		},
		commitReplaceFn: func(tmpOut, dst string) error {
			return os.Rename(tmpOut, dst)
		},
	}

	outcome := svc.executeConvertBatchWithPool(
		&Plan{PlanID: "pool-failure-aggregate"},
		"session-failure-aggregate",
		items,
		indices,
		make([]string, len(items)),
		true,
		runtime,
	)

	if outcome == nil {
		t.Fatal("expected non-nil outcome")
	}
	if outcome.firstFailure == nil {
		t.Fatal("expected firstFailure to be recorded")
	}
	startedCount := int(started.Load())
	if startedCount >= totalItems {
		t.Fatalf("expected admission close after first failure to stop feeding full batch, started=%d total=%d", startedCount, totalItems)
	}
	if got := len(outcome.failures); got != startedCount {
		t.Fatalf("expected all started job failures to be recorded (started=%d, failures=%d)", startedCount, got)
	}

	failureIndices := map[int]bool{}
	for _, failure := range outcome.failures {
		failureIndices[failure.itemIndex] = true
	}
	if !failureIndices[100] || !failureIndices[101] {
		t.Fatalf("expected failures for first two started items, got indices=%v", failureIndices)
	}

	if !failureIndices[outcome.firstFailure.itemIndex] {
		t.Fatalf("firstFailure index %d not present in aggregated failures=%v", outcome.firstFailure.itemIndex, failureIndices)
	}
	if outcome.firstFailure.err == nil {
		t.Fatal("expected non-nil firstFailure error")
	}
	if outcome.firstFailure.stage != "stage2" {
		t.Fatalf("expected stage2 first failure, got %q", outcome.firstFailure.stage)
	}
}

func TestExecuteConvertPoolWithTracking_RootedFailureSkipsSameFolderAndContinuesOtherFolder(t *testing.T) {
	tmp := t.TempDir()

	folderA := filepath.Join(tmp, "AlbumA")
	folderB := filepath.Join(tmp, "AlbumB")
	if err := os.MkdirAll(folderA, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folderB, 0755); err != nil {
		t.Fatal(err)
	}

	a1 := filepath.Join(folderA, "a1.wav")
	a2 := filepath.Join(folderA, "a2.wav")
	a3 := filepath.Join(folderA, "a3.wav")
	b1 := filepath.Join(folderB, "b1.wav")
	b2 := filepath.Join(folderB, "b2.wav")
	for _, p := range []string{a1, a2, a3, b1, b2} {
		if err := os.WriteFile(p, []byte("audio"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	items := []PlanItem{
		{Type: ItemTypeConvert, SourcePath: a1, TargetPath: filepath.Join(folderA, "a1.m4a")},
		{Type: ItemTypeConvert, SourcePath: b1, TargetPath: filepath.Join(folderB, "b1.m4a")},
		{Type: ItemTypeConvert, SourcePath: a2, TargetPath: filepath.Join(folderA, "a2.m4a")},
		{Type: ItemTypeConvert, SourcePath: b2, TargetPath: filepath.Join(folderB, "b2.m4a")},
		{Type: ItemTypeConvert, SourcePath: a3, TargetPath: filepath.Join(folderA, "a3.m4a")},
	}
	indices := []int{0, 1, 2, 3, 4}

	plan := &Plan{PlanID: "pool-rooted-domain-skip", RootPath: filepath.ToSlash(tmp), Items: items}
	svc := NewExecuteService(nil, ToolsConfig{Encoder: "qaac", QAACPath: getValidExecutablePath(t)})
	runner := newMockBatchBarrierRunner()
	runner.convertFailures[a1] = errors.New("forced convert failure in folder A")
	svc.SetRunner(runner)

	failedFolders := map[string]bool{}
	successfulFolders := map[string]bool{}

	result, err := svc.executeConvertPoolWithTracking(plan, "session-rooted-domain-skip", items, indices, failedFolders, successfulFolders)
	if err == nil {
		t.Fatal("expected convert pool tracking to fail")
	}
	if result == nil || result.Status != "failed" {
		t.Fatalf("expected failed result, got %+v", result)
	}

	convertCalls := runner.getConvertCalls()
	if len(convertCalls) == 0 {
		t.Fatal("expected convert calls to be recorded")
	}
	continuedOtherFolder := false
	for _, call := range convertCalls {
		if call == b1 || call == b2 {
			continuedOtherFolder = true
			break
		}
	}
	if !continuedOtherFolder {
		t.Fatalf("expected rooted mode to continue admitting other-folder work, convertCalls=%v", convertCalls)
	}

	folderAPath := getFolderForItem(plan.RootPath, items[0])
	if !failedFolders[folderAPath] {
		t.Fatalf("expected folder A to be marked failed, failedFolders=%v", failedFolders)
	}

	if _, statErr := os.Stat(a3); statErr != nil {
		t.Fatalf("expected unstarted folder A source a3 to remain due folder fail-fast skip, statErr=%v", statErr)
	}
}

func TestExecuteConvertPoolWithTracking_NonRootedFailureSkipsDeleteBarrier(t *testing.T) {
	tmp := t.TempDir()

	a := filepath.Join(tmp, "a.wav")
	b := filepath.Join(tmp, "b.wav")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("audio"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	items := []PlanItem{
		{Type: ItemTypeConvert, SourcePath: a, TargetPath: filepath.Join(tmp, "a.m4a")},
		{Type: ItemTypeConvert, SourcePath: b, TargetPath: filepath.Join(tmp, "b.m4a")},
	}
	indices := []int{0, 1}

	plan := &Plan{PlanID: "pool-nonrooted-delete-skip", RootPath: "", Items: items}
	svc := NewExecuteService(nil, ToolsConfig{Encoder: "qaac", QAACPath: getValidExecutablePath(t)})
	runner := newMockBatchBarrierRunner()
	runner.convertFailures[a] = errors.New("forced convert failure")
	svc.SetRunner(runner)

	_, err := svc.executeConvertPoolWithTracking(plan, "session-nonrooted-delete-skip", items, indices, map[string]bool{}, map[string]bool{})
	if err == nil {
		t.Fatal("expected convert pool tracking failure")
	}

	if calls := runner.getDeleteCalls(); len(calls) != 0 {
		t.Fatalf("expected no delete barrier calls in non-rooted failure case, got %v", calls)
	}
}

func TestExecuteConvertPoolWithTracking_NonRootedGlobalFailFastStopsFurtherAdmission(t *testing.T) {
	tmp := t.TempDir()

	totalItems := maxCPUWorkers() + 8
	if totalItems < 12 {
		totalItems = 12
	}

	items := make([]PlanItem, 0, totalItems)
	indices := make([]int, 0, totalItems)
	for i := 0; i < totalItems; i++ {
		src := filepath.Join(tmp, fmt.Sprintf("%02d.wav", i))
		dst := filepath.Join(tmp, fmt.Sprintf("%02d.m4a", i))
		if err := os.WriteFile(src, []byte("audio"), 0644); err != nil {
			t.Fatal(err)
		}
		items = append(items, PlanItem{Type: ItemTypeConvert, SourcePath: src, TargetPath: dst})
		indices = append(indices, i)
	}

	plan := &Plan{PlanID: "pool-nonrooted-global-stop", RootPath: "", Items: items}
	svc := NewExecuteService(nil, ToolsConfig{Encoder: "qaac", QAACPath: getValidExecutablePath(t)})
	runner := newMockPipelineRunner()
	runner.failOnConvertIndex = 0
	runner.convertDelay = 20 * time.Millisecond
	svc.SetRunner(runner)

	_, err := svc.executeConvertPoolWithTracking(plan, "session-nonrooted-global-stop", items, indices, map[string]bool{}, map[string]bool{})
	if err == nil {
		t.Fatal("expected convert pool tracking failure")
	}

	if got := len(runner.getConvertCalls()); got >= len(items) {
		t.Fatalf("expected global fail-fast to stop further admission, got %d/%d convert calls", got, len(items))
	}
}
