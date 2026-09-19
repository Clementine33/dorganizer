package fileops_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/onsei/organizer/backend/internal/services/fileops"
)

// TestGateSerializesFileManagementAndScans pins the two-way exclusion: a scan
// running refuses file management, and file management in flight refuses a
// scan. Each side sees a busy answer, never a queue.
func TestGateSerializesFileManagementAndScans(t *testing.T) {
	gate := fileops.NewGate(nil)

	releaseScan, err := gate.BeginScan()
	if err != nil {
		t.Fatalf("BeginScan: %v", err)
	}
	if _, busyErr := gate.BeginManual(); !fileops.IsBusy(busyErr) {
		t.Fatalf("file management during a scan = %v, want busy", busyErr)
	}
	releaseScan()

	releaseManual, err := gate.BeginManual()
	if err != nil {
		t.Fatalf("BeginManual: %v", err)
	}
	if _, busyErr := gate.BeginManual(); !fileops.IsBusy(busyErr) {
		t.Fatalf("a second file operation = %v, want busy (requests serialize)", busyErr)
	}
	if _, busyErr := gate.BeginScan(); !fileops.IsBusy(busyErr) {
		t.Fatalf("a scan during file management = %v, want busy", busyErr)
	}
	releaseManual()

	releaseScan, err = gate.BeginScan()
	if err != nil {
		t.Fatalf("BeginScan after release: %v", err)
	}
	releaseScan()
}

// TestGateRefusesFileManagementWhileASessionIsQueued covers the database half
// of the check: a queued or running planning session or execution outlives any
// request, so file management asks the repository.
func TestGateRefusesFileManagementWhileASessionIsQueued(t *testing.T) {
	active := false
	gate := fileops.NewGate(func() (bool, error) { return active, nil })

	release, err := gate.BeginManual()
	if err != nil {
		t.Fatalf("BeginManual with no session: %v", err)
	}
	release()

	active = true
	if _, err := gate.BeginManual(); !fileops.IsBusy(err) {
		t.Fatalf("file management during a session = %v, want busy", err)
	}
}

// TestGateEnqueueIsAtomicWithTheManualSlot covers the race the gate exists
// for: an enqueue that gets through is visible to the next file management
// request, and vice versa. The enqueue runs inside the same critical section
// that refuses it, which is what makes the pair atomic.
func TestGateEnqueueIsAtomicWithTheManualSlot(t *testing.T) {
	var mu sync.Mutex
	queued := false
	gate := fileops.NewGate(func() (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		return queued, nil
	})

	release, err := gate.BeginManual()
	if err != nil {
		t.Fatalf("BeginManual: %v", err)
	}
	enqueued := false
	if err := gate.Enqueue(func() error {
		enqueued = true
		return nil
	}); !fileops.IsBusy(err) {
		t.Fatalf("an enqueue during file management = %v, want busy", err)
	}
	if enqueued {
		t.Fatal("a refused enqueue must not have run")
	}
	release()

	if err := gate.Enqueue(func() error {
		mu.Lock()
		defer mu.Unlock()
		queued = true
		return nil
	}); err != nil {
		t.Fatalf("Enqueue with a free slot: %v", err)
	}
	// The session it registered is what refuses the next file operation.
	if _, err := gate.BeginManual(); !fileops.IsBusy(err) {
		t.Fatalf("file management after a queued session = %v, want busy", err)
	}
}

// TestGateScanReleaseIsIdempotentSafe guards the release path: a released slot
// never leaves the gate stuck, and an extra release cannot open it for a
// second writer.
func TestGateScanReleaseIsIdempotentSafe(t *testing.T) {
	gate := fileops.NewGate(nil)
	release, err := gate.BeginScan()
	if err != nil {
		t.Fatalf("BeginScan: %v", err)
	}
	release()
	release()

	manual, err := gate.BeginManual()
	if err != nil {
		t.Fatalf("BeginManual after a released scan: %v", err)
	}
	manual()
	if _, err := gate.BeginManual(); err != nil {
		t.Fatalf("BeginManual after release: %v", err)
	}
}

// TestGateReportsTheTasksCheckFailure keeps a broken session check loud: it is
// not a busy answer, so it never looks like a retryable conflict.
func TestGateReportsTheTasksCheckFailure(t *testing.T) {
	boom := errors.New("database is gone")
	gate := fileops.NewGate(func() (bool, error) { return false, boom })
	if _, err := gate.BeginManual(); err == nil || errors.Is(err, boom) == false {
		t.Fatalf("err = %v, want the check failure", err)
	} else if fileops.IsBusy(err) {
		t.Fatal("a failed check is not a busy answer")
	}
}
