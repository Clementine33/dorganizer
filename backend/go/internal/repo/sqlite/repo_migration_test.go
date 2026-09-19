package sqlite //nolint:testpackage // white-box tests exercise unexported internals

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenRefusesOlderDatabase is the compatibility contract: a database that
// was not created by this schema generation is refused at open time. Nothing is
// migrated, nothing is cleared, and the refusal names the way out (a new data
// directory) instead of resetting someone's data (ADR 0007 §7, spec D2).
func TestOpenRefusesOlderDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "older.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, execErr := db.Exec(`CREATE TABLE libraries (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		root_path TEXT NOT NULL DEFAULT ''
	)`); execErr != nil {
		t.Fatalf("create older schema: %v", execErr)
	}
	if _, execErr := db.Exec(
		"INSERT INTO libraries (id, name, root_path) VALUES ('lib-1', 'Music', '/music')",
	); execErr != nil {
		t.Fatalf("seed older library: %v", execErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close db: %v", closeErr)
	}

	repo, err := NewRepository(dbPath)
	if repo != nil {
		repo.Close()
		t.Fatal("expected the older database to be refused")
	}
	if !errors.Is(err, ErrIncompatibleDatabase) {
		t.Fatalf("expected ErrIncompatibleDatabase, got %v", err)
	}
	if !strings.Contains(err.Error(), "ONSEI_DATA_DIR") {
		t.Errorf("refusal must name the way out, got: %v", err)
	}

	// The refused database is untouched: the row that was there is still there.
	check, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen for inspection: %v", err)
	}
	defer check.Close()
	var name string
	if scanErr := check.QueryRow("SELECT name FROM libraries WHERE id = 'lib-1'").Scan(&name); scanErr != nil {
		t.Fatalf("older row did not survive the refusal: %v", scanErr)
	}
	if name != "Music" {
		t.Errorf("older row changed: name = %q", name)
	}
}

// TestOpenRefusesAnotherSchemaVersion refuses a database whose marker names a
// different schema generation, even though the marker table exists.
func TestOpenRefusesAnotherSchemaVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO schema_meta (key, value) VALUES ('schema_version', '99')`,
	} {
		if _, execErr := db.Exec(stmt); execErr != nil {
			t.Fatalf("exec %q: %v", stmt, execErr)
		}
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close db: %v", closeErr)
	}

	repo, err := NewRepository(dbPath)
	if repo != nil {
		repo.Close()
		t.Fatal("expected another schema version to be refused")
	}
	if !errors.Is(err, ErrIncompatibleDatabase) {
		t.Fatalf("expected ErrIncompatibleDatabase, got %v", err)
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("refusal must name the version it found, got: %v", err)
	}
}

// TestOpenCreatesAndReportsTheSchemaMarker opens a fresh database, writes the
// marker, and reopens it: a database created by this build is accepted by this
// build.
func TestOpenCreatesAndReportsTheSchemaMarker(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh.db")

	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("NewRepository on a fresh path failed: %v", err)
	}
	var marker string
	if scanErr := repo.DB().QueryRow(
		"SELECT value FROM schema_meta WHERE key = 'schema_version'",
	).Scan(&marker); scanErr != nil {
		t.Fatalf("read schema marker: %v", scanErr)
	}
	if marker != schemaVersion {
		t.Errorf("marker = %q, want %q", marker, schemaVersion)
	}
	// The world outside the library domain is gone from a fresh database: the
	// overview reads the scanned inventory, and the derived folder table that
	// used to sit beside it no longer exists.
	var count int
	if scanErr := repo.DB().QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'library_folders'",
	).Scan(&count); scanErr != nil {
		t.Fatalf("inspect library_folders: %v", scanErr)
	}
	if count != 0 {
		t.Error("library_folders must not exist: the inventory is the only listing source")
	}
	if closeErr := repo.Close(); closeErr != nil {
		t.Fatalf("close repo: %v", closeErr)
	}

	reopened, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("reopen of a database this build created failed: %v", err)
	}
	reopened.Close()
}
