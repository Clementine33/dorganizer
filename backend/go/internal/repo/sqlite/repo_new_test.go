package sqlite //nolint:testpackage // white-box tests exercise unexported internals

import (
	"database/sql"
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
		}

		for _, table := range tables {
			var count int
			err := repo.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).
				Scan(&count)
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

func TestConnectionPragmasAreAppliedToEveryConnection(t *testing.T) {
	repo := newTestRepository(t)
	ctx := t.Context()

	// Two connections held at once: the pragmas are connection-scoped, so a
	// second one is exactly what a one-shot PRAGMA statement fails to configure.
	first, err := repo.db.Conn(ctx)
	if err != nil {
		t.Fatalf("hold first connection: %v", err)
	}
	defer first.Close()
	second, err := repo.db.Conn(ctx)
	if err != nil {
		t.Fatalf("hold second connection: %v", err)
	}
	defer second.Close()

	for name, conn := range map[string]*sql.Conn{"first": first, "second": second} {
		var foreignKeys, busyTimeout int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("%s connection: read foreign_keys: %v", name, err)
		}
		if foreignKeys != 1 {
			t.Errorf("%s connection: foreign_keys = %d, want 1", name, foreignKeys)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatalf("%s connection: read busy_timeout: %v", name, err)
		}
		if busyTimeout != 5000 {
			t.Errorf("%s connection: busy_timeout = %d, want 5000", name, busyTimeout)
		}
	}
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

func TestEntriesPathLookupIsIndexedWithoutADuplicateIndex(t *testing.T) {
	repo := newTestRepository(t)

	var count int
	if err := repo.db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_entries_path'",
	).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 0 {
		t.Fatalf("idx_entries_path duplicates the primary key's own index, got %d", count)
	}

	rows, err := repo.db.Query(
		"EXPLAIN QUERY PLAN SELECT path FROM entries WHERE path = ? OR (path >= ? AND path < ?)",
		"/music/a", "/music/a/", "/music/a0",
	)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if len(plan) == 0 {
		t.Fatal("no query plan")
	}
	// The index that serves this is whichever one SQLite built for the primary
	// key; what matters is that the lookup searches it instead of scanning.
	for _, step := range plan {
		if strings.HasPrefix(step, "SCAN") {
			t.Fatalf("path lookup scans the table: %v", plan)
		}
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
