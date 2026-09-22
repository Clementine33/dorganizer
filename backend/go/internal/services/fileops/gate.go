// Package fileops owns direct file management inside a library member
// (rename, move, soft delete) and the process-wide admission control that
// keeps it from interleaving with the scanning, planning and execution paths
// that also read and write the same trees (ADR 0002 §1, §2).
package fileops

import (
	"errors"
	"fmt"
	"sync"
)

// BusyError is returned when admission is refused. A refusal is a normal
// answer, not a failure: the caller reports it and the user retries once the
// other side is done. Reason names what is in the way.
type BusyError struct {
	Reason string
}

func (e *BusyError) Error() string { return "busy: " + e.Reason }

// IsBusy reports whether err is an admission refusal.
func IsBusy(err error) bool {
	var busy *BusyError
	return errors.As(err, &busy)
}

// ActiveTasks reports whether a queued or running planning session or
// execution exists. Those sessions live in the database and outlive any single
// request, so the gate asks rather than tracks them.
type ActiveTasks func() (bool, error)

// Gate serializes the kinds of disk access that must not overlap: the managed
// task paths (scan, planning, execution), direct file management, and the
// idle-time database maintenance pass.
//
//	direct file management: refused while any scan is running or any session is
//	                       queued or running
//	scan:                  refused while direct file management is in flight
//	task enqueue:          refused while direct file management is in flight
//	maintenance:           refused while either of the above is in flight, and
//	                       refuses them while it holds the slot
//
// Every check and its registration happen under one mutex. An enqueue inserts
// its session row inside that mutex, so a file management request that starts
// afterwards sees the session instead of racing it, and vice versa. The
// maintenance slot follows the same rule: it is acquired only when nothing is
// running, and a task that arrives while it is held is refused, so a pass can
// never start alongside the work it would race.
//
// The gate deliberately does not change how scans, generations and executions
// behave towards each other: those rules stay where they were.
type Gate struct {
	mu     sync.Mutex
	manual bool
	maint  bool
	scans  int
	tasks  ActiveTasks
}

// NewGate creates the process-wide gate. tasks reports the database-side
// sessions; nil means only in-process scans are considered.
func NewGate(tasks ActiveTasks) *Gate {
	return &Gate{tasks: tasks}
}

// BeginScan registers one scanning operation (a library scan or a member
// refresh). The returned release must be called exactly once when the scan
// finishes.
func (g *Gate) BeginScan() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.manual {
		return nil, &BusyError{Reason: "direct file management is in progress"}
	}
	if g.maint {
		return nil, &BusyError{Reason: "database maintenance is in progress"}
	}
	g.scans++
	return g.releaseScan, nil
}

// BeginManual acquires the direct-file-management slot. It is refused while
// any scan is running or any session is queued or running, and while another
// direct file management request holds the slot: requests serialize, and a
// busy slot is answered instead of queued.
//
// Library root changes and deletions take this same slot, so they cannot
// interleave with file management.
func (g *Gate) BeginManual() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.manual {
		return nil, &BusyError{Reason: "another file operation is in progress"}
	}
	if g.maint {
		return nil, &BusyError{Reason: "database maintenance is in progress"}
	}
	if g.scans > 0 {
		return nil, &BusyError{Reason: "a scan is running"}
	}
	if g.tasks != nil {
		active, err := g.tasks()
		if err != nil {
			return nil, fmt.Errorf("check active sessions: %w", err)
		}
		if active {
			return nil, &BusyError{Reason: "a planning session or execution is queued or running"}
		}
	}
	g.manual = true
	return g.releaseManual, nil
}

// BeginMaintenance acquires the exclusive slot for the idle-time database
// maintenance pass. It is refused on the same conditions BeginManual answers -
// direct file management in flight, a scan running, a session queued or running
// - because the pass deletes rows and rewrites the database file, and must not
// do either while a task reads or writes it.
//
// The returned release belongs to one batch, not to a whole pass: a pass is a
// sequence of small batches, and holding the slot across all of them would
// refuse every task for its duration. Between batches the pass releases the
// slot and asks again, so a task that arrives in that gap is admitted and the
// pass gives up until the next tick.
func (g *Gate) BeginMaintenance() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.manual {
		return nil, &BusyError{Reason: "direct file management is in progress"}
	}
	if g.maint {
		return nil, &BusyError{Reason: "database maintenance is already in progress"}
	}
	if g.scans > 0 {
		return nil, &BusyError{Reason: "a scan is running"}
	}
	if g.tasks != nil {
		active, err := g.tasks()
		if err != nil {
			return nil, fmt.Errorf("check active sessions: %w", err)
		}
		if active {
			return nil, &BusyError{Reason: "a planning session or execution is queued or running"}
		}
	}
	g.maint = true
	return g.releaseMaintenance, nil
}

// Enqueue runs a session-enqueue write under the gate, refusing while direct
// file management or database maintenance holds the slot. The write itself
// happens inside the critical section: a file management request that arrives
// after it sees the queued session in the database, never a half-registered
// one.
func (g *Gate) Enqueue(fn func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.manual {
		return &BusyError{Reason: "direct file management is in progress"}
	}
	if g.maint {
		return &BusyError{Reason: "database maintenance is in progress"}
	}
	return fn()
}

func (g *Gate) releaseScan() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.scans > 0 {
		g.scans--
	}
}

func (g *Gate) releaseManual() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.manual = false
}

func (g *Gate) releaseMaintenance() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.maint = false
}
