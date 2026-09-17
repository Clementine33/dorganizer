package workset

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// defaultEncodeWorkers caps the automatic encode concurrency.
const defaultEncodeWorkers = 4

// encodeWindow is how many encode tasks — and with them, open components — one
// session runs at once: the configured value, or min(4, CPU count) when the
// caller left it automatic.
func (s *serviceImpl) encodeWindow() int {
	if s.encodeConcurrency > 0 {
		return s.encodeConcurrency
	}
	return min(defaultEncodeWorkers, runtime.NumCPU())
}

// encodeJob is one encode task of one open unit, as delivered to the pool.
type encodeJob struct {
	unit  *openUnit
	index int
}

// openUnit is one prepared-and-uncommitted unit of a running session. Its
// tasks enter the shared pool; it is sealed once delivery ended and every
// delivered task returned, and committable once it also delivered every task
// it declared and none failed.
type openUnit struct {
	mu        sync.Mutex
	prepared  PreparedUnit
	frozen    ExecutionUnit
	reportIdx int
	expected  int
	delivered int
	completed int
	failure   error
	ended     bool
	done      chan struct{}
	sealed    bool
}

// newOpenUnit tracks one prepared unit; expected is fixed before any delivery.
func newOpenUnit(prepared PreparedUnit, frozen ExecutionUnit, reportIdx int) *openUnit {
	return &openUnit{
		prepared:  prepared,
		frozen:    frozen,
		reportIdx: reportIdx,
		expected:  prepared.EncodeTasks(),
		done:      make(chan struct{}),
	}
}

// taskDelivered counts one task handed to the pool.
func (u *openUnit) taskDelivered() {
	u.mu.Lock()
	u.delivered++
	u.mu.Unlock()
}

// endDelivery marks delivery finished, normally or cut short by a stop, and
// seals the unit when nothing is outstanding.
func (u *openUnit) endDelivery() {
	u.mu.Lock()
	u.ended = true
	u.sealLocked()
	u.mu.Unlock()
}

// taskReturned records one task's outcome; the first failure of the unit wins.
// A task that returned because the session stopped still counts as completed,
// so no unit is left unaccounted.
func (u *openUnit) taskReturned(err error) {
	u.mu.Lock()
	u.completed++
	if err != nil && u.failure == nil {
		u.failure = err
	}
	u.sealLocked()
	u.mu.Unlock()
}

// sealLocked closes the unit's done channel once delivery ended and every
// delivered task returned.
func (u *openUnit) sealLocked() {
	if u.sealed || !u.ended || u.completed < u.delivered {
		return
	}
	u.sealed = true
	close(u.done)
}

// committable reports whether the unit may commit: sealed with every declared
// task delivered and none failed. delivered == completed alone would seal
// early — the first task of two returning while the second is undelivered.
func (u *openUnit) committable() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.sealed && u.delivered == u.expected && u.failure == nil
}

// sessionStop is the first real failure of a session: the report entry and
// frozen unit it belongs to, the tracker of that unit when one of its encode
// tasks failed, and the error. A nil error is the user's cancellation.
type sessionStop struct {
	reportIdx int
	unit      ExecutionUnit
	open      *openUnit
	err       error
}

// encodePool is one session's shared pool: N workers pulling encode tasks from
// one FIFO queue in frozen order. The first real failure is recorded once by
// whoever sees it — a worker as much as the coordinator preparing a unit — and
// stops the encode context, so no teardown decision depends on the coordinator
// being in a receive.
type encodePool struct {
	ctx     context.Context
	cancel  context.CancelFunc
	jobs    chan encodeJob
	wg      sync.WaitGroup
	stopped chan struct{}

	recordOnce sync.Once
	stop       *sessionStop
	stopMu     sync.Mutex
	endOnce    sync.Once
}

// newEncodePool starts the session's workers over one bounded queue.
func newEncodePool(parent context.Context, workers int) *encodePool {
	ctx, cancel := context.WithCancel(parent)
	p := &encodePool{
		ctx:     ctx,
		cancel:  cancel,
		jobs:    make(chan encodeJob, workers),
		stopped: make(chan struct{}),
	}
	for range workers {
		p.wg.Go(func() {
			for job := range p.jobs {
				p.work(job)
			}
		})
	}
	return p
}

// work runs one delivered task: its result is always accounted for on its
// unit, and a real failure records the session's stop.
func (p *encodePool) work(job encodeJob) {
	err := job.unit.prepared.EncodeTask(p.ctx, job.index)
	job.unit.taskReturned(err)
	if err != nil && !isCanceled(err) {
		p.recordStop(sessionStop{
			reportIdx: job.unit.reportIdx,
			unit:      job.unit.frozen,
			open:      job.unit,
			err:       err,
		})
	}
}

// deliver hands one task to the queue. A send the stop cut short is not
// counted: the task never entered the pool.
func (p *encodePool) deliver(unit *openUnit, index int) bool {
	select {
	case p.jobs <- encodeJob{unit: unit, index: index}:
		unit.taskDelivered()
		return true
	case <-p.ctx.Done():
		return false
	}
}

// recordStop records the session's first real failure and stops encoding;
// later failures are ignored, so the first one decides the session.
func (p *encodePool) recordStop(stop sessionStop) {
	p.recordOnce.Do(func() {
		p.stopMu.Lock()
		p.stop = &stop
		p.stopMu.Unlock()
		close(p.stopped)
		p.cancel()
	})
}

// recordedStop is the recorded failure, if any.
func (p *encodePool) recordedStop() *sessionStop {
	p.stopMu.Lock()
	defer p.stopMu.Unlock()
	return p.stop
}

// shutdown closes the queue, cancels the encode context and waits for every
// worker — with its encoder child — to return. Nothing staged may be cleaned
// before that.
func (p *encodePool) shutdown() {
	p.endOnce.Do(func() {
		close(p.jobs)
		p.cancel()
		p.wg.Wait()
	})
}

// isCanceled reports whether a task error is the session stopping rather than
// the work failing.
func isCanceled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// sessionRun is one running execution session: its frozen worklist and report,
// the shared encode pool and the bounded window of open units the coordinator
// commits in frozen order.
type sessionRun struct {
	d      *dispatcher
	ex     *sqlite.PlanExecution
	run    *executionRun
	task   Task
	ctx    context.Context
	report []ExecutionComponentView

	pool      *encodePool
	window    int
	open      []*openUnit
	next      int
	completed int
	doneOps   int
}

// fill opens frozen units into the window, in frozen order, until it is full
// or the worklist ends. It reports the stop that cut admission short.
func (s *sessionRun) fill(units []ExecutionUnit) *sessionStop {
	for len(s.open) < s.window && s.next < len(units) {
		if stop := s.prepare(units[s.next], s.next); stop != nil {
			return stop
		}
		s.next++
	}
	return nil
}

// settleHead closes the head's boundary. A sibling's failure stops the session
// even when this commit landed — the head keeps the facts its own commit
// returned — and so does the head's own stop. It reports whether the session
// ended; otherwise the commit cursor moves on and the next head's progress is
// written before it can block.
func (s *sessionRun) settleHead(
	head *openUnit,
	entry *ExecutionComponentView,
	commitErr error,
) bool {
	if stop := s.pool.recordedStop(); stop != nil {
		s.stopSession(*stop)
		return true
	}
	switch {
	case entry.Status == ExecComponentCanceled:
		s.stopAfterHead(sqlite.ExecStatusCanceled, "CANCELED", "execution canceled")
		return true
	case entry.Status == ExecComponentFailed:
		s.stopAfterHead(sqlite.ExecStatusFailed, entry.ErrorCode, unitStopMessage(head.frozen, entry))
		return true
	case commitErr != nil:
		code, message := taskFailureOf(commitErr)
		s.stopAfterHead(
			sqlite.ExecStatusFailed,
			code,
			fmt.Sprintf("component %s: %s", head.frozen.ID, message),
		)
		return true
	}
	if len(s.open) > 0 {
		s.d.persistProgress(s.ex.ExecutionID, s.completed, s.doneOps, s.open[0].frozen, s.report)
	}
	return false
}

// prepare opens the next frozen unit into the window: it is registered before
// any of its tasks is delivered, so a stop at any instant has a unit to count
// against and clean, and its tasks enter the shared queue in frozen order. A
// failure is attributed to this unit — never to the head.
func (s *sessionRun) prepare(u ExecutionUnit, reportIdx int) *sessionStop {
	prepared, err := s.task.PrepareUnit(s.ctx, s.d.svc.repo, UnitRunInput{
		WorksetRoot: s.run.rootPath,
		Options:     s.run.req.Options,
		Unit:        u,
		Outcome:     s.run.outcomes[u.Index],
	})
	if err != nil {
		if isCanceled(err) {
			return &sessionStop{}
		}
		return &sessionStop{reportIdx: reportIdx, unit: u, err: err}
	}
	unit := newOpenUnit(prepared, u, reportIdx)
	s.open = append(s.open, unit)
	for i := range unit.expected {
		if !s.pool.deliver(unit, i) {
			unit.endDelivery()
			return s.stopReason()
		}
	}
	unit.endDelivery()
	return nil
}

// wait blocks until the head unit can commit, or until the session must stop.
func (s *sessionRun) wait(head *openUnit) *sessionStop {
	select {
	case <-head.done:
		if head.committable() {
			return nil
		}
		return s.stopReason()
	case <-s.pool.stopped:
		return s.stopReason()
	case <-s.ctx.Done():
		return s.stopReason()
	}
}

// stopReason is the stop seen at this instant: the first real failure, or the
// user's cancellation when none was recorded. A real error wins even if a
// cancellation lands in the same window.
func (s *sessionRun) stopReason() *sessionStop {
	if recorded := s.pool.recordedStop(); recorded != nil {
		return recorded
	}
	return &sessionStop{}
}

// stopSession ends a session that stopped before every unit committed. The
// pool drains first — no staged file is touched while an encoder could still
// be writing it — then the first real failure is reported on its own unit's
// entry, and every unit still open is discarded and stays pending.
func (s *sessionRun) stopSession(observed sessionStop) {
	s.pool.shutdown()
	stop := observed
	if recorded := s.pool.recordedStop(); recorded != nil {
		stop = *recorded
	}
	if stop.err == nil {
		s.discardOpen(nil)
		s.d.finishExecution(s.ex, sqlite.ExecStatusCanceled, "CANCELED", "execution canceled", s.report)
		return
	}
	s.reportFailedUnit(stop)
	s.discardOpen(stop.open)
	entry := &s.report[stop.reportIdx]
	s.d.finishExecution(
		s.ex,
		sqlite.ExecStatusFailed,
		entry.ErrorCode,
		unitStopMessage(stop.unit, entry),
		s.report,
	)
}

// stopAfterHead ends a session that stopped at the head's own boundary: the
// head's result is its final report — nothing is discarded for it, since its
// commit was already its own — and every other open unit is discarded and
// stays pending.
func (s *sessionRun) stopAfterHead(status, code, message string) {
	s.pool.shutdown()
	s.discardOpen(nil)
	s.d.finishExecution(s.ex, status, code, message, s.report)
}

// reportFailedUnit fills the failing unit's own entry: a unit whose encode
// task failed reports its discarded facts, and one that never prepared takes
// the error's stage and code.
func (s *sessionRun) reportFailedUnit(stop sessionStop) {
	entry := &s.report[stop.reportIdx]
	if stop.open == nil {
		applyPrepareFailure(entry, stop.err)
		s.countUnit(stop.unit, 0)
		return
	}
	s.recordUnit(stop.unit, entry, stop.open.prepared.Discard(stop.err))
}

// discardOpen cleans every unit still open, skipping the one the stop already
// reported: its staged temps are removed and it stays pending, since no
// filesystem change happened for it. A temp that could not be removed is
// recorded on the entry so the leftover is never lost.
func (s *sessionRun) discardOpen(except *openUnit) {
	for _, unit := range s.open {
		if unit == except {
			continue
		}
		res := unit.prepared.Discard(nil)
		if len(res.Recovery) > 0 {
			entry := &s.report[unit.reportIdx]
			entry.Recovery = append(entry.Recovery, res.Recovery...)
		}
	}
}

// recordUnit applies one unit's result to its report entry, syncs the observed
// inventory and persists the progress at its boundary.
func (s *sessionRun) recordUnit(unit ExecutionUnit, entry *ExecutionComponentView, res UnitResult) {
	applyUnitResult(entry, res)
	s.d.syncUnitInventory(s.run.rootPath, res, entry)
	s.countUnit(unit, entry.CompletedOps)
}

// countUnit advances the session's progress and persists it.
func (s *sessionRun) countUnit(unit ExecutionUnit, completedOps int) {
	s.completed++
	s.doneOps += completedOps
	s.d.persistProgress(s.ex.ExecutionID, s.completed, s.doneOps, unit, s.report)
}
