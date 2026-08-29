package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ==================== Workset aggregates ====================

// ErrWorksetNotFound is returned when a workset cannot be found.
var ErrWorksetNotFound = errors.New("workset not found")

// ErrWorksetIdemConflict is returned when a workset create collides with an
// existing creation idempotency key.
var ErrWorksetIdemConflict = errors.New("workset idempotency key conflict")

// ErrVersionConflict is returned when an If-Match version precondition fails
// on a workset mutation.
var ErrVersionConflict = errors.New("workset version conflict")

// ErrGenerationNotFound is returned when a planning session cannot be found.
var ErrGenerationNotFound = errors.New("generation not found")

// ErrRevisionNotFound is returned when a workset revision association cannot
// be found.
var ErrRevisionNotFound = errors.New("revision not found")

// ErrGenerationIdemConflict is returned when a generation start collides with
// an active/terminal idempotency key.
var ErrGenerationIdemConflict = errors.New("generation idempotency key conflict")

// Planning session statuses.
const (
	GenStatusQueued      = "queued"
	GenStatusRunning     = "running"
	GenStatusCompleted   = "completed"
	GenStatusFailed      = "failed"
	GenStatusCanceled    = "canceled"
	GenStatusInterrupted = "interrupted"
)

// Workset is the persisted aggregate row.
type Workset struct {
	ID                string
	Title             string
	LibraryID         string // "" when orphaned (library deleted)
	RootPath          string // snapshot: library root at creation
	RootPathKey       string // snapshot: canonical root identity at creation
	Version           int    // single aggregate concurrency counter
	CurrentRevisionID string // "" until the first revision promotes
	CreationIdemKey   string // "" when not set
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// WorksetMember is one ordered album-folder member. RelPath is the durable
// normalized library-relative identity; the rest are display snapshots.
type WorksetMember struct {
	WorksetID   string
	MemberIndex int
	RelPath     string
	FolderID    string
	FolderPath  string
	FolderName  string
}

// WorksetDraft is the mutable workflow draft of one workset.
type WorksetDraft struct {
	WorksetID             string
	WorkflowSchemaVersion int
	StepsJSON             string
	DraftHash             string
	UpdatedAt             time.Time
}

// WorksetRevision is the immutable revision association row.
type WorksetRevision struct {
	PlanID          string
	WorksetID       string
	RevisionIndex   int
	DraftHash       string
	MemberHash      string
	WorksetsVersion int
	CreatedAt       time.Time
}

// PlanGeneration is one persisted planning session.
type PlanGeneration struct {
	GenerationID         string
	WorksetID            string
	Status               string
	IdempotencyKey       string
	RequestHash          string
	ExpectedDraftVersion int
	RequestJSON          string
	TotalRoots           int
	CompletedRoots       int
	CurrentRoot          string
	ErrorCount           int
	CancelRequested      bool
	RevisionID           string // "" until completed
	ErrorCode            string
	ErrorMessage         string
	StartedAt            time.Time
	FinishedAt           time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// ==================== Workset CRUD ====================

const worksetColumns = `id, title, library_id, root_path, root_path_key, version, current_revision_id, creation_idem_key, created_at, updated_at`

func scanWorkset(scanner interface{ Scan(...any) error }) (*Workset, error) {
	var w Workset
	var libraryID, currentRevisionID, creationKey sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(&w.ID, &w.Title, &libraryID, &w.RootPath, &w.RootPathKey, &w.Version, &currentRevisionID, &creationKey, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	w.LibraryID = libraryID.String
	w.CurrentRevisionID = currentRevisionID.String
	w.CreationIdemKey = creationKey.String
	w.CreatedAt = parseTimestamp(createdAt)
	w.UpdatedAt = parseTimestamp(updatedAt)
	return &w, nil
}

// GetWorkset retrieves a workset by id.
func (r *Repository) GetWorkset(id string) (*Workset, error) {
	row := r.db.QueryRow(`SELECT `+worksetColumns+` FROM worksets WHERE id = ?`, id)
	w, err := scanWorkset(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWorksetNotFound
		}
		return nil, err
	}
	return w, nil
}

// GetWorksetByCreationIdemKey retrieves the workset owned by a creation
// idempotency key. Returns nil when no key match exists.
func (r *Repository) GetWorksetByCreationIdemKey(key string) (*Workset, error) {
	row := r.db.QueryRow(`SELECT `+worksetColumns+` FROM worksets WHERE creation_idem_key = ?`, key)
	w, err := scanWorkset(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return w, nil
}

// ClearExpiredWorksetIdemKey releases an expired creation idempotency key so a
// retried create transparently makes a new workset. The row itself is never
// deleted.
func (r *Repository) ClearExpiredWorksetIdemKey(id string, cutoff time.Time) error {
	_, err := r.db.Exec(`
		UPDATE worksets SET creation_idem_key = NULL
		WHERE id = ? AND julianday(created_at) < julianday(?)
	`, id, cutoff.Format(timeFormat))
	return err
}

// CreateWorkset inserts a workset with its ordered members and seeded draft in
// one transaction. It fails with ErrWorksetIdemConflict when the creation key
// is already owned by another workset.
func (r *Repository) CreateWorkset(w *Workset, members []WorksetMember, draft WorksetDraft) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin create workset tx: %w", err)
	}
	defer tx.Rollback()

	if err := InsertWorksetTx(tx, w, members, draft); err != nil {
		return err
	}
	return tx.Commit()
}

// InsertWorksetTx is the transaction-scoped form of CreateWorkset.
func InsertWorksetTx(tx *sql.Tx, w *Workset, members []WorksetMember, draft WorksetDraft) error {
	var libID, idemKey any
	if w.LibraryID != "" {
		libID = w.LibraryID
	}
	if w.CreationIdemKey != "" {
		idemKey = w.CreationIdemKey
	}
	var currentRev any
	if w.CurrentRevisionID != "" {
		currentRev = w.CurrentRevisionID
	}
	if _, err := tx.Exec(`
		INSERT INTO worksets (id, title, library_id, root_path, root_path_key, version, current_revision_id, creation_idem_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, w.ID, w.Title, libID, w.RootPath, w.RootPathKey, w.Version, currentRev, idemKey, w.CreatedAt.Format(timeFormat), w.UpdatedAt.Format(timeFormat)); err != nil {
		if isUniqueConstraintError(err) {
			return ErrWorksetIdemConflict
		}
		return fmt.Errorf("insert workset: %w", err)
	}
	for _, m := range members {
		if _, err := tx.Exec(`
			INSERT INTO workset_members (workset_id, member_index, rel_path, folder_id, folder_path, folder_name)
			VALUES (?, ?, ?, ?, ?, ?)
		`, m.WorksetID, m.MemberIndex, m.RelPath, m.FolderID, m.FolderPath, m.FolderName); err != nil {
			return fmt.Errorf("insert workset member: %w", err)
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO workset_drafts (workset_id, workflow_schema_version, steps_json, draft_hash, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, draft.WorksetID, draft.WorkflowSchemaVersion, draft.StepsJSON, draft.DraftHash, draft.UpdatedAt.Format(timeFormat)); err != nil {
		return fmt.Errorf("insert workset draft: %w", err)
	}
	return nil
}

// ListWorksets lists worksets newest-first with keyset pagination on
// (updated_at, id). includeOrphaned=false excludes orphaned worksets. When
// libraryID is non-empty only worksets owned by that library are returned.
// The result set has at most limit rows; the caller derives the next cursor
// from the last row.
func (r *Repository) ListWorksets(cursorUpdatedAt string, cursorID string, limit int, libraryID string, includeOrphaned bool) ([]*Workset, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT ` + worksetColumns + ` FROM worksets`
	var args []any
	var conds []string
	if libraryID != "" {
		conds = append(conds, "library_id = ?")
		args = append(args, libraryID)
	} else if !includeOrphaned {
		conds = append(conds, "library_id IS NOT NULL")
	}
	if cursorUpdatedAt != "" || cursorID != "" {
		conds = append(conds, `(julianday(updated_at) < julianday(?) OR (julianday(updated_at) = julianday(?) AND id < ?))`)
		args = append(args, cursorUpdatedAt, cursorUpdatedAt, cursorID)
	}
	if len(conds) > 0 {
		query += " WHERE " + joinConds(conds)
	}
	query += ` ORDER BY julianday(updated_at) DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Workset
	for rows.Next() {
		w, err := scanWorkset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func joinConds(conds []string) string {
	out := conds[0]
	for _, c := range conds[1:] {
		out += " AND " + c
	}
	return out
}

// ListWorksetMembers returns the ordered members of a workset.
func (r *Repository) ListWorksetMembers(worksetID string) ([]*WorksetMember, error) {
	rows, err := r.db.Query(`
		SELECT workset_id, member_index, rel_path, folder_id, folder_path, folder_name
		FROM workset_members WHERE workset_id = ? ORDER BY member_index
	`, worksetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*WorksetMember
	for rows.Next() {
		var m WorksetMember
		if err := rows.Scan(&m.WorksetID, &m.MemberIndex, &m.RelPath, &m.FolderID, &m.FolderPath, &m.FolderName); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// GetWorksetDraft retrieves the workflow draft of a workset.
func (r *Repository) GetWorksetDraft(worksetID string) (*WorksetDraft, error) {
	var d WorksetDraft
	var updatedAt string
	err := r.db.QueryRow(`
		SELECT workset_id, workflow_schema_version, steps_json, draft_hash, updated_at
		FROM workset_drafts WHERE workset_id = ?
	`, worksetID).Scan(&d.WorksetID, &d.WorkflowSchemaVersion, &d.StepsJSON, &d.DraftHash, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	d.UpdatedAt = parseTimestamp(updatedAt)
	return &d, nil
}

// UpdateWorksetTitle renames a workset with the aggregate version guard.
// Returns ErrWorksetNotFound when the row is absent and ErrVersionConflict
// when the If-Match version is stale.
func (r *Repository) UpdateWorksetTitle(id, title string, expectedVersion int, now time.Time) error {
	return r.updateWorkset("title", id, title, expectedVersion, now, "title = ?, version = version + 1, updated_at = ?")
}

// UpdateWorksetDraft replaces the full workflow draft and bumps the aggregate
// version in one transaction. A draft save must never be visible without its
// version bump, so the upsert and the guarded update share one commit.
func (r *Repository) UpdateWorksetDraft(id string, schemaVersion int, stepsJSON, draftHash string, expectedVersion int, now time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE worksets SET version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?
	`, now.Format(timeFormat), id, expectedVersion)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return r.versionGuardResult(id)
	}
	if _, err := tx.Exec(`
		INSERT INTO workset_drafts (workset_id, workflow_schema_version, steps_json, draft_hash, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workset_id) DO UPDATE SET
			workflow_schema_version = excluded.workflow_schema_version,
			steps_json = excluded.steps_json,
			draft_hash = excluded.draft_hash,
			updated_at = excluded.updated_at
	`, id, schemaVersion, stepsJSON, draftHash, now.Format(timeFormat)); err != nil {
		return err
	}
	return tx.Commit()
}

// updateWorkset applies a title update with the version guard.
func (r *Repository) updateWorkset(kind, id, value string, expectedVersion int, now time.Time, setClause string) error {
	result, err := r.db.Exec(`
		UPDATE worksets SET `+setClause+`
		WHERE id = ? AND version = ?
	`, value, now.Format(timeFormat), id, expectedVersion)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return r.versionGuardResult(id)
	}
	return nil
}

// versionGuardResult distinguishes a stale If-Match from a missing workset.
func (r *Repository) versionGuardResult(id string) error {
	var n int
	if err := r.db.QueryRow("SELECT COUNT(*) FROM worksets WHERE id = ?", id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrWorksetNotFound
	}
	return ErrVersionConflict
}

// ==================== Revisions ====================

// GetWorksetRevision retrieves one revision association by plan id.
func (r *Repository) GetWorksetRevision(worksetID, planID string) (*WorksetRevision, error) {
	var rev WorksetRevision
	var createdAt string
	err := r.db.QueryRow(`
		SELECT plan_id, workset_id, revision_index, draft_hash, member_hash, worksets_version, created_at
		FROM workset_revisions WHERE workset_id = ? AND plan_id = ?
	`, worksetID, planID).Scan(&rev.PlanID, &rev.WorksetID, &rev.RevisionIndex, &rev.DraftHash, &rev.MemberHash, &rev.WorksetsVersion, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRevisionNotFound
		}
		return nil, err
	}
	rev.CreatedAt = parseTimestamp(createdAt)
	return &rev, nil
}

// LatestWorksetRevision returns the highest-index revision association, or nil.
func (r *Repository) LatestWorksetRevision(worksetID string) (*WorksetRevision, error) {
	var rev WorksetRevision
	var createdAt string
	err := r.db.QueryRow(`
		SELECT plan_id, workset_id, revision_index, draft_hash, member_hash, worksets_version, created_at
		FROM workset_revisions WHERE workset_id = ? ORDER BY revision_index DESC LIMIT 1
	`, worksetID).Scan(&rev.PlanID, &rev.WorksetID, &rev.RevisionIndex, &rev.DraftHash, &rev.MemberHash, &rev.WorksetsVersion, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	rev.CreatedAt = parseTimestamp(createdAt)
	return &rev, nil
}

// ListWorksetRevisions returns revision associations newest-first (keyset on
// revision_index, at most limit rows).
func (r *Repository) ListWorksetRevisions(worksetID string, beforeIndex int, limit int) ([]*WorksetRevision, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT plan_id, workset_id, revision_index, draft_hash, member_hash, worksets_version, created_at
		FROM workset_revisions WHERE workset_id = ?`
	var args []any
	args = append(args, worksetID)
	if beforeIndex > 0 {
		query += ` AND revision_index < ?`
		args = append(args, beforeIndex)
	}
	query += ` ORDER BY revision_index DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*WorksetRevision
	for rows.Next() {
		var rev WorksetRevision
		var createdAt string
		if err := rows.Scan(&rev.PlanID, &rev.WorksetID, &rev.RevisionIndex, &rev.DraftHash, &rev.MemberHash, &rev.WorksetsVersion, &createdAt); err != nil {
			return nil, err
		}
		rev.CreatedAt = parseTimestamp(createdAt)
		out = append(out, &rev)
	}
	return out, rows.Err()
}

// WorksetRevisionPersist bundles the atomic completion payload: the workflow
// plan snapshot inserts, the revision association, the current-revision
// promotion, and the generation completion. DraftHash/MemberHash are the
// canonical frozen inputs for dedup and needs_planning derivation.
type WorksetRevisionPersist struct {
	PlanID          string
	RootPath        string
	SnapshotToken   string
	LibraryID       string
	DraftHash       string
	MemberHash      string
	WorksetsVersion int
	Steps           []WorkflowStepRecord
	Roots           []WorkflowRootRecord
	Components      []WorkflowComponentRecord
}

// PersistWorksetRevision atomically writes a completed generation: the plan
// snapshot, its revision association (revision_index = MAX+1), the workset
// current-revision promotion with the aggregate version bump, and the
// generation terminal state. A partial revision is never visible.
func (r *Repository) PersistWorksetRevision(genID, worksetID string, now time.Time, p WorksetRevisionPersist) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin revision persist tx: %w", err)
	}
	defer tx.Rollback()

	if err := InsertWorkflowPlanTx(tx, p.PlanID, "workflow", p.RootPath, p.SnapshotToken, p.LibraryID, p.Steps, p.Roots, p.Components); err != nil {
		return err
	}

	var next int
	if err := tx.QueryRow("SELECT COALESCE(MAX(revision_index), 0) + 1 FROM workset_revisions WHERE workset_id = ?", worksetID).Scan(&next); err != nil {
		return fmt.Errorf("next revision index: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO workset_revisions (plan_id, workset_id, revision_index, draft_hash, member_hash, worksets_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, p.PlanID, worksetID, next, p.DraftHash, p.MemberHash, p.WorksetsVersion, now.Format(timeFormat)); err != nil {
		return fmt.Errorf("insert workset revision: %w", err)
	}
	if _, err := tx.Exec(`
		UPDATE worksets SET current_revision_id = ?, version = version + 1, updated_at = ?
		WHERE id = ?
	`, p.PlanID, now.Format(timeFormat), worksetID); err != nil {
		return fmt.Errorf("promote workset revision: %w", err)
	}
	result, err := tx.Exec(`
		UPDATE plan_generations SET status = 'completed', revision_id = ?, finished_at = ?, updated_at = ?
		WHERE generation_id = ? AND status IN ('queued','running')
	`, p.PlanID, now.Format(timeFormat), now.Format(timeFormat), genID)
	if err != nil {
		return fmt.Errorf("complete generation: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrGenerationNotFound
	}
	return tx.Commit()
}

// DeleteWorksetRevisionPlan removes a revision plan and its association
// (cascade) explicitly. Not exposed in phase 1; retained for future cleanups.
// func (r *Repository) DeleteWorksetRevisionPlan(planID string) error { ... }

// ==================== Planning sessions ====================

func scanGeneration(scanner interface{ Scan(...any) error }) (*PlanGeneration, error) {
	var g PlanGeneration
	var startedAt, finishedAt, revisionID sql.NullString
	var cancelRequested int
	var createdAt, updatedAt string
	if err := scanner.Scan(&g.GenerationID, &g.WorksetID, &g.Status, &g.IdempotencyKey, &g.RequestHash, &g.ExpectedDraftVersion, &g.RequestJSON,
		&g.TotalRoots, &g.CompletedRoots, &g.CurrentRoot, &g.ErrorCount, &cancelRequested, &revisionID, &g.ErrorCode, &g.ErrorMessage,
		&startedAt, &finishedAt, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	g.CancelRequested = cancelRequested != 0
	g.RevisionID = revisionID.String
	if startedAt.Valid && startedAt.String != "" {
		g.StartedAt = parseTimestamp(startedAt.String)
	}
	if finishedAt.Valid && finishedAt.String != "" {
		g.FinishedAt = parseTimestamp(finishedAt.String)
	}
	g.CreatedAt = parseTimestamp(createdAt)
	g.UpdatedAt = parseTimestamp(updatedAt)
	return &g, nil
}

const generationColumns = `generation_id, workset_id, status, idempotency_key, request_hash, expected_draft_version, request_json,
	total_roots, completed_roots, current_root, error_count, cancel_requested, revision_id, error_code, error_message,
	started_at, finished_at, created_at, updated_at`

// CreateGeneration inserts a queued planning session. A unique conflict on the
// workset+key partial index returns ErrGenerationIdemConflict.
func (r *Repository) CreateGeneration(g *PlanGeneration) error {
	_, err := r.db.Exec(`
		INSERT INTO plan_generations (generation_id, workset_id, status, idempotency_key, request_hash, expected_draft_version, request_json,
			total_roots, completed_roots, current_root, error_count, cancel_requested, created_at, updated_at)
		VALUES (?, ?, 'queued', ?, ?, ?, ?, ?, 0, '', 0, 0, ?, ?)
	`, g.GenerationID, g.WorksetID, g.IdempotencyKey, g.RequestHash, g.ExpectedDraftVersion, g.RequestJSON, g.TotalRoots, g.CreatedAt.Format(timeFormat), g.CreatedAt.Format(timeFormat))
	if err != nil {
		if isUniqueConstraintError(err) {
			return ErrGenerationIdemConflict
		}
		return fmt.Errorf("insert generation: %w", err)
	}
	return nil
}

// GetGenerationByWorksetKey returns the generation owned by a workset and an
// idempotency key. Returns nil when no row matches.
func (r *Repository) GetGenerationByWorksetKey(worksetID, key string) (*PlanGeneration, error) {
	row := r.db.QueryRow(`SELECT `+generationColumns+` FROM plan_generations WHERE workset_id = ? AND idempotency_key = ?`, worksetID, key)
	g, err := scanGeneration(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return g, nil
}

// GetGeneration retrieves a planning session by id.
func (r *Repository) GetGeneration(generationID string) (*PlanGeneration, error) {
	row := r.db.QueryRow(`SELECT `+generationColumns+` FROM plan_generations WHERE generation_id = ?`, generationID)
	g, err := scanGeneration(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGenerationNotFound
		}
		return nil, err
	}
	return g, nil
}

// GetActiveGenerationForWorkset returns the queued/running session of a
// workset, newest first. Returns nil when none is active.
func (r *Repository) GetActiveGenerationForWorkset(worksetID string) (*PlanGeneration, error) {
	row := r.db.QueryRow(`
		SELECT `+generationColumns+` FROM plan_generations
		WHERE workset_id = ? AND status IN ('queued','running')
		ORDER BY julianday(created_at) DESC, generation_id DESC LIMIT 1
	`, worksetID)
	g, err := scanGeneration(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return g, nil
}

// LatestGenerationForWorkset returns the most recently created session of a
// workset (any status), or nil.
func (r *Repository) LatestGenerationForWorkset(worksetID string) (*PlanGeneration, error) {
	row := r.db.QueryRow(`
		SELECT `+generationColumns+` FROM plan_generations
		WHERE workset_id = ? ORDER BY julianday(created_at) DESC, generation_id DESC LIMIT 1
	`, worksetID)
	g, err := scanGeneration(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return g, nil
}

// NextQueuedGeneration claims the oldest queued generation globally (FIFO by
// created_at, generation_id). Claiming is a conditional update: when the row
// was canceled between select and update, zero rows are affected and the
// dispatcher skips it (canceled-queued sessions never run).
func (r *Repository) NextQueuedGeneration() (*PlanGeneration, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT ` + generationColumns + ` FROM plan_generations
		WHERE status = 'queued'
		ORDER BY julianday(created_at) ASC, generation_id ASC LIMIT 1
	`)
	if err != nil {
		return nil, err
	}
	var g *PlanGeneration
	if rows.Next() {
		g, err = scanGeneration(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if g == nil {
		return nil, nil
	}

	result, err := tx.Exec(`
		UPDATE plan_generations SET status = 'running', started_at = ?, updated_at = ?
		WHERE generation_id = ? AND status = 'queued'
	`, time.Now().Format(timeFormat), time.Now().Format(timeFormat), g.GenerationID)
	if err != nil {
		return nil, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil, nil // canceled/claimed between select and update; skip
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	g.Status = GenStatusRunning
	return g, nil
}

// UpdateGenerationProgress records root-level progress of a running session.
func (r *Repository) UpdateGenerationProgress(generationID string, completedRoots, errorCount int, currentRoot string) error {
	_, err := r.db.Exec(`
		UPDATE plan_generations SET completed_roots = ?, error_count = ?, current_root = ?, updated_at = ?
		WHERE generation_id = ?
	`, completedRoots, errorCount, currentRoot, time.Now().Format(timeFormat), generationID)
	return err
}

// CancelGeneration transitions a session to canceled. A queued session is
// canceled synchronously so the dispatcher's claim skips it; a running session
// sets only the cooperative flag, and the worker writes the terminal status at
// its next checkpoint. Terminal rows keep their existing status (cancel is
// idempotent: canceling a completed/failed session is a no-op that still
// returns success).
func (r *Repository) CancelGeneration(generationID string) error {
	_, err := r.db.Exec(`
		UPDATE plan_generations SET status = 'canceled', finished_at = ?, updated_at = ?
		WHERE generation_id = ? AND status = 'queued'
	`, time.Now().Format(timeFormat), time.Now().Format(timeFormat), generationID)
	if err != nil {
		return err
	}
	if _, err := r.db.Exec(`
		UPDATE plan_generations SET cancel_requested = 1, updated_at = ?
		WHERE generation_id = ? AND status = 'running'
	`, time.Now().Format(timeFormat), generationID); err != nil {
		return err
	}
	return nil
}

// MarkGenerationFailed records a stable system failure. Only queued/running
// rows transition; a completed row is never downgraded.
func (r *Repository) MarkGenerationFailed(generationID, code, message string) error {
	_, err := r.db.Exec(`
		UPDATE plan_generations SET status = 'failed', error_code = ?, error_message = ?, finished_at = ?, updated_at = ?
		WHERE generation_id = ? AND status IN ('queued','running')
	`, code, message, time.Now().Format(timeFormat), time.Now().Format(timeFormat), generationID)
	return err
}

// MarkGenerationInterrupted records the startup interruption of stale
// queued/running sessions, releasing their idempotency keys for retry.
func (r *Repository) MarkGenerationInterrupted(generationID string) error {
	_, err := r.db.Exec(`
		UPDATE plan_generations SET status = 'interrupted', error_code = 'CANCELED', finished_at = ?, updated_at = ?
		WHERE generation_id = ? AND status = 'running'
	`, time.Now().Format(timeFormat), time.Now().Format(timeFormat), generationID)
	return err
}

// CompleteGenerationCanceled records the worker's cooperative cancellation
// exit (cancel_requested observed at a checkpoint). The session ends canceled,
// which releases its idempotency key for retry.
func (r *Repository) CompleteGenerationCanceled(generationID string) error {
	_, err := r.db.Exec(`
		UPDATE plan_generations SET status = 'canceled', error_code = 'CANCELED', finished_at = ?, updated_at = ?
		WHERE generation_id = ? AND status = 'running'
	`, time.Now().Format(timeFormat), time.Now().Format(timeFormat), generationID)
	return err
}

// InterruptStaleGenerations marks queued/running sessions interrupted at
// startup, releasing their idempotency keys for retry. Terminal rows are
// untouched.
func (r *Repository) InterruptStaleGenerations() error {
	_, err := r.db.Exec(`
		UPDATE plan_generations SET status = 'interrupted', finished_at = ?, updated_at = ?
		WHERE status IN ('queued','running')
	`, time.Now().Format(timeFormat), time.Now().Format(timeFormat))
	return err
}

// HasActiveScanForRoot reports whether any library scan is queued/running
// against the given root path.
func (r *Repository) HasActiveScanForRoot(rootPath string) (bool, error) {
	var n int
	err := r.db.QueryRow(`
		SELECT COUNT(*) FROM scan_sessions
		WHERE root_path = ? AND status IN ('queued','running','merging')
	`, rootPath).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ==================== Library coordination ====================

// CountWorksetsForLibrary returns the number of worksets linked to a library.
func (r *Repository) CountWorksetsForLibrary(libraryID string) (int, error) {
	var n int
	err := r.db.QueryRow("SELECT COUNT(*) FROM worksets WHERE library_id = ?", libraryID).Scan(&n)
	return n, err
}

// LibraryRootPath returns the root path of a library.
func (r *Repository) LibraryRootPath(libraryID string) (string, error) {
	var p string
	err := r.db.QueryRow("SELECT root_path FROM libraries WHERE id = ?", libraryID).Scan(&p)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrLibraryNotFound
		}
		return "", err
	}
	return p, nil
}
