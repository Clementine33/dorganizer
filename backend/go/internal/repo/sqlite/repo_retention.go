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

// DeletePlansOlderThanTx deletes plans rows with created_at < cutoff within tx.
// Cascading deletes will remove associated plan_items and execute_sessions automatically.
func (r *Repository) DeletePlansOlderThanTx(tx *sql.Tx, cutoff time.Time) (int64, error) {
	result, err := tx.Exec(
		"DELETE FROM plans WHERE julianday(created_at) < julianday(?)",
		cutoff.Format(timeFormat),
	)
	if err != nil {
		return 0, fmt.Errorf("delete plans older than %s: %w", cutoff.Format(timeFormat), err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

// RunRetentionCleanup deletes old rows in order: error_events -> scan_sessions -> plans.
// It opens a transaction, runs all deletes, and commits on success or rolls back on error.
func (r *Repository) RunRetentionCleanup(cutoff time.Time) (CleanupStats, error) {
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
