package workset

import (
	"context"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Subscribe streams generation lifecycle events over the emit callback.
//
// Every connection first receives a complete `session_snapshot` event (the
// current persisted state), then only future events: `progress` (root counts +
// current root), or a terminal event (`completed` with revision_id, `failed`,
// `canceled`, `interrupted`). There is no historical replay and never a fake
// percentage. Because the snapshot is emitted first, a reconnect after a
// disconnect resumes from the current truth; the detail GET remains the
// fallback when no SSE transport is available.
func (s *serviceImpl) Subscribe(ctx context.Context, worksetID, generationID string, emit func(event string, data any) error) error {
	// Scoped access check first: a session belonging to another workset is 404.
	g, err := s.repo.GetGeneration(generationID)
	if err != nil {
		if err == sqlite.ErrGenerationNotFound {
			return NewError(ErrKindNotFound, "GENERATION_NOT_FOUND", "generation not found", nil)
		}
		return NewError(ErrKindInternal, "INTERNAL", "failed to load generation", err)
	}
	if g.WorksetID != worksetID {
		return NewError(ErrKindNotFound, "GENERATION_NOT_FOUND", "generation not found", nil)
	}

	// Initial snapshot: the authoritative current state, always first.
	if err := emit("session_snapshot", toGenerationView(g)); err != nil {
		return err
	}
	// Terminal sessions have no future events; the snapshot above is the
	// whole payload (clients reconnect to the detail GET for any further
	// polling).
	if terminalStatus(g.Status) {
		return nil
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	last := -1
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		cur, err := s.repo.GetGeneration(generationID)
		if err != nil {
			return nil // disconnect fallback: client reconnects to detail
		}
		if cur.Status != g.Status {
			if terminalStatus(cur.Status) {
				switch cur.Status {
				case sqlite.GenStatusCompleted:
					return emit("completed", map[string]any{"generation_id": cur.GenerationID, "revision_id": cur.RevisionID})
				case sqlite.GenStatusFailed:
					return emit("failed", map[string]any{"generation_id": cur.GenerationID, "error_code": cur.ErrorCode, "error_message": cur.ErrorMessage})
				case sqlite.GenStatusCanceled:
					return emit("canceled", map[string]any{"generation_id": cur.GenerationID})
				case sqlite.GenStatusInterrupted:
					return emit("interrupted", map[string]any{"generation_id": cur.GenerationID})
				default:
					return emit("message", map[string]any{"status": cur.Status})
				}
			}
		}
		if cur.CompletedRoots != last {
			last = cur.CompletedRoots
			if err := emit("progress", map[string]any{
				"generation_id":   cur.GenerationID,
				"total_roots":     cur.TotalRoots,
				"completed_roots": cur.CompletedRoots,
				"current_root":    cur.CurrentRoot,
				"error_count":     cur.ErrorCount,
			}); err != nil {
				return err
			}
		}
		g = cur
	}
}

func terminalStatus(status string) bool {
	switch status {
	case sqlite.GenStatusCompleted, sqlite.GenStatusFailed, sqlite.GenStatusCanceled, sqlite.GenStatusInterrupted:
		return true
	}
	return false
}
