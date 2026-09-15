package workset

import (
	"context"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// executionProgressEvent is the wire shape of the progress event. It never
// carries a fabricated percentage: only counts that actually happened on disk.
type executionProgressEvent struct {
	ExecutionID         string `json:"execution_id"`
	Status              string `json:"status"`
	TotalComponents     int    `json:"total_components"`
	CompletedComponents int    `json:"completed_components"`
	TotalOperations     int    `json:"total_operations"`
	CompletedOperations int    `json:"completed_operations"`
	CurrentRoot         string `json:"current_root"`
	CurrentComponentID  string `json:"current_component_id"`
	CurrentPhase        string `json:"current_phase"`
}

// SubscribeExecution streams execution lifecycle events over the emit callback.
//
// Every connection first receives a complete `execution_snapshot` event (the
// current persisted state, per-component report included), then only future
// events: `progress` or a terminal event (`succeeded`, `failed`, `canceled`,
// `interrupted`). There is no historical replay; because the snapshot is
// emitted first, a reconnect resumes from the current truth and the detail GET
// stays the fallback when no SSE transport is available.
func (s *serviceImpl) SubscribeExecution(
	ctx context.Context,
	worksetID, operationType, executionID string,
	emit func(event string, data any) error,
) error {
	e, err := s.loadExecution(worksetID, operationType, executionID)
	if err != nil {
		return err
	}
	if err := emit(ExecutionEventSnapshot, executionViewOf(e)); err != nil {
		return err
	}
	if executionTerminal(e.Status) {
		return emitExecutionTerminal(emit, e)
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	last := progressOf(e)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		cur, loadErr := s.repo.GetExecution(executionID)
		if loadErr != nil {
			return nil // disconnect fallback: client reconnects to the detail route
		}
		if executionTerminal(cur.Status) {
			return emitExecutionTerminal(emit, cur)
		}
		if next := progressOf(cur); next != last {
			last = next
			if emitErr := emit(ExecutionEventProgress, next); emitErr != nil {
				return emitErr
			}
		}
	}
}

// progressOf builds the progress payload a reconnect or tick compares against.
func progressOf(e *sqlite.PlanExecution) executionProgressEvent {
	return executionProgressEvent{
		ExecutionID:         e.ExecutionID,
		Status:              e.Status,
		TotalComponents:     e.TotalComponents,
		CompletedComponents: e.CompletedComponents,
		TotalOperations:     e.TotalOperations,
		CompletedOperations: e.CompletedOperations,
		CurrentRoot:         e.CurrentRoot,
		CurrentComponentID:  e.CurrentComponentID,
		CurrentPhase:        e.CurrentPhase,
	}
}

// executionTerminal reports whether a status is final.
func executionTerminal(status string) bool {
	switch status {
	case sqlite.ExecStatusSucceeded, sqlite.ExecStatusFailed,
		sqlite.ExecStatusCanceled, sqlite.ExecStatusInterrupted:
		return true
	}
	return false
}

// emitExecutionTerminal sends the terminal event matching the session status.
func emitExecutionTerminal(emit func(event string, data any) error, e *sqlite.PlanExecution) error {
	switch e.Status {
	case sqlite.ExecStatusSucceeded:
		return emit(ExecutionEventSucceeded, map[string]any{
			"execution_id": e.ExecutionID,
			"plan_id":      e.PlanID,
		})
	case sqlite.ExecStatusFailed:
		return emit(ExecutionEventFailed, map[string]any{
			"execution_id":  e.ExecutionID,
			"error_code":    e.ErrorCode,
			"error_message": e.ErrorMessage,
		})
	case sqlite.ExecStatusCanceled:
		return emit(ExecutionEventCanceled, map[string]any{"execution_id": e.ExecutionID})
	case sqlite.ExecStatusInterrupted:
		return emit(ExecutionEventInterrupted, map[string]any{"execution_id": e.ExecutionID})
	default:
		return emit("message", map[string]any{"status": e.Status})
	}
}
