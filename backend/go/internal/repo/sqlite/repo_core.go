package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/onsei/organizer/backend/internal/pathnorm"
	_ "modernc.org/sqlite"
)

// timeFormat is the format used for storing timestamps in SQLite
const timeFormat = time.RFC3339Nano

// parseTimestamp parses a timestamp string with fallback to SQLite's default format
func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// Try RFC3339Nano first (our preferred format)
	if t, err := time.Parse(timeFormat, s); err == nil {
		return t
	}
	// Fallback to SQLite's default format
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	// Fallback to SQLite's datetime() function format
	if t, err := time.Parse("2006-01-02 15:04:05.999999999", s); err == nil {
		return t
	}
	return time.Time{}
}

// Plan represents a persisted Slim/Prune plan
type Plan struct {
	PlanID        string
	RootPath      string
	ScanRootPath  string
	LibraryID     string  // nullable: owning library when known (web-created plans)
	PlanType      string  // slim, prune, single_delete, single_convert
	SlimMode      *string // nullable: 1, 2, or nil
	SnapshotToken string
	Status        string // ready, executed, stale, canceled
	CreatedAt     time.Time
}

// PlanFolderError is a folder-scoped error persisted with a plan so the plan
// detail can be reconstructed without error_events (which are retention-managed
// and not plan-scoped).
type PlanFolderError struct {
	PlanID     string
	ErrorIndex int
	FolderPath string
	Code       string
	Message    string
	Retryable  bool
}

// PlanSuccessfulFolder records a folder that analyzed cleanly for a plan.
type PlanSuccessfulFolder struct {
	PlanID      string
	FolderIndex int
	FolderPath  string
}

// PlanItem represents a single operation in a plan
type PlanItem struct {
	PlanID                 string
	ItemIndex              int
	OpType                 string // convert_and_delete, delete
	SourcePath             string
	TargetPath             *string // nullable - nil for delete operations
	ReasonCode             string
	PreconditionPath       string
	PreconditionContentRev int
	PreconditionSize       int64
	PreconditionMtime      int64
}

// ScanSession represents a scan operation
type ScanSession struct {
	SessionID    string
	RootPath     string
	ScopePath    *string // nullable for full scans
	Kind         string  // full, folder
	Status       string  // queued, running, merging, completed, failed, canceled, interrupted
	ErrorCode    string
	ErrorMessage string
	StartedAt    time.Time
	FinishedAt   time.Time
}

// ExecuteSession represents an execute operation
type ExecuteSession struct {
	SessionID    string
	PlanID       string
	RootPath     string
	Status       string // running, completed, failed, canceled, interrupted
	StartedAt    time.Time
	FinishedAt   time.Time
	ErrorCode    string
	ErrorMessage string
}

// ErrorEvent represents an error during operations
type ErrorEvent struct {
	ID        int64
	Scope     string // scan, slim, prune, execute
	RootPath  string
	Path      *string // nullable - may not have a specific path
	Code      string
	Message   string
	Retryable bool
	CreatedAt time.Time
}

// CleanupStats holds counts of rows deleted by each cleanup operation
type CleanupStats struct {
	DeletedErrorEvents  int64
	DeletedScanSessions int64
	DeletedPlans        int64
}

// ==================== Repository ====================

type Repository struct {
	db *sql.DB

	// BitrateWriteMu serializes bitrate DB writes across concurrent Analyzer
	// goroutines to avoid SQLITE_BUSY under concurrent folder-plan requests.
	BitrateWriteMu sync.Mutex
}

func NewRepository(dbPath string) (*Repository, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Enable foreign key enforcement
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, err
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, err
	}

	if _, err := db.Exec("PRAGMA busy_timeout=5000;"); err != nil {
		db.Close()
		return nil, err
	}

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	// Lightweight migration: add plans.scan_root_path for split semantics.
	if _, err := db.Exec("ALTER TABLE plans ADD COLUMN scan_root_path TEXT NOT NULL DEFAULT ''"); err != nil {
		// Ignore duplicate-column errors for existing DBs.
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			db.Close()
			return nil, err
		}
	}

	if err := migrateLibraryRootPathKeys(db); err != nil {
		db.Close()
		return nil, err
	}

	if err := migratePlansLibrarySchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Repository{db: db}, nil
}

// tableHasColumn reports whether a column exists in a table.
func tableHasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// migrateLibraryRootPathKeys adds libraries.root_path_key to pre-key schemas,
// backfills it from the canonical root identity, and then enforces uniqueness.
// If existing rows canonicalize to the same key (e.g. `/music` and `/music/.`,
// or `C:/Music` and `c:/music`), the migration fails with an explicit
// diagnostic naming the conflicting libraries instead of silently merging or
// dropping data.
func migrateLibraryRootPathKeys(db *sql.DB) error {
	if _, err := db.Exec("ALTER TABLE libraries ADD COLUMN root_path_key TEXT NOT NULL DEFAULT ''"); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return err
		}
	}

	rows, err := db.Query("SELECT id, root_path FROM libraries")
	if err != nil {
		return err
	}
	defer rows.Close()

	type libRow struct{ id, root string }
	var libs []libRow
	for rows.Next() {
		var l libRow
		if err := rows.Scan(&l.id, &l.root); err != nil {
			return err
		}
		libs = append(libs, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	seen := make(map[string]string, len(libs)) // key -> first library id
	for _, l := range libs {
		key := pathnorm.RootPathKey(l.root)
		if first, ok := seen[key]; ok {
			return fmt.Errorf("library root canonicalization collision: libraries %q (%q) and %q (%q) resolve to the same root identity %q; resolve the duplicate libraries before opening this database", first, l.root, l.id, l.root, key)
		}
		seen[key] = l.id
		if _, err := db.Exec("UPDATE libraries SET root_path_key = ? WHERE id = ?", key, l.id); err != nil {
			return err
		}
	}

	if _, err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_libraries_root_path_key ON libraries(root_path_key)"); err != nil {
		return err
	}
	return nil
}

// migratePlansLibrarySchema upgrades pre-library_id plans schemas. SQLite
// cannot add a REFERENCES column via ALTER TABLE, so the plans table is
// rebuilt in a transaction (DDL is transactional) with foreign-key enforcement
// off for the rebuild. After the column exists it backfills ownership from the
// canonical root identity and ensures the listing index.
func migratePlansLibrarySchema(db *sql.DB) error {
	hasCol, err := tableHasColumn(db, "plans", "library_id")
	if err != nil {
		return err
	}
	if !hasCol {
		if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		defer func() {
			_, _ = db.Exec("PRAGMA foreign_keys=ON")
		}()

		if _, err := db.Exec("BEGIN"); err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				_, _ = db.Exec("ROLLBACK")
			}
		}()

		steps := []string{
			`CREATE TABLE plans_new (
				plan_id TEXT PRIMARY KEY,
				root_path TEXT NOT NULL,
				scan_root_path TEXT NOT NULL DEFAULT '',
				library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
				plan_type TEXT NOT NULL,
				slim_mode TEXT,
				snapshot_token TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'ready',
				created_at TEXT DEFAULT CURRENT_TIMESTAMP
			)`,
			`INSERT INTO plans_new (plan_id, root_path, scan_root_path, plan_type, slim_mode, snapshot_token, status, created_at)
			 SELECT plan_id, root_path, scan_root_path, plan_type, slim_mode, snapshot_token, status, created_at FROM plans`,
			`DROP TABLE plans`,
			`ALTER TABLE plans_new RENAME TO plans`,
			`CREATE INDEX idx_plans_root ON plans(root_path)`,
			`CREATE INDEX idx_plans_status ON plans(status)`,
			`CREATE INDEX idx_plans_library_created ON plans(library_id, created_at)`,
		}
		for _, s := range steps {
			if _, err := db.Exec(s); err != nil {
				return fmt.Errorf("plans schema migration: %w", err)
			}
		}
		if _, err := db.Exec("COMMIT"); err != nil {
			return err
		}
		committed = true
	}

	// Backfill ownership: a legacy plan whose scan_root_path uniquely matches a
	// library's canonical root key is attributed to that library. Unmatched or
	// ambiguous plans stay nullable and remain visible in the global list.
	libKeys := map[string]string{}
	libs, err := db.Query("SELECT id, root_path_key FROM libraries")
	if err != nil {
		return err
	}
	for libs.Next() {
		var id, key string
		if err := libs.Scan(&id, &key); err != nil {
			libs.Close()
			return err
		}
		libKeys[key] = id
	}
	libs.Close()
	if err := libs.Err(); err != nil {
		return err
	}

	plans, err := db.Query("SELECT plan_id, scan_root_path FROM plans WHERE library_id IS NULL")
	if err != nil {
		return err
	}
	var toUpdate []struct{ id, libraryID string }
	for plans.Next() {
		var planID, scanRoot string
		if err := plans.Scan(&planID, &scanRoot); err != nil {
			plans.Close()
			return err
		}
		if scanRoot == "" {
			continue
		}
		if libraryID, ok := libKeys[pathnorm.RootPathKey(scanRoot)]; ok {
			toUpdate = append(toUpdate, struct{ id, libraryID string }{planID, libraryID})
		}
	}
	plans.Close()
	if err := plans.Err(); err != nil {
		return err
	}

	for _, u := range toUpdate {
		if _, err := db.Exec("UPDATE plans SET library_id = ? WHERE plan_id = ?", u.libraryID, u.id); err != nil {
			return err
		}
	}

	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_plans_library_created ON plans(library_id, created_at)"); err != nil {
		return err
	}
	return nil
}

func initSchema(db *sql.DB) error {
	schemaTables := `
-- P0 Schema V1: Main entries table with content revision tracking
CREATE TABLE IF NOT EXISTS entries (
    path TEXT PRIMARY KEY,
    root_path TEXT NOT NULL DEFAULT '',
    parent_path TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    is_dir INTEGER NOT NULL DEFAULT 0,
    size INTEGER NOT NULL DEFAULT 0,
    mtime INTEGER NOT NULL DEFAULT 0,
    scan_id TEXT NOT NULL DEFAULT '',
    content_rev INTEGER NOT NULL DEFAULT 1,
    bitrate INTEGER,
    dirty_flag INTEGER NOT NULL DEFAULT 0,
    is_error INTEGER NOT NULL DEFAULT 0,
    error_reason TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
    -- Legacy columns for compatibility
    path_posix TEXT NOT NULL DEFAULT '',
    file_size INTEGER,
    duration_ms INTEGER,
    format TEXT
);

-- Staging table for scan operations (session-scoped)
CREATE TABLE IF NOT EXISTS entries_staging (
    session_id TEXT NOT NULL,
    path TEXT NOT NULL,
    root_path TEXT NOT NULL DEFAULT '',
    parent_path TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    is_dir INTEGER NOT NULL DEFAULT 0,
    size INTEGER NOT NULL DEFAULT 0,
    mtime INTEGER NOT NULL DEFAULT 0,
    operation TEXT NOT NULL DEFAULT 'upsert',
    status TEXT DEFAULT 'pending',
    file_size INTEGER,
    bitrate INTEGER,
    duration_ms INTEGER,
    format TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, path)
);

-- Scan session tracking
CREATE TABLE IF NOT EXISTS scan_sessions (
    session_id TEXT PRIMARY KEY,
    root_path TEXT NOT NULL,
    scope_path TEXT,
    kind TEXT NOT NULL DEFAULT 'full',
    status TEXT NOT NULL DEFAULT 'queued',
    error_code TEXT,
    error_message TEXT,
    started_at TEXT DEFAULT CURRENT_TIMESTAMP,
    finished_at TEXT
);

-- Persisted plans. Legacy schemas without library_id are upgraded by
-- migratePlansLibrarySchema (the FK cannot be added via ALTER TABLE).
CREATE TABLE IF NOT EXISTS plans (
    plan_id TEXT PRIMARY KEY,
    root_path TEXT NOT NULL,
    scan_root_path TEXT NOT NULL DEFAULT '',
    library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
    plan_type TEXT NOT NULL,
    slim_mode TEXT,
    snapshot_token TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ready',
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Plan-scoped folder outcomes, persisted with the plan for durable detail.
CREATE TABLE IF NOT EXISTS plan_errors (
    plan_id TEXT NOT NULL,
    error_index INTEGER NOT NULL,
    folder_path TEXT NOT NULL DEFAULT '',
    code TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    retryable INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (plan_id, error_index),
    FOREIGN KEY (plan_id) REFERENCES plans(plan_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS plan_successful_folders (
    plan_id TEXT NOT NULL,
    folder_index INTEGER NOT NULL,
    folder_path TEXT NOT NULL,
    PRIMARY KEY (plan_id, folder_index),
    FOREIGN KEY (plan_id) REFERENCES plans(plan_id) ON DELETE CASCADE
);

-- Plan items
CREATE TABLE IF NOT EXISTS plan_items (
    plan_id TEXT NOT NULL,
    item_index INTEGER NOT NULL,
    op_type TEXT NOT NULL,
    source_path TEXT NOT NULL,
    target_path TEXT,
    reason_code TEXT NOT NULL,
    precondition_path TEXT NOT NULL,
    precondition_content_rev INTEGER NOT NULL,
    precondition_size INTEGER NOT NULL,
    precondition_mtime INTEGER NOT NULL,
    PRIMARY KEY (plan_id, item_index),
    FOREIGN KEY (plan_id) REFERENCES plans(plan_id) ON DELETE CASCADE
);

-- Error events
CREATE TABLE IF NOT EXISTS error_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scope TEXT NOT NULL,
    root_path TEXT NOT NULL,
    path TEXT,
    code TEXT NOT NULL,
    message TEXT NOT NULL,
    retryable INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Execute sessions
CREATE TABLE IF NOT EXISTS execute_sessions (
    session_id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL,
    root_path TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running',
    started_at TEXT DEFAULT CURRENT_TIMESTAMP,
    finished_at TEXT,
    error_code TEXT,
    error_message TEXT,
    FOREIGN KEY (plan_id) REFERENCES plans(plan_id) ON DELETE CASCADE
);

-- Libraries (web library views)
CREATE TABLE IF NOT EXISTS libraries (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    root_path TEXT NOT NULL DEFAULT '',
    root_path_key TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
    last_scan_at TEXT,
    last_scan_status TEXT NOT NULL DEFAULT '',
    last_scan_error TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_libraries_root_path ON libraries(root_path);
-- idx_libraries_root_path_key is created in migrateLibraryRootPathKeys after
-- the column exists on both new and legacy schemas.

-- Library folders derived from scanned entries
CREATE TABLE IF NOT EXISTS library_folders (
    id TEXT PRIMARY KEY,
    library_id TEXT NOT NULL,
    path TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    relative_path TEXT NOT NULL DEFAULT '',
    audio_file_count INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (library_id) REFERENCES libraries(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_library_folders_lib_path ON library_folders(library_id, path);
`
	if _, err := db.Exec(schemaTables); err != nil {
		return err
	}

	// Create indexes
	schemaIndexes := `
-- Entry indexes
CREATE INDEX IF NOT EXISTS idx_entries_root_path ON entries(root_path);
CREATE INDEX IF NOT EXISTS idx_entries_parent_path ON entries(parent_path);
CREATE INDEX IF NOT EXISTS idx_entries_path_posix ON entries(path_posix);
CREATE INDEX IF NOT EXISTS idx_entries_path ON entries(path);
CREATE INDEX IF NOT EXISTS idx_entries_root_dir_path ON entries(root_path, is_dir, path);

-- Staging indexes
CREATE INDEX IF NOT EXISTS idx_staging_session ON entries_staging(session_id);
CREATE INDEX IF NOT EXISTS idx_entries_staging_status ON entries_staging(status);

-- Scan session indexes
CREATE INDEX IF NOT EXISTS idx_scan_sessions_root ON scan_sessions(root_path);
CREATE INDEX IF NOT EXISTS idx_scan_sessions_status ON scan_sessions(status);

-- Plan indexes
CREATE INDEX IF NOT EXISTS idx_plans_root ON plans(root_path);
CREATE INDEX IF NOT EXISTS idx_plans_status ON plans(status);
-- idx_plans_library_created is created by migratePlansLibrarySchema after the
-- library_id column exists on both new and legacy schemas.
CREATE INDEX IF NOT EXISTS idx_plan_items_plan ON plan_items(plan_id);

-- Error event indexes
CREATE INDEX IF NOT EXISTS idx_errors_root ON error_events(root_path);
CREATE INDEX IF NOT EXISTS idx_errors_scope ON error_events(scope);

-- Execute session indexes
CREATE INDEX IF NOT EXISTS idx_exec_plan ON execute_sessions(plan_id);
CREATE INDEX IF NOT EXISTS idx_exec_status ON execute_sessions(status);
`
	_, err := db.Exec(schemaIndexes)
	return err
}

func (r *Repository) Close() error {
	return r.db.Close()
}

// DB returns the underlying database connection
func (r *Repository) DB() *sql.DB {
	return r.db
}

// EnsureDBPath creates the database file at given path
func EnsureDBPath(path string) error {
	dir := path
	for len(dir) > 0 && dir[len(dir)-1] != '/' && dir[len(dir)-1] != '\\' {
		dir = dir[:len(dir)-1]
	}
	if dir != "" {
		return os.MkdirAll(dir, 0755)
	}
	return nil
}
