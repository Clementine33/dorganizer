package workset

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	appconfig "github.com/onsei/organizer/backend/internal/config"
	"github.com/onsei/organizer/backend/internal/pathnorm"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// Limit defaults/caps for feed and revision history pagination.
const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
	MaxMembers       = 500
)

// idemRetentionWindow is the guaranteed replay window for workset creation and
// generation idempotency keys, and the terminal-generation retention horizon.
const idemRetentionWindow = 30 * 24 * time.Hour

type serviceImpl struct {
	repo       *sqlite.Repository
	configDir  string
	dispatcher *dispatcher
}

// NewService creates the workset usecase service.
func NewService(repo *sqlite.Repository, configDir string, concurrency int) Service {
	s := &serviceImpl{repo: repo, configDir: configDir}
	s.dispatcher = newDispatcher(s, concurrency)
	return s
}

// DispatcherHandle exposes the background dispatcher for main wiring.
func (s *serviceImpl) DispatcherHandle() Dispatcher {
	return s.dispatcher
}

// ==================== Workset create ====================

func (s *serviceImpl) CreateWorkset(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	if err := validateTitle(title); err != nil {
		return nil, err
	}
	if len(req.FolderIDs) == 0 || len(req.FolderIDs) > MaxMembers {
		return nil, NewError(ErrKindInvalidArgument, "INVALID_FOLDER_COUNT", fmt.Sprintf("worksets require between 1 and %d album folders", MaxMembers), nil)
	}
	if err := validateIdemKey(req.IdempotencyKey); err != nil {
		return nil, err
	}

	lib, err := s.repo.GetLibrary(req.LibraryID)
	if err != nil {
		if err == sqlite.ErrLibraryNotFound {
			return nil, NewError(ErrKindNotFound, "LIBRARY_NOT_FOUND", "library not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load library", err)
	}

	// Idempotent replay: same key returns the same workset within the window.
	if req.IdempotencyKey != "" {
		existing, err := s.repo.GetWorksetByCreationIdemKey(req.IdempotencyKey)
		if err != nil {
			return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check idempotency key", err)
		}
		if existing != nil {
			withinWindow := time.Since(existing.CreatedAt) < idemRetentionWindow
			if withinWindow {
				view, err := s.GetWorkset(ctx, existing.ID)
				if err != nil {
					return nil, err
				}
				return &CreateResult{Workset: view, Created: false}, nil
			}
			// Expired key: release ownership so a retry creates a fresh workset.
			if err := s.repo.ClearExpiredWorksetIdemKey(existing.ID, time.Now().Add(-idemRetentionWindow)); err != nil {
				return nil, NewError(ErrKindInternal, "INTERNAL", "failed to expire idempotency key", err)
			}
		}
	}

	members, err := s.resolveMembers(lib, req.FolderIDs)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	ws := &sqlite.Workset{
		ID:              "ws-" + newToken(),
		Title:           title,
		LibraryID:       lib.ID,
		RootPath:        lib.RootPath,
		RootPathKey:     pathnorm.RootPathKey(lib.RootPath),
		Version:         1,
		CreationIdemKey: req.IdempotencyKey,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	draft := s.seedDraft(ws.ID, now)
	rows := make([]sqlite.WorksetMember, 0, len(members))
	for i, m := range members {
		rows = append(rows, sqlite.WorksetMember{
			WorksetID:   ws.ID,
			MemberIndex: i,
			RelPath:     m.RelPath,
			FolderID:    m.FolderID,
			FolderPath:  m.FolderPath,
			FolderName:  m.FolderName,
		})
	}
	if err := s.repo.CreateWorkset(ws, rows, draft); err != nil {
		if err == sqlite.ErrWorksetIdemConflict {
			// A concurrent create won the same key; replay it.
			existing, gerr := s.repo.GetWorksetByCreationIdemKey(req.IdempotencyKey)
			if gerr == nil && existing != nil {
				view, verr := s.GetWorkset(ctx, existing.ID)
				if verr == nil {
					return &CreateResult{Workset: view, Created: false}, nil
				}
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
			if err == sqlite.ErrLibraryFolderNotFound {
				return nil, NewError(ErrKindInvalidArgument, "LIBRARY_FOLDER_NOT_FOUND", fmt.Sprintf("folder %s not found in library", id), nil)
			}
			return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load library folder", err)
		}
		if !pathnorm.IsWithinRoot(lib.RootPath, f.Path) {
			return nil, NewError(ErrKindInvalidArgument, "FOLDER_OUTSIDE_LIBRARY", fmt.Sprintf("folder %s is outside library root", f.Path), nil)
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
		return NewError(ErrKindInvalidArgument, "INVALID_IDEMPOTENCY_KEY", "idempotency key is too long or malformed", nil)
	}
	return nil
}

// seedDraft builds the initial editable draft: one reconcile step with a
// complete inline policy snapshot. Tag literals are copied from config.json's
// prune.literal_tags (empty when unconfigured); output profiles default to
// wav + mp3@320 so a new workset is immediately usable. The snapshot is
// self-contained: later config or slot changes never alter this draft.
func (s *serviceImpl) seedDraft(worksetID string, now time.Time) sqlite.WorksetDraft {
	policy := reconcile.Policy{
		SchemaVersion:  1,
		ClassifierTags: loadPruneLiteralTags(s.configDir),
		Matched: reconcile.DesiredProfile{
			Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
			Encoded:  &reconcile.AudioOutputSpec{Codec: reconcile.CodecMp3, Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320}},
		},
		Unmatched: reconcile.DesiredProfile{
			Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
			Encoded:  &reconcile.AudioOutputSpec{Codec: reconcile.CodecMp3, Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320}},
		},
	}
	wf := planusecase.Workflow{
		SchemaVersion: planusecase.WorkflowSchemaVersion,
		Steps: []planusecase.WorkflowStep{{
			StepType: planusecase.StepTypeReconcileAudio,
			Policy:   planusecase.PolicySource{Kind: "inline", InlinePolicy: &policy},
		}},
	}
	wfJSON := mustJSON(wf)
	return sqlite.WorksetDraft{
		WorksetID:             worksetID,
		WorkflowSchemaVersion: 1,
		StepsJSON:             wfJSON,
		DraftHash:             hashJSON([]byte(wfJSON)),
		UpdatedAt:             now,
	}
}

// loadPruneLiteralTags reads the maintained initial literal tag list from
// config.json (prune.literal_tags). A missing/unreadable file yields an empty
// editable set — there is deliberately no compiled-in fallback.
func loadPruneLiteralTags(configDir string) []string {
	cfgPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil
	}
	var cfg appconfig.AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return reconcile.NormalizeTags(cfg.Prune.LiteralTags)
}

// newToken produces a sortable, collision-resistant identifier: a nanosecond
// timestamp plus a random suffix. The timestamp alone collides when two
// aggregates are created within the same nanosecond (e.g. test loops), which
// surfaced as a spurious idempotency-key conflict on the primary key.
func newToken() string {
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		// Fall back to the timestamp-only form rather than failing creation.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(rnd[:]))
}

func hashJSON(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// ==================== Views ====================

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
		views, next, err := s.listPage(ctx, q, limit, q.Cursor)
		return views, next, err
	}

	filtered := make([]*WorksetView, 0, limit+1)
	cursor := q.Cursor
	for {
		page, next, err := s.listPage(ctx, q, limit, cursor)
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
func (s *serviceImpl) listPage(ctx context.Context, q ListQuery, pageSize int, cursor string) ([]*WorksetView, string, error) {
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
		v, err := s.view(ctx, w)
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
	if v.PlanningState == PlanningUnplanned || v.PlanningState == PlanningNeedsPlanning || v.PlanningState == PlanningPlanning {
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
		if v.CurrentRevision.ValidationState == ValidationStale || v.CurrentRevision.ValidationState == ValidationUnavailable {
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
		if err == sqlite.ErrWorksetNotFound {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	return s.view(ctx, w)
}

func (s *serviceImpl) view(ctx context.Context, w *sqlite.Workset) (*WorksetView, error) {
	out := &WorksetView{
		WorksetID:     w.ID,
		Title:         w.Title,
		Version:       w.Version,
		PlanningState: s.planningState(ctx, w),
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
		roots, err := s.repo.GetWorkflowPlanRoots(w.CurrentRevisionID)
		if err == nil {
			for _, r := range roots {
				covered[r.RootPath] = true
			}
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

	if w.CurrentRevisionID != "" {
		sum, err := s.currentRevisionSummary(w)
		if err != nil {
			return nil, err
		}
		out.CurrentRevision = sum
	}

	active, err := s.repo.GetActiveGenerationForWorkset(w.ID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load active generation", err)
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
func (s *serviceImpl) planningState(ctx context.Context, w *sqlite.Workset) string {
	if w.LibraryID == "" {
		return PlanningOrphaned
	}
	if active, err := s.repo.GetActiveGenerationForWorkset(w.ID); err == nil && active != nil {
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

// currentRevisionSummary builds the compact immutable conclusion for the
// current revision with authoritative stale validation.
func (s *serviceImpl) currentRevisionSummary(w *sqlite.Workset) (*RevisionSummary, error) {
	rev, err := s.repo.GetWorksetRevision(w.ID, w.CurrentRevisionID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load current revision", err)
	}
	detail, err := s.repo.GetWorkflowPlanDetail(w.CurrentRevisionID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision plan", err)
	}
	stale, validation := s.validateRevision(w, detail)
	return &RevisionSummary{
		PlanID:          detail.Plan.PlanID,
		RevisionIndex:   rev.RevisionIndex,
		CreatedAt:       detail.Plan.CreatedAt,
		Status:          detail.Plan.Status,
		SummaryReason:   revisionSummaryReason(detail),
		BlockedCount:    revisionBlockedCount(detail),
		ValidationState: validation,
		Stale:           stale,
	}, nil
}

func revisionSummaryReason(detail *sqlite.WorkflowPlanDetail) string {
	if len(detail.Steps) == 0 {
		return ""
	}
	var s struct {
		SummaryReason string `json:"summary_reason"`
	}
	_ = json.Unmarshal([]byte(detail.Steps[0].StepSummaryJSON), &s)
	return s.SummaryReason
}

func revisionBlockedCount(detail *sqlite.WorkflowPlanDetail) int {
	count := 0
	for _, c := range detail.Components {
		if c.Status == "blocked" {
			count++
		}
	}
	for _, r := range detail.Roots {
		if r.RootStatus == "missing" {
			count++
		}
	}
	return count
}

// ==================== Mutations ====================

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
		if err == sqlite.ErrWorksetNotFound {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	if w.LibraryID == "" {
		return nil, NewError(ErrKindConflict, "ORPHANED_WORKSET", "orphaned worksets are read-only", nil)
	}
	if err := s.repo.UpdateWorksetTitle(id, title, req.IfMatchVersion, time.Now()); err != nil {
		if err == sqlite.ErrVersionConflict {
			return nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "workset version conflict", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to rename workset", err)
	}
	return s.GetWorkset(ctx, id)
}

// ==================== Draft ====================

func (s *serviceImpl) GetDraft(ctx context.Context, id string) (*Draft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := s.repo.GetWorkset(id)
	if err != nil {
		if err == sqlite.ErrWorksetNotFound {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	d, err := s.repo.GetWorksetDraft(id)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load draft", err)
	}
	if d == nil {
		return nil, NewError(ErrKindNotFound, "DRAFT_NOT_FOUND", "workset has no draft", nil)
	}
	wf, err := parseWorkflowJSON(d.StepsJSON)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to parse stored draft", err)
	}
	return &Draft{
		WorksetID:             d.WorksetID,
		Version:               w.Version,
		WorkflowSchemaVersion: d.WorkflowSchemaVersion,
		Workflow:              wf,
		WorkflowJSON:          d.StepsJSON,
		DraftHash:             d.DraftHash,
		UpdatedAt:             d.UpdatedAt,
	}, nil
}

func (s *serviceImpl) SaveDraft(ctx context.Context, id string, req SaveDraftRequest) (*WorksetView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := s.repo.GetWorkset(id)
	if err != nil {
		if err == sqlite.ErrWorksetNotFound {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	if w.LibraryID == "" {
		return nil, NewError(ErrKindConflict, "ORPHANED_WORKSET", "orphaned worksets are read-only", nil)
	}
	if err := validateWorkflow(req.Workflow); err != nil {
		return nil, err
	}
	// Reject while a generation is queued/running: the session freezes the
	// draft at enqueue time and must not race a replace.
	active, err := s.repo.GetActiveGenerationForWorkset(id)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active generation", err)
	}
	if active != nil {
		return nil, NewError(ErrKindConflict, "GENERATION_IN_PROGRESS", "cancel or wait for the active generation before editing the draft", nil)
	}
	stepsJSON := mustJSON(workflowToJSON(req.Workflow))
	draftHash := hashJSON([]byte(stepsJSON))
	if err := s.repo.UpdateWorksetDraft(id, req.Workflow.SchemaVersion, stepsJSON, draftHash, req.IfMatchVersion, time.Now()); err != nil {
		if err == sqlite.ErrVersionConflict {
			return nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "workset version conflict", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to save draft", err)
	}
	return s.GetWorkset(ctx, id)
}

// parseWorkflowJSON decodes strict workflow JSON.
func parseWorkflowJSON(s string) (planusecase.Workflow, error) {
	var wf planusecase.Workflow
	if err := json.Unmarshal([]byte(s), &wf); err != nil {
		return wf, err
	}
	return wf, nil
}

// workflowToJSON emits the canonical strict form for storage/hashing.
func workflowToJSON(wf planusecase.Workflow) planusecase.Workflow {
	return wf
}

// validateWorkflow accepts only schema v1 with exactly the supported step and
// a complete inline policy. The full reconcile.ValidatePolicy check applies
// here: both draft save and generation reject incomplete policies, because a
// draft is only saveable when it is an executable configuration.
func validateWorkflow(wf planusecase.Workflow) error {
	if wf.SchemaVersion != planusecase.WorkflowSchemaVersion {
		return NewError(ErrKindInvalidArgument, "INVALID_WORKFLOW_SCHEMA", fmt.Sprintf("unsupported workflow schema version %d", wf.SchemaVersion), nil)
	}
	if len(wf.Steps) != 1 || wf.Steps[0].StepType != planusecase.StepTypeReconcileAudio {
		return NewError(ErrKindInvalidArgument, "UNSUPPORTED_STEP", "schema v1 supports only the reconcile_audio_outputs step", nil)
	}
	policy := wf.Steps[0].Policy
	if policy.Kind != "inline" || policy.InlinePolicy == nil {
		return NewError(ErrKindInvalidArgument, "INVALID_POLICY_SOURCE", "workflow policy must be a complete inline policy", nil)
	}
	if err := reconcile.ValidatePolicy(*policy.InlinePolicy); err != nil {
		return NewError(ErrKindInvalidArgument, "INVALID_POLICY", err.Error(), nil)
	}
	return nil
}

// ==================== Generation start ====================

func (s *serviceImpl) StartGeneration(ctx context.Context, id string, req StartGenerationRequest) (*StartGenerationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.ExpectedDraftVersion <= 0 && req.IfMatchVersion > 0 {
		req.ExpectedDraftVersion = req.IfMatchVersion
	}
	if err := validateIdemKey(req.IdempotencyKey); err != nil {
		return nil, err
	}
	w, err := s.repo.GetWorkset(id)
	if err != nil {
		if err == sqlite.ErrWorksetNotFound {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	if w.LibraryID == "" {
		return nil, NewError(ErrKindConflict, "ORPHANED_WORKSET", "orphaned worksets are read-only", nil)
	}

	draft, err := s.repo.GetWorksetDraft(id)
	if err != nil || draft == nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load draft", err)
	}
	wf, err := parseWorkflowJSON(draft.StepsJSON)
	if err != nil {
		return nil, NewError(ErrKindInvalidArgument, "INVALID_WORKFLOW", "stored draft is invalid", err)
	}
	if err := validateWorkflow(wf); err != nil {
		return nil, err
	}

	// Reject while this workset has an active session.
	active, err := s.repo.GetActiveGenerationForWorkset(id)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active generation", err)
	}
	if active != nil {
		return nil, NewError(ErrKindConflict, "GENERATION_IN_PROGRESS", "a generation is already queued or running for this workset", nil)
	}

	// Reject while the owning library scans.
	scanning, err := s.repo.HasActiveScanForRoot(w.RootPath)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check library scan", err)
	}
	if scanning {
		return nil, NewError(ErrKindConflict, "SCAN_IN_PROGRESS", "wait for the library scan to finish before generating", nil)
	}

	members, err := s.repo.ListWorksetMembers(id)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load members", err)
	}
	if len(members) == 0 {
		return nil, NewError(ErrKindInvalidArgument, "INVALID_FOLDER_COUNT", "workset has no album folders", nil)
	}

	// Canonical request inputs: draft hash + member identity hash.
	draftHash := draft.DraftHash
	memberHash := hashMembers(members)

	// Dedup fast path: same draft hash, same member identity/order, and every
	// LIVE root fingerprint matching the current revision's persisted values →
	// return the current revision unchanged instead of enqueueing. This is
	// checked before the version precondition: an unchanged planning input is
	// never a conflict, even though a successful promotion bumped the version.
	if w.CurrentRevisionID != "" {
		fp, err := s.rootFingerprints(ctx, members)
		if err != nil {
			return nil, err
		}
		rev, rerr := s.repo.GetWorksetRevision(id, w.CurrentRevisionID)
		if rerr == nil && rev.DraftHash == draftHash && rev.MemberHash == memberHash && rootsMatch(w, fp, s.repo) {
			sum, serr := s.currentRevisionSummary(w)
			if serr == nil {
				return &StartGenerationResult{Revision: sum, Created: false}, nil
			}
		}
	}

	// Version precondition for a NEW session: the caller's view must not be
	// stale relative to the aggregate version it will freeze.
	if req.ExpectedDraftVersion > 0 && req.ExpectedDraftVersion != w.Version {
		return nil, NewError(ErrKindConflict, "DRAFT_VERSION_CONFLICT", "workset changed since the draft was read", nil)
	}

	// Idempotent replay: same key + same request returns the existing session.
	requestHash := hashJSON([]byte(fmt.Sprintf("%s|%s", draftHash, memberHash)))
	if req.IdempotencyKey != "" {
		existing, err := s.repo.GetGenerationByWorksetKey(id, req.IdempotencyKey)
		if err != nil {
			return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check generation idempotency", err)
		}
		if existing != nil {
			if existing.Status == sqlite.GenStatusCompleted || existing.Status == sqlite.GenStatusQueued || existing.Status == sqlite.GenStatusRunning {
				if existing.RequestHash == requestHash {
					return &StartGenerationResult{Generation: toGenerationView(existing), Created: false}, nil
				}
				return nil, NewError(ErrKindConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used with a different request", nil)
			}
			// failed/canceled/interrupted release the key for a fresh attempt.
		}
	}

	now := time.Now()
	gen := &sqlite.PlanGeneration{
		GenerationID:         "gen-" + newToken(),
		WorksetID:            id,
		IdempotencyKey:       req.IdempotencyKey,
		RequestHash:          requestHash,
		ExpectedDraftVersion: w.Version,
		RequestJSON:          mustJSON(map[string]any{"draft_hash": draftHash, "member_hash": memberHash, "roots": len(members)}),
		TotalRoots:           len(members),
		CreatedAt:            now,
	}
	if err := s.repo.CreateGeneration(gen); err != nil {
		if err == sqlite.ErrGenerationIdemConflict {
			existing, gerr := s.repo.GetGenerationByWorksetKey(id, req.IdempotencyKey)
			if gerr == nil && existing != nil {
				if existing.RequestHash == requestHash {
					return &StartGenerationResult{Generation: toGenerationView(existing), Created: false}, nil
				}
				return nil, NewError(ErrKindConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used with a different request", nil)
			}
			return nil, NewError(ErrKindConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key conflict", err)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to start generation", err)
	}
	s.dispatcher.wake()
	return &StartGenerationResult{Generation: toGenerationView(gen), Created: true}, nil
}

// GetGeneration returns the session view scoped to a workset.
func (s *serviceImpl) GetGeneration(ctx context.Context, worksetID, generationID string) (*GenerationView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, err := s.repo.GetGeneration(generationID)
	if err != nil {
		if err == sqlite.ErrGenerationNotFound {
			return nil, NewError(ErrKindNotFound, "GENERATION_NOT_FOUND", "generation not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load generation", err)
	}
	if g.WorksetID != worksetID {
		return nil, NewError(ErrKindNotFound, "GENERATION_NOT_FOUND", "generation not found", nil)
	}
	return toGenerationView(g), nil
}

// CancelGeneration cancels a session. It is idempotent: terminal rows keep
// their status. Orphaned worksets may still cancel a leftover generation for
// cleanup.
func (s *serviceImpl) CancelGeneration(ctx context.Context, worksetID, generationID string) (*GenerationView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, err := s.repo.GetGeneration(generationID)
	if err != nil {
		if err == sqlite.ErrGenerationNotFound {
			return nil, NewError(ErrKindNotFound, "GENERATION_NOT_FOUND", "generation not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load generation", err)
	}
	if g.WorksetID != worksetID {
		return nil, NewError(ErrKindNotFound, "GENERATION_NOT_FOUND", "generation not found", nil)
	}
	if g.Status == sqlite.GenStatusRunning || g.Status == sqlite.GenStatusQueued {
		if err := s.repo.CancelGeneration(generationID); err != nil {
			return nil, NewError(ErrKindInternal, "INTERNAL", "failed to cancel generation", err)
		}
		if g.Status == sqlite.GenStatusQueued {
			s.dispatcher.wake()
		}
	}
	return s.GetGeneration(ctx, worksetID, generationID)
}

// ==================== Revisions ====================

// ListRevisions returns one page of revision summaries newest-first with a
// keyset on revision_index plus the next-page cursor.
func (s *serviceImpl) ListRevisions(ctx context.Context, worksetID string, beforeIndex, limit int) (*RevisionListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	w, err := s.repo.GetWorkset(worksetID)
	if err != nil {
		if err == sqlite.ErrWorksetNotFound {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	revs, err := s.repo.ListWorksetRevisions(worksetID, beforeIndex, limit)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to list revisions", err)
	}
	out := make([]*RevisionSummary, 0, len(revs))
	for _, r := range revs {
		detail, derr := s.repo.GetWorkflowPlanDetail(r.PlanID)
		if derr != nil {
			continue // detached plan rows are skipped rather than failing the page
		}
		stale, validation := s.validateRevision(w, detail)
		out = append(out, &RevisionSummary{
			PlanID:          r.PlanID,
			RevisionIndex:   r.RevisionIndex,
			CreatedAt:       r.CreatedAt,
			Status:          detail.Plan.Status,
			SummaryReason:   revisionSummaryReason(detail),
			BlockedCount:    revisionBlockedCount(detail),
			ValidationState: validation,
			Stale:           stale,
		})
	}
	next := 0
	if len(revs) > 0 && len(revs) == limit {
		next = revs[len(revs)-1].RevisionIndex
	}
	return &RevisionListResult{Revisions: out, NextBeforeIndex: next}, nil
}

// GetRevision returns the immutable nested review detail.
func (s *serviceImpl) GetRevision(ctx context.Context, worksetID, planID string) (*RevisionView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rev, err := s.repo.GetWorksetRevision(worksetID, planID)
	if err != nil {
		if err == sqlite.ErrRevisionNotFound {
			return nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision", err)
	}
	detail, err := s.repo.GetWorkflowPlanDetail(planID)
	if err != nil {
		return nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
	}
	w, err := s.repo.GetWorkset(worksetID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	stale, _ := s.validateRevision(w, detail)

	out := &RevisionView{
		PlanID:        planID,
		RevisionIndex: rev.RevisionIndex,
		CreatedAt:     rev.CreatedAt,
	}
	roots := make([]RootValidation, 0, len(detail.Roots))
	for _, r := range detail.Roots {
		rootStale := false
		if stale != nil && *stale {
			rootStale = rootIsStale(s.repo, r)
		}
		roots = append(roots, RootValidation{
			RootIndex:            r.RootIndex,
			RootPath:             r.RootPath,
			RootStatus:           r.RootStatus,
			RootErrorCode:        r.RootErrorCode,
			RootErrorMessage:     r.RootErrorMessage,
			Stale:                rootStale,
			InventoryFingerprint: r.InventoryFingerprint,
			EntryCount:           r.EntryCount,
		})
	}
	out.Roots = roots
	for _, c := range detail.Components {
		out.ComponentRoots = append(out.ComponentRoots, ComponentRootRef{
			StepIndex:      c.StepIndex,
			ComponentIndex: c.ComponentIndex,
			ComponentID:    c.ComponentID,
			RootIndex:      c.RootIndex,
		})
	}
	out.Workflow = toPlanResponse(detail)
	return out, nil
}

// toPlanResponse converts a persisted workflow detail into the usecase
// response shape using the same reconstruction as the plan usecase
// GetWorkflowPlanDetail.
func toPlanResponse(detail *sqlite.WorkflowPlanDetail) planusecase.Response {
	out := planusecase.Response{
		PlanID:        detail.Plan.PlanID,
		SnapshotToken: detail.Plan.SnapshotToken,
		RootPath:      detail.Plan.RootPath,
		PlanKind:      detail.Plan.PlanKind,
		Summary: planusecase.Summary{
			SummaryReason: revisionSummaryReason(detail),
		},
		Operations:        []planusecase.Operation{},
		Errors:            []planusecase.FolderError{},
		SuccessfulFolders: []string{},
	}
	for _, st := range detail.Steps {
		sum := reconcileStepSummary(st.StepSummaryJSON)
		out.Summary.OperationCount += sum.OperationCount
		out.Summary.ErrorCount += sum.ErrorCount
		step := planusecase.StepResponse{
			StepType:   st.StepType,
			StepIndex:  st.StepIndex,
			Status:     st.Status,
			PolicyHash: st.PolicyHash,
			Summary:    sum,
		}
		_ = json.Unmarshal([]byte(st.PolicyJSON), &step.Policy)
		step.Classifier.Tags = strings.Split(st.ClassifierTags, "\x00")
		if step.Classifier.Tags[0] == "" && len(step.Classifier.Tags) == 1 {
			step.Classifier.Tags = []string{}
		}
		step.Classifier.Hash = st.ClassifierHash
		for _, c := range detail.Components {
			if c.StepIndex != st.StepIndex {
				continue
			}
			var comp reconcile.ComponentOutcome
			_ = json.Unmarshal([]byte(c.OutcomeJSON), &comp)
			step.Components = append(step.Components, comp)
		}
		out.Steps = append(out.Steps, step)
	}
	return out
}

func reconcileStepSummary(jsonStr string) reconcile.StepSummary {
	var sum reconcile.StepSummary
	_ = json.Unmarshal([]byte(jsonStr), &sum)
	return sum
}

// ==================== Fingerprint helpers ====================

// rootFingerprints recomputes the LIVE per-root inventory fingerprints for the
// given member folder paths (same entry collection and fingerprint function as
// the workflow runner). This is the dedup/stale authority: after a scan, the
// values reflect the current entries table.
func (s *serviceImpl) rootFingerprints(ctx context.Context, members []*sqlite.WorksetMember) (map[string]reconcile.ReconcileResult, error) {
	out := make(map[string]reconcile.ReconcileResult, len(members))
	for _, m := range members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := collectWorkflowEntries(s.repo, m.FolderPath)
		if err != nil {
			return nil, NewError(ErrKindInternal, "INTERNAL", fmt.Sprintf("failed to fingerprint %s: %v", m.FolderPath, err), err)
		}
		digest, count := reconcile.InventoryFingerprint(entries)
		out[m.FolderPath] = reconcile.ReconcileResult{Digest: digest, Count: count}
	}
	return out, nil
}

// rootsMatch compares the live fingerprints against the current revision's
// persisted fingerprints by root path.
func rootsMatch(w *sqlite.Workset, current map[string]reconcile.ReconcileResult, repo *sqlite.Repository) bool {
	if w.CurrentRevisionID == "" {
		return false
	}
	persisted, err := repo.GetWorkflowPlanRoots(w.CurrentRevisionID)
	if err != nil {
		return false
	}
	if len(persisted) != len(current) {
		return false
	}
	for _, r := range persisted {
		live, ok := current[r.RootPath]
		if !ok {
			return false
		}
		if live.Digest != r.InventoryFingerprint || live.Count != r.EntryCount {
			return false
		}
	}
	return true
}

func hashMembers(members []*sqlite.WorksetMember) string {
	h := sha256.New()
	for _, m := range members {
		h.Write([]byte(m.RelPath))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ==================== Cursor helpers ====================

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
