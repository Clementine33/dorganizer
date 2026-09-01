package workset

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Limit defaults/caps for feed and revision history pagination.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// ListWorksets lists worksets newest-first (keyset on updated_at, id). When a
// feed filter is set, rows are classified post-hoc and the page is filled by
// scanning successive keyset batches until `limit` matching views are found
// or the feed is exhausted. The next cursor is derived from the (limit+1)-th
// matching view — NOT from the end of the scanned keyset batch — so matched
// rows past the page boundary are never skipped, and pagination stays exact
// across pages. The returned cursor is "" when the feed is exhausted.
func (s *serviceImpl) ListWorksets(ctx context.Context, q ListQuery) ([]*WorksetView, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if q.Feed != "" && !ValidFeed(q.Feed) {
		return nil, "", NewError(ErrKindInvalidArgument, "INVALID_FEED_FILTER", "unknown feed filter", nil)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	if q.Feed == "" || q.Feed == FeedAll {
		views, next, err := s.listPage(q, limit, q.Cursor)
		return views, next, err
	}

	filtered := make([]*WorksetView, 0, limit+1)
	cursor := q.Cursor
	for {
		page, next, err := s.listPage(q, limit, cursor)
		if err != nil {
			return nil, "", err
		}
		for _, v := range page {
			if feedMatches(q.Feed, v) {
				filtered = append(filtered, v)
			}
		}
		// Page boundary: one past `limit` matched. The keyset cursor is
		// strictly exclusive (<), so point it at the LAST RETURNED match:
		// the next page starts right after it and nothing is skipped.
		if len(filtered) > limit {
			last := filtered[limit-1]
			return filtered[:limit], cursorEncode(last.UpdatedAt, last.WorksetID), nil
		}
		// Feed exhausted with fewer than limit matches.
		if next == "" {
			return filtered, "", nil
		}
		cursor = next
	}
}

// listPage fetches one keyset batch (limit = pageSize, starting at cursor)
// and converts rows to views. An empty cursor starts at the feed head.
func (s *serviceImpl) listPage(q ListQuery, pageSize int, cursor string) ([]*WorksetView, string, error) {
	q.Cursor = cursor
	limit := pageSize
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	cursorUpdatedAt, cursorID := parseCursor(q.Cursor)
	rows, err := s.repo.ListWorksets(cursorUpdatedAt, cursorID, limit+1, q.LibraryID, q.IncludeOrphaned)
	if err != nil {
		return nil, "", NewError(ErrKindInternal, "INTERNAL", "failed to list worksets", err)
	}
	var nextCursor string
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		nextCursor = cursorEncode(last.UpdatedAt, last.ID)
	}
	views := make([]*WorksetView, 0, len(rows))
	for _, w := range rows {
		v, err := s.view(w)
		if err != nil {
			return nil, "", err
		}
		views = append(views, v)
	}
	return views, nextCursor, nil
}

// feedMatches implements the mutually exclusive, error-first classification.
func feedMatches(feed string, v *WorksetView) bool {
	if feedErrory(v) {
		return feed == FeedError
	}
	if v.PlanningState == PlanningUnplanned || v.PlanningState == PlanningNeedsPlanning ||
		v.PlanningState == PlanningPlanning {
		return feed == FeedPending
	}
	return feed == FeedNormal
}

// feedErrory reports whether the workset belongs to the error bucket: orphaned,
// stale current revision, blocked components/roots, or a failed/interrupted
// latest generation.
func feedErrory(v *WorksetView) bool {
	if v.PlanningState == PlanningOrphaned {
		return true
	}
	if v.CurrentRevision != nil {
		if v.CurrentRevision.Stale != nil && *v.CurrentRevision.Stale {
			return true
		}
		if v.CurrentRevision.ValidationState == ValidationStale ||
			v.CurrentRevision.ValidationState == ValidationUnavailable {
			return true
		}
		if v.CurrentRevision.BlockedCount > 0 {
			return true
		}
	}
	if v.LatestGeneration != nil {
		if v.LatestGeneration.Status == "failed" || v.LatestGeneration.Status == "interrupted" {
			return true
		}
	}
	return false
}

// GetWorkset returns the authoritative aggregate detail view.
func (s *serviceImpl) GetWorkset(ctx context.Context, id string) (*WorksetView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := s.repo.GetWorkset(id)
	if err != nil {
		if errors.Is(err, sqlite.ErrWorksetNotFound) {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	return s.view(w)
}

func (s *serviceImpl) view(w *sqlite.Workset) (*WorksetView, error) {
	active, err := s.repo.GetActiveGenerationForWorkset(w.ID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load active generation", err)
	}
	out := &WorksetView{
		WorksetID:     w.ID,
		Title:         w.Title,
		Version:       w.Version,
		PlanningState: s.planningState(w, active),
		UpdatedAt:     w.UpdatedAt,
		CreatedAt:     w.CreatedAt,
	}
	if w.LibraryID != "" {
		lib, err := s.repo.GetLibrary(w.LibraryID)
		if err == nil {
			out.Library = &LibraryRef{LibraryID: lib.ID, Name: lib.Name, RootPath: lib.RootPath}
		}
	}

	members, err := s.repo.ListWorksetMembers(w.ID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset members", err)
	}
	covered := map[string]bool{}
	if w.CurrentRevisionID != "" {
		sum, roots, err := s.loadCurrentRevision(w)
		if err != nil {
			return nil, err
		}
		out.CurrentRevision = sum
		for _, r := range roots {
			covered[r.RootPath] = true
		}
	}
	for _, m := range members {
		state := MemberPending
		if covered[m.FolderPath] {
			state = MemberPlanned
		}
		out.Members = append(out.Members, MemberView{
			FolderID:   m.FolderID,
			FolderPath: m.FolderPath,
			FolderName: m.FolderName,
			RelPath:    m.RelPath,
			State:      state,
		})
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

	latest, err := s.repo.LatestGenerationForWorkset(w.ID)
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

	return out, nil
}

// planningState derives the workset-level planning state on read. It never
// consults live policy/classifier state, only canonical hashes.
func (s *serviceImpl) planningState(w *sqlite.Workset, active *sqlite.PlanGeneration) string {
	if w.LibraryID == "" {
		return PlanningOrphaned
	}
	if active != nil {
		return PlanningPlanning
	}
	if w.CurrentRevisionID == "" {
		return PlanningUnplanned
	}
	draft, err := s.repo.GetWorksetDraft(w.ID)
	if err != nil || draft == nil {
		return PlanningUnplanned
	}
	rev, err := s.repo.GetWorksetRevision(w.ID, w.CurrentRevisionID)
	if err != nil {
		return PlanningNeedsPlanning
	}
	if draft.DraftHash == rev.DraftHash {
		return PlanningPlanned
	}
	return PlanningNeedsPlanning
}

func parseCursor(cursor string) (updatedAt, id string) {
	if cursor == "" {
		return "", ""
	}
	idx := strings.LastIndex(cursor, "_")
	if idx < 0 {
		return "", ""
	}
	return cursor[:idx], cursor[idx+1:]
}

func cursorEncode(updatedAt time.Time, id string) string {
	return updatedAt.UTC().Format(time.RFC3339Nano) + "_" + id
}
