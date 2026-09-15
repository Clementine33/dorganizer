package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ==================== Workset operations ====================

// Operation is one independent Workset Operation (ADR 0004 §2) keyed by
// (workset, type). Version is the operation concurrency counter advanced by
// draft saves and revision publication; CurrentRevisionID is "" until the
// first successful generation publishes.
type Operation struct {
	WorksetID         string
	OperationType     string
	Version           int
	CurrentRevisionID string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// OperationDraft is the mutable sparse draft of one operation.
type OperationDraft struct {
	WorksetID     string
	OperationType string
	SchemaVersion int
	DraftJSON     string
	DraftHash     string
	UpdatedAt     time.Time
}

const operationColumns = `workset_id, operation_type, version, current_revision_id, created_at, updated_at`

func scanOperation(scanner interface{ Scan(...any) error }) (*Operation, error) {
	var o Operation
	var currentRevision sql.NullString
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&o.WorksetID,
		&o.OperationType,
		&o.Version,
		&currentRevision,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	o.CurrentRevisionID = currentRevision.String
	o.CreatedAt = parseTimestamp(createdAt)
	o.UpdatedAt = parseTimestamp(updatedAt)
	return &o, nil
}

// GetOperation loads one operation. Returns ErrOperationNotFound when the
// (workset, type) pair has no row, which is also the answer for an unknown
// workset: callers load the workset first when they need to distinguish.
func (r *Repository) GetOperation(worksetID, operationType string) (*Operation, error) {
	row := r.db.QueryRow(
		`SELECT `+operationColumns+` FROM workset_operations WHERE workset_id = ? AND operation_type = ?`,
		worksetID,
		operationType,
	)
	o, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOperationNotFound
		}
		return nil, err
	}
	return o, nil
}

// ListOperations returns a workset's operations in creation order.
func (r *Repository) ListOperations(worksetID string) ([]*Operation, error) {
	rows, err := r.db.Query(
		`SELECT `+operationColumns+` FROM workset_operations WHERE workset_id = ? ORDER BY created_at, operation_type`,
		worksetID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Operation
	for rows.Next() {
		o, scanErr := scanOperation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func insertOperation(tx *sql.Tx, o Operation) error {
	var currentRevision any
	if o.CurrentRevisionID != "" {
		currentRevision = o.CurrentRevisionID
	}
	if _, err := tx.Exec(`
		INSERT INTO workset_operations (workset_id, operation_type, version, current_revision_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, o.WorksetID, o.OperationType, o.Version, currentRevision, o.CreatedAt.Format(timeFormat), o.UpdatedAt.Format(timeFormat)); err != nil {
		return fmt.Errorf("insert operation: %w", err)
	}
	return nil
}

// GetOperationDraft loads one operation's draft. Returns nil when the
// operation has no draft row.
func (r *Repository) GetOperationDraft(worksetID, operationType string) (*OperationDraft, error) {
	var d OperationDraft
	var updatedAt string
	err := r.db.QueryRow(`
		SELECT workset_id, operation_type, schema_version, draft_json, draft_hash, updated_at
		FROM workset_operation_drafts WHERE workset_id = ? AND operation_type = ?
	`, worksetID, operationType).Scan(
		&d.WorksetID,
		&d.OperationType,
		&d.SchemaVersion,
		&d.DraftJSON,
		&d.DraftHash,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	d.UpdatedAt = parseTimestamp(updatedAt)
	return &d, nil
}

// SaveOperationDraft replaces the full draft document and bumps the operation
// version in one transaction. The draft write must never be visible without
// its version bump, so the guarded update and the upsert share one commit.
// ErrVersionConflict on a stale If-Match, ErrOperationNotFound when the
// operation row is gone.
func (r *Repository) SaveOperationDraft(
	worksetID, operationType string,
	schemaVersion int,
	draftJSON, draftHash string,
	expectedVersion int,
	now time.Time,
) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		UPDATE workset_operations SET version = version + 1, updated_at = ?
		WHERE workset_id = ? AND operation_type = ? AND version = ?
	`, now.Format(timeFormat), worksetID, operationType, expectedVersion)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return operationVersionGuardResult(tx, worksetID, operationType)
	}
	if err := upsertOperationDraft(tx, OperationDraft{
		WorksetID:     worksetID,
		OperationType: operationType,
		SchemaVersion: schemaVersion,
		DraftJSON:     draftJSON,
		DraftHash:     draftHash,
		UpdatedAt:     now,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertOperationDraft(tx *sql.Tx, d OperationDraft) error {
	if _, err := tx.Exec(`
		INSERT INTO workset_operation_drafts (workset_id, operation_type, schema_version, draft_json, draft_hash, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(workset_id, operation_type) DO UPDATE SET
			schema_version = excluded.schema_version,
			draft_json = excluded.draft_json,
			draft_hash = excluded.draft_hash,
			updated_at = excluded.updated_at
	`, d.WorksetID, d.OperationType, d.SchemaVersion, d.DraftJSON, d.DraftHash, d.UpdatedAt.Format(timeFormat)); err != nil {
		return fmt.Errorf("upsert operation draft: %w", err)
	}
	return nil
}

// operationVersionGuardResult distinguishes a stale version from a missing
// operation row.
func operationVersionGuardResult(tx *sql.Tx, worksetID, operationType string) error {
	var n int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM workset_operations WHERE workset_id = ? AND operation_type = ?",
		worksetID, operationType,
	).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrOperationNotFound
	}
	return ErrVersionConflict
}

// ==================== Operation revisions ====================

// ErrRevisionNotFound is returned when an operation revision cannot be found.
var ErrRevisionNotFound = errors.New("revision not found")

// OperationRevision is the immutable revision association row. ExcludedScope
// (NUL-joined member_ids) and DraftSnapshot (frozen sparse draft JSON) are part
// of the immutable snapshot (ADR 0004 §4).
type OperationRevision struct {
	PlanID           string
	WorksetID        string
	OperationType    string
	RevisionIndex    int
	DraftHash        string
	MemberHash       string
	OperationVersion int
	ExcludedScope    string
	DraftSnapshot    string
	CreatedAt        time.Time
}

// GetOperationRevision retrieves one revision association by plan id.
func (r *Repository) GetOperationRevision(worksetID, operationType, planID string) (*OperationRevision, error) {
	var rev OperationRevision
	var createdAt string
	err := r.db.QueryRow(`
		SELECT plan_id, workset_id, operation_type, revision_index, draft_hash, member_hash,
		       operation_version, excluded_scope, draft_snapshot, created_at
		FROM workset_operation_revisions WHERE workset_id = ? AND operation_type = ? AND plan_id = ?
	`, worksetID, operationType, planID).Scan(
		&rev.PlanID,
		&rev.WorksetID,
		&rev.OperationType,
		&rev.RevisionIndex,
		&rev.DraftHash,
		&rev.MemberHash,
		&rev.OperationVersion,
		&rev.ExcludedScope,
		&rev.DraftSnapshot,
		&createdAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRevisionNotFound
		}
		return nil, err
	}
	rev.CreatedAt = parseTimestamp(createdAt)
	return &rev, nil
}

// ListOperationRevisions returns revision associations newest-first (keyset on
// revision_index, at most limit rows).
func (r *Repository) ListOperationRevisions(
	worksetID, operationType string,
	beforeIndex int,
	limit int,
) ([]*OperationRevision, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT plan_id, workset_id, operation_type, revision_index, draft_hash, member_hash,
		       operation_version, excluded_scope, draft_snapshot, created_at
		FROM workset_operation_revisions WHERE workset_id = ? AND operation_type = ?`
	args := []any{worksetID, operationType}
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

	var out []*OperationRevision
	for rows.Next() {
		var rev OperationRevision
		var createdAt string
		if err := rows.Scan(
			&rev.PlanID,
			&rev.WorksetID,
			&rev.OperationType,
			&rev.RevisionIndex,
			&rev.DraftHash,
			&rev.MemberHash,
			&rev.OperationVersion,
			&rev.ExcludedScope,
			&rev.DraftSnapshot,
			&createdAt,
		); err != nil {
			return nil, err
		}
		rev.CreatedAt = parseTimestamp(createdAt)
		out = append(out, &rev)
	}
	return out, rows.Err()
}

// OperationRevisionPersist bundles the atomic completion payload: the plan
// plan snapshot inserts, the revision association, the operation's
// current-revision promotion and the generation completion. DraftHash and
// MemberHash are the canonical frozen inputs for dedup and needs_planning
// derivation.
type OperationRevisionPersist struct {
	PlanID        string
	RootPath      string
	SnapshotToken string
	LibraryID     string
	DraftHash     string
	MemberHash    string
	// OperationVersion is the operation version observed at enqueue time,
	// frozen on the revision for audit.
	OperationVersion int
	// ExcludedScope is the NUL-joined set of member_ids excluded from planning
	// for this revision; empty when nothing was excluded.
	ExcludedScope string
	// DraftSnapshot is the frozen sparse draft JSON: the review UI resolves the
	// per-member effective configs and inheritance sources from it.
	DraftSnapshot string
	// TaskSchemaVersion is the plan payload's schema version, stored on the
	// plan row so the review envelope can name it.
	TaskSchemaVersion int
	Steps             []PlanStepRecord
	Roots             []PlanRootRecord
	Components        []PlanComponentRecord
}

// PersistOperationRevision atomically writes a completed generation: the plan
// snapshot, its revision association (revision_index = MAX+1 within the
// operation), the operation's current-revision promotion with the operation
// version bump, and the generation terminal state. A partial revision is never
// visible.
func (r *Repository) PersistOperationRevision(
	genID, worksetID, operationType string,
	now time.Time,
	p OperationRevisionPersist,
) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin revision persist tx: %w", err)
	}
	defer tx.Rollback()

	taskKind := operationType
	if insertErr := InsertPlanTx(
		tx,
		p.PlanID,
		taskKind,
		p.TaskSchemaVersion,
		p.RootPath,
		p.SnapshotToken,
		p.LibraryID,
		p.Steps,
		p.Roots,
		p.Components,
	); insertErr != nil {
		return insertErr
	}

	var next int
	if nextErr := tx.QueryRow(
		"SELECT COALESCE(MAX(revision_index), 0) + 1 FROM workset_operation_revisions WHERE workset_id = ? AND operation_type = ?",
		worksetID,
		operationType,
	).Scan(&next); nextErr != nil {
		return fmt.Errorf("next revision index: %w", nextErr)
	}
	if _, revErr := tx.Exec(`
		INSERT INTO workset_operation_revisions (plan_id, workset_id, operation_type, revision_index, draft_hash, member_hash, operation_version, excluded_scope, draft_snapshot, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.PlanID, worksetID, operationType, next, p.DraftHash, p.MemberHash, p.OperationVersion, p.ExcludedScope, p.DraftSnapshot, now.Format(timeFormat)); revErr != nil {
		return fmt.Errorf("insert operation revision: %w", revErr)
	}
	if _, promoteErr := tx.Exec(`
		UPDATE workset_operations SET current_revision_id = ?, version = version + 1, updated_at = ?
		WHERE workset_id = ? AND operation_type = ?
	`, p.PlanID, now.Format(timeFormat), worksetID, operationType); promoteErr != nil {
		return fmt.Errorf("promote operation revision: %w", promoteErr)
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
