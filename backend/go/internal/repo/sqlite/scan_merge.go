package sqlite

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"time"
)

// MergeStaging performs transactional merge of staging entries into entries table.
//
// Deprecated: Use MergeStagingSimple or MergeStagingWithStalePaths instead.
// This function is kept for backward compatibility.
func (r *Repository) MergeStaging(args ...interface{}) error {
	if len(args) == 2 {
		// Old 2-arg form: MergeStaging(scanID, rootPath)
		scanID, ok := args[0].(string)
		if !ok {
			return fmt.Errorf("MergeStaging: scanID must be a string")
		}
		rootPath, ok := args[1].(string)
		if !ok {
			return fmt.Errorf("MergeStaging: rootPath must be a string")
		}
		return r.MergeStagingSimple(scanID, rootPath)
	}
	if len(args) == 3 {
		// New 3-arg form: MergeStaging(scanID, rootPath, stalePaths)
		scanID, ok := args[0].(string)
		if !ok {
			return fmt.Errorf("MergeStaging: scanID must be a string")
		}
		rootPath, ok := args[1].(string)
		if !ok {
			return fmt.Errorf("MergeStaging: rootPath must be a string")
		}
		stalePaths, ok := args[2].([]string)
		if !ok {
			return fmt.Errorf("MergeStaging: stalePaths must be []string")
		}
		return r.MergeStagingWithStalePaths(scanID, rootPath, stalePaths)
	}
	return fmt.Errorf("MergeStaging requires 2 or 3 arguments, got %d", len(args))
}

// MergeStagingSimple merges staging entries into the main entries table.
// This is the preferred method for most use cases.
func (r *Repository) MergeStagingSimple(scanID, rootPath string) error {
	return r.MergeStagingWithStalePaths(scanID, rootPath, nil)
}

// MergeStagingWithStalePaths merges staging entries and explicitly specifies
// which paths to preserve from stale cleanup.
//
// Deprecated: stalePaths is ignored and exists only for backward compatibility.
// The preserve set is determined solely by entries_staging for the given session_id.
// Use MergeStagingSimple for new code.
func (r *Repository) MergeStagingWithStalePaths(scanID, rootPath string, stalePaths []string) error {
	// Begin transaction
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now()

	// Step 1: Set-based merge with content_rev lifecycle logic
	// Using INSERT ... SELECT ... ON CONFLICT for efficient batch processing
	// Semantics:
	// - new row => content_rev=1, dirty_flag=0
	// - changed size/mtime => increment content_rev, dirty_flag=1, bitrate cleared
	// - unchanged size/mtime => preserve content_rev and bitrate
	_, err = tx.Exec(`
		INSERT INTO entries (
			path, root_path, parent_path, name, is_dir, size, mtime,
			scan_id, content_rev, dirty_flag, bitrate, format, updated_at
		)
		SELECT
			s.path,
			s.root_path,
			s.parent_path,
			s.name,
			s.is_dir,
			s.size,
			s.mtime,
			?,
			CASE
				WHEN e.path IS NULL THEN 1
				WHEN e.size != s.size OR e.mtime != s.mtime THEN e.content_rev + 1
				ELSE e.content_rev
			END,
CASE
			WHEN e.path IS NULL THEN 0
			WHEN e.size != s.size OR e.mtime != s.mtime THEN 1
			ELSE 0
		END,
			CASE
			WHEN e.path IS NULL THEN NULL
			WHEN e.size != s.size OR e.mtime != s.mtime THEN NULL
			ELSE e.bitrate
		END,
			NULLIF(s.format, ''),
			?
		FROM entries_staging s
		LEFT JOIN entries e ON s.path = e.path
		WHERE s.session_id = ?
		ON CONFLICT(path) DO UPDATE SET
			root_path = excluded.root_path,
			parent_path = excluded.parent_path,
			name = excluded.name,
			is_dir = excluded.is_dir,
			size = excluded.size,
			mtime = excluded.mtime,
			scan_id = excluded.scan_id,
			content_rev = excluded.content_rev,
			dirty_flag = excluded.dirty_flag,
			bitrate = excluded.bitrate,
			format = excluded.format,
			updated_at = excluded.updated_at
	`, scanID, now.Format(timeFormat), scanID)
	if err != nil {
		return fmt.Errorf("failed to merge staging entries: %w", err)
	}

	// Step 2: Apply stale cleanup (always scoped to a root)
	// Uses session-scoped subquery for paths to preserve - NO dynamic NOT IN (?, ?, ...)
	var sessionRootPath string
	var sessionScopePath sql.NullString
	sessionErr := tx.QueryRow(`
		SELECT root_path, scope_path
		FROM scan_sessions
		WHERE session_id = ?
	`, scanID).Scan(&sessionRootPath, &sessionScopePath)
	if sessionErr != nil && sessionErr != sql.ErrNoRows {
		return fmt.Errorf("failed to load scan session scope: %w", sessionErr)
	}

	if rootPath == "" {
		rootPath = sessionRootPath
	}

	// If still no rootPath, derive from staging entries
	if rootPath == "" {
		var stagingRootPath string
		err = tx.QueryRow(`
			SELECT root_path FROM entries_staging WHERE session_id = ? LIMIT 1
		`, scanID).Scan(&stagingRootPath)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("failed to get staging root_path: %w", err)
		}
		rootPath = stagingRootPath
	}

	// Normalize rootPath to POSIX format for consistent SQL matching
	rootPath = filepath.ToSlash(rootPath)

	// Never execute stale cleanup without an explicit root scope
	if rootPath != "" {
		scopePath := ""
		if sessionScopePath.Valid {
			// Normalize scope_path to POSIX as well for consistent matching
			scopePath = filepath.ToSlash(sessionScopePath.String)
		}

		if scopePath != "" {
			// Scoped cleanup: DELETE ... WHERE root_path=? AND (path=? OR path LIKE ? || '/%')
			// AND path NOT IN (SELECT path FROM entries_staging WHERE session_id=?)
			_, err = tx.Exec(`
				DELETE FROM entries
				WHERE root_path = ?
					AND (path = ? OR path LIKE ? || '/%')
					AND path NOT IN (SELECT path FROM entries_staging WHERE session_id = ?)
			`, rootPath, scopePath, scopePath, scanID)
			if err != nil {
				return fmt.Errorf("failed to cleanup scoped stale entries: %w", err)
			}
		} else {
			// Full-root cleanup: DELETE ... WHERE root_path=? AND path NOT IN
			// (SELECT path FROM entries_staging WHERE session_id=?)
			_, err = tx.Exec(`
				DELETE FROM entries
				WHERE root_path = ?
					AND path NOT IN (SELECT path FROM entries_staging WHERE session_id = ?)
			`, rootPath, scanID)
			if err != nil {
				return fmt.Errorf("failed to cleanup full-root stale entries: %w", err)
			}
		}
	}

	// Step 3: Clear staging for this session
	_, err = tx.Exec("DELETE FROM entries_staging WHERE session_id = ?", scanID)
	if err != nil {
		return fmt.Errorf("failed to clear staging: %w", err)
	}

	return tx.Commit()
}
