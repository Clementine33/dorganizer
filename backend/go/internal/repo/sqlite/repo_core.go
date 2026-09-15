package sqlite

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/onsei/organizer/backend/internal/pathnorm"
)

// timeFormat is the format used for storing timestamps in SQLite.
const timeFormat = time.RFC3339Nano

// parseTimestamp parses a timestamp string with fallback to SQLite's default format.
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
	LibraryID             string  // nullable: owning library when known
	PlanType              string  // display label: workflow
	SlimMode              *string // nullable legacy column; unused
	SnapshotToken         string
	Status                string // ready, executed, stale, canceled, failed
	PlanKind              string // workflow
	WorkflowSchemaVersion int    // >0 for workflow plans
	CreatedAt             time.Time
}

// ScanSession represents a scan operation.
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

// CleanupStats holds counts of rows deleted by each cleanup operation.
type CleanupStats struct {
	DeletedScanSessions int64
	DeletedGenerations  int64
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

	if err := migrateRetireStandalonePlanSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Repository{db: db}, nil
}

// migrateRetireStandalonePlanSchema drops the tables of the retired standalone
// plan-execute flow. Fresh databases never create them; legacy databases drop
// them here so no stale rows or foreign keys survive the retirement.
func migrateRetireStandalonePlanSchema(db *sql.DB) error {
	for _, table := range []string{
		"execute_sessions",
		"plan_items",
		"plan_errors",
		"plan_successful_folders",
		"error_events",
	} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
			return fmt.Errorf("retire standalone plan schema: drop %s: %w", table, err)
		}
	}
	return nil
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
			return fmt.Errorf(
				"library root canonicalization collision: libraries %q (%q) and %q (%q) resolve to the same root identity %q; resolve the duplicate libraries before opening this database",
				first,
				l.root,
				l.id,
				l.root,
				key,
			)
		}
		seen[key] = l.id
		if _, err := db.Exec("UPDATE libraries SET root_path_key = ? WHERE id = ?", key, l.id); err != nil {
			return err
		}
	}

	if _, err := db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_libraries_root_path_key ON libraries(root_path_key)",
	); err != nil {
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
		if _, fkErr := db.Exec("PRAGMA foreign_keys=OFF"); fkErr != nil {
			return fkErr
		}
		defer func() {
			_, _ = db.Exec("PRAGMA foreign_keys=ON")
		}()

		if _, beginErr := db.Exec("BEGIN"); beginErr != nil {
			return beginErr
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
			if _, stepErr := db.Exec(s); stepErr != nil {
				return fmt.Errorf("plans schema migration: %w", stepErr)
			}
		}
		if _, commitErr := db.Exec("COMMIT"); commitErr != nil {
			return commitErr
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
		if scanErr := libs.Scan(&id, &key); scanErr != nil {
			libs.Close()
			return scanErr
		}
		libKeys[key] = id
	}
	libs.Close()
	if rowsErr := libs.Err(); rowsErr != nil {
		return rowsErr
	}

	return backfillPlansLibraryOwnership(db, libKeys)
}

// backfillPlansLibraryOwnership attributes legacy nullable-library plans to the
// library whose canonical root key matches the plan's scan_root_path.
func backfillPlansLibraryOwnership(db *sql.DB, libKeys map[string]string) error {
	plans, err := db.Query("SELECT plan_id, scan_root_path FROM plans WHERE library_id IS NULL")
	if err != nil {
		return err
	}
	var toUpdate []struct{ id, libraryID string }
	for plans.Next() {
		var planID, scanRoot string
		if scanErr := plans.Scan(&planID, &scanRoot); scanErr != nil {
			plans.Close()
			return scanErr
		}
		if scanRoot == "" {
			continue
		}
		if libraryID, ok := libKeys[pathnorm.RootPathKey(scanRoot)]; ok {
			toUpdate = append(toUpdate, struct{ id, libraryID string }{planID, libraryID})
		}
	}
	plans.Close()
	if rowsErr := plans.Err(); rowsErr != nil {
		return rowsErr
	}

	for _, u := range toUpdate {
		if _, updateErr := db.Exec(
			"UPDATE plans SET library_id = ? WHERE plan_id = ?",
			u.libraryID,
			u.id,
		); updateErr != nil {
			return updateErr
		}
	}
	return createPlansLibraryIndex(db)
}

func createPlansLibraryIndex(db *sql.DB) error {
	_, err := db.Exec(
		"CREATE INDEX IF NOT EXISTS idx_plans_library_created ON plans(library_id, created_at)",
	)
	return err
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

	// Legacy plan rows are intermediate-state only. Their child tables are
	// dropped wholesale by migrateRetireStandalonePlanSchema right after this
	// migration; the plans cascade keeps this purge self-contained either way.
	purge := []string{
		"DELETE FROM plans",
	}
	for _, stmt := range purge {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("workflow migration purge: %w", err)
		}
	}

	// Rebuild plans via create + drop + rename (the same pattern the library
	// migration uses) so remaining legacy child tables re-resolve their FK
	// REFERENCES plans to the new table by name. A plain RENAME TO <legacy>
	// would retarget those FKs at the legacy name and leave them dangling
	// after the drop; the child tables themselves are dropped by
	// migrateRetireStandalonePlanSchema just after.
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
			plan_kind TEXT NOT NULL DEFAULT 'workflow',
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

// migrateWorksetSchema converges databases created before the operation model
// onto the columns the current code reads. Tables themselves come from
// initSchema's CREATE IF NOT EXISTS; this only adds columns that a legacy
// database cannot have. Retired pre-operation workset tables (workset_drafts,
// workset_revisions, workset_confirmations) are left untouched and unread:
// ADR 0004 requires no legacy-data migration and forbids deleting user data.
func migrateWorksetSchema(db *sql.DB) error {
	if _, err := db.Exec("ALTER TABLE plans ADD COLUMN workset_id TEXT NOT NULL DEFAULT ''"); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return err
		}
	}
	if err := migrateWorksetMemberIDs(db); err != nil {
		return err
	}
	if err := addColumn(db, "plan_generations", "operation_type", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, decl := range []string{
		"root_status TEXT NOT NULL DEFAULT 'ok'",
		"root_error_code TEXT NOT NULL DEFAULT ''",
		"root_error_message TEXT NOT NULL DEFAULT ''",
	} {
		if _, err := db.Exec("ALTER TABLE plan_roots ADD COLUMN " + decl); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
				return err
			}
		}
	}
	return nil
}

// migrateWorksetMemberIDs adds workset_members.member_id and backfills it
// once for pre-identity rows in member_index order. The backfill is
// idempotent: rows that already carry a member_id are never regenerated, so
// repeated opens, restarts, and member reorders keep identities stable. The
// unique index is created only after the backfill so legacy duplicate-free
// rows pass.
func migrateWorksetMemberIDs(db *sql.DB) error {
	if err := addColumn(db, "workset_members", "member_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	rows, err := db.Query(`
		SELECT workset_id, member_index FROM workset_members
		WHERE member_id = '' ORDER BY workset_id, member_index
	`)
	if err != nil {
		return fmt.Errorf("scan members without member_id: %w", err)
	}
	type key struct {
		worksetID string
		index     int
	}
	var pending []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.worksetID, &k.index); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, k)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if len(pending) > 0 {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, k := range pending {
			if _, err := tx.Exec(
				"UPDATE workset_members SET member_id = ? WHERE workset_id = ? AND member_index = ?",
				"m-"+newMigrationToken(), k.worksetID, k.index,
			); err != nil {
				return fmt.Errorf("backfill member_id: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit member_id backfill: %w", err)
		}
	}
	const memberIDIndex = `CREATE UNIQUE INDEX IF NOT EXISTS idx_workset_members_id ON workset_members(workset_id, member_id)`
	if _, err := db.Exec(memberIDIndex); err != nil {
		return fmt.Errorf("create member_id unique index: %w", err)
	}
	return nil
}

// addColumn adds a column when absent, tolerating the duplicate-column error
// on already-migrated databases.
func addColumn(db *sql.DB, table, column, decl string) error {
	if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl)); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return err
		}
	}
	return nil
}

// newMigrationToken mints one opaque identity token for a backfilled row.
func newMigrationToken() string {
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(rnd[:]))
}

// CanonicalJSONHash hashes JSON canonically: objects are recursively
// key-sorted, so key order and whitespace never change the hash; array order
// is preserved because step/member order is semantic. Unparseable input
// falls back to a raw-byte hash. Draft hashing and the draft-hash migration
// must use this same function so backfilled and freshly saved hashes agree.
func CanonicalJSONHash(b []byte) string {
	var v any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	canonical, err := json.Marshal(canonicalizeValue(v))
	if err != nil {
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// canonicalizeValue recursively normalizes for canonical hashing. Map
// iteration + encoding/json map marshaling yields sorted keys.
func canonicalizeValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = canonicalizeValue(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = canonicalizeValue(t[i])
		}
		return t
	default:
		return v
	}
}

// schemaTablesDDL is the full CREATE-TABLE schema (V1+). Kept out of
// initSchema so the function stays a thin executor.
const schemaTablesDDL = `
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

-- Persisted plans: the standalone plan-execute flow was retired; every row now
-- belongs to a workset revision. Legacy schemas without library_id are
-- upgraded by migratePlansLibrarySchema (the FK cannot be added via ALTER
-- TABLE). workset_id is set for workset-owned revision plans (FK semantics
-- are managed in app logic).
CREATE TABLE IF NOT EXISTS plans (
    plan_id TEXT PRIMARY KEY,
    root_path TEXT NOT NULL,
    scan_root_path TEXT NOT NULL DEFAULT '',
    library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
    plan_type TEXT NOT NULL,
    slim_mode TEXT,
    snapshot_token TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ready',
    plan_kind TEXT NOT NULL DEFAULT 'workflow',
    workflow_schema_version INTEGER NOT NULL DEFAULT 0,
    workset_id TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
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
-- library is deleted). title duplicates are allowed. version is the metadata
-- concurrency counter (rename only); drafts, generations and confirmations
-- advance their operation's own version (workset_operations.version).
-- creation_idem_key enables replay of workset creation for up to 30 days
-- (expired keys are cleared, never the row).
CREATE TABLE IF NOT EXISTS worksets (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
    root_path TEXT NOT NULL DEFAULT '',
    root_path_key TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
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

-- Independent Workset Operations (ADR 0004 §2), keyed by (workset, type).
-- version is the operation concurrency counter: draft saves and revision
-- publication advance it, and If-Match on draft/generation/confirmation
-- writes is bound to it. current_revision_id is never mutated by a failed,
-- canceled or interrupted generation.
CREATE TABLE IF NOT EXISTS workset_operations (
    workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
    operation_type TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    current_revision_id TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workset_id, operation_type)
);

-- One sparse operation draft per operation. draft_json holds the four common
-- setting groups plus sparse member overrides; an unoverridden unit is absent
-- rather than materialized from the common value. draft_hash is the canonical
-- content hash used for needs_planning derivation and generation dedup.
CREATE TABLE IF NOT EXISTS workset_operation_drafts (
    workset_id TEXT NOT NULL,
    operation_type TEXT NOT NULL,
    schema_version INTEGER NOT NULL DEFAULT 1,
    draft_json TEXT NOT NULL DEFAULT '{}',
    draft_hash TEXT NOT NULL DEFAULT '',
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (workset_id, operation_type),
    FOREIGN KEY (workset_id, operation_type)
        REFERENCES workset_operations(workset_id, operation_type) ON DELETE CASCADE
);

-- Immutable operation revisions. revision_index is monotonic within the
-- operation; draft_hash, excluded_scope and draft_snapshot are frozen at
-- generation time (audit + replay + frozen review). operation_version is
-- frozen for audit only (the version is never used as a mutation lock).
CREATE TABLE IF NOT EXISTS workset_operation_revisions (
    plan_id TEXT PRIMARY KEY REFERENCES plans(plan_id) ON DELETE CASCADE,
    workset_id TEXT NOT NULL,
    operation_type TEXT NOT NULL,
    revision_index INTEGER NOT NULL,
    draft_hash TEXT NOT NULL DEFAULT '',
    member_hash TEXT NOT NULL DEFAULT '',
    operation_version INTEGER NOT NULL DEFAULT 0,
    excluded_scope TEXT NOT NULL DEFAULT '',
    draft_snapshot TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (workset_id, operation_type, revision_index)
);

-- One confirmation per operation revision, bound to plan_id: a new revision
-- never inherits the previous confirmation.
CREATE TABLE IF NOT EXISTS workset_operation_confirmations (
    plan_id TEXT PRIMARY KEY REFERENCES plans(plan_id) ON DELETE CASCADE,
    workset_id TEXT NOT NULL,
    operation_type TEXT NOT NULL,
    confirmed_version INTEGER NOT NULL DEFAULT 0,
    confirmed_at TEXT DEFAULT CURRENT_TIMESTAMP
);

-- Async planning session. status: queued|running|completed|failed|canceled|
-- interrupted. idempotency_key is guaranteed for 30 days for completed rows;
-- failed/canceled/interrupted release the key immediately. cancel_requested is
-- observed by the worker at cooperative checkpoints. operation_type scopes the
-- session to one operation: idempotency, the single-active-session rule and
-- SSE/detail addressing all resolve within (workset_id, operation_type).
CREATE TABLE IF NOT EXISTS plan_generations (
    generation_id TEXT PRIMARY KEY,
    workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
    operation_type TEXT NOT NULL DEFAULT '',
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

-- The pre-operation index constrained idempotency keys per workset; drop it so
-- the operation-scoped index below is the only authority.
DROP INDEX IF EXISTS idx_plan_generations_idem;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_generations_op_idem
    ON plan_generations(workset_id, operation_type, idempotency_key)
    WHERE idempotency_key <> '' AND status IN ('queued','running','completed');
CREATE INDEX IF NOT EXISTS idx_plan_generations_op_status
    ON plan_generations(workset_id, operation_type, status, created_at);
CREATE INDEX IF NOT EXISTS idx_plan_generations_queue
    ON plan_generations(status, created_at, generation_id);

-- Workset execution sessions: the durable record of one confirmed revision
-- being executed (ADR 0004 §4, M2). status: queued|running|succeeded|failed|
-- canceled|interrupted. A revision is executed at most once: the unique
-- idempotency index holds the key for the session's whole life, and the
-- plan_id index answers "has this revision already been executed".
-- report_json is the per-component outcome report (pending entries included);
-- it is replaced on every component boundary so a crash leaves the facts of
-- everything that already happened on disk.
CREATE TABLE IF NOT EXISTS plan_executions (
    execution_id TEXT PRIMARY KEY,
    workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
    operation_type TEXT NOT NULL DEFAULT '',
    plan_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    delete_mode TEXT NOT NULL DEFAULT 'soft',
    idempotency_key TEXT NOT NULL DEFAULT '',
    request_hash TEXT NOT NULL DEFAULT '',
    expected_operation_version INTEGER NOT NULL DEFAULT 0,
    request_json TEXT NOT NULL DEFAULT '',
    total_components INTEGER NOT NULL DEFAULT 0,
    completed_components INTEGER NOT NULL DEFAULT 0,
    total_operations INTEGER NOT NULL DEFAULT 0,
    completed_operations INTEGER NOT NULL DEFAULT 0,
    current_root TEXT NOT NULL DEFAULT '',
    current_component_id TEXT NOT NULL DEFAULT '',
    current_component_index INTEGER NOT NULL DEFAULT 0,
    current_phase TEXT NOT NULL DEFAULT '',
    report_json TEXT NOT NULL DEFAULT '[]',
    cancel_requested INTEGER NOT NULL DEFAULT 0,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    started_at TEXT,
    finished_at TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_executions_op_idem
    ON plan_executions(workset_id, operation_type, idempotency_key)
    WHERE idempotency_key <> '';
-- One execution per revision is a storage invariant, not a check: two
-- concurrent starts would otherwise both pass the read-only eligibility gates.
DROP INDEX IF EXISTS idx_plan_executions_plan;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plan_executions_plan_unique ON plan_executions(plan_id);
CREATE INDEX IF NOT EXISTS idx_plan_executions_op_status
    ON plan_executions(workset_id, operation_type, status, created_at);
CREATE INDEX IF NOT EXISTS idx_plan_executions_queue
    ON plan_executions(status, created_at, execution_id);

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

// schemaIndexesDDL is the CREATE-INDEX DDL applied after the tables exist.
const schemaIndexesDDL = `
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
CREATE INDEX IF NOT EXISTS idx_plan_workflow_steps_plan ON plan_workflow_steps(plan_id);
CREATE INDEX IF NOT EXISTS idx_plan_roots_plan ON plan_roots(plan_id);
CREATE INDEX IF NOT EXISTS idx_plan_components_plan ON plan_components(plan_id);
`

// initSchema creates the full schema (tables, indexes, seed rows).
func initSchema(db *sql.DB) error {
	if _, err := db.Exec(schemaTablesDDL); err != nil {
		return err
	}
	if _, err := db.Exec(schemaIndexesDDL); err != nil {
		return err
	}
	// Fixed three policy slots exist from initialization (storage-level
	// cardinality invariant; there is no insert/delete API).
	_, err := db.Exec(`INSERT OR IGNORE INTO policy_slots (slot_index) VALUES (1), (2), (3)`)
	return err
}

func (r *Repository) Close() error {
	return r.db.Close()
}

// DB returns the underlying database connection.
func (r *Repository) DB() *sql.DB {
	return r.db
}

// EnsureDBPath creates the database file at given path.
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
