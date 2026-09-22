package admission_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/onsei/organizer/backend/internal/admission"
)

// TestGateSerializesMaintenanceWithEverythingElse pins the exclusion the
// idle-time maintenance pass depends on: a scan or a queued session refuses it,
// and while it holds the slot it refuses scans, file management and enqueues -
// which is why a pass can never run alongside the work it would race.
func TestGateSerializesMaintenanceWithEverythingElse(t *testing.T) {
	gate := admission.NewGate(nil)

	releaseScan, err := gate.BeginScan()
	if err != nil {
		t.Fatalf("BeginScan: %v", err)
	}
	if _, busyErr := gate.BeginMaintenance(); !admission.IsBusy(busyErr) {
		t.Fatalf("maintenance during a scan = %v, want busy", busyErr)
	}
	releaseScan()

	releaseMaintenance, err := gate.BeginMaintenance()
	if err != nil {
		t.Fatalf("BeginMaintenance: %v", err)
	}
	if _, busyErr := gate.BeginMaintenance(); !admission.IsBusy(busyErr) {
		t.Fatalf("a second maintenance pass = %v, want busy", busyErr)
	}
	if _, busyErr := gate.BeginScan(); !admission.IsBusy(busyErr) {
		t.Fatalf("a scan during maintenance = %v, want busy", busyErr)
	} else if !strings.Contains(busyErr.Error(), "maintenance") {
		t.Fatalf("a scan refused during maintenance says %q, want it to name maintenance", busyErr)
	}
	if _, busyErr := gate.BeginManual(); !admission.IsBusy(busyErr) {
		t.Fatalf("file management during maintenance = %v, want busy", busyErr)
	}
	if err = gate.Enqueue(func() error { return nil }); !admission.IsBusy(err) {
		t.Fatalf("an enqueue during maintenance = %v, want busy", err)
	}
	releaseMaintenance()

	releaseScan, err = gate.BeginScan()
	if err != nil {
		t.Fatalf("BeginScan after a released maintenance slot: %v", err)
	}
	releaseScan()
}

// TestGateRefusesMaintenanceWhileASessionIsQueued covers the database half of
// the check for the maintenance slot.
func TestGateRefusesMaintenanceWhileASessionIsQueued(t *testing.T) {
	active := false
	gate := admission.NewGate(func() (bool, error) { return active, nil })

	release, err := gate.BeginMaintenance()
	if err != nil {
		t.Fatalf("BeginMaintenance with no session: %v", err)
	}
	release()

	active = true
	if _, err := gate.BeginMaintenance(); !admission.IsBusy(err) {
		t.Fatalf("maintenance during a session = %v, want busy", err)
	}
}

// TestGateSerializesFileManagementAndScans pins the two-way exclusion: a scan
// running refuses file management, and file management in flight refuses a
// scan. Each side sees a busy answer, never a queue.
func TestGateSerializesFileManagementAndScans(t *testing.T) {
	gate := admission.NewGate(nil)

	releaseScan, err := gate.BeginScan()
	if err != nil {
		t.Fatalf("BeginScan: %v", err)
	}
	if _, busyErr := gate.BeginManual(); !admission.IsBusy(busyErr) {
		t.Fatalf("file management during a scan = %v, want busy", busyErr)
	}
	releaseScan()

	releaseManual, err := gate.BeginManual()
	if err != nil {
		t.Fatalf("BeginManual: %v", err)
	}
	if _, busyErr := gate.BeginManual(); !admission.IsBusy(busyErr) {
		t.Fatalf("a second file operation = %v, want busy (requests serialize)", busyErr)
	}
	if _, busyErr := gate.BeginScan(); !admission.IsBusy(busyErr) {
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
	gate := admission.NewGate(func() (bool, error) { return active, nil })

	release, err := gate.BeginManual()
	if err != nil {
		t.Fatalf("BeginManual with no session: %v", err)
	}
	release()

	active = true
	if _, err := gate.BeginManual(); !admission.IsBusy(err) {
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
	gate := admission.NewGate(func() (bool, error) {
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
	}); !admission.IsBusy(err) {
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
	if _, err := gate.BeginManual(); !admission.IsBusy(err) {
		t.Fatalf("file management after a queued session = %v, want busy", err)
	}
}

// TestGateScanReleaseIsIdempotentSafe guards the release path: a released slot
// never leaves the gate stuck, and an extra release cannot open it for a
// second writer.
func TestGateScanReleaseIsIdempotentSafe(t *testing.T) {
	gate := admission.NewGate(nil)
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
	gate := admission.NewGate(func() (bool, error) { return false, boom })
	if _, err := gate.BeginManual(); err == nil || errors.Is(err, boom) == false {
		t.Fatalf("err = %v, want the check failure", err)
	} else if admission.IsBusy(err) {
		t.Fatal("a failed check is not a busy answer")
	}
}
