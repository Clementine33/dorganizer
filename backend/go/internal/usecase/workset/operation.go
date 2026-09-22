package workset

import (
	"context"
	"errors"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
)

// requireTask returns the registered task for an operation type. Only
// registered kinds are addressable state: an unimplemented operation type has
// no operation, draft, revision or session to resolve.
func (s *serviceImpl) requireTask(operationType string) (Task, error) {
	for _, task := range s.tasks {
		if task.Kind() == operationType {
			return task, nil
		}
	}
	return nil, NewError(
		ErrKindNotFound,
		"UNKNOWN_OPERATION_TYPE",
		"unsupported operation type "+operationType,
		nil,
	)
}

// loadWorkset loads a workset or fails with the canonical not-found error.
func (s *serviceImpl) loadWorkset(id string) (*sqlite.Workset, error) {
	w, err := s.repo.GetWorkset(id)
	if err != nil {
		if errors.Is(err, sqlite.ErrWorksetNotFound) {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	return w, nil
}

// loadOperation loads the workset and its operation, applying the ownership
// checks every operation-scoped read and write shares: unknown workset,
// unsupported operation type and unestablished operation are all not-found.
func (s *serviceImpl) loadOperation(worksetID, operationType string) (*sqlite.Operation, error) {
	if _, err := s.requireTask(operationType); err != nil {
		return nil, err
	}
	w, err := s.loadWorkset(worksetID)
	if err != nil {
		return nil, err
	}
	op, err := s.repo.GetOperation(w.ID, operationType)
	if err != nil {
		if errors.Is(err, sqlite.ErrOperationNotFound) {
			return nil, NewError(ErrKindNotFound, "OPERATION_NOT_FOUND", "operation not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load operation", err)
	}
	return op, nil
}

// rejectOrphaned refuses writes on a workset whose library is gone. Orphaned
// worksets stay reviewable and read-only (ADR 0001 §5).
func (s *serviceImpl) rejectOrphaned(worksetID string) error {
	w, err := s.loadWorkset(worksetID)
	if err != nil {
		return err
	}
	if w.LibraryID == "" {
		return NewError(ErrKindConflict, "ORPHANED_WORKSET", "orphaned worksets are read-only", nil)
	}
	return nil
}

// GetOperation returns the operation-scoped aggregate: its version, derived
// planning state, current revision and generation state.
func (s *serviceImpl) GetOperation(ctx context.Context, worksetID, operationType string) (*OperationView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	op, err := s.loadOperation(worksetID, operationType)
	if err != nil {
		return nil, err
	}
	return s.operationView(op)
}

// operationView builds the operation view from an already-loaded row.
func (s *serviceImpl) operationView(op *sqlite.Operation) (*OperationView, error) {
	out := &OperationView{
		WorksetID:     op.WorksetID,
		OperationType: op.OperationType,
		Version:       op.Version,
	}
	w, err := s.repo.GetWorkset(op.WorksetID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	active, err := s.repo.GetActiveGenerationForOperation(op.WorksetID, op.OperationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load active generation", err)
	}
	out.PlanningState = s.planningState(w, op, active)
	if op.CurrentRevisionID != "" {
		summary, _, revErr := s.loadCurrentRevision(op)
		if revErr != nil {
			return nil, revErr
		}
		out.CurrentRevision = summary
	}
	if active != nil {
		out.ActiveGeneration = &GenerationProgress{
			GenerationID:   active.GenerationID,
			Status:         active.Status,
			TotalRoots:     active.TotalRoots,
			CompletedRoots: active.CompletedRoots,
			CurrentRoot:    active.CurrentRoot,
			ErrorCount:     active.ErrorCount,
		}
	}
	latest, err := s.repo.LatestGenerationForOperation(op.WorksetID, op.OperationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load latest generation", err)
	}
	if latest != nil && latest.Status != sqlite.GenStatusRunning && latest.Status != sqlite.GenStatusQueued {
		out.LatestGeneration = &GenerationSummary{
			GenerationID: latest.GenerationID,
			Status:       latest.Status,
			ErrorCode:    latest.ErrorCode,
			ErrorMessage: latest.ErrorMessage,
			FinishedAt:   latest.FinishedAt,
		}
	}
	activeExec, err := s.repo.GetActiveExecutionForOperation(op.WorksetID, op.OperationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load active execution", err)
	}
	if activeExec != nil {
		out.ActiveExecution = executionProgressOf(activeExec)
	}
	latestExec, err := s.repo.LatestExecutionForOperation(op.WorksetID, op.OperationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load latest execution", err)
	}
	if latestExec != nil {
		out.LatestExecution = executionRefOf(latestExec)
	}
	return out, nil
}

// planningState derives the operation's planning state on read. It compares
// the live canonical draft hash with the current revision's frozen hash; a
// rename or another operation's change never affects it (ADR 0001 §2).
func (s *serviceImpl) planningState(
	w *sqlite.Workset,
	op *sqlite.Operation,
	active *sqlite.PlanGeneration,
) string {
	if w.LibraryID == "" {
		return PlanningOrphaned
	}
	if active != nil {
		return PlanningPlanning
	}
	if op.CurrentRevisionID == "" {
		return PlanningUnplanned
	}
	draft, err := s.repo.GetOperationDraft(op.WorksetID, op.OperationType)
	if err != nil || draft == nil {
		return PlanningUnplanned
	}
	rev, err := s.repo.GetOperationRevision(op.WorksetID, op.OperationType, op.CurrentRevisionID)
	if err != nil {
		return PlanningNeedsPlanning
	}
	if draft.DraftHash == rev.DraftHash {
		return PlanningPlanned
	}
	return PlanningNeedsPlanning
}
