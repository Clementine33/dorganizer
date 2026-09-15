package workset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	appconfig "github.com/onsei/organizer/backend/internal/config"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/execute"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
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

// softDeleteDirName mirrors the soft-delete convention owned by the execute
// service: removed media is preserved at <member root>/Delete/<relative path>.
const softDeleteDirName = "Delete"

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
	outcomes map[int]string
}

// prepareExecution loads the frozen worklist, the persisted report and the
// component outcomes. A session whose persisted state cannot be resolved fails
// closed with a stable code instead of running a guessed worklist.
func (d *dispatcher) prepareExecution(ex *sqlite.PlanExecution) (*executionRun, bool) {
	req, err := parseExecutionRequest(ex.RequestJSON)
	if err != nil {
		d.finishExecution(ex, sqlite.ExecStatusFailed, "REQUEST_LOAD_FAILED", "failed to load the frozen request", nil)
		return nil, false
	}
	report, err := parseExecutionReport(ex.ReportJSON)
	if err != nil || len(report) != len(req.Components) {
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
	plan, err := d.svc.repo.GetWorkflowPlanDetail(ex.PlanID)
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
	outcomes := make(map[int]string, len(plan.Components))
	for _, c := range plan.Components {
		outcomes[c.ComponentIndex] = c.OutcomeJSON
	}
	return &executionRun{req: req, report: report, rootPath: w.RootPath, outcomes: outcomes}, true
}

// executeRun runs one claimed session: components in frozen order, first
// failure stops admission, and every component boundary persists its facts.
func (d *dispatcher) executeRun(ex *sqlite.PlanExecution) {
	run, ok := d.prepareExecution(ex)
	if !ok {
		return
	}
	req, report := run.req, run.report

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopWatch := make(chan struct{})
	go watchExecutionCancel(ctx, cancel, stopWatch, d.svc.repo, ex.ExecutionID)
	defer close(stopWatch)

	mode := execute.DeleteMode(req.DeleteMode)
	tools := d.svc.executionTools()
	completed, doneOps := ex.CompletedComponents, ex.CompletedOperations

	for i := range req.Components {
		c := req.Components[i]
		if ctx.Err() != nil {
			d.finishExecution(ex, sqlite.ExecStatusCanceled, "CANCELED", "execution canceled", report)
			return
		}
		// The report is indexed by the frozen worklist order; a mismatch would
		// mislabel facts, so the session fails closed instead.
		if report[i].ComponentIndex != c.ComponentIndex {
			d.finishExecution(
				ex,
				sqlite.ExecStatusFailed,
				"REQUEST_LOAD_FAILED",
				"worklist and report disagree",
				report,
			)
			return
		}
		outcome, ok := componentOutcome(run.outcomes, c.ComponentIndex)
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
		d.persistProgress(ex.ExecutionID, completed, doneOps, c, report)

		res, runErr := execute.RunComponent(ctx, execute.ComponentRunRequest{
			Root:       c.RootPath,
			Component:  outcome,
			Specs:      c.Profile,
			DeleteMode: mode,
			Tools:      tools,
		})
		entry := &report[i]
		applyComponentResult(entry, res, runErr, ctx)
		d.syncComponentInventory(run.rootPath, c, res, entry)
		completed++
		doneOps += entry.CompletedOps
		d.persistProgress(ex.ExecutionID, completed, doneOps, c, report)

		switch entry.Status {
		case ExecComponentCanceled:
			d.finishExecution(ex, sqlite.ExecStatusCanceled, "CANCELED", "execution canceled", report)
			return
		case ExecComponentFailed:
			// First failure stops admission: every later component stays pending
			// in the report and is never executed.
			message := fmt.Sprintf("component %s stopped at %s: %s", c.ComponentID, entry.Stage, entry.ErrorMessage)
			d.finishExecution(ex, sqlite.ExecStatusFailed, entry.ErrorCode, message, report)
			return
		}
	}
	d.persistProgress(ex.ExecutionID, completed, doneOps, executionComponent{}, report)
	d.finishExecution(ex, sqlite.ExecStatusSucceeded, "", "", report)
}

// applyComponentResult records one component run's observed facts on its
// report entry: the outcome lists, the completed count and the terminal status.
func applyComponentResult(
	entry *ExecutionComponentView,
	res execute.ComponentRunResult,
	runErr error,
	ctx context.Context,
) {
	entry.Committed = nonNil(res.Committed)
	entry.Removed = nonNil(res.Removed)
	entry.Remaining = nonNil(res.Remaining)
	entry.Recovery = nonNil(res.Recovery)
	entry.CompletedOps = len(res.Committed) + len(res.Removed)
	entry.Status = ExecComponentSucceeded
	if runErr == nil {
		return
	}
	entry.Status = ExecComponentFailed
	entry.Stage, entry.ErrorCode, entry.ErrorMessage = componentErrorOf(runErr)
	if ctx.Err() != nil || entry.ErrorCode == execute.ComponentCodeCanceled {
		entry.Status = ExecComponentCanceled
		entry.ErrorMessage = "component run canceled"
	}
}

// componentOutcome decodes the frozen outcome of one component.
func componentOutcome(outcomes map[int]string, index int) (reconcile.ComponentOutcome, bool) {
	raw, ok := outcomes[index]
	if !ok {
		return reconcile.ComponentOutcome{}, false
	}
	var outcome reconcile.ComponentOutcome
	if err := json.Unmarshal([]byte(raw), &outcome); err != nil {
		return reconcile.ComponentOutcome{}, false
	}
	return outcome, true
}

// persistProgress records the component-boundary progress plus the full report.
func (d *dispatcher) persistProgress(
	executionID string,
	completed, doneOps int,
	c executionComponent,
	report []ExecutionComponentView,
) {
	_ = d.svc.repo.UpdateExecutionProgress(executionID, sqlite.ExecutionProgress{
		CompletedComponents:   completed,
		CompletedOperations:   doneOps,
		CurrentRoot:           c.RootPath,
		CurrentComponentID:    c.ComponentID,
		CurrentComponentIndex: c.ComponentIndex,
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

// parseExecutionRequest decodes the frozen worklist.
func parseExecutionRequest(raw string) (*executionRequest, error) {
	var req executionRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return nil, err
	}
	if req.DeleteMode == "" {
		req.DeleteMode = ExecutionDeleteModeSoft
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

// executionTools resolves the encoder tools from config.json, matching the
// planner's own resolution; empty paths fall back to PATH.
func (s *serviceImpl) executionTools() execute.ToolsConfig {
	tools := execute.ToolsConfig{}
	data, err := os.ReadFile(filepath.Join(s.configDir, "config.json"))
	if err != nil {
		return tools
	}
	cfg := appconfig.DefaultAppConfig()
	if json.Unmarshal(data, &cfg) != nil {
		return tools
	}
	tools.FFmpegPath = cfg.Tools.FFmpegPath
	tools.FFprobePath = cfg.Tools.FFprobePath
	return tools
}

// componentErrorOf extracts the stable failure facts of a component run.
func componentErrorOf(err error) (stage, code, message string) {
	cerr, ok := errors.AsType[*execute.ComponentError](err)
	if !ok {
		return "", "COMPONENT_FAILED", err.Error()
	}
	msg := cerr.Message
	if cerr.Path != "" {
		msg = fmt.Sprintf("%s (%s)", msg, cerr.Path)
	}
	return cerr.Stage, cerr.Code, msg
}

// syncComponentInventory applies this component's observed disk changes to the
// entries inventory: removed sources lose their row, committed outputs and
// soft-delete destinations are refreshed from disk. A sync failure is disclosed
// on the component instead of being reported as "unchanged".
func (d *dispatcher) syncComponentInventory(
	rootPath string,
	c executionComponent,
	res execute.ComponentRunResult,
	entry *ExecutionComponentView,
) {
	paths := make([]string, 0, len(res.Committed)+len(res.Recovery))
	paths = append(paths, res.Committed...)
	for _, p := range res.Recovery {
		if underRecoveryDir(c.RootPath, p) {
			paths = append(paths, p)
		}
	}
	facts := make([]sqlite.InventoryFile, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(filepath.FromSlash(p))
		if err != nil {
			entry.InventorySynced = false
			entry.InventorySyncError = fmt.Sprintf("stat %s: %v", p, err)
			return
		}
		facts = append(facts, sqlite.InventoryFile{Path: p, Size: info.Size(), Mtime: info.ModTime().Unix()})
	}
	if err := d.svc.repo.SyncObservedInventory(rootPath, res.Removed, facts); err != nil {
		entry.InventorySynced = false
		entry.InventorySyncError = err.Error()
		return
	}
	entry.InventorySynced = true
}

// underRecoveryDir reports whether a persisted path is inside the member root's
// soft-delete recovery folder: the only recovery entries that are real media in
// their scanned place. A temp leftover is not an inventory fact.
func underRecoveryDir(componentRoot, p string) bool {
	rel, err := filepath.Rel(filepath.FromSlash(componentRoot), filepath.FromSlash(p))
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) > 1 && parts[0] == softDeleteDirName
}

// nonNil turns a nil path slice into an empty one so the persisted report never
// marshals a planned-empty list as JSON null.
func nonNil(paths []string) []string {
	if paths == nil {
		return []string{}
	}
	return paths
}
