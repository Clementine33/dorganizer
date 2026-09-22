package workset

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// CreateCurrentWorkset creates the current processing record of one
// (library, operation) pair, replacing the record the caller saw as current.
//
// The requested scope is validated against the scanned inventory — the same
// listing the caller selected from — and every directory that does not become
// a member is reported with its reason instead of being silently dropped. A
// request whose whole selection is unusable creates nothing; a request that
// replaces a busy record (queued or running planning or execution) is refused
// and leaves that record untouched.
func (s *serviceImpl) CreateCurrentWorkset(
	ctx context.Context,
	req CreateCurrentRequest,
) (*CreateCurrentResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := s.requireTask(req.OperationType); err != nil {
		return nil, err
	}
	if len(req.FolderPaths) == 0 || len(req.FolderPaths) > MaxMembers {
		return nil, NewError(
			ErrKindInvalidArgument,
			"INVALID_FOLDER_COUNT",
			fmt.Sprintf("a record requires between 1 and %d member folders", MaxMembers),
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

	members, skipped, err := s.resolveSelectedMembers(lib, req.FolderPaths)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, NewError(
			ErrKindInvalidArgument,
			"NO_AUDIO_MEMBERS",
			"none of the selected folders can join this operation",
			nil,
		).WithDetails(skipPaths(skipped))
	}
	if len(members) > MaxMembers {
		return nil, NewError(
			ErrKindInvalidArgument,
			"INVALID_FOLDER_COUNT",
			fmt.Sprintf("a record holds at most %d member folders", MaxMembers),
			nil,
		)
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = lib.Name
	}
	if err := validateTitle(title); err != nil {
		return nil, err
	}
	requestHash := createRequestHash(req)
	if result, replayed, replayErr := s.replayCreate(
		ctx,
		lib,
		req,
		requestHash,
		skipped,
	); replayErr != nil ||
		replayed {
		return result, replayErr
	}
	return s.persistCurrentWorkset(ctx, lib, req, requestHash, title, members, skipped)
}

// replayCreate answers a repeated creation request with the record its
// idempotency key already owns, provided that record is still the current one
// and the request is the same request. A key that now belongs to replaced
// content, or a key reused for a different request, is a conflict: neither
// resurrects the record it replaced nor replaces the current record again.
func (s *serviceImpl) replayCreate(
	ctx context.Context,
	lib *sqlite.Library,
	req CreateCurrentRequest,
	requestHash string,
	skipped []SkippedFolder,
) (*CreateCurrentResult, bool, error) {
	if req.IdempotencyKey == "" {
		return nil, false, nil
	}
	existing, err := s.repo.GetWorksetByCreationIdemKey(req.IdempotencyKey)
	if err != nil {
		return nil, false, NewError(ErrKindInternal, "INTERNAL", "failed to check idempotency key", err)
	}
	if existing == nil {
		return nil, false, nil
	}
	if time.Since(existing.CreatedAt) >= idemRetentionWindow {
		// Expired key: release ownership so a retry creates a fresh record.
		if expireErr := s.repo.ClearExpiredWorksetIdemKey(
			existing.ID,
			time.Now().Add(-idemRetentionWindow),
		); expireErr != nil {
			return nil, false, NewError(ErrKindInternal, "INTERNAL", "failed to expire idempotency key", expireErr)
		}
		return nil, false, nil
	}
	if existing.CreationRequestHash != requestHash {
		return nil, false, NewError(
			ErrKindConflict,
			"IDEMPOTENCY_KEY_REUSED",
			"this idempotency key was used for a different request",
			nil,
		)
	}
	current, err := s.repo.GetCurrentWorkset(lib.ID, req.OperationType)
	if err != nil {
		return nil, false, NewError(ErrKindInternal, "INTERNAL", "failed to load the current record", err)
	}
	if current == nil || current.ID != existing.ID {
		return nil, false, NewError(
			ErrKindConflict,
			"RECORD_REPLACED",
			"this creation request has already been replaced by a newer record",
			nil,
		)
	}
	view, err := s.GetWorkset(ctx, existing.ID)
	if err != nil {
		return nil, false, err
	}
	return &CreateCurrentResult{
		Workset:  view,
		Created:  false,
		Recorded: len(view.Members),
		Skipped:  skipped,
	}, true, nil
}

// persistCurrentWorkset writes the new record and removes the one it replaces
// in one transaction.
func (s *serviceImpl) persistCurrentWorkset(
	ctx context.Context,
	lib *sqlite.Library,
	req CreateCurrentRequest,
	requestHash, title string,
	members []sqlite.WorksetMember,
	skipped []SkippedFolder,
) (*CreateCurrentResult, error) {
	now := time.Now()
	ws := &sqlite.Workset{
		ID:                  "ws-" + newToken(),
		Title:               title,
		LibraryID:           lib.ID,
		OperationType:       req.OperationType,
		RootPath:            lib.RootPath,
		RootPathKey:         pathnorm.RootPathKey(lib.RootPath),
		Version:             1,
		CreationIdemKey:     req.IdempotencyKey,
		CreationRequestHash: requestHash,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	rows := make([]sqlite.WorksetMember, 0, len(members))
	for i, m := range members {
		m.WorksetID = ws.ID
		m.MemberID = "m-" + newToken()
		m.MemberIndex = i
		rows = append(rows, m)
	}
	// Only the requested operation is materialized: a record exists for the
	// work someone asked for, and creation never initializes a task that was
	// not requested (ADR 0001 §1).
	task, err := s.requireTask(req.OperationType)
	if err != nil {
		return nil, err
	}
	raw, hash, schemaVersion := task.SeedDraft()
	ops := []sqlite.Operation{{
		WorksetID:     ws.ID,
		OperationType: task.Kind(),
		Version:       1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}}
	drafts := []sqlite.OperationDraft{{
		WorksetID:     ws.ID,
		OperationType: task.Kind(),
		SchemaVersion: schemaVersion,
		DraftJSON:     string(raw),
		DraftHash:     hash,
		UpdatedAt:     now,
	}}
	if replaceErr := s.repo.ReplaceCurrentWorkset(ws, rows, ops, drafts, req.ExpectedCurrentID); replaceErr != nil {
		switch {
		case errors.Is(replaceErr, sqlite.ErrWorksetIdemConflict):
			if result, replayed, _ := s.replayCreate(ctx, lib, req, requestHash, skipped); replayed {
				return result, nil
			}
			return nil, NewError(ErrKindConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key conflict", replaceErr)
		case errors.Is(replaceErr, sqlite.ErrCurrentRecordChanged):
			return nil, NewError(
				ErrKindConflict,
				"RECORD_REPLACED",
				"the current record changed while this request was in flight",
				replaceErr,
			)
		case errors.Is(replaceErr, sqlite.ErrRecordBusy):
			return nil, NewError(
				ErrKindConflict,
				"RECORD_BUSY",
				"the record being replaced has a queued or running session",
				replaceErr,
			)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to create the current record", replaceErr)
	}
	view, err := s.GetWorkset(ctx, ws.ID)
	if err != nil {
		return nil, err
	}
	return &CreateCurrentResult{Workset: view, Created: true, Recorded: len(rows), Skipped: skipped}, nil
}

// resolveSelectedMembers turns selected library-relative paths into ordered
// members. Every path that cannot join is reported with its reason rather than
// dropped in silence, and a path is only accepted when the scanned inventory
// knows it as a directory of the library root that holds audio somewhere
// beneath it.
func (s *serviceImpl) resolveSelectedMembers(
	lib *sqlite.Library,
	relPaths []string,
) ([]sqlite.WorksetMember, []SkippedFolder, error) {
	clean := make([]string, 0, len(relPaths))
	skipped := make([]SkippedFolder, 0)
	seen := make(map[string]struct{}, len(relPaths))
	for _, raw := range relPaths {
		rel, ok := pathnorm.RelPath(raw)
		if !ok {
			skipped = append(skipped, SkippedFolder{Path: raw, Reason: SkipInvalidPath})
			continue
		}
		if _, dup := seen[rel]; dup {
			skipped = append(skipped, SkippedFolder{Path: rel, Reason: SkipDuplicate})
			continue
		}
		// A member is a direct child of the library root: a nested directory is
		// browsable inside its member, never a scope of its own (ADR 0001 §1).
		if strings.Contains(rel, "/") {
			skipped = append(skipped, SkippedFolder{Path: rel, Reason: SkipNotDirect})
			continue
		}
		if isRecoveryRelPath(lib.RootPath, rel) {
			skipped = append(skipped, SkippedFolder{Path: rel, Reason: SkipRecoveryDir})
			continue
		}
		seen[rel] = struct{}{}
		clean = append(clean, rel)
	}

	counts, err := s.repo.DirAudioCounts(lib.RootPath, clean)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to read the scanned inventory", err)
	}
	out := make([]sqlite.WorksetMember, 0, len(clean))
	for _, rel := range clean {
		count, known := counts[rel]
		if !known {
			// Not a directory of the last scan: it never existed, was removed
			// on disk, or is a file. Either way it cannot be a member.
			skipped = append(skipped, SkippedFolder{Path: rel, Reason: SkipMissing})
			continue
		}
		if count == 0 {
			skipped = append(skipped, SkippedFolder{Path: rel, Reason: SkipNoAudio})
			continue
		}
		out = append(out, sqlite.WorksetMember{
			RelPath:    rel,
			FolderPath: pathnorm.JoinRel(lib.RootPath, rel),
			FolderName: rel,
		})
	}
	return out, skipped, nil
}

// isRecoveryRelPath reports whether a relative path names the library-level
// recovery directory; it is never a member.
func isRecoveryRelPath(rootPath, rel string) bool {
	if rel == pathnorm.RecoveryDirName {
		return true
	}
	return pathnorm.IsWindowsCaseInsensitivePath(rootPath) &&
		strings.EqualFold(rel, pathnorm.RecoveryDirName)
}

// skipPaths lists the skipped paths as error details, so a refused creation
// still names what it could not use.
func skipPaths(skipped []SkippedFolder) []string {
	out := make([]string, 0, len(skipped))
	for _, s := range skipped {
		out = append(out, s.Path+": "+s.Reason)
	}
	return out
}

// createRequestHash is the canonical identity of one creation request. The
// member selection hashes as a set: the order the caller selected in is not
// part of the request, so a retry that reorders the same selection still
// replays instead of conflicting.
func createRequestHash(req CreateCurrentRequest) string {
	paths := make([]string, 0, len(req.FolderPaths))
	for _, p := range req.FolderPaths {
		if rel, ok := pathnorm.RelPath(p); ok {
			paths = append(paths, rel)
		} else {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	raw, err := json.Marshal(struct {
		LibraryID         string   `json:"library_id"`
		OperationType     string   `json:"operation_type"`
		FolderPaths       []string `json:"folder_paths"`
		ExpectedCurrentID string   `json:"expected_current_id"`
	}{req.LibraryID, req.OperationType, paths, req.ExpectedCurrentID})
	if err != nil {
		return ""
	}
	return sqlite.CanonicalJSONHash(raw)
}

// GetCurrentWorkset returns the current record of one (library, operation)
// pair, or nil when the pair has none. It is the read the workbench uses to
// answer "does this library have a conversion record".
func (s *serviceImpl) GetCurrentWorkset(
	ctx context.Context,
	libraryID, operationType string,
) (*WorksetView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := s.requireTask(operationType); err != nil {
		return nil, err
	}
	w, err := s.repo.GetCurrentWorkset(libraryID, operationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load the current record", err)
	}
	if w == nil {
		return nil, nil
	}
	return s.view(w)
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

// RenameWorkset renames the workset (always allowed, even during
// generation). It is guarded by the workset metadata version, never by an
// operation version.
func (s *serviceImpl) RenameWorkset(ctx context.Context, id string, req RenameRequest) (*WorksetView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if err := validateTitle(title); err != nil {
		return nil, err
	}
	w, err := s.loadWorkset(id)
	if err != nil {
		return nil, err
	}
	if w.LibraryID == "" {
		return nil, NewError(ErrKindConflict, "ORPHANED_WORKSET", "orphaned worksets are read-only", nil)
	}
	if err := s.repo.RenameWorkset(id, title, req.IfMatchVersion, time.Now()); err != nil {
		if errors.Is(err, sqlite.ErrVersionConflict) {
			return nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "workset version conflict", nil)
		}
		if errors.Is(err, sqlite.ErrWorksetNotFound) {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to rename workset", err)
	}
	return s.GetWorkset(ctx, id)
}
