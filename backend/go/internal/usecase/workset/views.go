package workset

import (
	"context"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
)

// Limit defaults/caps for the record feed pagination.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

// ListWorksets lists worksets newest-first (keyset on updated_at, id). The
// returned cursor is "" when the list is exhausted.
func (s *serviceImpl) ListWorksets(ctx context.Context, q ListQuery) ([]*WorksetView, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	views, next, err := s.listPage(q, limit, q.Cursor)
	return views, next, err
}

// listPage fetches one keyset batch (limit = pageSize, starting at cursor)
// and converts rows to views. An empty cursor starts at the list head.
func (s *serviceImpl) listPage(q ListQuery, pageSize int, cursor string) ([]*WorksetView, string, error) {
	limit := pageSize
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	cursorUpdatedAt, cursorID := parseCursor(cursor)
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

// GetWorkset returns the workset metadata view.
func (s *serviceImpl) GetWorkset(ctx context.Context, id string) (*WorksetView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := s.loadWorkset(id)
	if err != nil {
		return nil, err
	}
	return s.view(w)
}

// view assembles the workset metadata view: identity, library, fixed members
// and one entry per established operation.
func (s *serviceImpl) view(w *sqlite.Workset) (*WorksetView, error) {
	out := &WorksetView{
		WorksetID: w.ID,
		Title:     w.Title,
		Version:   w.Version,
		UpdatedAt: w.UpdatedAt,
		CreatedAt: w.CreatedAt,
	}
	if w.LibraryID != "" {
		lib, libErr := s.repo.GetLibrary(w.LibraryID)
		if libErr == nil {
			out.Library = &LibraryRef{LibraryID: lib.ID, Name: lib.Name, RootPath: lib.RootPath}
		}
	}
	members, err := s.repo.ListWorksetMembers(w.ID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset members", err)
	}
	for _, m := range members {
		out.Members = append(out.Members, MemberView{
			MemberID:   m.MemberID,
			FolderPath: m.FolderPath,
			FolderName: m.FolderName,
			RelPath:    m.RelPath,
		})
	}
	ops, err := s.repo.ListOperations(w.ID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load operations", err)
	}
	for _, op := range ops {
		view, opErr := s.operationView(op)
		if opErr != nil {
			return nil, opErr
		}
		out.Operations = append(out.Operations, view)
	}
	return out, nil
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
