package workset

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onsei/organizer/backend/internal/pathnorm"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// MaxMembers caps the ordered album folders in one workset.
const MaxMembers = 500

// idemRetentionWindow is the guaranteed replay window for workset creation and
// generation idempotency keys, and the terminal-generation retention horizon.
const idemRetentionWindow = 30 * 24 * time.Hour

// newToken produces a sortable, collision-resistant identifier: a nanosecond
// timestamp plus a random suffix. The timestamp alone collides when two
// aggregates are created within the same nanosecond (e.g. test loops), which
// surfaced as a spurious idempotency-key conflict on the primary key.
func newToken() string {
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		// Fall back to the timestamp-only form rather than failing creation.
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(rnd[:]))
}

func (s *serviceImpl) CreateWorkset(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if err := validateTitle(title); err != nil {
		return nil, err
	}
	if len(req.FolderIDs) == 0 || len(req.FolderIDs) > MaxMembers {
		return nil, NewError(
			ErrKindInvalidArgument,
			"INVALID_FOLDER_COUNT",
			fmt.Sprintf("worksets require between 1 and %d album folders", MaxMembers),
			nil,
		)
	}
	if err := validateIdemKey(req.IdempotencyKey); err != nil {
		return nil, err
	}
	lib, err := s.repo.GetLibrary(req.LibraryID)
	if err != nil {
		if errors.Is(err, sqlite.ErrLibraryNotFound) {
			return nil, NewError(ErrKindNotFound, "LIBRARY_NOT_FOUND", "library not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load library", err)
	}
	if result, replayed, err := s.replayCreate(ctx, req.IdempotencyKey); err != nil || replayed {
		return result, err
	}
	members, err := s.resolveMembers(lib, req.FolderIDs)
	if err != nil {
		return nil, err
	}
	return s.persistWorkset(ctx, title, req.IdempotencyKey, lib, members)
}

func (s *serviceImpl) replayCreate(ctx context.Context, key string) (*CreateResult, bool, error) {
	if key == "" {
		return nil, false, nil
	}
	existing, err := s.repo.GetWorksetByCreationIdemKey(key)
	if err != nil {
		return nil, false, NewError(ErrKindInternal, "INTERNAL", "failed to check idempotency key", err)
	}
	if existing == nil {
		return nil, false, nil
	}
	if time.Since(existing.CreatedAt) < idemRetentionWindow {
		view, err := s.GetWorkset(ctx, existing.ID)
		if err != nil {
			return nil, false, err
		}
		return &CreateResult{Workset: view, Created: false}, true, nil
	}
	// Expired key: release ownership so a retry creates a fresh workset.
	if err := s.repo.ClearExpiredWorksetIdemKey(existing.ID, time.Now().Add(-idemRetentionWindow)); err != nil {
		return nil, false, NewError(ErrKindInternal, "INTERNAL", "failed to expire idempotency key", err)
	}
	return nil, false, nil
}

func (s *serviceImpl) persistWorkset(
	ctx context.Context,
	title, idemKey string,
	lib *sqlite.Library,
	members []sqlite.WorksetMember,
) (*CreateResult, error) {
	now := time.Now()
	ws := &sqlite.Workset{
		ID:              "ws-" + newToken(),
		Title:           title,
		LibraryID:       lib.ID,
		RootPath:        lib.RootPath,
		RootPathKey:     pathnorm.RootPathKey(lib.RootPath),
		Version:         1,
		CreationIdemKey: idemKey,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	rows := make([]sqlite.WorksetMember, 0, len(members))
	for i, m := range members {
		m.WorksetID = ws.ID
		m.MemberIndex = i
		rows = append(rows, m)
	}
	if err := s.repo.CreateWorkset(ws, rows, s.seedDraft(ws.ID, now)); err != nil {
		if errors.Is(err, sqlite.ErrWorksetIdemConflict) {
			if result, replayed, _ := s.replayCreate(ctx, idemKey); replayed {
				return result, nil
			}
			return nil, NewError(ErrKindConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key conflict", err)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to create workset", err)
	}
	view, err := s.GetWorkset(ctx, ws.ID)
	if err != nil {
		return nil, err
	}
	return &CreateResult{Workset: view, Created: true}, nil
}

// resolveMembers validates folder ids against the library and returns ordered,
// de-duplicated members with normalized relative paths.
func (s *serviceImpl) resolveMembers(lib *sqlite.Library, folderIDs []string) ([]sqlite.WorksetMember, error) {
	seen := map[string]struct{}{}
	for _, id := range folderIDs {
		if _, dup := seen[id]; dup {
			return nil, NewError(ErrKindInvalidArgument, "DUPLICATE_FOLDER", "duplicate folder id", nil)
		}
		seen[id] = struct{}{}
	}
	seenRel := map[string]struct{}{}
	var out []sqlite.WorksetMember
	for _, id := range folderIDs {
		f, err := s.repo.GetLibraryFolder(lib.ID, id)
		if err != nil {
			if errors.Is(err, sqlite.ErrLibraryFolderNotFound) {
				return nil, NewError(
					ErrKindInvalidArgument,
					"LIBRARY_FOLDER_NOT_FOUND",
					fmt.Sprintf("folder %s not found in library", id),
					nil,
				)
			}
			return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load library folder", err)
		}
		if !pathnorm.IsWithinRoot(lib.RootPath, f.Path) {
			return nil, NewError(
				ErrKindInvalidArgument,
				"FOLDER_OUTSIDE_LIBRARY",
				fmt.Sprintf("folder %s is outside library root", f.Path),
				nil,
			)
		}
		rel := strings.TrimPrefix(f.RelativePath, "/")
		if rel == "" {
			rel = f.Name
		}
		if _, dup := seenRel[rel]; dup {
			return nil, NewError(ErrKindInvalidArgument, "DUPLICATE_FOLDER", "duplicate folder", nil)
		}
		seenRel[rel] = struct{}{}
		out = append(out, sqlite.WorksetMember{
			RelPath:    rel,
			FolderID:   f.ID,
			FolderPath: f.Path,
			FolderName: f.Name,
		})
	}
	return out, nil
}

func validateTitle(title string) error {
	if title == "" {
		return NewError(ErrKindInvalidArgument, "INVALID_TITLE", "title is required", nil)
	}
	n := utf8.RuneCountInString(title)
	if n < 1 || n > 120 {
		return NewError(ErrKindInvalidArgument, "INVALID_TITLE", "title must be 1-120 characters", nil)
	}
	return nil
}

func validateIdemKey(key string) error {
	if key != "" && (len(key) > 255 || strings.ContainsAny(key, " \t\r\n")) {
		return NewError(
			ErrKindInvalidArgument,
			"INVALID_IDEMPOTENCY_KEY",
			"idempotency key is too long or malformed",
			nil,
		)
	}
	return nil
}

// RenameWorkset renames the workset (always allowed, even during generation).
func (s *serviceImpl) RenameWorkset(ctx context.Context, id string, req RenameRequest) (*WorksetView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if err := validateTitle(title); err != nil {
		return nil, err
	}
	w, err := s.repo.GetWorkset(id)
	if err != nil {
		if errors.Is(err, sqlite.ErrWorksetNotFound) {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	if w.LibraryID == "" {
		return nil, NewError(ErrKindConflict, "ORPHANED_WORKSET", "orphaned worksets are read-only", nil)
	}
	if err := s.repo.UpdateWorksetTitle(id, title, req.IfMatchVersion, time.Now()); err != nil {
		if errors.Is(err, sqlite.ErrVersionConflict) {
			return nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "workset version conflict", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to rename workset", err)
	}
	return s.GetWorkset(ctx, id)
}
