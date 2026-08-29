package sqlite

import (
	"database/sql"
	"fmt"
	"time"
)

// ==================== Retention Cleanup ====================

// DeleteErrorEventsOlderThanTx deletes error_events rows with created_at < cutoff within tx
func (r *Repository) DeleteErrorEventsOlderThanTx(tx *sql.Tx, cutoff time.Time) (int64, error) {
	result, err := tx.Exec(
		"DELETE FROM error_events WHERE julianday(created_at) < julianday(?)",
		cutoff.Format(timeFormat),
	)
	if err != nil {
		return 0, fmt.Errorf("delete error_events older than %s: %w", cutoff.Format(timeFormat), err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// DeleteScanSessionsOlderThanTx deletes scan_sessions rows where COALESCE(finished_at, started_at) < cutoff within tx.
func (r *Repository) DeleteScanSessionsOlderThanTx(tx *sql.Tx, cutoff time.Time) (int64, error) {
	result, err := tx.Exec(
		"DELETE FROM scan_sessions WHERE julianday(COALESCE(finished_at, started_at)) < julianday(?)",
		cutoff.Format(timeFormat),
	)
	if err != nil {
		return 0, fmt.Errorf("delete scan_sessions older than %s: %w", cutoff.Format(timeFormat), err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// DeletePlansOlderThanTx deletes standalone plans rows with created_at <
// cutoff within tx. Workset-owned revision plans are exempt: they are durable
// aggregate history and must never be automatically purged, even when their
// owners are orphaned. Cascading deletes remove associated plan_items and
// execute_sessions automatically.
func (r *Repository) DeletePlansOlderThanTx(tx *sql.Tx, cutoff time.Time) (int64, error) {
	result, err := tx.Exec(
		"DELETE FROM plans WHERE workset_id = '' AND julianday(created_at) < julianday(?)",
		cutoff.Format(timeFormat),
	)
	if err != nil {
		return 0, fmt.Errorf("delete plans older than %s: %w", cutoff.Format(timeFormat), err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// DeletePlanGenerationsFinishedOlderThanTx purges terminal planning session
// rows whose finished_at (or created_at for rows finished without a timestamp)
// is older than cutoff. revision_id is ON DELETE SET NULL, so purging a
// session never cascades into its revision plan.
func (r *Repository) DeletePlanGenerationsFinishedOlderThanTx(tx *sql.Tx, cutoff time.Time) (int64, error) {
	result, err := tx.Exec(
		"DELETE FROM plan_generations WHERE finished_at IS NOT NULL AND julianday(COALESCE(finished_at, created_at)) < julianday(?)",
		cutoff.Format(timeFormat),
	)
	if err != nil {
		return 0, fmt.Errorf("delete finished plan generations older than %s: %w", cutoff.Format(timeFormat), err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// RunRetentionCleanup deletes old rows in order: error_events -> scan_sessions
// -> plan_generations (terminal session ledger, generationCutoff) -> plans
// (standalone plans, cutoff; workset revisions exempt). It opens a
// transaction, runs all deletes, and commits on success or rolls back on
// error. The 7-day legacy cutoff and the 30-day generation window are kept
// separate so the idempotency-key guarantee horizon stays aligned with the
// terminal-session purge.
func (r *Repository) RunRetentionCleanup(cutoff time.Time) (CleanupStats, error) {
	return r.RunRetentionCleanupWithCutoffs(cutoff, cutoff)
}

// RunRetentionCleanupWithCutoffs is RunRetentionCleanup with an explicit
// generation cutoff (default: cutoff when generationCutoff zero).
func (r *Repository) RunRetentionCleanupWithCutoffs(cutoff, generationCutoff time.Time) (CleanupStats, error) {
	if generationCutoff.IsZero() {
		generationCutoff = cutoff
	}
	tx, err := r.db.Begin()
	if err != nil {
		return CleanupStats{}, fmt.Errorf("begin retention cleanup tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	// Ensure foreign key enforcement on the transaction connection.
	// PRAGMA settings are connection-scoped in SQLite and do not carry over
	// to a transaction started on a connection that may have been reset,
	// so we re-enable explicitly before any cascade-dependent deletes.
	if _, err := tx.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return CleanupStats{}, fmt.Errorf("enable foreign keys in retention tx: %w", err)
	}

	var stats CleanupStats

	stats.DeletedErrorEvents, err = r.DeleteErrorEventsOlderThanTx(tx, cutoff)
	if err != nil {
		return CleanupStats{}, err
	}

	stats.DeletedScanSessions, err = r.DeleteScanSessionsOlderThanTx(tx, cutoff)
	if err != nil {
		return CleanupStats{}, err
	}

	stats.DeletedGenerations, err = r.DeletePlanGenerationsFinishedOlderThanTx(tx, generationCutoff)
	if err != nil {
		return CleanupStats{}, err
	}

	stats.DeletedPlans, err = r.DeletePlansOlderThanTx(tx, cutoff)
	if err != nil {
		return CleanupStats{}, err
	}

	if err := tx.Commit(); err != nil {
		return CleanupStats{}, fmt.Errorf("commit retention cleanup tx: %w", err)
	}
	committed = true

	return stats, nil
}
