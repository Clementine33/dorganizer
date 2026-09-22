package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrGenerationNotFound is returned when a planning session cannot be found.
var ErrGenerationNotFound = errors.New("generation not found")

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

// PlanGeneration is one persisted planning session, owned by exactly one
// operation of one workset.
type PlanGeneration struct {
	GenerationID         string
	WorksetID            string
	OperationType        string
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

func scanGeneration(scanner interface{ Scan(...any) error }) (*PlanGeneration, error) {
	var g PlanGeneration
	var startedAt, finishedAt, revisionID sql.NullString
	var cancelRequested int
	var createdAt, updatedAt string
	if err := scanner.Scan(
		&g.GenerationID,
		&g.WorksetID,
		&g.OperationType,
		&g.Status,
		&g.IdempotencyKey,
		&g.RequestHash,
		&g.ExpectedDraftVersion,
		&g.RequestJSON,
		&g.TotalRoots,
		&g.CompletedRoots,
		&g.CurrentRoot,
		&g.ErrorCount,
		&cancelRequested,
		&revisionID,
		&g.ErrorCode,
		&g.ErrorMessage,
		&startedAt,
		&finishedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
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

const generationColumns = `generation_id, workset_id, operation_type, status, idempotency_key, request_hash, expected_draft_version, request_json,
	total_roots, completed_roots, current_root, error_count, cancel_requested, revision_id, error_code, error_message,
	started_at, finished_at, created_at, updated_at`

// CreateGeneration inserts a queued planning session. A unique conflict on the
// operation+key partial index returns ErrGenerationIdemConflict.
func (r *Repository) CreateGeneration(g *PlanGeneration) error {
	_, err := r.db.Exec(`
		INSERT INTO plan_generations (generation_id, workset_id, operation_type, status, idempotency_key, request_hash, expected_draft_version, request_json,
			total_roots, completed_roots, current_root, error_count, cancel_requested, created_at, updated_at)
		VALUES (?, ?, ?, 'queued', ?, ?, ?, ?, ?, 0, '', 0, 0, ?, ?)
	`, g.GenerationID, g.WorksetID, g.OperationType, g.IdempotencyKey, g.RequestHash, g.ExpectedDraftVersion, g.RequestJSON, g.TotalRoots, g.CreatedAt.Format(timeFormat), g.CreatedAt.Format(timeFormat))
	if err != nil {
		if isUniqueConstraintError(err) {
			return ErrGenerationIdemConflict
		}
		return fmt.Errorf("insert generation: %w", err)
	}
	return nil
}

// GetGenerationByOperationKey returns the generation owned by an operation and
// an idempotency key. Returns nil when no row matches.
func (r *Repository) GetGenerationByOperationKey(worksetID, operationType, key string) (*PlanGeneration, error) {
	row := r.db.QueryRow(
		`SELECT `+generationColumns+` FROM plan_generations
		 WHERE workset_id = ? AND operation_type = ? AND idempotency_key = ?`,
		worksetID,
		operationType,
		key,
	)
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

// GetActiveGenerationForOperation returns the queued/running session of one
// operation, newest first. Returns nil when none is active.
func (r *Repository) GetActiveGenerationForOperation(worksetID, operationType string) (*PlanGeneration, error) {
	row := r.db.QueryRow(`
		SELECT `+generationColumns+` FROM plan_generations
		WHERE workset_id = ? AND operation_type = ? AND status IN ('queued','running')
		ORDER BY julianday(created_at) DESC, generation_id DESC LIMIT 1
	`, worksetID, operationType)
	g, err := scanGeneration(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return g, nil
}

// LatestGenerationForOperation returns the most recently created session of
// one operation (any status), or nil.
func (r *Repository) LatestGenerationForOperation(worksetID, operationType string) (*PlanGeneration, error) {
	row := r.db.QueryRow(`
		SELECT `+generationColumns+` FROM plan_generations
		WHERE workset_id = ? AND operation_type = ?
		ORDER BY julianday(created_at) DESC, generation_id DESC LIMIT 1
	`, worksetID, operationType)
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
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
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
func (r *Repository) UpdateGenerationProgress(
	generationID string,
	completedRoots, errorCount int,
	currentRoot string,
) error {
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
