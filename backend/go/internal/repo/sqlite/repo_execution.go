package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"path"
	"time"
)

// ErrExecutionNotFound is returned when an execution session cannot be found.
var ErrExecutionNotFound = errors.New("execution not found")

// ErrExecutionIdemConflict is returned when an execution start collides with an
// existing idempotency key of the same operation.
var ErrExecutionIdemConflict = errors.New("execution idempotency key conflict")

// ErrExecutionInProgress is returned when an operation that must wait for an
// active execution (library deletion) is attempted.
var ErrExecutionInProgress = errors.New("execution in progress")

// Execution session statuses. Terminal statuses never regress.
const (
	ExecStatusQueued      = "queued"
	ExecStatusRunning     = "running"
	ExecStatusSucceeded   = "succeeded"
	ExecStatusFailed      = "failed"
	ExecStatusCanceled    = "canceled"
	ExecStatusInterrupted = "interrupted"
)

// PlanExecution is one persisted execution session: the durable record of a
// confirmed revision being executed against the disk. RequestJSON freezes the
// ordered component worklist (root, partition and effective target profile per
// component) at creation time; ReportJSON is the per-component outcome report.
type PlanExecution struct {
	ExecutionID              string
	WorksetID                string
	OperationType            string
	PlanID                   string
	Status                   string
	DeleteMode               string
	IdempotencyKey           string
	RequestHash              string
	ExpectedOperationVersion int
	RequestJSON              string
	TotalComponents          int
	CompletedComponents      int
	TotalOperations          int
	CompletedOperations      int
	CurrentRoot              string
	CurrentComponentID       string
	CurrentComponentIndex    int
	CurrentPhase             string
	ReportJSON               string
	CancelRequested          bool
	ErrorCode                string
	ErrorMessage             string
	StartedAt                time.Time
	FinishedAt               time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

const executionColumns = `execution_id, workset_id, operation_type, plan_id, status, delete_mode,
	idempotency_key, request_hash, expected_operation_version, request_json,
	total_components, completed_components, total_operations, completed_operations,
	current_root, current_component_id, current_component_index, current_phase, report_json,
	cancel_requested, error_code, error_message, started_at, finished_at, created_at, updated_at`

func scanExecution(scanner interface{ Scan(...any) error }) (*PlanExecution, error) {
	var e PlanExecution
	var startedAt, finishedAt sql.NullString
	var cancelRequested int
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&e.ExecutionID,
		&e.WorksetID,
		&e.OperationType,
		&e.PlanID,
		&e.Status,
		&e.DeleteMode,
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
		&e.ReportJSON,
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

// ErrDraftChanged is returned when a guarded execution start observes a draft
// hash that no longer matches the revision's frozen one.
var ErrDraftChanged = errors.New("draft changed")

// ErrWorksetOrphaned is returned when a guarded execution start observes a
// workset whose library is gone.
var ErrWorksetOrphaned = errors.New("workset is orphaned")

// ErrExecutionNotEligible is the residual outcome of a guarded execution start
// whose guard predicates refused the insert without a re-readable cause.
var ErrExecutionNotEligible = errors.New("execution is not eligible")

// ExecutionGuards are the persisted facts an execution start must re-check
// inside its own write transaction: the read-only eligibility gates cannot
// freeze the operation version, the confirmation or the draft hash on their
// own.
type ExecutionGuards struct {
	ExpectedOperationVersion int
	ExpectedCurrentRevision  string
	ExpectedDraftHash        string
	RequireConfirmation      bool
}

// CreateExecutionGuarded inserts a queued execution session only when the
// persisted eligibility facts still hold, as one conditional write statement:
// the guard predicates and the insert are evaluated atomically by SQLite, so a
// concurrent draft save, revision promotion, confirmation, generation or
// library deletion cannot slip between the gate checks and the session's
// creation. The unique indexes cover the remaining invariants (one session per
// idempotency key and per revision).
//
// Sentinel failures: ErrExecutionIdemConflict (key or revision already has a
// session), ErrVersionConflict, ErrRevisionNotFound (no longer the current
// revision), ErrDraftChanged, ErrConfirmationNotFound, ErrGenerationInProgress,
// ErrWorksetOrphaned, ErrWorksetNotFound, ErrOperationNotFound.
func (r *Repository) CreateExecutionGuarded(e *PlanExecution, g ExecutionGuards) error {
	now := e.CreatedAt.Format(timeFormat)
	result, err := r.db.Exec(`
		INSERT INTO plan_executions (
			execution_id, workset_id, operation_type, plan_id, status, delete_mode,
			idempotency_key, request_hash, expected_operation_version, request_json,
			total_components, total_operations, report_json, created_at, updated_at
		)
		SELECT ?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
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
		AND EXISTS (
			SELECT 1 FROM workset_operation_confirmations c WHERE c.plan_id = ?
		)
		AND NOT EXISTS (
			SELECT 1 FROM plan_generations gen
			WHERE gen.workset_id = ? AND gen.operation_type = ? AND gen.status IN ('queued','running')
		)
	`, e.ExecutionID, e.WorksetID, e.OperationType, e.PlanID, e.DeleteMode,
		e.IdempotencyKey, e.RequestHash, e.ExpectedOperationVersion, e.RequestJSON,
		e.TotalComponents, e.TotalOperations, e.ReportJSON, now, now,
		e.WorksetID,
		e.WorksetID, e.OperationType, g.ExpectedOperationVersion, e.PlanID,
		e.WorksetID, e.OperationType, g.ExpectedDraftHash,
		e.PlanID,
		e.WorksetID, e.OperationType)
	if err != nil {
		if isUniqueConstraintError(err) {
			return ErrExecutionIdemConflict
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
// ErrExecutionNotEligible.
func (r *Repository) classifyExecutionGuards(e *PlanExecution, g ExecutionGuards) error {
	var libraryID sql.NullString
	err := r.db.QueryRow("SELECT library_id FROM worksets WHERE id = ?", e.WorksetID).Scan(&libraryID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrWorksetNotFound
	}
	if err != nil {
		return err
	}
	if !libraryID.Valid || libraryID.String == "" {
		return ErrWorksetOrphaned
	}
	var version int
	var current string
	err = r.db.QueryRow(`
		SELECT version, COALESCE(current_revision_id, '') FROM workset_operations
		WHERE workset_id = ? AND operation_type = ?
	`, e.WorksetID, e.OperationType).Scan(&version, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOperationNotFound
	}
	if err != nil {
		return err
	}
	if version != g.ExpectedOperationVersion {
		return ErrVersionConflict
	}
	if current != e.PlanID || g.ExpectedCurrentRevision != e.PlanID {
		return ErrRevisionNotFound
	}
	var draftHash string
	err = r.db.QueryRow(`
		SELECT COALESCE(draft_hash, '') FROM workset_operation_drafts
		WHERE workset_id = ? AND operation_type = ?
	`, e.WorksetID, e.OperationType).Scan(&draftHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDraftChanged
	}
	if err != nil {
		return err
	}
	if draftHash != g.ExpectedDraftHash {
		return ErrDraftChanged
	}
	if g.RequireConfirmation {
		var confirmed int
		if confirmErr := r.db.QueryRow(
			"SELECT COUNT(*) FROM workset_operation_confirmations WHERE plan_id = ?", e.PlanID,
		).Scan(&confirmed); confirmErr != nil {
			return confirmErr
		}
		if confirmed == 0 {
			return ErrConfirmationNotFound
		}
	}
	var generating int
	if genErr := r.db.QueryRow(`
		SELECT COUNT(*) FROM plan_generations
		WHERE workset_id = ? AND operation_type = ? AND status IN ('queued','running')
	`, e.WorksetID, e.OperationType).Scan(&generating); genErr != nil {
		return genErr
	}
	if generating > 0 {
		return ErrGenerationInProgress
	}
	// The revision already has a session (the caller distinguishes a key replay
	// from an executed revision), or another session of the operation is active.
	if existing, existErr := r.GetExecutionForRevision(e.PlanID); existErr == nil && existing != nil {
		return ErrExecutionIdemConflict
	}
	if active, activeErr := r.GetActiveExecutionForOperation(
		e.WorksetID,
		e.OperationType,
	); activeErr == nil &&
		active != nil {
		return ErrExecutionInProgress
	}
	return ErrExecutionNotEligible
}

// GetExecution retrieves an execution session by id.
func (r *Repository) GetExecution(executionID string) (*PlanExecution, error) {
	row := r.db.QueryRow(`SELECT `+executionColumns+` FROM plan_executions WHERE execution_id = ?`, executionID)
	e, err := scanExecution(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrExecutionNotFound
		}
		return nil, err
	}
	return e, nil
}

// GetExecutionByOperationKey returns the execution owned by an operation and an
// idempotency key. Returns nil when no row matches. Every status replays: a
// retried start must observe the session it already created.
func (r *Repository) GetExecutionByOperationKey(worksetID, operationType, key string) (*PlanExecution, error) {
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
func (r *Repository) GetExecutionForRevision(planID string) (*PlanExecution, error) {
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
func (r *Repository) GetActiveExecutionForOperation(worksetID, operationType string) (*PlanExecution, error) {
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
func (r *Repository) LatestExecutionForOperation(worksetID, operationType string) (*PlanExecution, error) {
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
func (r *Repository) NextQueuedExecution() (*PlanExecution, error) {
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
	var e *PlanExecution
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
	e.Status = ExecStatusRunning
	e.StartedAt = now
	return e, nil
}

// ExecutionProgress is the component-boundary progress of a running session.
// Completed counts only operations that already happened on disk.
type ExecutionProgress struct {
	CompletedComponents   int
	CompletedOperations   int
	CurrentRoot           string
	CurrentComponentID    string
	CurrentComponentIndex int
	CurrentPhase          string
}

// UpdateExecutionProgress records component-boundary progress and the current
// report. ReportJSON is the complete report: pending entries included.
func (r *Repository) UpdateExecutionProgress(
	executionID string,
	p ExecutionProgress,
	reportJSON string,
) error {
	_, err := r.db.Exec(`
		UPDATE plan_executions SET
			completed_components = ?, completed_operations = ?,
			current_root = ?, current_component_id = ?, current_component_index = ?,
			current_phase = ?, report_json = ?, updated_at = ?
		WHERE execution_id = ?
	`, p.CompletedComponents, p.CompletedOperations, p.CurrentRoot, p.CurrentComponentID,
		p.CurrentComponentIndex, p.CurrentPhase, reportJSON, time.Now().Format(timeFormat), executionID)
	return err
}

// FinishExecution writes a terminal status with the final report. Only
// queued/running rows transition, so a terminal status never regresses and a
// canceled-queued session is not resurrected by a worker finishing a stale run.
func (r *Repository) FinishExecution(
	executionID, status, errorCode, errorMessage, reportJSON string,
) error {
	_, err := r.db.Exec(`
		UPDATE plan_executions SET status = ?, error_code = ?, error_message = ?,
			report_json = ?, finished_at = ?, updated_at = ?
		WHERE execution_id = ? AND status IN ('queued','running')
	`, status, errorCode, errorMessage, reportJSON, time.Now().Format(timeFormat),
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

// InventoryFile is one observed file fact to write into the entries inventory
// after an execution changed it on disk.
type InventoryFile struct {
	Path  string // persisted POSIX form
	Size  int64
	Mtime int64
}

// SyncObservedInventory applies the observed disk changes of an execution to the
// entries inventory without a rescan: removed paths lose their row, changed
// paths are refreshed with the scan merge's content_rev semantics (a changed
// size/mtime bumps content_rev and clears the bitrate fact). rootPath is the
// scan root the entries belong to.
func (r *Repository) SyncObservedInventory(
	rootPath string,
	removed []string,
	changed []InventoryFile,
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
	}
	for _, f := range changed {
		if err := upsertObservedFile(tx, rootPath, f); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// upsertObservedFile writes one observed file with the same lifecycle rules the
// scan merge applies: new rows start at content_rev 1, changed rows bump it and
// drop the stale bitrate, unchanged rows keep both.
func upsertObservedFile(tx *sql.Tx, rootPath string, f InventoryFile) error {
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
