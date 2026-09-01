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

// Plan represents a persisted plan.
type Plan struct {
	PlanID                string
	RootPath              string
	ScanRootPath          string
	LibraryID             string  // nullable: owning library when known (web-created plans)
	PlanType              string  // display label: workflow, single_delete, single_convert
	SlimMode              *string // nullable legacy column; unused for workflow plans
	SnapshotToken         string
	Status                string // ready, executed, stale, canceled, failed
	PlanKind              string // single_action, workflow
	WorkflowSchemaVersion int    // >0 for workflow plans, 0 for single actions
	CreatedAt             time.Time
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
	DeletedGenerations  int64
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

	if err := migratePlansWorkflowSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	if err := migrateWorksetSchema(db); err != nil {
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

// migratePlansWorkflowSchema is the breaking migration for the workflow plan
// refactor. Plan rows and their per-plan/execute intermediate state are
// intermediate-state only: every legacy plan and execute session is purged in
// one transaction, and the plans table is rebuilt with the new plan_kind /
// workflow_schema_version columns. Libraries, entries and error_events are
// preserved.
func migratePlansWorkflowSchema(db *sql.DB) error {
	hasWorkflow, err := tableHasColumn(db, "plans", "workflow_schema_version")
	if err != nil {
		return err
	}
	if !hasWorkflow {
		if err := migrateWorkflowSchemaInner(db); err != nil {
			return err
		}
	}
	return migratePolicySlotsSchema(db)
}

// migratePolicySlotsSchema converges every schema onto the fixed-three
// policy_slots table and the trimmed plan_workflow_steps columns. The old
// named/versioned classifier registry has no successor: legacy databases are
// rebuilt per the no-compatibility agreement (fresh dev data was accepted).
func migratePolicySlotsSchema(db *sql.DB) error {
	// Fresh databases already have the new plan_workflow_steps (no
	// policy_source_kind column) from initSchema.
	hasSourceKind, err := tableHasColumn(db, "plan_workflow_steps", "policy_source_kind")
	if err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS policy_slots (
		slot_index INTEGER PRIMARY KEY CHECK (slot_index BETWEEN 1 AND 3),
		name TEXT NOT NULL DEFAULT '',
		policy_json TEXT,
		updated_at TEXT DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("policy slots migration: %w", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO policy_slots (slot_index) VALUES (1), (2), (3)`); err != nil {
		return fmt.Errorf("policy slots seed: %w", err)
	}
	if !hasSourceKind {
		return nil
	}
	// Legacy workflow schema: plan snapshots carry classifier name/version and
	// preset-source metadata. Compat was declined; drop and rebuild the table
	// (snapshots are intermediate state, same rationale as the plans purge).
	if _, err := db.Exec(`DROP TABLE plan_workflow_steps`); err != nil {
		return fmt.Errorf("policy slots migration drop: %w", err)
	}
	steps := []string{
		`CREATE TABLE plan_workflow_steps (
			plan_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			step_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'ok',
			policy_schema_version INTEGER NOT NULL DEFAULT 0,
			policy_json TEXT NOT NULL DEFAULT '',
			policy_hash TEXT NOT NULL DEFAULT '',
			classifier_pattern TEXT NOT NULL DEFAULT '',
			classifier_hash TEXT NOT NULL DEFAULT '',
			step_summary_json TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (plan_id, step_index),
			FOREIGN KEY (plan_id) REFERENCES plans(plan_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_plan_workflow_steps_plan ON plan_workflow_steps(plan_id)`,
		`DROP TABLE IF EXISTS classifiers`,
		`DROP INDEX IF EXISTS idx_classifiers_version`,
	}
	for _, stmt := range steps {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("policy slots migration rebuild: %w", err)
		}
	}
	return nil
}

func migrateWorkflowSchemaInner(db *sql.DB) error {
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return err
	}
	if _, err := db.Exec("BEGIN"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = db.Exec("ROLLBACK")
		}
	}()

	purge := []string{
		"DELETE FROM execute_sessions",
		"DELETE FROM plan_errors",
		"DELETE FROM plan_successful_folders",
		"DELETE FROM plan_items",
		"DELETE FROM plans",
	}
	for _, stmt := range purge {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("workflow migration purge: %w", err)
		}
	}

	// Rebuild plans via create + drop + rename (the same pattern the library
	// migration uses) so the retained child tables (plan_items, execute_sessions)
	// re-resolve their FK REFERENCES plans to the new table by name. A plain
	// RENAME TO <legacy> would retarget those FKs at the legacy name and leave
	// them dangling after the drop.
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
			plan_kind TEXT NOT NULL DEFAULT 'single_action',
			workflow_schema_version INTEGER NOT NULL DEFAULT 0,
			created_at TEXT DEFAULT CURRENT_TIMESTAMP
		)`,
		`DROP TABLE plans`,
		`ALTER TABLE plans_new RENAME TO plans`,
		`CREATE TABLE IF NOT EXISTS plan_workflow_steps (
			plan_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			step_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'ok',
			policy_schema_version INTEGER NOT NULL DEFAULT 0,
			policy_json TEXT NOT NULL DEFAULT '',
			policy_hash TEXT NOT NULL DEFAULT '',
			classifier_pattern TEXT NOT NULL DEFAULT '',
			classifier_hash TEXT NOT NULL DEFAULT '',
			step_summary_json TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (plan_id, step_index),
			FOREIGN KEY (plan_id) REFERENCES plans(plan_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS plan_roots (
			plan_id TEXT NOT NULL,
			root_index INTEGER NOT NULL,
			root_path TEXT NOT NULL,
			root_identity TEXT NOT NULL DEFAULT '',
			inventory_fingerprint TEXT NOT NULL DEFAULT '',
			entry_count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (plan_id, root_index),
			FOREIGN KEY (plan_id) REFERENCES plans(plan_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS plan_components (
			plan_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			component_index INTEGER NOT NULL,
			component_id TEXT NOT NULL,
			root_index INTEGER NOT NULL DEFAULT 0,
			partition TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			reason_code TEXT NOT NULL DEFAULT '',
			outcome_json TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (plan_id, component_index),
			FOREIGN KEY (plan_id) REFERENCES plans(plan_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_plans_root ON plans(root_path)`,
		`CREATE INDEX IF NOT EXISTS idx_plans_status ON plans(status)`,
		`CREATE INDEX IF NOT EXISTS idx_plans_library_created ON plans(library_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_plan_workflow_steps_plan ON plan_workflow_steps(plan_id)`,
		`CREATE INDEX IF NOT EXISTS idx_plan_roots_plan ON plan_roots(plan_id)`,
		`CREATE INDEX IF NOT EXISTS idx_plan_components_plan ON plan_components(plan_id)`,
	}
	for _, stmt := range steps {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("workflow migration rebuild: %w", err)
		}
	}

	if _, err := db.Exec("COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// migrateWorksetSchema adds the workset aggregate tables, the plan revision
// association columns, and the plan_roots root-outcome columns to schemas
// created before the workset model. It is additive: standalone plans keep
// workset_id ” and every existing plan_roots row stays status 'ok'. The
// tables themselves are created by initSchema's CREATE IF NOT EXISTS, so this
// migration only guarantees the columns and indexes exist on legacy databases.
func migrateWorksetSchema(db *sql.DB) error {
	if _, err := db.Exec("ALTER TABLE plans ADD COLUMN workset_id TEXT NOT NULL DEFAULT ''"); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return err
		}
	}
	if _, err := db.Exec("ALTER TABLE plan_roots ADD COLUMN root_status TEXT NOT NULL DEFAULT 'ok'"); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return err
		}
	}
	if _, err := db.Exec("ALTER TABLE plan_roots ADD COLUMN root_error_code TEXT NOT NULL DEFAULT ''"); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return err
		}
	}
	if _, err := db.Exec("ALTER TABLE plan_roots ADD COLUMN root_error_message TEXT NOT NULL DEFAULT ''"); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return err
		}
	}
	workTable := "CREATE TABLE IF NOT EXISTS worksets (id TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '', library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL, root_path TEXT NOT NULL DEFAULT '', root_path_key TEXT NOT NULL DEFAULT '', version INTEGER NOT NULL DEFAULT 1, current_revision_id TEXT, creation_idem_key TEXT, created_at TEXT DEFAULT CURRENT_TIMESTAMP, updated_at TEXT DEFAULT CURRENT_TIMESTAMP)"
	for _, stmt := range []string{
		workTable,
		`CREATE TABLE IF NOT EXISTS workset_members (
			workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
			member_index INTEGER NOT NULL,
			rel_path TEXT NOT NULL,
			folder_id TEXT NOT NULL DEFAULT '',
			folder_path TEXT NOT NULL DEFAULT '',
			folder_name TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (workset_id, member_index),
			UNIQUE (workset_id, rel_path)
		)`,
		`CREATE TABLE IF NOT EXISTS workset_drafts (
			workset_id TEXT PRIMARY KEY REFERENCES worksets(id) ON DELETE CASCADE,
			workflow_schema_version INTEGER NOT NULL DEFAULT 1,
			steps_json TEXT NOT NULL DEFAULT '[]',
			draft_hash TEXT NOT NULL DEFAULT '',
			updated_at TEXT DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS workset_revisions (
			plan_id TEXT PRIMARY KEY REFERENCES plans(plan_id) ON DELETE CASCADE,
			workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
			revision_index INTEGER NOT NULL,
			draft_hash TEXT NOT NULL DEFAULT '',
			member_hash TEXT NOT NULL DEFAULT '',
			worksets_version INTEGER NOT NULL DEFAULT 0,
			created_at TEXT DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (workset_id, revision_index)
		)`,
		`CREATE TABLE IF NOT EXISTS plan_generations (
			generation_id TEXT PRIMARY KEY,
			workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
			status TEXT NOT NULL DEFAULT 'queued',
			idempotency_key TEXT NOT NULL DEFAULT '',
			request_hash TEXT NOT NULL DEFAULT '',
			expected_draft_version INTEGER NOT NULL DEFAULT 0,
			request_json TEXT NOT NULL DEFAULT '',
			total_roots INTEGER NOT NULL DEFAULT 0,
			completed_roots INTEGER NOT NULL DEFAULT 0,
			current_root TEXT NOT NULL DEFAULT '',
			error_count INTEGER NOT NULL DEFAULT 0,
			cancel_requested INTEGER NOT NULL DEFAULT 0,
			revision_id TEXT REFERENCES plans(plan_id) ON DELETE SET NULL,
			error_code TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			started_at TEXT,
			finished_at TEXT,
			created_at TEXT DEFAULT CURRENT_TIMESTAMP,
			updated_at TEXT DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_worksets_idem ON worksets(creation_idem_key) WHERE creation_idem_key IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_worksets_library_updated ON worksets(library_id, updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_worksets_updated ON worksets(updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_workset_revisions_workset ON workset_revisions(workset_id, revision_index DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_generations_idem ON plan_generations(workset_id, idempotency_key) WHERE idempotency_key <> '' AND status IN ('queued','running','completed')`,
		`CREATE INDEX IF NOT EXISTS idx_plan_generations_workset_status ON plan_generations(workset_id, status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_plan_generations_queue ON plan_generations(status, created_at, generation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_plans_workset_created ON plans(workset_id, created_at)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("workset schema migration: %w", err)
		}
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
-- plan_kind discriminates workflow plans (declare outputs) from the retained
-- independent single-action path; workflow_schema_version is 0 for
-- single-action rows. workset_id is NULL for standalone plans and set for
-- workset-owned revision plans (FK semantics are managed in app logic).
CREATE TABLE IF NOT EXISTS plans (
    plan_id TEXT PRIMARY KEY,
    root_path TEXT NOT NULL,
    scan_root_path TEXT NOT NULL DEFAULT '',
    library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
    plan_type TEXT NOT NULL,
    slim_mode TEXT,
    snapshot_token TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ready',
    plan_kind TEXT NOT NULL DEFAULT 'single_action',
    workflow_schema_version INTEGER NOT NULL DEFAULT 0,
    workset_id TEXT NOT NULL DEFAULT '',
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

-- Three fixed global policy slots. The count is an invariant of the storage
-- itself: rows 1..3 exist from initialization, and there is no insert/delete
-- API. name is the editable display name; policy_json is NULL while the slot
-- is unconfigured.
CREATE TABLE IF NOT EXISTS policy_slots (
    slot_index INTEGER PRIMARY KEY CHECK (slot_index BETWEEN 1 AND 3),
    name TEXT NOT NULL DEFAULT '',
    policy_json TEXT,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Workflow plan steps: resolved policy/classifier snapshots plus the step
-- summary. Policy snapshots are immutable per plan. classifier_pattern stores
-- the canonical normalized tag snapshot (newline-joined) and classifier_hash
-- the tag-set hash.
CREATE TABLE IF NOT EXISTS plan_workflow_steps (
    plan_id TEXT NOT NULL,
    step_index INTEGER NOT NULL,
    step_type TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ok',
    policy_schema_version INTEGER NOT NULL DEFAULT 0,
    policy_json TEXT NOT NULL DEFAULT '',
    policy_hash TEXT NOT NULL DEFAULT '',
    classifier_pattern TEXT NOT NULL DEFAULT '',
    classifier_hash TEXT NOT NULL DEFAULT '',
    step_summary_json TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plan_id, step_index),
    FOREIGN KEY (plan_id) REFERENCES plans(plan_id) ON DELETE CASCADE
);

-- Planning roots with their metadata inventory fingerprints. root_status is
-- 'ok' for planned roots and 'missing' for members whose subtree no longer
-- exists at planning time; error_code/message carry the stable root outcome.
CREATE TABLE IF NOT EXISTS plan_roots (
    plan_id TEXT NOT NULL,
    root_index INTEGER NOT NULL,
    root_path TEXT NOT NULL,
    root_identity TEXT NOT NULL DEFAULT '',
    inventory_fingerprint TEXT NOT NULL DEFAULT '',
    entry_count INTEGER NOT NULL DEFAULT 0,
    root_status TEXT NOT NULL DEFAULT 'ok',
    root_error_code TEXT NOT NULL DEFAULT '',
    root_error_message TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plan_id, root_index),
    FOREIGN KEY (plan_id) REFERENCES plans(plan_id) ON DELETE CASCADE
);

-- Component outcomes (lanes, decisions, operations, projected inventory)
-- persisted as deterministic JSON snapshots.
CREATE TABLE IF NOT EXISTS plan_components (
    plan_id TEXT NOT NULL,
    step_index INTEGER NOT NULL,
    component_index INTEGER NOT NULL,
    component_id TEXT NOT NULL,
    root_index INTEGER NOT NULL DEFAULT 0,
    partition TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    reason_code TEXT NOT NULL DEFAULT '',
    outcome_json TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (plan_id, component_index),
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

-- Worksets: long-lived aggregates owned by a library (nullable once the
-- library is deleted). title duplicates are allowed. version is the single
-- aggregate concurrency counter (rename, draft save, revision promotion).
-- current_revision_id is never mutated after generation; a failed/canceled/
-- interrupted generation never clears it. creation_idem_key enables replay of
-- workset creation for up to 30 days (expired keys are cleared, never the row).
CREATE TABLE IF NOT EXISTS worksets (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
    root_path TEXT NOT NULL DEFAULT '',
    root_path_key TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    current_revision_id TEXT,
    creation_idem_key TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_worksets_idem ON worksets(creation_idem_key) WHERE creation_idem_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_worksets_library_updated ON worksets(library_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_worksets_updated ON worksets(updated_at);

-- Ordered album-folder membership. rel_path (normalized library-relative
-- path) is the durable member identity; folder_id/path/name are display
-- snapshots that may churn across rescans.
CREATE TABLE IF NOT EXISTS workset_members (
    workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
    member_index INTEGER NOT NULL,
    rel_path TEXT NOT NULL,
    folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    folder_name TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (workset_id, member_index),
    UNIQUE (workset_id, rel_path)
);

-- One mutable workflow draft per workset. steps_json is the canonical strict
-- workflow JSON; draft_hash is the canonical content hash used for
-- needs_planning derivation and generation dedup.
CREATE TABLE IF NOT EXISTS workset_drafts (
    workset_id TEXT PRIMARY KEY REFERENCES worksets(id) ON DELETE CASCADE,
    workflow_schema_version INTEGER NOT NULL DEFAULT 1,
    steps_json TEXT NOT NULL DEFAULT '[]',
    draft_hash TEXT NOT NULL DEFAULT '',
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Immutable plan revisions owned by a workset. revision_index is monotonic
-- within the workset; draft_hash frozen at generation time (audit + replay);
-- worksets_version frozen for audit only (the aggregate version is never used
-- as a mutation lock).
CREATE TABLE IF NOT EXISTS workset_revisions (
    plan_id TEXT PRIMARY KEY REFERENCES plans(plan_id) ON DELETE CASCADE,
    workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
    revision_index INTEGER NOT NULL,
    draft_hash TEXT NOT NULL DEFAULT '',
    member_hash TEXT NOT NULL DEFAULT '',
    worksets_version INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (workset_id, revision_index)
);

CREATE INDEX IF NOT EXISTS idx_workset_revisions_workset ON workset_revisions(workset_id, revision_index DESC);

-- Async planning session. status: queued|running|completed|failed|canceled|
-- interrupted. idempotency_key is guaranteed for 30 days for completed rows;
-- failed/canceled/interrupted release the key immediately. cancel_requested is
-- observed by the worker at cooperative checkpoints.
CREATE TABLE IF NOT EXISTS plan_generations (
    generation_id TEXT PRIMARY KEY,
    workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'queued',
    idempotency_key TEXT NOT NULL DEFAULT '',
    request_hash TEXT NOT NULL DEFAULT '',
    expected_draft_version INTEGER NOT NULL DEFAULT 0,
    request_json TEXT NOT NULL DEFAULT '',
    total_roots INTEGER NOT NULL DEFAULT 0,
    completed_roots INTEGER NOT NULL DEFAULT 0,
    current_root TEXT NOT NULL DEFAULT '',
    error_count INTEGER NOT NULL DEFAULT 0,
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    revision_id TEXT REFERENCES plans(plan_id) ON DELETE SET NULL,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    started_at TEXT,
    finished_at TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_generations_idem
    ON plan_generations(workset_id, idempotency_key)
    WHERE idempotency_key <> '' AND status IN ('queued','running','completed');
CREATE INDEX IF NOT EXISTS idx_plan_generations_workset_status
    ON plan_generations(workset_id, status, created_at);
CREATE INDEX IF NOT EXISTS idx_plan_generations_queue
    ON plan_generations(status, created_at, generation_id);

-- Global custom classifier tag library. Holds user-entered literal tags for
-- cross-workset reuse. Case-insensitively unique normalized_tag prevents duplicates.
CREATE TABLE IF NOT EXISTS classifier_tag_library (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tag TEXT NOT NULL,
    normalized_tag TEXT NOT NULL UNIQUE,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_classifier_tag_norm ON classifier_tag_library(normalized_tag);
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
CREATE INDEX IF NOT EXISTS idx_plan_workflow_steps_plan ON plan_workflow_steps(plan_id);
CREATE INDEX IF NOT EXISTS idx_plan_roots_plan ON plan_roots(plan_id);
CREATE INDEX IF NOT EXISTS idx_plan_components_plan ON plan_components(plan_id);

-- Error event indexes
CREATE INDEX IF NOT EXISTS idx_errors_root ON error_events(root_path);
CREATE INDEX IF NOT EXISTS idx_errors_scope ON error_events(scope);

-- Execute session indexes
CREATE INDEX IF NOT EXISTS idx_exec_plan ON execute_sessions(plan_id);
CREATE INDEX IF NOT EXISTS idx_exec_status ON execute_sessions(status);
`
	_, err := db.Exec(schemaIndexes)
	if err != nil {
		return err
	}
	// Fixed three policy slots exist from initialization (storage-level
	// cardinality invariant; there is no insert/delete API).
	_, err = db.Exec(`INSERT OR IGNORE INTO policy_slots (slot_index) VALUES (1), (2), (3)`)
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
