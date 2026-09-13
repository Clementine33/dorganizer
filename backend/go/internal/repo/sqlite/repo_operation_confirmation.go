package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrConfirmationNotFound is returned when a revision has no confirmation.
var ErrConfirmationNotFound = errors.New("confirmation not found")

// OperationConfirmation is the formal confirmation record of one operation
// revision (ADR 0004 §4). It is stored independently of the revision content
// and survives draft changes and input drift; a new revision never inherits it.
type OperationConfirmation struct {
	PlanID           string
	WorksetID        string
	OperationType    string
	ConfirmedVersion int // operation version observed at confirm time
	ConfirmedAt      time.Time
}

// GetOperationConfirmation loads the confirmation of one revision, or
// ErrConfirmationNotFound.
func (r *Repository) GetOperationConfirmation(planID string) (*OperationConfirmation, error) {
	var c OperationConfirmation
	var confirmedAt string
	err := r.db.QueryRow(`
		SELECT plan_id, workset_id, operation_type, confirmed_version, confirmed_at
		FROM workset_operation_confirmations WHERE plan_id = ?
	`, planID).Scan(&c.PlanID, &c.WorksetID, &c.OperationType, &c.ConfirmedVersion, &confirmedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConfirmationNotFound
		}
		return nil, err
	}
	c.ConfirmedAt = parseTimestamp(confirmedAt)
	return &c, nil
}

// ConfirmOperationRevision atomically re-checks the version and
// current-revision conditions inside the operation and inserts the confirmation
// row. The version guard doubles as the concurrency check: a concurrent draft
// save or revision promotion bumps the operation version, so a stale If-Match
// fails here instead of confirming an outdated plan. Returns ErrVersionConflict
// on a stale version, ErrRevisionNotFound when the revision is not the
// operation's current one, and ErrOperationNotFound when the operation is gone.
// The caller validates input freshness and blocked components before calling;
// those checks are read-only and confirmed inside this transaction's version
// window (ADR 0004 §4: the guard cannot freeze the disk, and future Execute
// re-validates preconditions).
func (r *Repository) ConfirmOperationRevision(
	worksetID, operationType, planID string,
	expectedVersion int,
	now time.Time,
) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var currentRev string
	var currentVersion int
	err = tx.QueryRow(
		"SELECT COALESCE(current_revision_id, ''), version FROM workset_operations WHERE workset_id = ? AND operation_type = ?",
		worksetID,
		operationType,
	).Scan(&currentRev, &currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOperationNotFound
	}
	if err != nil {
		return fmt.Errorf("load operation for confirmation: %w", err)
	}
	if currentVersion != expectedVersion {
		return ErrVersionConflict
	}
	if currentRev != planID {
		return ErrRevisionNotFound
	}

	// Idempotency: an existing confirmation of this same revision wins.
	var exists int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM workset_operation_confirmations WHERE plan_id = ?", planID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check existing confirmation: %w", err)
	}
	if exists == 0 {
		if _, err := tx.Exec(`
			INSERT INTO workset_operation_confirmations (plan_id, workset_id, operation_type, confirmed_version, confirmed_at)
			VALUES (?, ?, ?, ?, ?)
		`, planID, worksetID, operationType, currentVersion, now.Format(timeFormat)); err != nil {
			return fmt.Errorf("insert confirmation: %w", err)
		}
	}
	return tx.Commit()
}
