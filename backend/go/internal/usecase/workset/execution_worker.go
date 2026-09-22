package workset

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
)

// Execution component statuses reported per component.
const (
	ExecComponentPending   = "pending"
	ExecComponentSucceeded = "succeeded"
	ExecComponentFailed    = "failed"
	ExecComponentCanceled  = "canceled"
)

// ExecPhaseComponent is the phase value reported while a session runs. It is
// the finest granularity the worker can observe: the stages inside one
// component run (precheck/materialize/validate/commit/remove) are reported by
// that component's own result once it returns.
const ExecPhaseComponent = "component"

// cancelPollInterval is how often a running session observes the cooperative
// cancel flag. Cancellation is honored at the component's safe stage
// boundaries, never mid-write.
const cancelPollInterval = 500 * time.Millisecond

// Result writes are retried a bounded number of times before the session stops.
// The retry is for a transient lock or a momentarily busy disk: it re-runs the
// write, never the encode or the commit that produced the result.
const (
	resultWriteAttempts = 3
	resultWriteBackoff  = 50 * time.Millisecond
)

// runExecutionLoop is the single execution worker: sessions run one at a time,
// globally serialized, so two worksets can never write overlapping roots
// concurrently and a scan can never overlap an execution.
func (d *dispatcher) runExecutionLoop() {
	for {
		select {
		case <-d.done:
			return
		default:
		}
		ex, err := d.svc.repo.NextQueuedExecution()
		if err != nil {
			if !d.retryClaim(d.execWakeC, err) {
				return
			}
			continue
		}
		if ex == nil {
			select {
			case <-d.done:
				return
			case <-d.execWakeC:
			}
			continue
		}
		d.executeRun(ex)
	}
}

// executionRun is the resolved state of one claimed session before any
// component runs.
type executionRun struct {
	req      *executionRequest
	report   []ExecutionComponentView
	rootPath string
	outcomes map[int]json.RawMessage
	plan     *sqlite.PlanDetail
}

// prepareExecution loads the frozen units, the component results already
// recorded for them and the frozen unit payloads. A session whose persisted
// state cannot be resolved fails closed with a stable code instead of running a
// guessed worklist.
func (d *dispatcher) prepareExecution(ex *sqlite.PlanExecution) (*executionRun, bool) {
	req, err := parseExecutionRequest(ex.RequestJSON)
	if err != nil {
		d.finishExecution(ex, sqlite.ExecStatusFailed, "REQUEST_LOAD_FAILED", "failed to load the frozen request")
		return nil, false
	}
	report := initialComponentReport(req.Units)
	results, err := d.svc.repo.ListExecutionComponentResults(ex.ExecutionID, 0, 0)
	if err != nil {
		d.finishExecution(
			ex,
			sqlite.ExecStatusFailed,
			"REQUEST_LOAD_FAILED",
			"failed to load the component results",
		)
		return nil, false
	}
	reportIdx := make(map[int]int, len(report))
	for i := range report {
		reportIdx[report[i].ComponentIndex] = i
	}
	for _, res := range results {
		at, ok := reportIdx[res.ComponentIndex]
		if !ok {
			d.finishExecution(
				ex,
				sqlite.ExecStatusFailed,
				"REQUEST_LOAD_FAILED",
				"a component result does not belong to the frozen worklist",
			)
			return nil, false
		}
		applyComponentResult(&report[at], res)
	}
	w, err := d.svc.repo.GetWorkset(ex.WorksetID)
	if err != nil {
		d.finishExecution(ex, sqlite.ExecStatusFailed, "WORKSET_LOAD_FAILED", "failed to load workset")
		return nil, false
	}
	plan, err := d.svc.repo.GetPlanDetail(ex.PlanID)
	if err != nil {
		d.finishExecution(
			ex,
			sqlite.ExecStatusFailed,
			"REVISION_LOAD_FAILED",
			"failed to load the frozen revision",
		)
		return nil, false
	}
	outcomes := make(map[int]json.RawMessage, len(plan.Components))
	for _, c := range plan.Components {
		outcomes[c.ComponentIndex] = json.RawMessage(c.OutcomeJSON)
	}
	return &executionRun{req: req, report: report, rootPath: w.RootPath, outcomes: outcomes, plan: plan}, true
}

// executeRun runs one claimed session: the frozen units are prepared into a
// bounded window, their encode tasks share one pool across every component and
// member folder, and a single coordinator commits them strictly in frozen
// order. The first failure stops admission, and every unit boundary persists
// its facts. The business work of one unit belongs to the operation's task.
func (d *dispatcher) executeRun(ex *sqlite.PlanExecution) {
	run, ok := d.prepareExecution(ex)
	if !ok {
		return
	}
	task, taskErr := d.svc.requireTask(ex.OperationType)
	if taskErr != nil {
		d.finishExecution(
			ex,
			sqlite.ExecStatusFailed,
			"REQUEST_LOAD_FAILED",
			"operation task is not registered",
		)
		return
	}
	req, report := run.req, run.report

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopWatch := make(chan struct{})
	go watchExecutionCancel(ctx, cancel, stopWatch, d.svc.repo, ex.ExecutionID)
	defer close(stopWatch)

	// The run revalidates the revision's recorded inputs against the disk before
	// the first write: a folder that drifted after planning stops the session
	// here, with every file untouched, instead of failing component by component
	// halfway through (ADR 0001 §2).
	if code, message, verified := d.verifyInputs(ctx, ex, run); !verified {
		status := sqlite.ExecStatusFailed
		if code == "CANCELED" {
			status = sqlite.ExecStatusCanceled
		}
		d.finishExecution(ex, status, code, message)
		return
	}
	// The report is indexed by the frozen unit order and every unit needs its
	// frozen payload; a mismatch would mislabel facts, so the session fails
	// closed before anything is written.
	if code, message, ready := worklistReady(req, run); !ready {
		d.finishExecution(ex, sqlite.ExecStatusFailed, code, message)
		return
	}

	session := &sessionRun{
		d:      d,
		ex:     ex,
		run:    run,
		task:   task,
		ctx:    ctx,
		report: report,
		window: d.svc.encodeWindow(),
	}
	// An empty execution keeps its existing completion rules: no worker pool
	// starts and no frozen unit is indexed.
	if len(req.Units) == 0 {
		d.finishExecution(ex, sqlite.ExecStatusSucceeded, "", "")
		return
	}
	session.pool = newEncodePool(ctx, cancel, session.window)

	for i := range req.Units { // one commit per frozen unit, in frozen order
		if stop := session.stopNow(); stop != nil {
			session.stopSession(*stop)
			return
		}
		// The head being worked on is never displayed as a previous one —
		// including at N = 1, where the window empties after every commit. Each
		// boundary publishes the next head with the result it commits, so only
		// the first unit needs a write of its own, before preparation or
		// delivery can block (ADR 0007 decision 4).
		if i == 0 {
			d.persistPosition(ex.ExecutionID, req.Units[i])
		}
		if stop := session.fill(req.Units); stop != nil {
			session.stopSession(*stop)
			return
		}
		head := session.open[0]
		if stop := session.wait(head); stop != nil {
			session.stopSession(*stop)
			return
		}
		// Commit admission: a sealed unit is no commit permit, so the stop is
		// rechecked here and again by Commit before its first mutation.
		if stop := session.stopNow(); stop != nil {
			session.stopSession(*stop)
			return
		}
		res, commitErr := head.prepared.Commit(session.ctx)
		entry := &report[head.reportIdx]
		session.open = session.open[1:]
		if writeErr := session.recordUnit(head.frozen, entry, res, nextUnit(req.Units, i)); writeErr != nil {
			// The component committed on disk but its result is not durable.
			// Nothing here may claim otherwise: the session stops, and the
			// teardown leaves the component's real facts unpersisted rather
			// than inventing them.
			code, message := taskFailureOf(writeErr)
			log.Printf(
				"execution %s: component %s committed but its result could not be persisted: %v",
				ex.ExecutionID, head.frozen.ID, writeErr,
			)
			session.stopAfterHead(
				sqlite.ExecStatusFailed,
				code,
				fmt.Sprintf("component %s: %s", head.frozen.ID, message),
			)
			return
		}
		if session.settleHead(head, entry, commitErr) {
			return
		}
	}
	session.pool.shutdown()
	d.finishExecution(ex, sqlite.ExecStatusSucceeded, "", "")
}

// worklistReady checks the frozen worklist's invariant before any write: every
// unit has its frozen payload. The report is built from the worklist itself, so
// it cannot disagree with it.
func worklistReady(req *executionRequest, run *executionRun) (code, message string, ready bool) {
	for i := range req.Units {
		if _, ok := run.outcomes[req.Units[i].Index]; !ok {
			return "REVISION_LOAD_FAILED", "frozen component is unreadable", false
		}
	}
	return "", "", true
}

// nextUnit is the unit after position i in the frozen worklist, or the empty
// position when i is the last one: that is what a boundary write publishes.
func nextUnit(units []ExecutionUnit, i int) ExecutionUnit {
	if i+1 < len(units) {
		return units[i+1]
	}
	return ExecutionUnit{}
}

// applyPrepareFailure records a failed preparation on the unit's own entry: the
// error's stage and code belong to that unit, never to a neighbour the window
// happened to open first.
func applyPrepareFailure(entry *ExecutionComponentView, err error) {
	entry.Status = ExecComponentFailed
	if werr, ok := AsError(err); ok {
		entry.Stage, entry.ErrorCode, entry.ErrorMessage = werr.Stage, werr.Code, werr.Message
		return
	}
	entry.ErrorCode, entry.ErrorMessage = "COMPONENT_FAILED", err.Error()
}

// unitStopMessage names the unit and the stage it stopped in.
func unitStopMessage(unit ExecutionUnit, entry *ExecutionComponentView) string {
	if entry.Stage == "" {
		return fmt.Sprintf("component %s stopped: %s", unit.ID, entry.ErrorMessage)
	}
	return fmt.Sprintf("component %s stopped at %s: %s", unit.ID, entry.Stage, entry.ErrorMessage)
}

// taskFailureOf extracts the stable code and message of a session-level unit
// failure.
func taskFailureOf(err error) (code, message string) {
	if werr, ok := AsError(err); ok {
		return werr.Code, werr.Message
	}
	return "COMPONENT_FAILED", err.Error()
}

// verifyInputs refreshes the scanned inventory of the session's roots and
// re-judges the revision's recorded inputs against it. A session that cannot
// verify the disk does not write to it: it answers the terminal status the run
// must end on, or verified = true to proceed.
func (d *dispatcher) verifyInputs(
	ctx context.Context,
	ex *sqlite.PlanExecution,
	run *executionRun,
) (code, message string, verified bool) {
	if scanErr := d.svc.refreshRoots(ctx, unitRoots(run.req.Units), run.rootPath, nil); scanErr != nil {
		if ctx.Err() != nil {
			return "CANCELED", "execution canceled", false
		}
		return "SCAN_FAILED", "failed to refresh the scanned inventory before executing", false
	}
	health, healthErr := d.svc.revisionHealth(ex.OperationType, run.plan)
	if healthErr != nil {
		return "REVISION_LOAD_FAILED", "failed to evaluate the revision's inputs", false
	}
	if health.InputMoved {
		return ExecBlockedInput,
			"the scanned inventory no longer matches the revision's recorded inputs; regenerate the plan",
			false
	}
	return "", "", true
}

// applyUnitResult records one unit run's observed facts on its report entry:
// the outcome lists, the completed count and the terminal status.
func applyUnitResult(entry *ExecutionComponentView, res UnitResult) {
	entry.Committed = res.Committed
	entry.Removed = res.Removed
	entry.Remaining = res.Remaining
	entry.Recovery = res.Recovery
	entry.CompletedOps = len(res.Committed) + len(res.Removed)
	entry.Stage = res.Stage
	entry.ErrorCode = res.ErrorCode
	entry.ErrorMessage = res.ErrorMessage
	switch {
	case res.Canceled:
		entry.Status = ExecComponentCanceled
	case res.ErrorCode != "":
		entry.Status = ExecComponentFailed
	default:
		entry.Status = ExecComponentSucceeded
	}
}

// executionPositionOf names the component a session is working on, or the empty
// position once it has moved past the last one.
func executionPositionOf(u ExecutionUnit) sqlite.ExecutionProgress {
	return sqlite.ExecutionProgress{
		CurrentRoot:           u.RootPath,
		CurrentComponentID:    u.ID,
		CurrentComponentIndex: u.Index,
		CurrentPhase:          ExecPhaseComponent,
	}
}

// persistPosition records which component the run is on without touching any
// component result. A failure is not fatal: the next boundary write states the
// position again along with the result it commits.
func (d *dispatcher) persistPosition(executionID string, u ExecutionUnit) {
	_ = d.svc.repo.UpdateExecutionPosition(executionID, executionPositionOf(u))
}

// persistUnitResult commits one component's result and moves the session to the
// next component in one short transaction, retrying a bounded number of times.
// The retry replays the write only: the filesystem work already happened, and
// the result row is replaced wholesale, so a replay cannot double-count. A
// failure that survives the retries is returned — the caller stops the session
// rather than pretending the result is durable.
func (d *dispatcher) persistUnitResult(
	executionID string,
	entry *ExecutionComponentView,
	next ExecutionUnit,
) error {
	var err error
	for range resultWriteAttempts {
		err = d.svc.repo.SaveExecutionComponentResult(
			executionID,
			componentResultRow(entry),
			executionPositionOf(next),
		)
		if err == nil {
			return nil
		}
		time.Sleep(resultWriteBackoff)
	}
	return NewError(
		ErrKindInternal,
		"RESULT_PERSIST_FAILED",
		"the component result could not be persisted",
		err,
	)
}

// finishExecution writes the terminal status. A failure is logged rather than
// swallowed: the row then stays running until the startup sweep marks it
// interrupted, and that sweep is a fallback, not a recovery of the session's
// real outcome.
func (d *dispatcher) finishExecution(ex *sqlite.PlanExecution, status, code, message string) {
	err := d.svc.repo.FinishExecution(ex.ExecutionID, status, code, message)
	if err != nil {
		log.Printf("execution %s: terminal write failed (status=%s): %v", ex.ExecutionID, status, err)
	}
}

// watchExecutionCancel polls the cooperative cancel flag of a running session
// and cancels the run's context. A canceled queued session never reaches here
// (the claim skips canceled rows).
func watchExecutionCancel(
	ctx context.Context,
	cancel context.CancelFunc,
	stop <-chan struct{},
	repo *sqlite.Repository,
	executionID string,
) {
	ticker := time.NewTicker(cancelPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur, err := repo.GetExecution(executionID)
			if err == nil && cur.CancelRequested {
				cancel()
				return
			}
		}
	}
}

// parseExecutionRequest decodes the frozen request: the task-owned options and
// the ordered units.
func parseExecutionRequest(raw string) (*executionRequest, error) {
	var req executionRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// syncUnitInventory applies this unit's observed disk changes to the entries
// inventory: removed sources lose their row, committed outputs and soft-delete
// destinations are refreshed from disk, and the outputs this unit generated are
// credentialed so the next plan can accept them without re-encoding. A sync
// failure (or a task-side stat failure) is disclosed on the unit instead of
// being reported as "unchanged".
func (d *dispatcher) syncUnitInventory(worksetRoot string, res UnitResult, entry *ExecutionComponentView) {
	if res.InventoryError != "" {
		entry.InventorySynced = false
		entry.InventorySyncError = res.InventoryError
		return
	}
	if err := d.svc.repo.SyncObservedInventory(
		worksetRoot,
		res.InventoryRemoved,
		res.InventoryRefreshed,
		res.Generated,
	); err != nil {
		entry.InventorySynced = false
		entry.InventorySyncError = err.Error()
		return
	}
	entry.InventorySynced = true
}
