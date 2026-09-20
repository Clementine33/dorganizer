package workset

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Execution eligibility reasons returned with PLAN_NOT_EXECUTABLE. They are
// stable contract values the UI can branch on.
const (
	ExecBlockedNotCurrent = "NOT_CURRENT_REVISION"
	ExecBlockedDraft      = "DRAFT_CHANGED"
	ExecBlockedGeneration = "GENERATION_IN_PROGRESS"
	ExecBlockedComponents = "BLOCKED_COMPONENTS"
	ExecBlockedInput      = "INPUT_CHANGED"
	ExecBlockedAlreadyRan = "ALREADY_EXECUTED"
)

// Execution event names streamed by Subscribe.
const (
	ExecutionEventSnapshot    = "execution_snapshot"
	ExecutionEventProgress    = "progress"
	ExecutionEventSucceeded   = "succeeded"
	ExecutionEventFailed      = "failed"
	ExecutionEventCanceled    = "canceled"
	ExecutionEventInterrupted = "interrupted"
)

// StartExecutionRequest is the POST .../revisions/{planId}/executions request.
// The file worklist and the session options (such as the obsolete-audio
// handling) are the frozen revision's, never the client's; the one thing the
// caller chooses is how much of that revision this session runs.
type StartExecutionRequest struct {
	IfMatchVersion int
	IdempotencyKey string
	// FolderPaths scopes the session to these members, named by their
	// library-relative paths as the record holds them. Empty means every member,
	// which is what an execution was before the scope existed.
	FolderPaths []string
}

// StartExecutionResult distinguishes a fresh session from an idempotent replay.
type StartExecutionResult struct {
	Execution *ExecutionView
	Created   bool
}

// ExecutionComponentView is one component's frozen work and observed outcome.
// It is also the persisted report shape, so a crash keeps every component's
// facts without a second table.
type ExecutionComponentView struct {
	ComponentIndex     int      `json:"component_index"`
	ComponentID        string   `json:"component_id"`
	RootPath           string   `json:"root_path"`
	Partition          string   `json:"partition"`
	Status             string   `json:"status"` // pending|succeeded|failed|canceled
	Stage              string   `json:"stage,omitempty"`
	Operations         int      `json:"operations"`
	CompletedOps       int      `json:"completed_operations"`
	Committed          []string `json:"committed"`
	Removed            []string `json:"removed"`
	Remaining          []string `json:"remaining"`
	Recovery           []string `json:"recovery"`
	ErrorCode          string   `json:"error_code,omitempty"`
	ErrorMessage       string   `json:"error_message,omitempty"`
	InventorySynced    bool     `json:"inventory_synced"`
	InventorySyncError string   `json:"inventory_sync_error,omitempty"`
}

// ExecutionView is the session detail payload; it is also the SSE snapshot, so
// a reconnect starts from the same authoritative shape as GET.
type ExecutionView struct {
	ExecutionID         string                   `json:"execution_id"`
	WorksetID           string                   `json:"workset_id"`
	OperationType       string                   `json:"operation_type"`
	PlanID              string                   `json:"plan_id"`
	Status              string                   `json:"status"`
	Options             json.RawMessage          `json:"options,omitempty"`
	TotalComponents     int                      `json:"total_components"`
	CompletedComponents int                      `json:"completed_components"`
	TotalOperations     int                      `json:"total_operations"`
	CompletedOperations int                      `json:"completed_operations"`
	CurrentRoot         string                   `json:"current_root"`
	CurrentComponentID  string                   `json:"current_component_id"`
	CurrentPhase        string                   `json:"current_phase"`
	Components          []ExecutionComponentView `json:"components"`
	// SelectedFolders is the scope this session ran: the record's relative
	// paths, empty when it ran the whole revision.
	SelectedFolders []string `json:"selected_folders,omitempty"`
	ErrorCode       string   `json:"error_code"`
	ErrorMessage    string   `json:"error_message"`
	StartedAt       string   `json:"started_at"`
	FinishedAt      string   `json:"finished_at"`
	CreatedAt       string   `json:"created_at"`
}

// ExecutionRef is the compact session reference attached to the operation and
// revision views so a client can find the session that already exists.
type ExecutionRef struct {
	ExecutionID  string `json:"execution_id"`
	PlanID       string `json:"plan_id"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
}

// ExecutionProgress is the active-session progress attached to the operation
// view.
type ExecutionProgress struct {
	ExecutionID         string          `json:"execution_id"`
	PlanID              string          `json:"plan_id"`
	Status              string          `json:"status"`
	Options             json.RawMessage `json:"options,omitempty"`
	TotalComponents     int             `json:"total_components"`
	CompletedComponents int             `json:"completed_components"`
	TotalOperations     int             `json:"total_operations"`
	CompletedOperations int             `json:"completed_operations"`
	CurrentRoot         string          `json:"current_root"`
	CurrentComponentID  string          `json:"current_component_id"`
	CurrentPhase        string          `json:"current_phase"`
}

// executionRequest is the frozen session input persisted on the row: the
// task-owned options payload and the ordered execution units (generic identity
// plus the task's opaque payload), plus the scope the caller asked for — which
// the view reads back so a client that did not start the run still knows it was
// scoped.
type executionRequest struct {
	Options json.RawMessage `json:"options,omitempty"`
	Units   []ExecutionUnit `json:"units"`
	Folders []string        `json:"folders,omitempty"`
}

// StartExecution enqueues one execution of the operation's current revision,
// optionally scoped to some of that revision's folders. The frozen revision and
// the operation version are the whole authority: a revision is executed at most
// once — a scoped run executes it too, and the folders it left out are the work
// of the next plan — and success never authorizes a replay.
func (s *serviceImpl) StartExecution(
	ctx context.Context,
	worksetID, operationType, planID string,
	req StartExecutionRequest,
) (*StartExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateIdemKey(req.IdempotencyKey); err != nil {
		return nil, err
	}
	if err := s.rejectOrphaned(worksetID); err != nil {
		return nil, err
	}
	op, err := s.loadOperation(worksetID, operationType)
	if err != nil {
		return nil, err
	}
	// Replays answer before the version and eligibility gates: a retried request
	// must observe what it already asked for. The request is its revision and
	// the folders it scoped the run to — the same key with another scope is
	// another request, never a replay.
	selection := normalizeSelection(req.FolderPaths)
	requestHash := executionRequestHash(planID, selection)
	if result, replayed, replayErr := s.replayExecution(
		op,
		req.IdempotencyKey,
		requestHash,
	); replayErr != nil ||
		replayed {
		return result, replayErr
	}
	reasons, frozen, gateErr := s.executionEligibility(op, planID, req.IfMatchVersion)
	if gateErr != nil {
		return nil, gateErr
	}
	if len(reasons) > 0 {
		return nil, notExecutable(reasons)
	}
	scoped, scopeErr := s.scopeExecution(op.WorksetID, frozen, selection)
	if scopeErr != nil {
		return nil, scopeErr
	}
	rev, revErr := s.repo.GetOperationRevision(op.WorksetID, op.OperationType, planID)
	if revErr != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision", revErr)
	}
	return s.persistExecution(op, planID, req.IdempotencyKey, requestHash, rev.DraftHash, scoped, selection)
}

// normalizeSelection is the scope as it gets frozen: no blanks, no duplicates,
// and one order — so the same set of folders is the same request however it was
// listed.
func normalizeSelection(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

// executionRequestHash covers everything the caller asked for.
func executionRequestHash(planID string, selection []string) string {
	return hashJSON([]byte(planID + "|" + strings.Join(selection, "\x1f")))
}

// scopeExecution narrows a frozen session to the folders the caller selected.
// A path the record does not hold is refused outright: quietly running fewer
// folders than were asked for would be a scope change nobody approved. A
// selected folder that the plan found nothing to do in simply contributes no
// units — that is a conclusion about the folder, not a refusal.
func (s *serviceImpl) scopeExecution(
	worksetID string,
	frozen FrozenExecution,
	selection []string,
) (FrozenExecution, error) {
	if len(selection) == 0 {
		return frozen, nil
	}
	members, err := s.repo.ListWorksetMembers(worksetID)
	if err != nil {
		return FrozenExecution{}, NewError(ErrKindInternal, "INTERNAL", "failed to load members", err)
	}
	byRelPath := make(map[string]string, len(members))
	for _, member := range members {
		byRelPath[member.RelPath] = member.FolderPath
	}
	selected := make(map[string]bool, len(selection))
	for _, relPath := range selection {
		folderPath, ok := byRelPath[relPath]
		if !ok {
			return FrozenExecution{}, NewError(
				ErrKindInvalidArgument,
				"FOLDER_NOT_IN_RECORD",
				"not a folder of this record: "+relPath,
				nil,
			)
		}
		selected[folderPath] = true
	}
	units := make([]ExecutionUnit, 0, len(frozen.Units))
	total := 0
	for _, unit := range frozen.Units {
		if !selected[unit.RootPath] {
			continue
		}
		units = append(units, unit)
		total += unit.Operations
	}
	frozen.Units = units
	frozen.TotalOperations = total
	return frozen, nil
}

// replayExecution answers an idempotent retry with the session the key already
// created, whatever its status. A key reused for a different revision is a
// conflict, never a second session.
func (s *serviceImpl) replayExecution(
	op *sqlite.Operation, key, requestHash string,
) (*StartExecutionResult, bool, error) {
	if key == "" {
		return nil, false, nil
	}
	existing, err := s.repo.GetExecutionByOperationKey(op.WorksetID, op.OperationType, key)
	if err != nil {
		return nil, false, NewError(ErrKindInternal, "INTERNAL", "failed to check execution idempotency", err)
	}
	if existing == nil {
		return nil, false, nil
	}
	if existing.RequestHash != requestHash {
		return nil, false, NewError(
			ErrKindConflict,
			"IDEMPOTENCY_KEY_REUSED",
			"idempotency key was used with a different revision or delete mode",
			nil,
		)
	}
	return &StartExecutionResult{Execution: executionViewOf(existing), Created: false}, true, nil
}

// executionEligibility checks every gate of the execution authorization in
// order and returns the fail-forward reasons plus the frozen execution units.
// Reasons are accumulated so one response names all the disqualifiers.
func (s *serviceImpl) executionEligibility(
	op *sqlite.Operation, planID string, ifMatchVersion int,
) ([]string, FrozenExecution, error) {
	if op.CurrentRevisionID == "" || op.CurrentRevisionID != planID {
		return []string{ExecBlockedNotCurrent}, FrozenExecution{}, nil
	}
	if ifMatchVersion != op.Version {
		return nil, FrozenExecution{}, NewError(ErrKindConflict, "VERSION_CONFLICT", "operation version conflict", nil)
	}
	rev, err := s.repo.GetOperationRevision(op.WorksetID, op.OperationType, planID)
	if err != nil {
		if errors.Is(err, sqlite.ErrRevisionNotFound) {
			return nil, FrozenExecution{}, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
		}
		return nil, FrozenExecution{}, NewError(ErrKindInternal, "INTERNAL", "failed to load revision", err)
	}
	// An active session wins the answer: it already holds the revision.
	active, err := s.repo.GetActiveExecutionForOperation(op.WorksetID, op.OperationType)
	if err != nil {
		return nil, FrozenExecution{}, NewError(ErrKindInternal, "INTERNAL", "failed to check active execution", err)
	}
	if active != nil {
		return nil, FrozenExecution{}, NewError(
			ErrKindConflict,
			"EXECUTION_IN_PROGRESS",
			"an execution is already queued or running for this operation",
			nil,
		)
	}
	detail, reasons, err := s.executionBlockReasons(op, planID, rev)
	if err != nil {
		return nil, FrozenExecution{}, err
	}
	if len(reasons) > 0 {
		return reasons, FrozenExecution{}, nil
	}
	frozen, freezeErr := s.freezeExecution(op, detail, rev)
	if freezeErr != nil {
		return nil, FrozenExecution{}, freezeErr
	}
	w, err := s.repo.GetWorkset(op.WorksetID)
	if err != nil {
		return nil, FrozenExecution{}, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	scanning, err := s.repo.HasActiveScanForRoot(w.RootPath)
	if err != nil {
		return nil, FrozenExecution{}, NewError(ErrKindInternal, "INTERNAL", "failed to check library scan", err)
	}
	if scanning {
		return nil, FrozenExecution{}, NewError(
			ErrKindConflict,
			"SCAN_IN_PROGRESS",
			"wait for the library scan to finish before executing",
			nil,
		)
	}
	return nil, frozen, nil
}

// executionBlockReasons collects every disqualifier of the current revision:
// session history, draft drift, a running generation, blocked components and
// input freshness. Terminating errors are returned separately from the
// fail-forward reasons.
func (s *serviceImpl) executionBlockReasons(
	op *sqlite.Operation, planID string, rev *sqlite.OperationRevision,
) (*sqlite.PlanDetail, []string, error) {
	reasons := make([]string, 0, 4)
	done, err := s.repo.GetExecutionForRevision(planID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to check executed revisions", err)
	}
	if done != nil {
		reasons = append(reasons, ExecBlockedAlreadyRan)
	}
	draft, err := s.repo.GetOperationDraft(op.WorksetID, op.OperationType)
	if err != nil || draft == nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load draft", err)
	}
	if rev.DraftHash != draft.DraftHash {
		reasons = append(reasons, ExecBlockedDraft)
	}
	generating, err := s.repo.GetActiveGenerationForOperation(op.WorksetID, op.OperationType)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active generation", err)
	}
	if generating != nil {
		reasons = append(reasons, ExecBlockedGeneration)
	}
	detail, err := s.repo.GetPlanDetail(planID)
	if err != nil {
		return nil, nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
	}
	health, healthErr := s.revisionHealth(op.OperationType, detail)
	if healthErr != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to evaluate revision health", healthErr)
	}
	if health.UnitsBlocked {
		reasons = append(reasons, ExecBlockedComponents)
	}
	if health.InputMoved {
		reasons = append(reasons, ExecBlockedInput)
	}
	return detail, reasons, nil
}

// freezeExecution asks the operation's task to validate that the revision can
// run and to freeze its ordered execution units. Business block reasons are
// fail-forward values, not errors.
// freezeExecution asks the operation's task to validate that the revision can
// run and to freeze its ordered execution units and session options. Business
// block reasons are fail-forward values, not errors.
func (s *serviceImpl) freezeExecution(
	op *sqlite.Operation,
	detail *sqlite.PlanDetail,
	rev *sqlite.OperationRevision,
) (FrozenExecution, error) {
	task, err := s.requireTask(op.OperationType)
	if err != nil {
		return FrozenExecution{}, err
	}
	members, err := s.repo.ListWorksetMembers(op.WorksetID)
	if err != nil {
		return FrozenExecution{}, NewError(ErrKindInternal, "INTERNAL", "failed to load members", err)
	}
	frozen, reasons, err := task.FreezeExecution(s.repo, RevisionFacts{
		DraftSnapshot: []byte(rev.DraftSnapshot),
		Members:       members,
		Detail:        detail,
	})
	if err != nil {
		return FrozenExecution{}, err
	}
	if len(reasons) > 0 {
		return FrozenExecution{}, notExecutable(reasons)
	}
	return frozen, nil
}

// executionOptionsOf reads the frozen session options back out of the
// session's own request payload: the options are stored once, with the frozen
// units, and never mirrored into a column. The payload itself is task-owned
// and opaque here.
func executionOptionsOf(e *sqlite.PlanExecution) json.RawMessage {
	req, err := parseExecutionRequest(e.RequestJSON)
	if err != nil {
		return nil
	}
	return req.Options
}

// executionFoldersOf reads the session's scope back out of the same payload,
// so a client that did not start the run still knows which folders it covered.
func executionFoldersOf(e *sqlite.PlanExecution) []string {
	req, err := parseExecutionRequest(e.RequestJSON)
	if err != nil {
		return nil
	}
	return req.Folders
}

func (s *serviceImpl) persistExecution(
	op *sqlite.Operation,
	planID, key, requestHash, revisionDraftHash string,
	frozen FrozenExecution,
	selection []string,
) (*StartExecutionResult, error) {
	report := initialComponentReport(frozen.Units)
	exec := &sqlite.PlanExecution{
		ExecutionID:              "exec-" + newToken(),
		WorksetID:                op.WorksetID,
		OperationType:            op.OperationType,
		PlanID:                   planID,
		Status:                   sqlite.ExecStatusQueued,
		IdempotencyKey:           key,
		RequestHash:              requestHash,
		ExpectedOperationVersion: op.Version,
		RequestJSON: mustJSON(executionRequest{
			Options: frozen.Options,
			Units:   frozen.Units,
			Folders: selection,
		}),
		TotalComponents: len(frozen.Units),
		TotalOperations: frozen.TotalOperations,
		ReportJSON:      mustJSON(report),
		CreatedAt:       time.Now(),
	}
	createErr := s.enqueue(func() error {
		return s.repo.CreateExecutionGuarded(exec, sqlite.ExecutionGuards{
			ExpectedOperationVersion: op.Version,
			ExpectedCurrentRevision:  planID,
			ExpectedDraftHash:        revisionDraftHash,
		})
	})
	if err := createErr; err != nil {
		if errors.Is(err, sqlite.ErrExecutionIdemConflict) {
			// The insert collided with the key index (same operation+key) or with
			// the one-execution-per-revision index. Read the outcome: a matching
			// key replays its session, another request reusing the key conflicts,
			// and a revision that already has a session cannot be executed again.
			existing, loadErr := s.repo.GetExecutionByOperationKey(op.WorksetID, op.OperationType, key)
			if loadErr == nil && existing != nil {
				if existing.RequestHash == requestHash {
					return &StartExecutionResult{Execution: executionViewOf(existing), Created: false}, nil
				}
				return nil, NewError(
					ErrKindConflict,
					"IDEMPOTENCY_KEY_REUSED",
					"idempotency key was used with a different request",
					err,
				)
			}
			return nil, notExecutable([]string{ExecBlockedAlreadyRan})
		}
		switch {
		case errors.Is(err, sqlite.ErrVersionConflict):
			return nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "operation version conflict", nil)
		case errors.Is(err, sqlite.ErrRevisionNotFound):
			return nil, notExecutable([]string{ExecBlockedNotCurrent})
		case errors.Is(err, sqlite.ErrDraftChanged):
			return nil, notExecutable([]string{ExecBlockedDraft})
		case errors.Is(err, sqlite.ErrGenerationInProgress):
			return nil, notExecutable([]string{ExecBlockedGeneration})
		case errors.Is(err, sqlite.ErrExecutionInProgress):
			return nil, NewError(
				ErrKindConflict,
				"EXECUTION_IN_PROGRESS",
				"an execution is already queued or running for this operation",
				nil,
			)
		case errors.Is(err, sqlite.ErrWorksetOrphaned):
			return nil, NewError(ErrKindConflict, "ORPHANED_WORKSET", "orphaned worksets are read-only", nil)
		case errors.Is(err, sqlite.ErrWorksetNotFound):
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		case errors.Is(err, sqlite.ErrExecutionNotEligible):
			return nil, NewError(
				ErrKindConflict,
				"PLAN_NOT_EXECUTABLE",
				"revision cannot be executed",
				nil,
			)
		case errors.Is(err, sqlite.ErrOperationNotFound):
			return nil, NewError(ErrKindNotFound, "OPERATION_NOT_FOUND", "operation not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to start execution", err)
	}
	s.dispatcher.wakeExecution()
	return &StartExecutionResult{Execution: executionViewOf(exec), Created: true}, nil
}

// initialComponentReport builds the full report skeleton: every frozen
// component exists as pending from the moment the session does, so an
// interrupted session still names what it never executed.
func initialComponentReport(units []ExecutionUnit) []ExecutionComponentView {
	report := make([]ExecutionComponentView, 0, len(units))
	for _, u := range units {
		report = append(report, ExecutionComponentView{
			ComponentIndex: u.Index,
			ComponentID:    u.ID,
			RootPath:       u.RootPath,
			Partition:      u.Partition,
			Status:         ExecComponentPending,
			Operations:     u.Operations,
			Committed:      []string{},
			Removed:        []string{},
			Remaining:      []string{},
			Recovery:       []string{},
		})
	}
	return report
}

// GetExecution returns the session view scoped to a workset operation.
func (s *serviceImpl) GetExecution(
	ctx context.Context,
	worksetID, operationType, executionID string,
) (*ExecutionView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e, err := s.loadExecution(worksetID, operationType, executionID)
	if err != nil {
		return nil, err
	}
	return executionViewOf(e), nil
}

// CancelExecution cancels a session. It is idempotent: terminal rows keep their
// status. Orphaned worksets may still cancel a leftover session.
func (s *serviceImpl) CancelExecution(
	ctx context.Context,
	worksetID, operationType, executionID string,
) (*ExecutionView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e, err := s.loadExecution(worksetID, operationType, executionID)
	if err != nil {
		return nil, err
	}
	if e.Status == sqlite.ExecStatusQueued || e.Status == sqlite.ExecStatusRunning {
		if cancelErr := s.repo.CancelExecution(executionID); cancelErr != nil {
			return nil, NewError(ErrKindInternal, "INTERNAL", "failed to cancel execution", cancelErr)
		}
		if e.Status == sqlite.ExecStatusQueued {
			s.dispatcher.wakeExecution()
		}
	}
	return s.GetExecution(ctx, worksetID, operationType, executionID)
}

// loadExecution loads a session and checks its operation ownership: a session
// of another workset or another operation is not found.
func (s *serviceImpl) loadExecution(
	worksetID, operationType, executionID string,
) (*sqlite.PlanExecution, error) {
	e, err := s.repo.GetExecution(executionID)
	if err != nil {
		if errors.Is(err, sqlite.ErrExecutionNotFound) {
			return nil, NewError(ErrKindNotFound, "EXECUTION_NOT_FOUND", "execution not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load execution", err)
	}
	if e.WorksetID != worksetID || e.OperationType != operationType {
		return nil, NewError(ErrKindNotFound, "EXECUTION_NOT_FOUND", "execution not found", nil)
	}
	return e, nil
}

// notExecutable builds the canonical conflict error for one or more reasons.
func notExecutable(reasons []string) error {
	return NewError(ErrKindConflict, "PLAN_NOT_EXECUTABLE", "revision cannot be executed", nil).
		WithDetails(reasons)
}

// executionViewOf converts a persisted row to its view payload.
func executionViewOf(e *sqlite.PlanExecution) *ExecutionView {
	view := &ExecutionView{
		ExecutionID:         e.ExecutionID,
		WorksetID:           e.WorksetID,
		OperationType:       e.OperationType,
		PlanID:              e.PlanID,
		Status:              e.Status,
		Options:             executionOptionsOf(e),
		TotalComponents:     e.TotalComponents,
		CompletedComponents: e.CompletedComponents,
		TotalOperations:     e.TotalOperations,
		CompletedOperations: e.CompletedOperations,
		CurrentRoot:         e.CurrentRoot,
		CurrentComponentID:  e.CurrentComponentID,
		CurrentPhase:        e.CurrentPhase,
		ErrorCode:           e.ErrorCode,
		ErrorMessage:        e.ErrorMessage,
		StartedAt:           formatTime(e.StartedAt),
		FinishedAt:          formatTime(e.FinishedAt),
		CreatedAt:           formatTime(e.CreatedAt),
		Components:          []ExecutionComponentView{},
		SelectedFolders:     executionFoldersOf(e),
	}
	var report []ExecutionComponentView
	if err := json.Unmarshal([]byte(e.ReportJSON), &report); err == nil {
		view.Components = report
	}
	return view
}

// executionRefOf converts a persisted row to its compact reference.
func executionRefOf(e *sqlite.PlanExecution) *ExecutionRef {
	return &ExecutionRef{
		ExecutionID:  e.ExecutionID,
		PlanID:       e.PlanID,
		Status:       e.Status,
		ErrorCode:    e.ErrorCode,
		ErrorMessage: e.ErrorMessage,
		FinishedAt:   formatTime(e.FinishedAt),
	}
}

func executionProgressOf(e *sqlite.PlanExecution) *ExecutionProgress {
	return &ExecutionProgress{
		ExecutionID:         e.ExecutionID,
		PlanID:              e.PlanID,
		Status:              e.Status,
		Options:             executionOptionsOf(e),
		TotalComponents:     e.TotalComponents,
		CompletedComponents: e.CompletedComponents,
		TotalOperations:     e.TotalOperations,
		CompletedOperations: e.CompletedOperations,
		CurrentRoot:         e.CurrentRoot,
		CurrentComponentID:  e.CurrentComponentID,
		CurrentPhase:        e.CurrentPhase,
	}
}
