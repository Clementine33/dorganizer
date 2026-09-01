package workset

import (
	"context"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// generationSnapshot is the wire shape of the session_snapshot event. It
// mirrors the HTTP layer's generationViewResponse (snake_case JSON) so SSE
// clients can parse the snapshot with the same contract as GET
// /planning-sessions/{id}; emitting the tagless usecase view would serialize
// PascalCase and be silently dropped by the frontend.
type generationSnapshot struct {
	GenerationID   string `json:"generation_id"`
	WorksetID      string `json:"workset_id"`
	Status         string `json:"status"`
	TotalRoots     int    `json:"total_roots"`
	CompletedRoots int    `json:"completed_roots"`
	CurrentRoot    string `json:"current_root"`
	ErrorCount     int    `json:"error_count"`
	RevisionID     string `json:"revision_id"`
	ErrorCode      string `json:"error_code"`
	ErrorMessage   string `json:"error_message"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	CreatedAt      string `json:"created_at"`
}

func snapshotOf(g *sqlite.PlanGeneration) generationSnapshot {
	return generationSnapshot{
		GenerationID:   g.GenerationID,
		WorksetID:      g.WorksetID,
		Status:         g.Status,
		TotalRoots:     g.TotalRoots,
		CompletedRoots: g.CompletedRoots,
		CurrentRoot:    g.CurrentRoot,
		ErrorCount:     g.ErrorCount,
		RevisionID:     g.RevisionID,
		ErrorCode:      g.ErrorCode,
		ErrorMessage:   g.ErrorMessage,
		StartedAt:      formatTime(g.StartedAt),
		FinishedAt:     formatTime(g.FinishedAt),
		CreatedAt:      formatTime(g.CreatedAt),
	}
}

// formatTime renders a zero time as the empty string, matching the HTTP
// detail payload's convention.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

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
	g, err := s.loadGeneration(worksetID, generationID)
	if err != nil {
		return err
	}

	// Initial snapshot: the authoritative current state, always first.
	if err := emit("session_snapshot", snapshotOf(g)); err != nil {
		return err
	}
	// Terminal sessions have no future events; the snapshot above is the
	// whole payload. Emit an explicit terminal event so clients that keyed
	// their state machine on terminal SSE events (rather than inspecting the
	// snapshot) reach a confirmed terminal instead of reporting a transport
	// error when the stream ends.
	if terminalStatus(g.Status) {
		return emitTerminal(emit, g.Status, g)
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
				return emitTerminal(emit, cur.Status, cur)
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

// emitTerminal sends the terminal event matching the session status.
func emitTerminal(emit func(event string, data any) error, status string, g *sqlite.PlanGeneration) error {
	switch status {
	case sqlite.GenStatusCompleted:
		return emit("completed", map[string]any{"generation_id": g.GenerationID, "revision_id": g.RevisionID})
	case sqlite.GenStatusFailed:
		return emit("failed", map[string]any{"generation_id": g.GenerationID, "error_code": g.ErrorCode, "error_message": g.ErrorMessage})
	case sqlite.GenStatusCanceled:
		return emit("canceled", map[string]any{"generation_id": g.GenerationID})
	case sqlite.GenStatusInterrupted:
		return emit("interrupted", map[string]any{"generation_id": g.GenerationID})
	default:
		return emit("message", map[string]any{"status": status})
	}
}
