// Package maintenance owns the background pass that keeps the database file
// from growing without bound: it deletes the rows retention no longer keeps and
// returns the pages those deletes freed to the filesystem.
//
// The pass runs only while the application is idle, and it stays on the clock
// rather than on the disk: it asks the process-wide admission gate for the slot
// direct file management uses, holds it for one small batch at a time, and
// abandons the whole pass when a task arrives - so maintenance never runs
// alongside a scan, a planning session or an execution. A pass that could not
// finish continues on the next tick instead of waiting out the interval.
//
// It is deliberately not a service with its own state: the application starts
// it, the application's context stops it, and nothing else observes it.
package maintenance

import (
	"context"
	"log"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/fileops"
)

// Defaults for the Options fields a caller leaves zero.
const (
	DefaultInterval       = 24 * time.Hour
	DefaultTick           = 15 * time.Minute
	DefaultBatchRows      = 500
	DefaultMaxBatches     = 8
	DefaultVacuumPages    = 256
	DefaultMaxVacuumSteps = 8
	DefaultYieldDelay     = 250 * time.Millisecond
)

// Options configures a Loop.
type Options struct {
	// ScanRetention and GenerationRetention are the windows the pass derives
	// its cutoffs from, recomputed every pass so a long-running process never
	// deletes against a stale cutoff. There is no default: they are policy, and
	// GenerationRetention has to stay aligned with the workset
	// idempotency-key horizon - a generation row is the record of a key, so
	// purging it early would release a key that is still inside its guarantee.
	ScanRetention       time.Duration
	GenerationRetention time.Duration

	// Interval is how long a completed pass defers the next one; Tick is how
	// often the loop looks for work at all, and so how soon a pass that was
	// refused or ran out of budget is resumed.
	Interval time.Duration
	Tick     time.Duration

	// BatchRows caps the rows one table's delete removes per transaction, and
	// MaxBatches caps how many batches one pass runs.
	BatchRows  int
	MaxBatches int

	// VacuumPages caps how many free pages one reclaim step asks for, and
	// MaxVacuumSteps caps how many steps one pass runs.
	VacuumPages    int
	MaxVacuumSteps int

	// YieldDelay is how long a pass waits between batches with the slot
	// released. It is what gives a task that arrives mid-pass a real chance to
	// be admitted rather than refused.
	YieldDelay time.Duration
}

func (o Options) withDefaults() Options {
	for _, d := range []struct {
		field *time.Duration
		def   time.Duration
	}{
		{&o.Interval, DefaultInterval},
		{&o.Tick, DefaultTick},
		{&o.YieldDelay, DefaultYieldDelay},
	} {
		if *d.field <= 0 {
			*d.field = d.def
		}
	}
	for _, i := range []struct {
		field *int
		def   int
	}{
		{&o.BatchRows, DefaultBatchRows},
		{&o.MaxBatches, DefaultMaxBatches},
		{&o.VacuumPages, DefaultVacuumPages},
		{&o.MaxVacuumSteps, DefaultMaxVacuumSteps},
	} {
		if *i.field <= 0 {
			*i.field = i.def
		}
	}
	return o
}

// Loop is the maintenance pass scheduler. Start it once, at process start, and
// run it until the context it is given is cancelled: it holds no state that
// outlives a pass.
type Loop struct {
	repo    *sqlite.Repository
	acquire func() (func(), error)
	opts    Options
}

// New builds a Loop over repo. acquire is the admission gate's maintenance
// entry point (fileops.Gate.BeginMaintenance) - passing it in rather than
// reaching for the gate keeps the loop independent of how admission is decided.
func New(repo *sqlite.Repository, acquire func() (func(), error), opts Options) *Loop {
	return &Loop{repo: repo, acquire: acquire, opts: opts.withDefaults()}
}

// Run blocks until ctx is cancelled, running a pass whenever one is due. The
// first pass runs immediately: at startup nothing else is running, so it is the
// cheapest moment there is.
//
// A completed pass waits out the interval; a pass that did not complete - it was
// refused, or ran out of budget - waits one tick and tries again, so a busy
// application is not left unclean for a whole interval.
func (l *Loop) Run(ctx context.Context) {
	for {
		if l.pass(ctx) {
			if !wait(ctx, l.opts.Interval) {
				return
			}
			continue
		}
		if !wait(ctx, l.opts.Tick) {
			return
		}
	}
}

// wait sleeps for d and reports false when ctx ends first.
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// slot is the admission slot held for the duration of one batch.
type slot struct {
	acquire func() (func(), error)
	release func()
}

// hold takes the slot, or reports false when it is not available. Idempotent:
// holding it already is success.
//
// A refusal is a normal answer - the application is busy, which is exactly what
// this pass waits for - so it is not logged. Any other error means the
// admission check itself is broken (its active-session query failed, say), and
// that ends the pass loudly rather than being retried every tick as if it were
// contention.
func (s *slot) hold() bool {
	if s.release != nil {
		return true
	}
	release, err := s.acquire()
	if err != nil {
		if !fileops.IsBusy(err) {
			log.Printf("maintenance: admission check failed: %v", err)
		}
		return false
	}
	s.release = release
	return true
}

// drop hands the slot back.
func (s *slot) drop() {
	if s.release != nil {
		s.release()
		s.release = nil
	}
}

// yield hands the slot back, waits, and takes it again, reporting false if the
// task that took it in the gap now has it - the pass gives up until the next
// tick rather than competing for the slot it just released.
func (s *slot) yield(ctx context.Context, wait time.Duration) bool {
	s.drop()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	return s.hold()
}
