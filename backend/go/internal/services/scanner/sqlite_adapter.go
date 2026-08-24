package scanner

import (
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// SQLiteRepositoryAdapter adapts sqlite.Repository to scanner.Repository.
type SQLiteRepositoryAdapter struct {
	repo *sqlite.Repository
	// testExecSeam allows tests to inject failures after N exec calls.
	// Production code leaves this nil (uses real exec).
	testExecSeam func(callCount int) error
}

// execFn is a testable wrapper around sql.Stmt.Exec for deterministic failure injection.
func (a *SQLiteRepositoryAdapter) execStmt(stmt *sql.Stmt, callCount int, args ...any) (sql.Result, error) {
	if a.testExecSeam != nil {
		if err := a.testExecSeam(callCount); err != nil {
			return nil, err
		}
	}
	return stmt.Exec(args...)
}

// NewSQLiteRepositoryAdapter creates a scanner repository adapter for sqlite.
func NewSQLiteRepositoryAdapter(repo *sqlite.Repository) *SQLiteRepositoryAdapter {
	return &SQLiteRepositoryAdapter{repo: repo}
}

const defaultBatchSize = 1000

// WriteStagingEntries writes scanner staging entries into sqlite entries_staging.
// Entries are chunked into batches (defaultBatchSize=1000) within a single transaction
// for atomicity. On any mid-batch error, the entire transaction is rolled back.
func (a *SQLiteRepositoryAdapter) WriteStagingEntries(_ string, entries []StagingEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := a.repo.DB().Begin()
	if err != nil {
		return fmt.Errorf("begin staging tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO entries_staging (
			session_id, path, root_path, parent_path, name, is_dir, size, mtime, format, operation
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'upsert')
	`)
	if err != nil {
		return fmt.Errorf("prepare staging insert: %w", err)
	}
	defer stmt.Close()

	// Process entries in chunks to stay within SQLite parameter limits and improve performance
	for batchStart := 0; batchStart < len(entries); batchStart += defaultBatchSize {
		batchEnd := batchStart + defaultBatchSize
		if batchEnd > len(entries) {
			batchEnd = len(entries)
		}

		batch := entries[batchStart:batchEnd]
		execCount := 0
		for _, e := range batch {
			isDir := 0
			if e.IsDir {
				isDir = 1
			}
			execCount++
			if _, err := a.execStmt(stmt, execCount, e.SessionID, e.Path, e.RootPath, e.ParentPath, e.Name, isDir, e.Size, e.Mtime, e.Format); err != nil {
				return fmt.Errorf("insert staging entry %q: %w", e.Path, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit staging tx: %w", err)
	}
	return nil
}

// MergeStaging merges staged entries and returns merged count.
func (a *SQLiteRepositoryAdapter) MergeStaging(sessionID, rootPath string, stalePaths []string) (int, error) {
	var err error
	if len(stalePaths) > 0 {
		err = a.repo.MergeStagingWithStalePaths(sessionID, rootPath, stalePaths)
	} else {
		err = a.repo.MergeStagingSimple(sessionID, rootPath)
	}
	if err != nil {
		return 0, err
	}

	// scan_id is set to sessionID during merge updates/inserts.
	var mergedCount int
	if err := a.repo.DB().QueryRow("SELECT COUNT(*) FROM entries WHERE scan_id = ?", sessionID).Scan(&mergedCount); err != nil {
		return 0, err
	}
	return mergedCount, nil
}

// CreateScanSession persists a scanner scan session in sqlite.
func (a *SQLiteRepositoryAdapter) CreateScanSession(session *ScanSession) error {
	var scopePath *string
	if session.ScopePath != nil {
		p := filepath.ToSlash(*session.ScopePath)
		scopePath = &p
	}

	return a.repo.CreateScanSession(&sqlite.ScanSession{
		SessionID:    session.SessionID,
		RootPath:     filepath.ToSlash(session.RootPath),
		ScopePath:    scopePath,
		Kind:         session.Kind,
		Status:       session.Status,
		ErrorCode:    session.ErrorCode,
		ErrorMessage: session.ErrorMessage,
		StartedAt:    session.StartedAt,
		FinishedAt:   session.FinishedAt,
	})
}

// UpdateScanSessionStatus updates session lifecycle status in sqlite.
func (a *SQLiteRepositoryAdapter) UpdateScanSessionStatus(sessionID, status, errorCode, errorMessage string) error {
	return a.repo.UpdateScanSessionStatus(sessionID, status, errorCode, errorMessage)
}

// CleanupStagingSession deletes all staging entries for a session.
// This is used for failure cleanup to remove partial staging data.
// Note: Partial staging persistence may exist before cleanup - this is expected.
func (a *SQLiteRepositoryAdapter) CleanupStagingSession(sessionID string) error {
	_, err := a.repo.DB().Exec(
		"DELETE FROM entries_staging WHERE session_id = ?",
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("cleanup staging session %s: %w", sessionID, err)
	}
	return nil
}

// pipelineRepo is an internal interface for pipeline batch writing capabilities.
// This interface is NOT part of the public Repository interface and is only
// used internally by ScannerService for streaming batch writes.
type pipelineRepo interface {
	WriteStagingBatch(sessionID string, batch []StagingEntry) error
	CleanupStagingSession(sessionID string) error
}

// Compile-time interface check
var _ pipelineRepo = (*SQLiteRepositoryAdapter)(nil)

// WriteStagingBatch writes a batch of staging entries in a single transaction.
// This is used for pipeline batch writing (batchSize=1000).
// Each batch is committed individually - partial persistence is possible on failure.
func (a *SQLiteRepositoryAdapter) WriteStagingBatch(sessionID string, batch []StagingEntry) error {
	if len(batch) == 0 {
		return nil
	}

	tx, err := a.repo.DB().Begin()
	if err != nil {
		return fmt.Errorf("begin staging batch tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO entries_staging (
			session_id, path, root_path, parent_path, name, is_dir, size, mtime, format, operation
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'upsert')
	`)
	if err != nil {
		return fmt.Errorf("prepare staging insert: %w", err)
	}
	defer stmt.Close()

	for _, e := range batch {
		isDir := 0
		if e.IsDir {
			isDir = 1
		}
		if _, err := stmt.Exec(e.SessionID, e.Path, e.RootPath, e.ParentPath, e.Name, isDir, e.Size, e.Mtime, e.Format); err != nil {
			return fmt.Errorf("insert staging entry %q: %w", e.Path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit staging batch tx: %w", err)
	}
	return nil
}

// Ensure compile-time interface compatibility.
var _ Repository = (*SQLiteRepositoryAdapter)(nil)
