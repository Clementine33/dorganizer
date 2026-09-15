package workset

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// Execution delete modes. The session freezes one at creation; soft deletion is
// the default and keeps the <root>/Delete/<relative path> recovery convention.
const (
	ExecutionDeleteModeSoft = "soft"
	ExecutionDeleteModeHard = "hard"
)

// Execution eligibility reasons returned with PLAN_NOT_EXECUTABLE. They are
// stable contract values the UI can branch on.
const (
	ExecBlockedNotCurrent   = "NOT_CURRENT_REVISION"
	ExecBlockedNotConfirmed = "NOT_CONFIRMED"
	ExecBlockedDraft        = "DRAFT_CHANGED"
	ExecBlockedGeneration   = "GENERATION_IN_PROGRESS"
	ExecBlockedComponents   = "BLOCKED_COMPONENTS"
	ExecBlockedInput        = "INPUT_CHANGED"
	ExecBlockedAlreadyRan   = "ALREADY_EXECUTED"
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

// StartExecutionRequest is the POST .../revisions/{planId}/executions payload.
// The body only carries the execution choice: the file worklist is the frozen
// revision's, never the client's.
type StartExecutionRequest struct {
	IfMatchVersion int
	IdempotencyKey string
	DeleteMode     string
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
	DeleteMode          string                   `json:"delete_mode"`
	TotalComponents     int                      `json:"total_components"`
	CompletedComponents int                      `json:"completed_components"`
	TotalOperations     int                      `json:"total_operations"`
	CompletedOperations int                      `json:"completed_operations"`
	CurrentRoot         string                   `json:"current_root"`
	CurrentComponentID  string                   `json:"current_component_id"`
	CurrentPhase        string                   `json:"current_phase"`
	Components          []ExecutionComponentView `json:"components"`
	ErrorCode           string                   `json:"error_code"`
	ErrorMessage        string                   `json:"error_message"`
	StartedAt           string                   `json:"started_at"`
	FinishedAt          string                   `json:"finished_at"`
	CreatedAt           string                   `json:"created_at"`
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
	ExecutionID         string `json:"execution_id"`
	PlanID              string `json:"plan_id"`
	Status              string `json:"status"`
	DeleteMode          string `json:"delete_mode"`
	TotalComponents     int    `json:"total_components"`
	CompletedComponents int    `json:"completed_components"`
	TotalOperations     int    `json:"total_operations"`
	CompletedOperations int    `json:"completed_operations"`
	CurrentRoot         string `json:"current_root"`
	CurrentComponentID  string `json:"current_component_id"`
	CurrentPhase        string `json:"current_phase"`
}

// executionComponent is one frozen worklist entry persisted with the session.
// The component outcome itself stays in the immutable revision snapshot; this
// list pins the order and the effective target profile each component runs
// with, so the worker never re-resolves policy or re-reconciles.
type executionComponent struct {
	ComponentIndex int                      `json:"component_index"`
	ComponentID    string                   `json:"component_id"`
	RootIndex      int                      `json:"root_index"`
	RootPath       string                   `json:"root_path"`
	Partition      string                   `json:"partition"`
	Operations     int                      `json:"operations"`
	Profile        reconcile.DesiredProfile `json:"profile"`
}

// executionRequest is the frozen session input persisted on the row.
type executionRequest struct {
	PlanID     string               `json:"plan_id"`
	DeleteMode string               `json:"delete_mode"`
	Components []executionComponent `json:"components"`
}

// StartExecution enqueues one execution of the operation's current, confirmed
// revision. The frozen revision, its confirmation and the operation version are
// the whole authority: a revision is executed at most once, and success never
// authorizes a replay of the same revision.
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
	deleteMode := req.DeleteMode
	if deleteMode == "" {
		deleteMode = ExecutionDeleteModeSoft
	}
	if deleteMode != ExecutionDeleteModeSoft && deleteMode != ExecutionDeleteModeHard {
		return nil, NewError(
			ErrKindInvalidArgument,
			"INVALID_DELETE_MODE",
			"delete_mode must be soft or hard",
			nil,
		)
	}
	if err := s.rejectOrphaned(worksetID); err != nil {
		return nil, err
	}
	op, err := s.loadOperation(worksetID, operationType)
	if err != nil {
		return nil, err
	}
	// Replays answer before the version and eligibility gates: a retried request
	// must observe what it already asked for.
	requestHash := hashJSON([]byte(planID + "|" + deleteMode))
	if result, replayed, replayErr := s.replayExecution(
		op,
		req.IdempotencyKey,
		requestHash,
	); replayErr != nil ||
		replayed {
		return result, replayErr
	}
	reasons, input, gateErr := s.executionEligibility(op, planID, req.IfMatchVersion)
	if gateErr != nil {
		return nil, gateErr
	}
	if len(reasons) > 0 {
		return nil, notExecutable(reasons)
	}
	rev, revErr := s.repo.GetOperationRevision(op.WorksetID, op.OperationType, planID)
	if revErr != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision", revErr)
	}
	return s.persistExecution(op, planID, deleteMode, req.IdempotencyKey, requestHash, rev.DraftHash, input)
}

// replayExecution answers an idempotent retry with the session the key already
// created, whatever its status. A key reused for a different revision or delete
// mode is a conflict, never a second session.
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
// order and returns the fail-forward reasons plus the frozen worklist. Reasons
// are accumulated so one response names all the disqualifiers.
func (s *serviceImpl) executionEligibility(
	op *sqlite.Operation, planID string, ifMatchVersion int,
) ([]string, []executionComponent, error) {
	if op.CurrentRevisionID == "" || op.CurrentRevisionID != planID {
		return []string{ExecBlockedNotCurrent}, nil, nil
	}
	if ifMatchVersion != op.Version {
		return nil, nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "operation version conflict", nil)
	}
	rev, err := s.repo.GetOperationRevision(op.WorksetID, op.OperationType, planID)
	if err != nil {
		if errors.Is(err, sqlite.ErrRevisionNotFound) {
			return nil, nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
		}
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision", err)
	}
	// An active session wins the answer: it already holds the revision.
	active, err := s.repo.GetActiveExecutionForOperation(op.WorksetID, op.OperationType)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active execution", err)
	}
	if active != nil {
		return nil, nil, NewError(
			ErrKindConflict,
			"EXECUTION_IN_PROGRESS",
			"an execution is already queued or running for this operation",
			nil,
		)
	}
	detail, reasons, err := s.executionBlockReasons(op, planID, rev)
	if err != nil {
		return nil, nil, err
	}
	if len(reasons) > 0 {
		return reasons, nil, nil
	}
	worklist, workErr := s.executionWorklist(op.WorksetID, detail, rev)
	if workErr != nil {
		return nil, nil, workErr
	}
	w, err := s.repo.GetWorkset(op.WorksetID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	scanning, err := s.repo.HasActiveScanForRoot(w.RootPath)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to check library scan", err)
	}
	if scanning {
		return nil, nil, NewError(
			ErrKindConflict,
			"SCAN_IN_PROGRESS",
			"wait for the library scan to finish before executing",
			nil,
		)
	}
	return nil, worklist, nil
}

// executionBlockReasons collects every disqualifier of a confirmed revision:
// session history, confirmation, draft drift, a running generation, blocked
// components and input freshness. Terminating errors are returned separately
// from the fail-forward reasons.
func (s *serviceImpl) executionBlockReasons(
	op *sqlite.Operation, planID string, rev *sqlite.OperationRevision,
) (*sqlite.WorkflowPlanDetail, []string, error) {
	reasons := make([]string, 0, 4)
	done, err := s.repo.GetExecutionForRevision(planID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to check executed revisions", err)
	}
	if done != nil {
		reasons = append(reasons, ExecBlockedAlreadyRan)
	}
	if _, confErr := s.repo.GetOperationConfirmation(planID); confErr != nil {
		if !errors.Is(confErr, sqlite.ErrConfirmationNotFound) {
			return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load confirmation", confErr)
		}
		reasons = append(reasons, ExecBlockedNotConfirmed)
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
	detail, err := s.repo.GetWorkflowPlanDetail(planID)
	if err != nil {
		return nil, nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
	}
	for _, c := range detail.Components {
		if c.Status == "blocked" {
			reasons = append(reasons, ExecBlockedComponents)
			break
		}
	}
	for _, r := range detail.Roots {
		// A missing root and a stale fingerprint are both "the disk no longer
		// matches what was confirmed"; the real per-file disk precheck runs again
		// at execution time.
		if r.RootStatus == "missing" || rootIsStale(s.repo, r) {
			reasons = append(reasons, ExecBlockedInput)
			break
		}
	}
	return detail, reasons, nil
}

// executionWorklist freezes the ordered component work: the persisted component
// order, each component's root path and the effective target profile its
// partition resolves to in the revision's own draft snapshot.
func (s *serviceImpl) executionWorklist(
	worksetID string,
	detail *sqlite.WorkflowPlanDetail,
	rev *sqlite.OperationRevision,
) ([]executionComponent, error) {
	doc, err := ParseDraft(rev.DraftSnapshot)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "stored revision snapshot is invalid", err)
	}
	members, err := s.repo.ListWorksetMembers(worksetID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load members", err)
	}
	effective, err := ResolveEffective(doc, members)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "stored revision snapshot does not resolve", err)
	}
	policyByRoot := map[string]reconcile.Policy{}
	for _, e := range effective {
		if !e.Excluded {
			policyByRoot[e.FolderPath] = e.Policy
		}
	}
	rootByIndex := map[int]sqlite.WorkflowRootRecord{}
	for _, r := range detail.Roots {
		rootByIndex[r.RootIndex] = r
	}
	worklist := make([]executionComponent, 0, len(detail.Components))
	for _, c := range detail.Components {
		root, ok := rootByIndex[c.RootIndex]
		if !ok {
			return nil, notExecutable([]string{ExecBlockedInput})
		}
		policy, ok := policyByRoot[root.RootPath]
		if !ok {
			return nil, notExecutable([]string{ExecBlockedInput})
		}
		profile := reconcile.ProfileFor(policy, reconcile.Partition(c.Partition))
		worklist = append(worklist, executionComponent{
			ComponentIndex: c.ComponentIndex,
			ComponentID:    c.ComponentID,
			RootIndex:      c.RootIndex,
			RootPath:       root.RootPath,
			Partition:      c.Partition,
			Operations:     componentOperationCount(c.OutcomeJSON),
			Profile:        profile,
		})
	}
	return worklist, nil
}

// componentOperationCount counts the frozen executable operations of one
// persisted component outcome. An unreadable snapshot counts zero: the worker
// still runs the component and fails closed on it.
func componentOperationCount(outcomeJSON string) int {
	var outcome reconcile.ComponentOutcome
	if err := json.Unmarshal([]byte(outcomeJSON), &outcome); err != nil {
		return 0
	}
	return len(outcome.Operations)
}

// persistExecution writes the queued session and pokes the worker.
func (s *serviceImpl) persistExecution(
	op *sqlite.Operation,
	planID, deleteMode, key, requestHash, revisionDraftHash string,
	worklist []executionComponent,
) (*StartExecutionResult, error) {
	report := initialComponentReport(worklist)
	total := 0
	for _, c := range worklist {
		total += c.Operations
	}
	exec := &sqlite.PlanExecution{
		ExecutionID:              "exec-" + newToken(),
		WorksetID:                op.WorksetID,
		OperationType:            op.OperationType,
		PlanID:                   planID,
		Status:                   sqlite.ExecStatusQueued,
		DeleteMode:               deleteMode,
		IdempotencyKey:           key,
		RequestHash:              requestHash,
		ExpectedOperationVersion: op.Version,
		RequestJSON: mustJSON(executionRequest{
			PlanID:     planID,
			DeleteMode: deleteMode,
			Components: worklist,
		}),
		TotalComponents: len(worklist),
		TotalOperations: total,
		ReportJSON:      mustJSON(report),
		CreatedAt:       time.Now(),
	}
	createErr := s.repo.CreateExecutionGuarded(exec, sqlite.ExecutionGuards{
		ExpectedOperationVersion: op.Version,
		ExpectedCurrentRevision:  planID,
		ExpectedDraftHash:        revisionDraftHash,
		RequireConfirmation:      true,
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
		case errors.Is(err, sqlite.ErrConfirmationNotFound):
			return nil, notExecutable([]string{ExecBlockedNotConfirmed})
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
func initialComponentReport(worklist []executionComponent) []ExecutionComponentView {
	report := make([]ExecutionComponentView, 0, len(worklist))
	for _, c := range worklist {
		report = append(report, ExecutionComponentView{
			ComponentIndex: c.ComponentIndex,
			ComponentID:    c.ComponentID,
			RootPath:       c.RootPath,
			Partition:      c.Partition,
			Status:         ExecComponentPending,
			Operations:     c.Operations,
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
		DeleteMode:          e.DeleteMode,
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
		DeleteMode:          e.DeleteMode,
		TotalComponents:     e.TotalComponents,
		CompletedComponents: e.CompletedComponents,
		TotalOperations:     e.TotalOperations,
		CompletedOperations: e.CompletedOperations,
		CurrentRoot:         e.CurrentRoot,
		CurrentComponentID:  e.CurrentComponentID,
		CurrentPhase:        e.CurrentPhase,
	}
}
