package sqlite

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestMergePreservesBitrateWhenUnchanged(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// Seed old row in entries with bitrate=320000 (using P0 schema)
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, scan_id, content_rev, bitrate, updated_at)
		VALUES ('/music/test.mp3', '/music', '/music', 'test.mp3', 0, 1024000, ?, 'scan-prev', 1, 320000, datetime('now'))
	`, now)
	if err != nil {
		t.Fatalf("failed to seed entry: %v", err)
	}

	// Create staging entry with same size (but no bitrate - tests preservation)
	// The staging entry represents the same file scanned again
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-123', '/music/test.mp3', '/music', '/music', 'test.mp3', 0, 1024000, ?, 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Now call merge - this should preserve bitrate because size is unchanged
	// Using type-safe MergeStagingSimple
	err = repo.MergeStagingSimple("scan-123", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify: bitrate should be preserved (not null)
	var bitrate sql.NullInt64
	err = repo.db.QueryRow("SELECT bitrate FROM entries WHERE path = ?", "/music/test.mp3").Scan(&bitrate)
	if err != nil {
		t.Fatalf("failed to query entry: %v", err)
	}

	if !bitrate.Valid || bitrate.Int64 != 320000 {
		t.Errorf("expected bitrate=320000 (preserved), got bitrate=%v", bitrate.Int64)
	}

	// Verify: content_rev should be preserved (not incremented)
	var contentRev int
	err = repo.db.QueryRow("SELECT content_rev FROM entries WHERE path = ?", "/music/test.mp3").Scan(&contentRev)
	if err != nil {
		t.Fatalf("failed to query content_rev: %v", err)
	}
	if contentRev != 1 {
		t.Errorf("expected content_rev=1 (preserved), got content_rev=%d", contentRev)
	}

	// Verify: dirty_flag should be 0
	var dirtyFlag int
	err = repo.db.QueryRow("SELECT dirty_flag FROM entries WHERE path = ?", "/music/test.mp3").Scan(&dirtyFlag)
	if err != nil {
		t.Fatalf("failed to query dirty_flag: %v", err)
	}
	if dirtyFlag != 0 {
		t.Errorf("expected dirty_flag=0, got dirty_flag=%d", dirtyFlag)
	}
}

func TestMergeIncrementsContentRevOnChange(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	oldMtime := time.Now().Add(-24 * time.Hour).Unix()
	newMtime := time.Now().Unix()

	// Seed old row in entries
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, scan_id, content_rev, bitrate, updated_at)
		VALUES ('/music/test.mp3', '/music', '/music', 'test.mp3', 0, 1024000, ?, 'scan-prev', 1, 320000, datetime('now'))
	`, oldMtime)
	if err != nil {
		t.Fatalf("failed to seed entry: %v", err)
	}

	// Create staging entry with changed mtime
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-123', '/music/test.mp3', '/music', '/music', 'test.mp3', 0, 1024000, ?, 'upsert')
	`, newMtime)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Now call merge - content should have changed
	err = repo.MergeStagingSimple("scan-123", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify: content_rev should be incremented
	var contentRev int
	err = repo.db.QueryRow("SELECT content_rev FROM entries WHERE path = ?", "/music/test.mp3").Scan(&contentRev)
	if err != nil {
		t.Fatalf("failed to query content_rev: %v", err)
	}
	if contentRev != 2 {
		t.Errorf("expected content_rev=2, got content_rev=%d", contentRev)
	}

	// Verify: dirty_flag should be 1
	var dirtyFlag int
	err = repo.db.QueryRow("SELECT dirty_flag FROM entries WHERE path = ?", "/music/test.mp3").Scan(&dirtyFlag)
	if err != nil {
		t.Fatalf("failed to query dirty_flag: %v", err)
	}
	if dirtyFlag != 1 {
		t.Errorf("expected dirty_flag=1, got dirty_flag=%d", dirtyFlag)
	}

	// Verify: bitrate should be cleared
	var bitrate sql.NullInt64
	err = repo.db.QueryRow("SELECT bitrate FROM entries WHERE path = ?", "/music/test.mp3").Scan(&bitrate)
	if err != nil {
		t.Fatalf("failed to query bitrate: %v", err)
	}
	if bitrate.Valid && bitrate.Int64 != 0 {
		t.Errorf("expected bitrate=0 (cleared), got bitrate=%d", bitrate.Int64)
	}
}

func TestMergeClearsStagingScope(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// Create multiple staging entries for scan-123
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES 
			('scan-123', '/music/a.mp3', '/music', '/music', 'a.mp3', 0, 1024000, ?, 'upsert'),
			('scan-123', '/music/b.mp3', '/music', '/music', 'b.mp3', 0, 1024000, ?, 'upsert')
	`, now, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Merge with scope scan-123
	err = repo.MergeStagingSimple("scan-123", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify: staging entries for scan-123 should be cleared
	var count int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries_staging WHERE session_id = ?", "scan-123").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query staging: %v", err)
	}

	if count != 0 {
		t.Errorf("expected 0 staging entries after merge, got %d", count)
	}
}

func TestMergeAppliesStaleCleanup(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// Create existing entry that is NOT in staging (stale)
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime)
		VALUES ('/music/old.mp3', '/music', '/music', 'old.mp3', 0, 1024000, ?)
	`, now)
	if err != nil {
		t.Fatalf("failed to seed entry: %v", err)
	}

	// Create staging entry for different file
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-123', '/music/new.mp3', '/music', '/music', 'new.mp3', 0, 1024000, ?, 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Merge with empty scope to trigger full-root stale cleanup
	err = repo.MergeStagingSimple("scan-123", "")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify: stale entry should be deleted
	var exists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/old.mp3").Scan(&exists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}

	if exists != 0 {
		t.Errorf("expected stale entry to be deleted, but it still exists")
	}
}

func TestMergeFullRootCleanupDoesNotTouchOtherRoots(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// Seed entries in two different roots.
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime)
		VALUES
			('/music/old.mp3', '/music', '/music', 'old.mp3', 0, 1024000, ?),
			('/other/keep.mp3', '/other', '/other', 'keep.mp3', 0, 1024000, ?)
	`, now, now)
	if err != nil {
		t.Fatalf("failed to seed entries: %v", err)
	}

	// Stage a new file under /music to drive full-root cleanup there.
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-123', '/music/new.mp3', '/music', '/music', 'new.mp3', 0, 1024000, ?, 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	err = repo.MergeStagingSimple("scan-123", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var musicOldExists, otherKeepExists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/old.mp3").Scan(&musicOldExists)
	if err != nil {
		t.Fatalf("failed to query /music/old.mp3: %v", err)
	}
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/other/keep.mp3").Scan(&otherKeepExists)
	if err != nil {
		t.Fatalf("failed to query /other/keep.mp3: %v", err)
	}

	if musicOldExists != 0 {
		t.Errorf("expected /music/old.mp3 to be deleted during /music cleanup")
	}
	if otherKeepExists != 1 {
		t.Errorf("expected /other/keep.mp3 to remain untouched")
	}
}

func TestMergeNewEntryGetsContentRev1(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// Create staging entry (no existing entry in entries table)
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-123', '/music/new.mp3', '/music', '/music', 'new.mp3', 0, 1024000, ?, 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Merge
	err = repo.MergeStagingSimple("scan-123", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify: new entry should have content_rev=1
	var contentRev int
	err = repo.db.QueryRow("SELECT content_rev FROM entries WHERE path = ?", "/music/new.mp3").Scan(&contentRev)
	if err != nil {
		t.Fatalf("failed to query entry: %v", err)
	}
	if contentRev != 1 {
		t.Errorf("expected content_rev=1 for new entry, got content_rev=%d", contentRev)
	}
}

func TestMergeUnchangedRowClearsDirtyFlag(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// Seed existing entry with dirty_flag=1 (from previous change)
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, scan_id, content_rev, dirty_flag, updated_at)
		VALUES ('/music/test.mp3', '/music', '/music', 'test.mp3', 0, 1024000, ?, 'scan-prev', 2, 1, datetime('now'))
	`, now)
	if err != nil {
		t.Fatalf("failed to seed entry: %v", err)
	}

	// Create staging entry with same size/mtime (unchanged)
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-123', '/music/test.mp3', '/music', '/music', 'test.mp3', 0, 1024000, ?, 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Merge
	err = repo.MergeStagingSimple("scan-123", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify: dirty_flag should be 0 for unchanged row (regardless of previous value)
	var dirtyFlag int
	err = repo.db.QueryRow("SELECT dirty_flag FROM entries WHERE path = ?", "/music/test.mp3").Scan(&dirtyFlag)
	if err != nil {
		t.Fatalf("failed to query dirty_flag: %v", err)
	}
	if dirtyFlag != 0 {
		t.Errorf("expected dirty_flag=0 for unchanged row (was 1 before merge), got dirty_flag=%d", dirtyFlag)
	}
}

func TestMergePersistsFormatFromStaging(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, format, operation)
		VALUES ('scan-123', '/music/new.mp3', '/music', '/music', 'new.mp3', 0, 1024000, ?, 'audio/mpeg', 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	err = repo.MergeStagingSimple("scan-123", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var format sql.NullString
	err = repo.db.QueryRow("SELECT format FROM entries WHERE path = ?", "/music/new.mp3").Scan(&format)
	if err != nil {
		t.Fatalf("failed to query format: %v", err)
	}
	if !format.Valid || format.String != "audio/mpeg" {
		t.Fatalf("expected format audio/mpeg, got %q", format.String)
	}
}

func TestMergeWithExplicitStalePaths(t *testing.T) {
	// Verifies that stalePaths parameter is ignored; preserve behavior only
	// considers entries_staging entries for the session. This test ensures
	// callers are not relying on stalePaths to protect files.

	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// Create existing entries - one in staging, one not
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime)
		VALUES 
			('/music/in_staging.mp3', '/music', '/music', 'in_staging.mp3', 0, 1024000, ?),
			('/music/not_in_staging.mp3', '/music', '/music', 'not_in_staging.mp3', 0, 1024000, ?)
	`, now, now)
	if err != nil {
		t.Fatalf("failed to seed entries: %v", err)
	}

	// Create staging only for in_staging.mp3
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-123', '/music/in_staging.mp3', '/music', '/music', 'in_staging.mp3', 0, 1024000, ?, 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Call merge with stalePaths containing a path that is NOT in staging.
	// The stalePaths should be ignored - only staging determines what is preserved.
	err = repo.MergeStagingWithStalePaths("scan-123", "/music", []string{"/music/not_in_staging.mp3"})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify: in_staging.mp3 should be preserved (was in staging)
	var keepExists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/in_staging.mp3").Scan(&keepExists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if keepExists != 1 {
		t.Errorf("expected in_staging.mp3 to exist (in staging)")
	}

	// Verify: not_in_staging.mp3 should be DELETED (stalePaths ignored it)
	var deleteExists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/not_in_staging.mp3").Scan(&deleteExists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if deleteExists != 0 {
		t.Errorf("expected not_in_staging.mp3 to be deleted (stalePaths is ignored, not in staging)")
	}
}

func TestMergeScopedCleanupWithEmptyStaging(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// First create a scan session with a scope (folder scan)
	_, err = repo.db.Exec(`
		INSERT INTO scan_sessions (session_id, root_path, scope_path, kind, status, started_at)
		VALUES ('scan-empty', '/music', '/music/albums', 'folder', 'running', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("failed to create scan session: %v", err)
	}

	// Seed existing entries in the scope that should be cleaned
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime)
		VALUES 
			('/music/albums/old1.mp3', '/music', '/music/albums', 'old1.mp3', 0, 1024000, ?),
			('/music/albums/old2.mp3', '/music', '/music/albums', 'old2.mp3', 0, 1024000, ?),
			('/music/other.mp3', '/music', '/music', 'other.mp3', 0, 1024000, ?)
	`, now, now, now)
	if err != nil {
		t.Fatalf("failed to seed entries: %v", err)
	}

	// Create empty staging (no files scanned)
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-empty', '/music/albums/.placeholder', '/music', '/music/albums', '.placeholder', 0, 0, ?, 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Merge with scope - should clean the scope even with minimal staging
	err = repo.MergeStagingSimple("scan-empty", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify: scope entries that were NOT in staging should be deleted
	// The placeholder IS in staging so it should remain
	var old1Exists, old2Exists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/albums/old1.mp3").Scan(&old1Exists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/albums/old2.mp3").Scan(&old2Exists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if old1Exists != 0 {
		t.Errorf("expected old1.mp3 to be deleted (not in scanned set)")
	}
	if old2Exists != 0 {
		t.Errorf("expected old2.mp3 to be deleted (not in scanned set)")
	}

	// Verify: placeholder should remain (it was in staging/scanned set)
	var placeholderExists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/albums/.placeholder").Scan(&placeholderExists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if placeholderExists != 1 {
		t.Errorf("expected placeholder to remain (in scanned set)")
	}

	// Verify: other entries outside scope should remain
	var otherExists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/other.mp3").Scan(&otherExists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if otherExists != 1 {
		t.Errorf("expected other.mp3 to remain")
	}
}

func TestMergeBoundarySafeScopeMatching(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// Create scan session with scope
	_, err = repo.db.Exec(`
		INSERT INTO scan_sessions (session_id, root_path, scope_path, kind, status, started_at)
		VALUES ('scan-boundary', '/music', '/music/albums', 'folder', 'running', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("failed to create scan session: %v", err)
	}

	// Seed entries that should be matched by boundary-safe scope
	// Paths that should be in scope: /music/albums, /music/albums/file.mp3, /music/albums/sub/file.mp3
	// Paths that should NOT be in scope: /music/albums-something, /music/albums2
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime)
		VALUES 
			('/music/albums', '/music', '/music', 'albums', 1, 0, ?),
			('/music/albums/file1.mp3', '/music', '/music/albums', 'file1.mp3', 0, 1024000, ?),
			('/music/albums/sub/file2.mp3', '/music', '/music/albums/sub', 'file2.mp3', 0, 1024000, ?),
			('/music/albums-something/should_stay.mp3', '/music', '/music/albums-something', 'should_stay.mp3', 0, 1024000, ?),
			('/music/albums2/also_stay.mp3', '/music', '/music/albums2', 'also_stay.mp3', 0, 1024000, ?),
			('/music/other/outside.mp3', '/music', '/music/other', 'outside.mp3', 0, 1024000, ?)
	`, now, now, now, now, now, now)
	if err != nil {
		t.Fatalf("failed to seed entries: %v", err)
	}

	// Create staging with only file1
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-boundary', '/music/albums/file1.mp3', '/music', '/music/albums', 'file1.mp3', 0, 1024000, ?, 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Merge - should only clean within scope boundary
	err = repo.MergeStagingSimple("scan-boundary", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify: file1.mp3 should remain (in staging)
	var file1Exists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/albums/file1.mp3").Scan(&file1Exists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if file1Exists != 1 {
		t.Errorf("expected file1.mp3 to remain")
	}

	// Verify: file2.mp3 should be deleted (in scope but not in staging)
	var file2Exists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/albums/sub/file2.mp3").Scan(&file2Exists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if file2Exists != 0 {
		t.Errorf("expected file2.mp3 to be deleted (in scope but not scanned)")
	}

	// Verify: albums-something/should_stay.mp3 should remain (NOT in scope)
	var shouldStayExists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/albums-something/should_stay.mp3").Scan(&shouldStayExists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if shouldStayExists != 1 {
		t.Errorf("expected should_stay.mp3 to remain (not in scope)")
	}

	// Verify: albums2/also_stay.mp3 should remain (NOT in scope)
	var alsoStayExists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/albums2/also_stay.mp3").Scan(&alsoStayExists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if alsoStayExists != 1 {
		t.Errorf("expected also_stay.mp3 to remain (not in scope)")
	}

	// Verify: outside.mp3 should remain (not in scope)
	var outsideExists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/other/outside.mp3").Scan(&outsideExists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if outsideExists != 1 {
		t.Errorf("expected outside.mp3 to remain (not in scope)")
	}
}

func TestMergeWithTrulyEmptyStaging(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// First create a scan session with a scope (folder scan)
	_, err = repo.db.Exec(`
		INSERT INTO scan_sessions (session_id, root_path, scope_path, kind, status, started_at)
		VALUES ('scan-zero', '/music', '/music/albums', 'folder', 'running', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("failed to create scan session: %v", err)
	}

	// Seed existing entries in the scope
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime)
		VALUES 
			('/music/albums/old1.mp3', '/music', '/music/albums', 'old1.mp3', 0, 1024000, ?),
			('/music/albums/old2.mp3', '/music', '/music/albums', 'old2.mp3', 0, 1024000, ?),
			('/music/other.mp3', '/music', '/music', 'other.mp3', 0, 1024000, ?)
	`, now, now, now)
	if err != nil {
		t.Fatalf("failed to seed entries: %v", err)
	}

	// DO NOT create any staging entries - this tests truly zero-row staging

	// Merge with scope - should clean scope entries since staging is empty
	// (empty preserve list means "delete everything in scope")
	err = repo.MergeStagingSimple("scan-zero", "/music")
	if err != nil {
		t.Fatalf("merge with empty staging should not fail: %v", err)
	}

	// Verify: entries in scope should be deleted (empty staging = clean all in scope)
	var old1Exists, old2Exists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/albums/old1.mp3").Scan(&old1Exists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/albums/old2.mp3").Scan(&old2Exists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	// With zero staging entries, preservePaths is empty so cleanup removes all in scope
	if old1Exists != 0 {
		t.Errorf("expected old1.mp3 to be deleted (empty staging = clean all in scope)")
	}
	if old2Exists != 0 {
		t.Errorf("expected old2.mp3 to be deleted (empty staging = clean all in scope)")
	}

	// Verify: other entries outside scope should remain
	var otherExists int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path = ?", "/music/other.mp3").Scan(&otherExists)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if otherExists != 1 {
		t.Errorf("expected other.mp3 to remain")
	}
}

func TestMergeNewEntryDefaultsToNonError(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-err-default', '/music/new_error_named.mp3', '/music', '/music', 'new_error_named.mp3', 0, 1024000, ?, 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	err = repo.MergeStagingSimple("scan-err-default", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var isError int
	var reason sql.NullString
	err = repo.db.QueryRow("SELECT is_error, error_reason FROM entries WHERE path = ?", "/music/new_error_named.mp3").Scan(&isError, &reason)
	if err != nil {
		t.Fatalf("failed to query entry error fields: %v", err)
	}

	if isError != 0 {
		t.Errorf("expected is_error=0 for new merged entry, got %d", isError)
	}
	if reason.Valid {
		t.Errorf("expected error_reason NULL for new merged entry, got %q", reason.String)
	}
}

func TestMergePreservesExistingErrorFlags(t *testing.T) {
	// Setup: create temp database
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	oldMtime := time.Now().Add(-24 * time.Hour).Unix()
	newMtime := time.Now().Unix()

	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, scan_id, content_rev, dirty_flag, is_error, error_reason, updated_at)
		VALUES ('/music/existing.mp3', '/music', '/music', 'existing.mp3', 0, 1024000, ?, 'scan-prev', 1, 0, 1, 'manual-seeded', datetime('now'))
	`, oldMtime)
	if err != nil {
		t.Fatalf("failed to seed entry: %v", err)
	}

	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-err-preserve', '/music/existing.mp3', '/music', '/music', 'existing.mp3', 0, 1024000, ?, 'upsert')
	`, newMtime)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	err = repo.MergeStagingSimple("scan-err-preserve", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var isError int
	var reason sql.NullString
	err = repo.db.QueryRow("SELECT is_error, error_reason FROM entries WHERE path = ?", "/music/existing.mp3").Scan(&isError, &reason)
	if err != nil {
		t.Fatalf("failed to query entry error fields: %v", err)
	}

	if isError != 1 {
		t.Errorf("expected is_error to be preserved as 1, got %d", isError)
	}
	if !reason.Valid || reason.String != "manual-seeded" {
		t.Errorf("expected error_reason to be preserved as 'manual-seeded', got valid=%v value=%q", reason.Valid, reason.String)
	}
}

// TestMergeLargeSetStaleCleanup tests that stale cleanup works efficiently with large staging sets
// using session-scoped subquery instead of dynamic NOT IN placeholders.
func TestMergeLargeSetStaleCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()

	// Create many existing entries that should be cleaned up (2000+ to test large-set handling)
	for i := 0; i < 2100; i++ {
		_, err = repo.db.Exec(`
			INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime)
			VALUES (?, '/music', '/music', ?, 0, 1024000, ?)
		`, fmt.Sprintf("/music/stale%04d.mp3", i), fmt.Sprintf("stale%04d.mp3", i), now)
		if err != nil {
			t.Fatalf("failed to seed entry %d: %v", i, err)
		}
	}

	// Create many staging entries (2000+ to test large-set handling)
	for i := 0; i < 2100; i++ {
		_, err = repo.db.Exec(`
			INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
			VALUES ('scan-large', ?, '/music', '/music', ?, 0, 1024000, ?, 'upsert')
		`, fmt.Sprintf("/music/new%04d.mp3", i), fmt.Sprintf("new%04d.mp3", i), now)
		if err != nil {
			t.Fatalf("failed to seed staging %d: %v", i, err)
		}
	}

	// Merge should work efficiently without building large NOT IN (?, ?, ...) clause
	err = repo.MergeStagingSimple("scan-large", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify: all stale entries should be deleted
	var staleCount int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path LIKE '/music/stale%'").Scan(&staleCount)
	if err != nil {
		t.Fatalf("failed to query stale count: %v", err)
	}
	if staleCount != 0 {
		t.Errorf("expected 0 stale entries, got %d", staleCount)
	}

	// Verify: all new entries should exist with correct content_rev
	var newCount int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries WHERE path LIKE '/music/new%'").Scan(&newCount)
	if err != nil {
		t.Fatalf("failed to query new count: %v", err)
	}
	if newCount != 2100 {
		t.Errorf("expected 2100 new entries, got %d", newCount)
	}

	// Verify a sample entry has content_rev=1 (new entries)
	var contentRev int
	err = repo.db.QueryRow("SELECT content_rev FROM entries WHERE path = ?", "/music/new0000.mp3").Scan(&contentRev)
	if err != nil {
		t.Fatalf("failed to query content_rev: %v", err)
	}
	if contentRev != 1 {
		t.Errorf("expected content_rev=1 for new entry, got %d", contentRev)
	}

	// Verify staging is cleared
	var stagingCount int
	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries_staging WHERE session_id = ?", "scan-large").Scan(&stagingCount)
	if err != nil {
		t.Fatalf("failed to query staging count: %v", err)
	}
	if stagingCount != 0 {
		t.Errorf("expected 0 staging entries after merge, got %d", stagingCount)
	}
}

// TestMergeUpsertContentRevIncrementSemantics tests that content_rev increment
// semantics work correctly for both new and existing entries.
func TestMergeUpsertContentRevIncrementSemantics(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	now := time.Now().Unix()
	oldMtime := now - 3600

	// Seed existing entry with content_rev=3
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, scan_id, content_rev, bitrate, dirty_flag)
		VALUES ('/music/existing.mp3', '/music', '/music', 'existing.mp3', 0, 1024000, ?, 'scan-prev', 3, 320000, 0)
	`, oldMtime)
	if err != nil {
		t.Fatalf("failed to seed entry: %v", err)
	}

	// Test 1: New entry should get content_rev=1
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-upsert', '/music/new.mp3', '/music', '/music', 'new.mp3', 0, 1024000, ?, 'upsert')
	`, now)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Test 2: Unchanged existing entry should preserve content_rev
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-upsert', '/music/existing.mp3', '/music', '/music', 'existing.mp3', 0, 1024000, ?, 'upsert')
	`, oldMtime) // Same size and mtime
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Test 3: Changed existing entry should increment content_rev and clear bitrate
	changedMtime := now
	_, err = repo.db.Exec(`
		INSERT INTO entries_staging (session_id, path, root_path, parent_path, name, is_dir, size, mtime, operation)
		VALUES ('scan-upsert', '/music/changed.mp3', '/music', '/music', 'changed.mp3', 0, 2048000, ?, 'upsert')
	`, changedMtime)
	if err != nil {
		t.Fatalf("failed to seed staging: %v", err)
	}

	// Seed existing entry that will be changed
	_, err = repo.db.Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, scan_id, content_rev, bitrate, dirty_flag)
		VALUES ('/music/changed.mp3', '/music', '/music', 'changed.mp3', 0, 1024000, ?, 'scan-prev', 2, 256000, 0)
	`, oldMtime)
	if err != nil {
		t.Fatalf("failed to seed entry: %v", err)
	}

	err = repo.MergeStagingSimple("scan-upsert", "/music")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	// Verify new entry has content_rev=1
	var newContentRev int
	err = repo.db.QueryRow("SELECT content_rev FROM entries WHERE path = ?", "/music/new.mp3").Scan(&newContentRev)
	if err != nil {
		t.Fatalf("failed to query new content_rev: %v", err)
	}
	if newContentRev != 1 {
		t.Errorf("expected content_rev=1 for new entry, got %d", newContentRev)
	}

	// Verify unchanged entry preserves content_rev=3
	var unchangedContentRev int
	err = repo.db.QueryRow("SELECT content_rev FROM entries WHERE path = ?", "/music/existing.mp3").Scan(&unchangedContentRev)
	if err != nil {
		t.Fatalf("failed to query unchanged content_rev: %v", err)
	}
	if unchangedContentRev != 3 {
		t.Errorf("expected content_rev=3 for unchanged entry (preserved), got %d", unchangedContentRev)
	}

	// Verify unchanged entry preserves bitrate
	var unchangedBitrate sql.NullInt64
	err = repo.db.QueryRow("SELECT bitrate FROM entries WHERE path = ?", "/music/existing.mp3").Scan(&unchangedBitrate)
	if err != nil {
		t.Fatalf("failed to query unchanged bitrate: %v", err)
	}
	if !unchangedBitrate.Valid || unchangedBitrate.Int64 != 320000 {
		t.Errorf("expected bitrate=320000 for unchanged entry (preserved), got %v", unchangedBitrate.Int64)
	}

	// Verify changed entry has content_rev=3 (incremented from 2)
	var changedContentRev int
	err = repo.db.QueryRow("SELECT content_rev FROM entries WHERE path = ?", "/music/changed.mp3").Scan(&changedContentRev)
	if err != nil {
		t.Fatalf("failed to query changed content_rev: %v", err)
	}
	if changedContentRev != 3 {
		t.Errorf("expected content_rev=3 for changed entry (incremented from 2), got %d", changedContentRev)
	}

	// Verify changed entry has dirty_flag=1
	var changedDirtyFlag int
	err = repo.db.QueryRow("SELECT dirty_flag FROM entries WHERE path = ?", "/music/changed.mp3").Scan(&changedDirtyFlag)
	if err != nil {
		t.Fatalf("failed to query changed dirty_flag: %v", err)
	}
	if changedDirtyFlag != 1 {
		t.Errorf("expected dirty_flag=1 for changed entry, got %d", changedDirtyFlag)
	}

	// Verify changed entry has cleared bitrate
	var changedBitrate sql.NullInt64
	err = repo.db.QueryRow("SELECT bitrate FROM entries WHERE path = ?", "/music/changed.mp3").Scan(&changedBitrate)
	if err != nil {
		t.Fatalf("failed to query changed bitrate: %v", err)
	}
	if changedBitrate.Valid {
		t.Errorf("expected NULL bitrate for changed entry (cleared), got %v", changedBitrate.Int64)
	}
}
