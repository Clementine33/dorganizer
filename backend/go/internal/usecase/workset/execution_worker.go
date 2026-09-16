package workset

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
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
		if err != nil || ex == nil {
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

// prepareExecution loads the frozen units, the persisted report and the
// frozen unit payloads. A session whose persisted state cannot be resolved
// fails closed with a stable code instead of running a guessed worklist.
func (d *dispatcher) prepareExecution(ex *sqlite.PlanExecution) (*executionRun, bool) {
	req, err := parseExecutionRequest(ex.RequestJSON)
	if err != nil {
		d.finishExecution(ex, sqlite.ExecStatusFailed, "REQUEST_LOAD_FAILED", "failed to load the frozen request", nil)
		return nil, false
	}
	report, err := parseExecutionReport(ex.ReportJSON)
	if err != nil || len(report) != len(req.Units) {
		d.finishExecution(
			ex,
			sqlite.ExecStatusFailed,
			"REQUEST_LOAD_FAILED",
			"failed to load the execution report",
			nil,
		)
		return nil, false
	}
	w, err := d.svc.repo.GetWorkset(ex.WorksetID)
	if err != nil {
		d.finishExecution(ex, sqlite.ExecStatusFailed, "WORKSET_LOAD_FAILED", "failed to load workset", report)
		return nil, false
	}
	plan, err := d.svc.repo.GetPlanDetail(ex.PlanID)
	if err != nil {
		d.finishExecution(
			ex,
			sqlite.ExecStatusFailed,
			"REVISION_LOAD_FAILED",
			"failed to load the frozen revision",
			report,
		)
		return nil, false
	}
	outcomes := make(map[int]json.RawMessage, len(plan.Components))
	for _, c := range plan.Components {
		outcomes[c.ComponentIndex] = json.RawMessage(c.OutcomeJSON)
	}
	return &executionRun{req: req, report: report, rootPath: w.RootPath, outcomes: outcomes, plan: plan}, true
}

// executeRun runs one claimed session: units in frozen order, first failure
// stops admission, and every unit boundary persists its facts. The business
// work of one unit belongs to the operation's task.
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
			run.report,
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
	// halfway through (ADR 0004 §4).
	if code, message, verified := d.verifyInputs(ctx, ex, run); !verified {
		status := sqlite.ExecStatusFailed
		if code == "CANCELED" {
			status = sqlite.ExecStatusCanceled
		}
		d.finishExecution(ex, status, code, message, report)
		return
	}

	completed, doneOps := ex.CompletedComponents, ex.CompletedOperations

	for i := range req.Units {
		u := req.Units[i]
		if ctx.Err() != nil {
			d.finishExecution(ex, sqlite.ExecStatusCanceled, "CANCELED", "execution canceled", report)
			return
		}
		// The report is indexed by the frozen unit order; a mismatch would
		// mislabel facts, so the session fails closed instead.
		if report[i].ComponentIndex != u.Index {
			d.finishExecution(
				ex,
				sqlite.ExecStatusFailed,
				"REQUEST_LOAD_FAILED",
				"worklist and report disagree",
				report,
			)
			return
		}
		outcome, ok := run.outcomes[u.Index]
		if !ok {
			d.finishExecution(
				ex,
				sqlite.ExecStatusFailed,
				"REVISION_LOAD_FAILED",
				"frozen component is unreadable",
				report,
			)
			return
		}
		d.persistProgress(ex.ExecutionID, completed, doneOps, u, report)

		res, runErr := task.RunUnit(ctx, d.svc.repo, UnitRunInput{
			WorksetRoot: run.rootPath,
			Options:     req.Options,
			Unit:        u,
			Outcome:     outcome,
		})
		if runErr != nil {
			code, message := "COMPONENT_FAILED", runErr.Error()
			if werr, ok := AsError(runErr); ok {
				code, message = werr.Code, werr.Message
			}
			d.finishExecution(ex, sqlite.ExecStatusFailed, code, message, report)
			return
		}
		entry := &report[i]
		applyUnitResult(entry, res)
		d.syncUnitInventory(run.rootPath, res, entry)
		completed++
		doneOps += entry.CompletedOps
		d.persistProgress(ex.ExecutionID, completed, doneOps, u, report)

		switch entry.Status {
		case ExecComponentCanceled:
			d.finishExecution(ex, sqlite.ExecStatusCanceled, "CANCELED", "execution canceled", report)
			return
		case ExecComponentFailed:
			// First failure stops admission: every later component stays pending
			// in the report and is never executed.
			message := fmt.Sprintf("component %s stopped at %s: %s", u.ID, entry.Stage, entry.ErrorMessage)
			d.finishExecution(ex, sqlite.ExecStatusFailed, entry.ErrorCode, message, report)
			return
		}
	}
	d.persistProgress(ex.ExecutionID, completed, doneOps, ExecutionUnit{}, report)
	d.finishExecution(ex, sqlite.ExecStatusSucceeded, "", "", report)
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

// persistProgress records the component-boundary progress plus the full report.
func (d *dispatcher) persistProgress(
	executionID string,
	completed, doneOps int,
	u ExecutionUnit,
	report []ExecutionComponentView,
) {
	_ = d.svc.repo.UpdateExecutionProgress(executionID, sqlite.ExecutionProgress{
		CompletedComponents:   completed,
		CompletedOperations:   doneOps,
		CurrentRoot:           u.RootPath,
		CurrentComponentID:    u.ID,
		CurrentComponentIndex: u.Index,
		CurrentPhase:          ExecPhaseComponent,
	}, mustJSON(report))
}

// finishExecution writes the terminal status with the given report.
func (d *dispatcher) finishExecution(
	ex *sqlite.PlanExecution,
	status, code, message string,
	report []ExecutionComponentView,
) {
	if report == nil {
		report = []ExecutionComponentView{}
	}
	_ = d.svc.repo.FinishExecution(ex.ExecutionID, status, code, message, mustJSON(report))
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

// parseExecutionReport decodes the persisted per-component report.
func parseExecutionReport(raw string) ([]ExecutionComponentView, error) {
	if raw == "" {
		return []ExecutionComponentView{}, nil
	}
	var report []ExecutionComponentView
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil, err
	}
	return report, nil
}

// syncUnitInventory applies this unit's observed disk changes to the entries
// inventory: removed sources lose their row, committed outputs and soft-delete
// destinations are refreshed from disk. A sync failure (or a task-side stat
// failure) is disclosed on the unit instead of being reported as "unchanged".
func (d *dispatcher) syncUnitInventory(worksetRoot string, res UnitResult, entry *ExecutionComponentView) {
	if res.InventoryError != "" {
		entry.InventorySynced = false
		entry.InventorySyncError = res.InventoryError
		return
	}
	if err := d.svc.repo.SyncObservedInventory(worksetRoot, res.InventoryRemoved, res.InventoryRefreshed); err != nil {
		entry.InventorySynced = false
		entry.InventorySyncError = err.Error()
		return
	}
	entry.InventorySynced = true
}
