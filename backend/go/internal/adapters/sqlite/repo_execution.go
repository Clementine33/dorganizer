package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/onsei/organizer/backend/internal/inventory"
	"github.com/onsei/organizer/backend/internal/library"
	"github.com/onsei/organizer/backend/internal/workset"
)

// ErrExecutionInProgress is returned when an operation that must wait for an
// active execution (library deletion) is attempted.

const executionColumns = `execution_id, workset_id, operation_type, plan_id, status,
	idempotency_key, request_hash, expected_operation_version, request_json,
	total_components, completed_components, total_operations, completed_operations,
	current_root, current_component_id, current_component_index, current_phase,
	cancel_requested, error_code, error_message, started_at, finished_at, created_at, updated_at`

// executionProgressColumns is the control-only projection the cancel watchdog
// and the event stream poll: no request payload, no per-component result.
const executionProgressColumns = `execution_id, workset_id, operation_type, plan_id, status,
	cancel_requested, total_components, completed_components, total_operations, completed_operations,
	current_root, current_component_id, current_component_index, current_phase,
	error_code, error_message, started_at, finished_at, created_at, updated_at`

func scanExecution(scanner interface{ Scan(...any) error }) (*workset.PlanExecution, error) {
	var e workset.PlanExecution
	var startedAt, finishedAt sql.NullString
	var cancelRequested int
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&e.ExecutionID,
		&e.WorksetID,
		&e.OperationType,
		&e.PlanID,
		&e.Status,
		&e.IdempotencyKey,
		&e.RequestHash,
		&e.ExpectedOperationVersion,
		&e.RequestJSON,
		&e.TotalComponents,
		&e.CompletedComponents,
		&e.TotalOperations,
		&e.CompletedOperations,
		&e.CurrentRoot,
		&e.CurrentComponentID,
		&e.CurrentComponentIndex,
		&e.CurrentPhase,
		&cancelRequested,
		&e.ErrorCode,
		&e.ErrorMessage,
		&startedAt,
		&finishedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	e.CancelRequested = cancelRequested != 0
	if startedAt.Valid && startedAt.String != "" {
		e.StartedAt = parseTimestamp(startedAt.String)
	}
	if finishedAt.Valid && finishedAt.String != "" {
		e.FinishedAt = parseTimestamp(finishedAt.String)
	}
	e.CreatedAt = parseTimestamp(createdAt)
	e.UpdatedAt = parseTimestamp(updatedAt)
	return &e, nil
}

// scanExecutionProgress scans executionProgressColumns into the same type,
// leaving the fields the projection does not carry at their zero value.
func scanExecutionProgress(scanner interface{ Scan(...any) error }) (*workset.PlanExecution, error) {
	var e workset.PlanExecution
	var startedAt, finishedAt sql.NullString
	var cancelRequested int
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&e.ExecutionID,
		&e.WorksetID,
		&e.OperationType,
		&e.PlanID,
		&e.Status,
		&cancelRequested,
		&e.TotalComponents,
		&e.CompletedComponents,
		&e.TotalOperations,
		&e.CompletedOperations,
		&e.CurrentRoot,
		&e.CurrentComponentID,
		&e.CurrentComponentIndex,
		&e.CurrentPhase,
		&e.ErrorCode,
		&e.ErrorMessage,
		&startedAt,
		&finishedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	e.CancelRequested = cancelRequested != 0
	if startedAt.Valid && startedAt.String != "" {
		e.StartedAt = parseTimestamp(startedAt.String)
	}
	if finishedAt.Valid && finishedAt.String != "" {
		e.FinishedAt = parseTimestamp(finishedAt.String)
	}
	e.CreatedAt = parseTimestamp(createdAt)
	e.UpdatedAt = parseTimestamp(updatedAt)
	return &e, nil
}

// CreateExecutionGuarded inserts a queued execution session only when the
// persisted eligibility facts still hold, as one conditional write statement:
// the guard predicates and the insert are evaluated atomically by SQLite, so a
// concurrent draft save, revision promotion, generation or library deletion
// cannot slip between the gate checks and the session's creation. The unique
// indexes cover the remaining invariants (one session per idempotency key and
// per revision).
//
// Sentinel failures: workset.ErrExecutionIdemConflict (key or revision already has a
// session), workset.ErrVersionConflict, workset.ErrRevisionNotFound (no longer the current
// revision), workset.ErrDraftChanged, ErrGenerationInProgress, workset.ErrWorksetOrphaned,
// workset.ErrWorksetNotFound, workset.ErrOperationNotFound.
func (r *Repository) CreateExecutionGuarded(e *workset.PlanExecution, g workset.ExecutionGuards) error {
	now := e.CreatedAt.Format(timeFormat)
	result, err := r.db.Exec(`
		INSERT INTO plan_executions (
			execution_id, workset_id, operation_type, plan_id, status,
			idempotency_key, request_hash, expected_operation_version, request_json,
			total_components, total_operations, created_at, updated_at
		)
		SELECT ?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?
		WHERE EXISTS (
			SELECT 1 FROM worksets w WHERE w.id = ? AND w.library_id IS NOT NULL
		)
		AND EXISTS (
			SELECT 1 FROM workset_operations o
			WHERE o.workset_id = ? AND o.operation_type = ?
			  AND o.version = ? AND o.current_revision_id = ?
		)
		AND EXISTS (
			SELECT 1 FROM workset_operation_drafts d
			WHERE d.workset_id = ? AND d.operation_type = ? AND d.draft_hash = ?
		)
		AND NOT EXISTS (
			SELECT 1 FROM plan_generations gen
			WHERE gen.workset_id = ? AND gen.operation_type = ? AND gen.status IN ('queued','running')
		)
	`, e.ExecutionID, e.WorksetID, e.OperationType, e.PlanID,
		e.IdempotencyKey, e.RequestHash, e.ExpectedOperationVersion, e.RequestJSON,
		e.TotalComponents, e.TotalOperations, now, now,
		e.WorksetID,
		e.WorksetID, e.OperationType, g.ExpectedOperationVersion, e.PlanID,
		e.WorksetID, e.OperationType, g.ExpectedDraftHash,
		e.WorksetID, e.OperationType)
	if err != nil {
		if isUniqueConstraintError(err) {
			return workset.ErrExecutionIdemConflict
		}
		return fmt.Errorf("insert execution: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return r.classifyExecutionGuards(e, g)
	}
	return nil
}

// classifyExecutionGuards names the fact that refused a guarded insert. The
// refusing write already committed, so the re-read observes it; the residual
// race (a fact that changed and changed back) is reported as
// workset.ErrExecutionNotEligible.
func (r *Repository) classifyExecutionGuards(e *workset.PlanExecution, g workset.ExecutionGuards) error {
	var libraryID sql.NullString
	err := r.db.QueryRow("SELECT library_id FROM worksets WHERE id = ?", e.WorksetID).Scan(&libraryID)
	if errors.Is(err, sql.ErrNoRows) {
		return workset.ErrWorksetNotFound
	}
	if err != nil {
		return err
	}
	if !libraryID.Valid || libraryID.String == "" {
		return workset.ErrWorksetOrphaned
	}
	var version int
	var current string
	err = r.db.QueryRow(`
		SELECT version, COALESCE(current_revision_id, '') FROM workset_operations
		WHERE workset_id = ? AND operation_type = ?
	`, e.WorksetID, e.OperationType).Scan(&version, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return workset.ErrOperationNotFound
	}
	if err != nil {
		return err
	}
	if version != g.ExpectedOperationVersion {
		return workset.ErrVersionConflict
	}
	if current != e.PlanID || g.ExpectedCurrentRevision != e.PlanID {
		return workset.ErrRevisionNotFound
	}
	var draftHash string
	err = r.db.QueryRow(`
		SELECT COALESCE(draft_hash, '') FROM workset_operation_drafts
		WHERE workset_id = ? AND operation_type = ?
	`, e.WorksetID, e.OperationType).Scan(&draftHash)
	if errors.Is(err, sql.ErrNoRows) {
		return workset.ErrDraftChanged
	}
	if err != nil {
		return err
	}
	if draftHash != g.ExpectedDraftHash {
		return workset.ErrDraftChanged
	}
	var generating int
	if genErr := r.db.QueryRow(`
		SELECT COUNT(*) FROM plan_generations
		WHERE workset_id = ? AND operation_type = ? AND status IN ('queued','running')
	`, e.WorksetID, e.OperationType).Scan(&generating); genErr != nil {
		return genErr
	}
	if generating > 0 {
		return library.ErrGenerationInProgress
	}
	// The revision already has a session (the caller distinguishes a key replay
	// from an executed revision), or another session of the operation is active.
	if existing, existErr := r.GetExecutionForRevision(e.PlanID); existErr == nil && existing != nil {
		return workset.ErrExecutionIdemConflict
	}
	if active, activeErr := r.GetActiveExecutionForOperation(
		e.WorksetID,
		e.OperationType,
	); activeErr == nil &&
		active != nil {
		return library.ErrExecutionInProgress
	}
	return workset.ErrExecutionNotEligible
}

// GetExecution retrieves an execution session by id.
func (r *Repository) GetExecution(executionID string) (*workset.PlanExecution, error) {
	row := r.db.QueryRow(`SELECT `+executionColumns+` FROM plan_executions WHERE execution_id = ?`, executionID)
	e, err := scanExecution(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, workset.ErrExecutionNotFound
		}
		return nil, err
	}
	return e, nil
}

// GetExecutionByOperationKey returns the execution owned by an operation and an
// idempotency key. Returns nil when no row matches. Every status replays: a
// retried start must observe the session it already created.
func (r *Repository) GetExecutionByOperationKey(worksetID, operationType, key string) (*workset.PlanExecution, error) {
	if key == "" {
		return nil, nil
	}
	row := r.db.QueryRow(
		`SELECT `+executionColumns+` FROM plan_executions
		 WHERE workset_id = ? AND operation_type = ? AND idempotency_key = ?`,
		worksetID, operationType, key,
	)
	e, err := scanExecution(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return e, nil
}

// GetExecutionForRevision returns the newest execution of one revision, or nil
// when the revision was never executed. A revision is executed at most once, so
// the newest row is the only row.
func (r *Repository) GetExecutionForRevision(planID string) (*workset.PlanExecution, error) {
	row := r.db.QueryRow(
		`SELECT `+executionColumns+` FROM plan_executions
		 WHERE plan_id = ? ORDER BY julianday(created_at) DESC, execution_id DESC LIMIT 1`,
		planID,
	)
	e, err := scanExecution(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return e, nil
}

// GetActiveExecutionForOperation returns the queued/running session of one
// operation, newest first. Returns nil when none is active.
func (r *Repository) GetActiveExecutionForOperation(worksetID, operationType string) (*workset.PlanExecution, error) {
	row := r.db.QueryRow(
		`SELECT `+executionColumns+` FROM plan_executions
		 WHERE workset_id = ? AND operation_type = ? AND status IN ('queued','running')
		 ORDER BY julianday(created_at) DESC, execution_id DESC LIMIT 1`,
		worksetID, operationType,
	)
	e, err := scanExecution(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return e, nil
}

// LatestExecutionForOperation returns the most recently created session of one
// operation (any status), or nil.
func (r *Repository) LatestExecutionForOperation(worksetID, operationType string) (*workset.PlanExecution, error) {
	row := r.db.QueryRow(
		`SELECT `+executionColumns+` FROM plan_executions
		 WHERE workset_id = ? AND operation_type = ?
		 ORDER BY julianday(created_at) DESC, execution_id DESC LIMIT 1`,
		worksetID, operationType,
	)
	e, err := scanExecution(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return e, nil
}

// NextQueuedExecution claims the oldest queued session globally. Claiming is a
// conditional update: a session canceled between select and update affects zero
// rows and is skipped, so a canceled-queued session never starts.
func (r *Repository) NextQueuedExecution() (*workset.PlanExecution, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT ` + executionColumns + ` FROM plan_executions
		WHERE status = 'queued'
		ORDER BY julianday(created_at) ASC, execution_id ASC LIMIT 1
	`)
	if err != nil {
		return nil, err
	}
	var e *workset.PlanExecution
	if rows.Next() {
		e, err = scanExecution(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
	}
	rows.Close()
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}
	if e == nil {
		return nil, nil
	}

	now := time.Now()
	result, err := tx.Exec(`
		UPDATE plan_executions SET status = 'running', started_at = ?, updated_at = ?
		WHERE execution_id = ? AND status = 'queued'
	`, now.Format(timeFormat), now.Format(timeFormat), e.ExecutionID)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return nil, nil // canceled/claimed between select and update; skip
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	e.Status = workset.ExecStatusRunning
	e.StartedAt = now
	return e, nil
}

// UpdateExecutionPosition records where a running session is without touching
// any component result. It is used for the first unit of a run, which no
// boundary has written yet; later boundaries publish the next position with
// their own result.
func (r *Repository) UpdateExecutionPosition(executionID string, p workset.ExecutionPosition) error {
	_, err := r.db.Exec(`
		UPDATE plan_executions SET
			current_root = ?, current_component_id = ?, current_component_index = ?,
			current_phase = ?, updated_at = ?
		WHERE execution_id = ?
	`, p.CurrentRoot, p.CurrentComponentID, p.CurrentComponentIndex, p.CurrentPhase,
		time.Now().Format(timeFormat), executionID)
	return err
}

// SaveExecutionComponentResult writes one component's result and moves the
// session's position in a single transaction. The counters are recomputed from
// the result rows rather than adjusted, so replaying this call after a failed
// commit cannot double-count; the result row itself is replaced wholesale, which
// is what makes the replay idempotent. p is the position the run moves to —
// the next component to work on — and is left empty for the last one.
func (r *Repository) SaveExecutionComponentResult(
	executionID string,
	res workset.ExecutionComponentResult,
	p workset.ExecutionPosition,
) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin component result tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO execution_component_results (
			execution_id, component_index, status, completed_operations, result_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(execution_id, component_index) DO UPDATE SET
			status = excluded.status,
			completed_operations = excluded.completed_operations,
			result_json = excluded.result_json,
			updated_at = excluded.updated_at
	`, executionID, res.ComponentIndex, res.Status, res.CompletedOperations, res.ResultJSON,
		time.Now().Format(timeFormat)); err != nil {
		return fmt.Errorf("save component result: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE plan_executions SET
			completed_components = (
				SELECT COUNT(*) FROM execution_component_results
				WHERE execution_id = ? AND status <> 'pending'
			),
			completed_operations = (
				SELECT COALESCE(SUM(completed_operations), 0) FROM execution_component_results
				WHERE execution_id = ?
			),
			current_root = ?, current_component_id = ?, current_component_index = ?,
			current_phase = ?, updated_at = ?
		WHERE execution_id = ?
	`, executionID, executionID, p.CurrentRoot, p.CurrentComponentID, p.CurrentComponentIndex,
		p.CurrentPhase, time.Now().Format(timeFormat), executionID); err != nil {
		return fmt.Errorf("update execution position: %w", err)
	}

	return tx.Commit()
}

// ListExecutionComponentResults returns the results of one session in
// component order, starting at fromIndex (inclusive). A limit of zero or less
// returns every remaining row.
func (r *Repository) ListExecutionComponentResults(
	executionID string,
	fromIndex, limit int,
) ([]workset.ExecutionComponentResult, error) {
	query := `
		SELECT component_index, status, completed_operations, result_json, updated_at
		FROM execution_component_results
		WHERE execution_id = ? AND component_index >= ?
		ORDER BY component_index
	`
	args := []any{executionID, fromIndex}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list component results: %w", err)
	}
	defer rows.Close()

	results := make([]workset.ExecutionComponentResult, 0)
	for rows.Next() {
		var res workset.ExecutionComponentResult
		var updatedAt string
		if err := rows.Scan(
			&res.ComponentIndex, &res.Status, &res.CompletedOperations, &res.ResultJSON, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan component result: %w", err)
		}
		res.UpdatedAt = parseTimestamp(updatedAt)
		results = append(results, res)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate component results: %w", err)
	}
	return results, nil
}

// GetExecutionProgress reads the control fields of one session without its
// frozen request or any component result: what the cancel watchdog and the
// event stream poll. It returns workset.ErrExecutionNotFound for an unknown session.
func (r *Repository) GetExecutionProgress(executionID string) (*workset.PlanExecution, error) {
	row := r.db.QueryRow(
		`SELECT `+executionProgressColumns+` FROM plan_executions WHERE execution_id = ?`,
		executionID,
	)
	e, err := scanExecutionProgress(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, workset.ErrExecutionNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// FinishExecution writes a terminal status. Only queued/running rows
// transition, so a terminal status never regresses and a canceled-queued
// session is not resurrected by a worker finishing a stale run. The component
// results are already durable: this row holds no report to rewrite.
func (r *Repository) FinishExecution(
	executionID, status, errorCode, errorMessage string,
) error {
	_, err := r.db.Exec(`
		UPDATE plan_executions SET status = ?, error_code = ?, error_message = ?,
			finished_at = ?, updated_at = ?
		WHERE execution_id = ? AND status IN ('queued','running')
	`, status, errorCode, errorMessage, time.Now().Format(timeFormat),
		time.Now().Format(timeFormat), executionID)
	return err
}

// CancelExecution transitions a session to canceled. A queued session is
// canceled synchronously so the claim skips it; a running session sets only the
// cooperative flag, and the worker writes the terminal status at its next safe
// boundary. Terminal rows keep their status (cancel is idempotent).
func (r *Repository) CancelExecution(executionID string) error {
	now := time.Now().Format(timeFormat)
	if _, err := r.db.Exec(`
		UPDATE plan_executions SET status = 'canceled', error_code = 'CANCELED',
			finished_at = ?, updated_at = ?
		WHERE execution_id = ? AND status = 'queued'
	`, now, now, executionID); err != nil {
		return err
	}
	if _, err := r.db.Exec(`
		UPDATE plan_executions SET cancel_requested = 1, updated_at = ?
		WHERE execution_id = ? AND status = 'running'
	`, now, executionID); err != nil {
		return err
	}
	return nil
}

// InterruptStaleExecutions marks queued/running sessions interrupted at
// startup. Partial results stay on the row; nothing is resumed, re-encoded or
// re-deleted.
func (r *Repository) InterruptStaleExecutions() (int64, error) {
	now := time.Now().Format(timeFormat)
	result, err := r.db.Exec(`
		UPDATE plan_executions SET status = 'interrupted', error_code = 'INTERRUPTED', updated_at = ?
		WHERE status IN ('queued','running')
	`, now)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// HasActiveExecutionForRoot reports whether any queued/running execution
// belongs to a workset of the given root path. It is the scan-side half of the
// scan/execution mutual exclusion.
func (r *Repository) HasActiveExecutionForRoot(rootPath string) (bool, error) {
	var n int
	err := r.db.QueryRow(`
		SELECT COUNT(*) FROM plan_executions e
		JOIN worksets w ON w.id = e.workset_id
		WHERE w.root_path = ? AND e.status IN ('queued','running')
	`, rootPath).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SyncObservedInventory applies the observed disk changes of an execution to the
// entries inventory without a rescan: removed paths lose their row, changed
// paths are refreshed with the scan merge's content_rev semantics (a changed
// size/mtime bumps content_rev and clears the bitrate fact), and the generation
// credentials of the outputs this execution committed are written beside them,
// so the next plan reads them with the inventory. rootPath is the scan root the
// entries belong to.
func (r *Repository) SyncObservedInventory(
	rootPath string,
	removed []string,
	changed []inventory.InventoryFile,
	generated []inventory.GenerationRecord,
) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, p := range removed {
		if _, err := tx.Exec("DELETE FROM entries WHERE path = ?", p); err != nil {
			return fmt.Errorf("remove inventory row %s: %w", p, err)
		}
		// A credential describes the bytes that were there. With the file gone,
		// it describes nothing — and a later arrival at the same path would
		// otherwise inherit it.
		if _, err := tx.Exec("DELETE FROM generation_records WHERE path = ?", p); err != nil {
			return fmt.Errorf("remove generation record %s: %w", p, err)
		}
	}
	for _, f := range changed {
		if err := upsertObservedFile(tx, rootPath, f); err != nil {
			return err
		}
	}
	for _, g := range generated {
		if err := upsertGenerationRecord(tx, g); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// upsertObservedFile writes one observed file with the same lifecycle rules the
// scan merge applies: new rows start at content_rev 1, changed rows bump it and
// drop the stale bitrate, unchanged rows keep both.
func upsertObservedFile(tx *sql.Tx, rootPath string, f inventory.InventoryFile) error {
	var contentRev, size, mtime int64
	var bitrate sql.NullInt64
	err := tx.QueryRow(
		"SELECT content_rev, size, mtime, bitrate FROM entries WHERE path = ?", f.Path,
	).Scan(&contentRev, &size, &mtime, &bitrate)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, insErr := tx.Exec(`
			INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, content_rev, dirty_flag, updated_at)
			VALUES (?, ?, ?, ?, 0, ?, ?, 1, 0, ?)
		`, f.Path, rootPath, path.Dir(f.Path), path.Base(f.Path), f.Size, f.Mtime, time.Now().Format(timeFormat)); insErr != nil {
			return fmt.Errorf("insert inventory row %s: %w", f.Path, insErr)
		}
		return nil
	case err != nil:
		return fmt.Errorf("load inventory row %s: %w", f.Path, err)
	}
	touched := size != f.Size || mtime != f.Mtime
	var bitrateVal any
	if touched {
		contentRev++
	} else if bitrate.Valid {
		bitrateVal = bitrate.Int64
	}
	dirty := 0
	if touched {
		dirty = 1
	}
	if _, err := tx.Exec(`
		UPDATE entries SET root_path = ?, parent_path = ?, name = ?, size = ?, mtime = ?,
			content_rev = ?, dirty_flag = ?, bitrate = ?, updated_at = ?
		WHERE path = ?
	`, rootPath, path.Dir(f.Path), path.Base(f.Path), f.Size, f.Mtime, contentRev,
		dirty, bitrateVal, time.Now().Format(timeFormat), f.Path); err != nil {
		return fmt.Errorf("update inventory row %s: %w", f.Path, err)
	}
	return nil
}
