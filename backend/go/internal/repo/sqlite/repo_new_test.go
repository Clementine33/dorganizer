package sqlite

import (
	"strings"
	"testing"
)

func TestNewRepository(t *testing.T) {
	t.Run("creates required tables", func(t *testing.T) {
		repo := newTestRepository(t)

		tables := []string{
			"entries",
			"entries_staging",
			"scan_sessions",
			"plans",
			"plan_items",
			"error_events",
			"execute_sessions",
		}

		for _, table := range tables {
			var count int
			err := repo.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
			if err != nil {
				t.Errorf("failed to check table %s: %v", table, err)
			}
			if count != 1 {
				t.Errorf("table %s does not exist", table)
			}
		}
	})

	t.Run("creates entries columns", func(t *testing.T) {
		repo := newTestRepository(t)

		requiredColumns := []string{
			"path",
			"root_path",
			"parent_path",
			"name",
			"is_dir",
			"size",
			"mtime",
			"scan_id",
			"content_rev",
			"bitrate",
			"dirty_flag",
			"is_error",
			"error_reason",
			"updated_at",
			// Legacy columns for compatibility
			"path_posix",
			"file_size",
			"duration_ms",
			"format",
		}

		for _, col := range requiredColumns {
			var count int
			err := repo.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name=?", col).Scan(&count)
			if err != nil {
				t.Errorf("failed to check column %s: %v", col, err)
			}
			if count != 1 {
				t.Errorf("column %s does not exist in entries table", col)
			}
		}
	})

	t.Run("sets entries path as primary key", func(t *testing.T) {
		repo := newTestRepository(t)

		var pk int
		err := repo.db.QueryRow("SELECT pk FROM pragma_table_info('entries') WHERE name='path'").Scan(&pk)
		if err != nil {
			t.Fatalf("failed to inspect entries.path PK flag: %v", err)
		}
		if pk != 1 {
			t.Fatalf("expected entries.path to be primary key, got pk=%d", pk)
		}
	})

	t.Run("enables WAL journal mode", func(t *testing.T) {
		repo := newTestRepository(t)

		var mode string
		err := repo.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
		if err != nil {
			t.Fatalf("failed to query journal mode: %v", err)
		}
		if !strings.EqualFold(mode, "wal") {
			t.Fatalf("expected WAL journal mode, got %q", mode)
		}
	})
}

func TestNewRepository_CreatesEntriesRootDirPathIndex(t *testing.T) {
	repo := newTestRepository(t)

	var count int
	err := repo.db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_entries_root_dir_path'",
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to query sqlite_master for idx_entries_root_dir_path: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected idx_entries_root_dir_path index to exist, got count=%d", count)
	}
}

func TestRepoSchemaInit(t *testing.T) {
	repo := newTestRepository(t)

	var count int
	err := repo.db.QueryRow("SELECT COUNT(*) FROM entries").Scan(&count)
	if err != nil {
		t.Fatalf("entries table not found: %v", err)
	}

	err = repo.db.QueryRow("SELECT COUNT(*) FROM entries_staging").Scan(&count)
	if err != nil {
		t.Fatalf("entries_staging table not found: %v", err)
	}
}
