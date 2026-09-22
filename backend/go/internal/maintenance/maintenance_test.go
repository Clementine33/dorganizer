//nolint:testpackage // drives the unexported pass directly for the deterministic exclusion test
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/fileops"
)

// These tests drive the real loop against a real database. They wait for the
// effect they assert on, never for the clock: a pass does file I/O, and no
// amount of virtual time makes that finish. The durations are short because
// they are only ever waited on, never compared.
const (
	testTick     = 5 * time.Millisecond
	testInterval = 200 * time.Millisecond
	testTimeout  = 5 * time.Second
)

// newLoopRepo builds a real database for a loop to run against: a temp file
// created the way an install creates it, schema and incremental auto-vacuum
// included.
func newLoopRepo(t *testing.T) *sqlite.Repository {
	t.Helper()
	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "maintenance.db"))
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

// seedOldScans writes finished scan sessions older than every window these
// tests use, so a pass has something to delete.
func seedOldScans(t *testing.T, repo *sqlite.Repository, rows int) {
	t.Helper()
	finished := time.Now().Add(-30 * 24 * time.Hour)
	for i := range rows {
		session := &sqlite.ScanSession{
			SessionID: fmt.Sprintf("scan-%03d", i),
			RootPath:  "/music",
			Kind:      "full",
			Status:    "completed",
			StartedAt: finished,
		}
		if err := repo.CreateScanSession(session); err != nil {
			t.Fatalf("create scan %d: %v", i, err)
		}
		// The terminal timestamp is what retention judges the row by, and
		// CreateScanSession does not write one.
		if _, err := repo.DB().Exec(
			"UPDATE scan_sessions SET finished_at = ? WHERE session_id = ?",
			finished.Format(time.RFC3339Nano), session.SessionID,
		); err != nil {
			t.Fatalf("age scan %d: %v", i, err)
		}
	}
}

// seedQueuedGeneration makes the database report an active session, which is
// what refuses a pass on a machine that is planning or executing.
func seedQueuedGeneration(t *testing.T, repo *sqlite.Repository) {
	t.Helper()
	now := time.Now().Format(time.RFC3339Nano)
	if _, err := repo.DB().Exec(`
		INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at)
		VALUES ('lib-1', 'Onsei', '/music', '/music', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed library: %v", err)
	}
	if _, err := repo.DB().Exec(`
		INSERT INTO worksets (id, title, library_id, root_path, root_path_key, version, created_at, updated_at)
		VALUES ('ws-1', 'maintenance ws', 'lib-1', '/music', '/music', 1, ?, ?)
	`, now, now); err != nil {
		t.Fatalf("seed workset: %v", err)
	}
	if err := repo.CreateGeneration(&sqlite.PlanGeneration{
		GenerationID:  "gen-queued",
		WorksetID:     "ws-1",
		OperationType: "conversion",
		Status:        sqlite.GenStatusQueued,
		CreatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("seed generation: %v", err)
	}
}

func countScans(t *testing.T, repo *sqlite.Repository) int {
	t.Helper()
	var count int
	if err := repo.DB().QueryRow("SELECT COUNT(*) FROM scan_sessions").Scan(&count); err != nil {
		t.Fatalf("count scan_sessions: %v", err)
	}
	return count
}

// waitFor polls until cond holds, or fails the test. The loop works on real
// files, so this is the only honest way to wait for what it did.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// fakeAcquire stands in for the admission gate in the tests that are about
// scheduling. The exclusion itself is pinned against the real gate in
// TestPassWithRealGateNeverOverlapsATask.
type fakeAcquire struct {
	mu    sync.Mutex
	calls int
	busy  bool
	err   error
}

func (f *fakeAcquire) begin() (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	switch {
	case f.err != nil:
		return nil, f.err
	case f.busy:
		return nil, &fileops.BusyError{Reason: "test: busy"}
	default:
		return func() {}, nil
	}
}

func (f *fakeAcquire) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// loopOptions drives a loop with windows the seeded rows are older than, one row
// per batch so a test can count batches, and a tick short enough to observe.
func loopOptions() Options {
	return Options{
		ScanRetention:       24 * time.Hour,
		GenerationRetention: 24 * time.Hour,
		Tick:                testTick,
		Interval:            testInterval,
		BatchRows:           1,
		MaxBatches:          8,
		VacuumPages:         16,
		MaxVacuumSteps:      2,
		YieldDelay:          time.Millisecond,
	}
}

// startLoop runs a loop in the background and returns a stop function that
// waits for it to return.
func startLoop(t *testing.T, loop *Loop) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// logCapture collects the loop's log output for the assertions that only the
// log can carry. The loop writes from its own goroutine while the test reads,
// so the buffer is guarded.
type logCapture struct {
	mu   sync.Mutex
	text strings.Builder
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text.Write(p)
}

func (c *logCapture) contains(substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Contains(c.text.String(), substr)
}

func captureLog(t *testing.T) *logCapture {
	t.Helper()
	logs := &logCapture{}
	previous := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	return logs
}

// A refusal is congestion, not a failure: the pass leaves everything alone, says
// nothing about it, and comes back on the next tick.
func TestLoop_DefersWhileTheApplicationIsBusy(t *testing.T) {
	repo := newLoopRepo(t)
	seedOldScans(t, repo, 3)
	acquire := &fakeAcquire{busy: true}
	logs := captureLog(t)
	stop := startLoop(t, New(repo, acquire.begin, loopOptions()))

	waitFor(t, "several admission attempts", func() bool { return acquire.callCount() >= 3 })
	stop()

	if got := countScans(t, repo); got != 3 {
		t.Errorf("scans deleted while the application was busy: %d remain, want 3", got)
	}
	if logs.contains("admission check failed") {
		t.Errorf("a refusal was reported as a failure")
	}
}

func TestLoop_HoldsTheSlotOneBatchAtATime(t *testing.T) {
	repo := newLoopRepo(t)
	seedOldScans(t, repo, 3)
	acquire := &fakeAcquire{}
	stop := startLoop(t, New(repo, acquire.begin, loopOptions()))
	defer stop()

	waitFor(t, "every seeded row deleted", func() bool { return countScans(t, repo) == 0 })

	// Three rows at one row per batch means the slot went back between them; a
	// pass that held it throughout would have acquired once.
	if calls := acquire.callCount(); calls < 3 {
		t.Errorf("acquired %d times for three batches, want the slot released between them", calls)
	}
}

func TestLoop_ContinuesOnTheNextTickWhenTheBudgetRunsOut(t *testing.T) {
	repo := newLoopRepo(t)
	seedOldScans(t, repo, 3)
	acquire := &fakeAcquire{}
	options := loopOptions()
	options.MaxBatches = 1
	stop := startLoop(t, New(repo, acquire.begin, options))
	defer stop()

	// One pass is one batch: the rows go one per tick, not one per interval.
	waitFor(t, "the second row to go", func() bool { return countScans(t, repo) == 2 })
	waitFor(t, "the third row to go", func() bool { return countScans(t, repo) == 1 })
	waitFor(t, "the last row to go", func() bool { return countScans(t, repo) == 0 })
}

// TestPassRunsOnePassNow is the start-up contract: the application runs one pass
// before it serves anything, so the retention work that must not race a client
// is over by the time a client can be refused.
func TestPassRunsOnePassNow(t *testing.T) {
	repo := newLoopRepo(t)
	seedOldScans(t, repo, 1)
	loop := New(repo, (&fakeAcquire{}).begin, loopOptions())

	if !loop.Pass(t.Context()) {
		t.Fatal("a start-up pass with work to do must complete")
	}
	if rows := countScans(t, repo); rows != 0 {
		t.Fatalf("scans = %d, want the start-up pass to have deleted the old row", rows)
	}
}

func TestLoop_DoesNotRepeatACompletedPassBeforeTheInterval(t *testing.T) {
	repo := newLoopRepo(t) // nothing to delete: the first pass completes
	acquire := &fakeAcquire{}
	stop := startLoop(t, New(repo, acquire.begin, loopOptions()))
	defer stop()

	waitFor(t, "the first pass", func() bool { return acquire.callCount() >= 1 })
	if calls := acquire.callCount(); calls != 1 {
		t.Fatalf("the first pass acquired %d times with nothing to do, want 1", calls)
	}

	// Several ticks pass with no second attempt: a pass that got everything done
	// waits out the interval, where an incomplete one would be retried.
	time.Sleep(5 * testTick)
	if calls := acquire.callCount(); calls != 1 {
		t.Errorf("a completed pass ran again inside the interval: %d calls, want 1", calls)
	}

	waitFor(t, "the next pass after the interval", func() bool { return acquire.callCount() > 1 })
}

// A broken admission check is not congestion: it is reported, and it leaves the
// rows alone rather than looking like an application that is merely busy.
func TestLoop_ReportsABrokenAdmissionCheck(t *testing.T) {
	repo := newLoopRepo(t)
	seedOldScans(t, repo, 1)
	acquire := &fakeAcquire{err: errors.New("active-session query failed")}
	logs := captureLog(t)
	stop := startLoop(t, New(repo, acquire.begin, loopOptions()))

	waitFor(t, "the failed check to be reported", func() bool {
		return logs.contains("admission check failed")
	})
	stop()

	if got := countScans(t, repo); got != 1 {
		t.Errorf("scans remaining = %d, want 1: a failed check must not delete anything", got)
	}
}

// TestPassWithRealGateNeverOverlapsATask checks the exclusion the pass depends
// on against the real gate and the real pass: a fake acquire can show the
// scheduling rules, but only the real gate can show that maintenance and a task
// are never in flight together.
func TestPassWithRealGateNeverOverlapsATask(t *testing.T) {
	repo := newLoopRepo(t)
	seedOldScans(t, repo, 3)
	gate := fileops.NewGate(repo.HasActiveSession)
	loop := New(repo, gate.BeginMaintenance, loopOptions())
	ctx := t.Context()

	// A scan in flight refuses the whole pass.
	releaseScan, err := gate.BeginScan()
	if err != nil {
		t.Fatalf("BeginScan: %v", err)
	}
	if loop.pass(ctx) {
		t.Error("the pass reported completion while a scan was running")
	}
	if got := countScans(t, repo); got != 3 {
		t.Fatalf("the pass deleted rows while a scan was running: %d remain, want 3", got)
	}
	releaseScan()

	// A scan that arrives while maintenance holds the slot is refused, never
	// interleaved, and so is an enqueue.
	releaseMaintenance, err := gate.BeginMaintenance()
	if err != nil {
		t.Fatalf("BeginMaintenance: %v", err)
	}
	if _, busyErr := gate.BeginScan(); !fileops.IsBusy(busyErr) {
		t.Errorf("a scan started while maintenance held the slot: %v", busyErr)
	}
	if err := gate.Enqueue(func() error { return nil }); !fileops.IsBusy(err) {
		t.Errorf("a session was enqueued while maintenance held the slot: %v", err)
	}
	releaseMaintenance()

	// A queued session refuses the pass too: it lives in the database and
	// outlives any single request.
	seedQueuedGeneration(t, repo)
	if loop.pass(ctx) {
		t.Error("the pass reported completion while a session was queued")
	}
	if got := countScans(t, repo); got != 3 {
		t.Fatalf("the pass deleted rows while a session was queued: %d remain, want 3", got)
	}

	// With the session finished and the slot free, the same pass does its work.
	if _, err := repo.DB().Exec(
		"UPDATE plan_generations SET status = 'completed', finished_at = ? WHERE generation_id = 'gen-queued'",
		time.Now().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("finish the seeded session: %v", err)
	}
	if !loop.pass(ctx) {
		t.Error("the pass did not complete with the slot free")
	}
	if got := countScans(t, repo); got != 0 {
		t.Errorf("scans remaining after a free pass: %d, want 0", got)
	}
}
