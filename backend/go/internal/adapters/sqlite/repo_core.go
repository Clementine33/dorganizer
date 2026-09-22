package sqlite

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite"
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
	PlanID            string
	RootPath          string
	ScanRootPath      string
	LibraryID         string // nullable: owning library when known
	SnapshotToken     string
	Status            string // ready, executed, stale, canceled, failed
	TaskKind          string // the Task that owns the payload
	TaskSchemaVersion int    // >0 once the payload schema is known
	CreatedAt         time.Time
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

// schemaVersion is the on-disk schema generation this build reads and writes.
// The marker row in schema_meta is what makes an older database recognisable:
// a database without it (or with another version) is refused at open time, and
// neither migrated nor cleared (ADR 0008 §3).
const schemaVersion = "3"

// ErrIncompatibleDatabase marks a database this build must not open: it was
// created by a different schema generation, so the operator has to point
// ONSEI_DATA_DIR at a new directory.
var ErrIncompatibleDatabase = errors.New("incompatible database")

// connectionPragmas is appended to the database path to configure every
// connection the driver opens. See NewRepository for why these are connection
// parameters and not one-shot PRAGMA statements.
//
// txlock=immediate starts every read-write transaction by taking the write
// lock. Deferred transactions take their read snapshot at the first statement
// and then upgrade it when they write; if another connection commits in
// between, that upgrade is refused (SQLITE_BUSY_SNAPSHOT) rather than applied -
// and SQLite only started refusing it in 3.51.3, where losing the race used to
// be silent. Every write path here reads before it writes (find the queued
// session, load the draft, then claim or persist), so the transaction has to
// own the write lock before it reads. Read-only transactions are unaffected:
// the driver leaves them deferred.
//
// auto_vacuum(2) is incremental auto-vacuum, which only takes effect before the
// first table exists: the connection that creates the file writes the mode into
// the header, and every connection after that reads it back from there. On a
// database that already has tables the setting is a silent no-op, which is why
// reclamation asks AutoVacuumMode what the file was created with instead of
// trying to enable it later.
const connectionPragmas = "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=auto_vacuum(2)&_txlock=immediate"

// NewRepository opens (creating it when absent) the repository database at
// dbPath and refuses one that belongs to another schema generation.
//
// dbPath is a filesystem path, or ":memory:", and never a DSN: the connection
// string is assembled here. The driver splits it at the first "?" regardless,
// so a path containing one was never openable; a caller that ever needs to pass
// its own query parameters must merge them with connectionPragmas instead of
// appending a second "?".
func NewRepository(dbPath string) (*Repository, error) {
	// foreign_keys and busy_timeout are connection-scoped: applied with a
	// one-shot PRAGMA statement they only reached whichever pooled connection
	// happened to run it, so cascades silently did nothing elsewhere and other
	// connections gave up on the first lock instead of waiting. The driver
	// applies DSN _pragma parameters to every connection it opens.
	// auto_vacuum needs the same treatment for a second reason: it is only
	// effective before the first table is created, so the connection that runs
	// initSchema has to carry it already.
	db, err := sql.Open("sqlite", dbPath+connectionPragmas)
	if err != nil {
		return nil, err
	}

	if err := checkSchemaCompatibility(db, dbPath); err != nil {
		db.Close()
		return nil, err
	}

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Repository{db: db}, nil
}

// checkSchemaCompatibility refuses to open a database that was not created by
// this schema generation. An empty file is a fresh database and passes; a file
// with tables but no marker, a missing marker row, or a different version is
// reported with the path and the way out (a new data directory), never by
// rewriting or emptying what is there.
func checkSchemaCompatibility(db *sql.DB, dbPath string) error {
	tables, err := userTables(db)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}
	if _, ok := tables["schema_meta"]; !ok {
		return fmt.Errorf(
			"%w: %s holds %d tables but no schema marker, so it was created by an older version; point ONSEI_DATA_DIR at a new directory instead (this build does not migrate and never clears existing data)",
			ErrIncompatibleDatabase,
			dbPath,
			len(tables),
		)
	}
	var version string
	if queryErr := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).
		Scan(&version); queryErr != nil {
		if errors.Is(queryErr, sql.ErrNoRows) {
			return fmt.Errorf(
				"%w: %s has no schema_version row; point ONSEI_DATA_DIR at a new directory instead",
				ErrIncompatibleDatabase,
				dbPath,
			)
		}
		return queryErr
	}
	if version != schemaVersion {
		return fmt.Errorf(
			"%w: %s is schema version %q and this build reads %q; point ONSEI_DATA_DIR at a new directory instead",
			ErrIncompatibleDatabase,
			dbPath,
			version,
			schemaVersion,
		)
	}
	return nil
}

// userTables lists the application tables of the database.
func userTables(db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
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

-- Generation credentials: what this app wrote at a path, and the facts that
-- bind the record to those bytes. A row exists only for an output that passed
-- the executor's stream and full-decode verification and was committed, so the
-- row's presence is the verification. A file converted outside this app has no
-- row at all: it is judged by its measured rate where that can judge it, and
-- otherwise it is unconfirmed — never assumed adequate.
CREATE TABLE IF NOT EXISTS generation_records (
    path TEXT PRIMARY KEY,
    codec TEXT NOT NULL,
    encoder TEXT NOT NULL,
    encoder_version TEXT NOT NULL,
    bitrate_kbps INTEGER NOT NULL,
    mode TEXT NOT NULL,
    size INTEGER NOT NULL,
    mtime INTEGER NOT NULL,
    content_sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL
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

-- On-disk schema marker. A database without it (or with another version) was
-- created by a different generation and is refused at open time rather than
-- migrated or reset (ADR 0008 §3).
CREATE TABLE IF NOT EXISTS schema_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

-- Persisted plans: one immutable snapshot of the current plan of one
-- processing record. Everything task-specific is opaque here: task_kind names
-- the Task that owns the payload and task_schema_version is that payload's
-- schema version. Only the current plan of a record is kept: publishing a new
-- one deletes the plan it replaced and that plan's execution rows in the same
-- transaction.
CREATE TABLE IF NOT EXISTS plans (
    plan_id TEXT PRIMARY KEY,
    root_path TEXT NOT NULL,
    scan_root_path TEXT NOT NULL DEFAULT '',
    library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
    snapshot_token TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ready',
    task_kind TEXT NOT NULL DEFAULT '',
    task_schema_version INTEGER NOT NULL DEFAULT 0,
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
INSERT OR IGNORE INTO policy_slots (slot_index) VALUES (1), (2), (3);

-- Task payload rows of one plan (owned by the task named in plans.task_kind;
-- the conversion task stores its resolved policy/classifier snapshots here).
-- classifier_pattern stores the task's canonical tag snapshot.
CREATE TABLE IF NOT EXISTS conversion_steps (
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
CREATE TABLE IF NOT EXISTS conversion_components (
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
CREATE UNIQUE INDEX IF NOT EXISTS idx_libraries_root_path_key ON libraries(root_path_key);

-- Worksets: the current processing record of one (library, operation). At
-- most one row per pair — the unique index below is the storage-level
-- guarantee, not an application check. library_id is only NULL for a record
-- whose library row vanished outside the delete path; the delete path removes
-- the record instead of orphaning it. title duplicates are allowed. version is
-- the metadata concurrency counter (rename only); drafts and generations
-- advance their operation's own version (workset_operations.version).
-- creation_idem_key enables replay of a creation request for up to 30 days;
-- creation_request_hash is the canonical hash of the request that owns the
-- key, so a replay with different content is a conflict, not a second record.
CREATE TABLE IF NOT EXISTS worksets (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    library_id TEXT REFERENCES libraries(id) ON DELETE SET NULL,
    operation_type TEXT NOT NULL DEFAULT '',
    root_path TEXT NOT NULL DEFAULT '',
    root_path_key TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    creation_idem_key TEXT,
    creation_request_hash TEXT NOT NULL DEFAULT '',
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_worksets_idem ON worksets(creation_idem_key) WHERE creation_idem_key IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_worksets_library_operation
    ON worksets(library_id, operation_type) WHERE library_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_worksets_library_updated ON worksets(library_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_worksets_updated ON worksets(updated_at);

-- Ordered member directories. rel_path (normalized library-relative path) is
-- the durable member identity — the workbench addresses a member by it, and a
-- rescan cannot change it. folder_path/folder_name are display snapshots of
-- the same directory, derived from the root and the relative path.
CREATE TABLE IF NOT EXISTS workset_members (
    workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
    member_id TEXT NOT NULL DEFAULT '',
    member_index INTEGER NOT NULL,
    rel_path TEXT NOT NULL,
    folder_path TEXT NOT NULL DEFAULT '',
    folder_name TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (workset_id, member_index),
    UNIQUE (workset_id, rel_path)
);

-- Independent Workset Operations (ADR 0001 §1), keyed by (workset, type).
-- version is the operation concurrency counter: draft saves and revision
-- publication advance it, and If-Match on draft/generation
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

-- Workset execution sessions: the durable record of one revision
-- being executed (ADR 0001 §2). status: queued|running|succeeded|failed|
-- canceled|interrupted. A revision is executed at most once: the unique
-- idempotency index holds the key for the session's whole life, and the
-- plan_id index answers "has this revision already been executed".
-- This row carries the session's own state only. What each component did is
-- in execution_component_results, committed as that component finishes, so a
-- crash leaves every finished component's facts on disk without the session
-- row ever holding a report that grows with the worklist.
CREATE TABLE IF NOT EXISTS plan_executions (
    execution_id TEXT PRIMARY KEY,
    workset_id TEXT NOT NULL REFERENCES worksets(id) ON DELETE CASCADE,
    operation_type TEXT NOT NULL DEFAULT '',
    plan_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
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

-- One component's observed result, written in the boundary transaction that
-- moves the session past it. The parts a component cannot observe for itself —
-- its id, root path, partition and operation count — stay in the session's
-- frozen request; the status and completed_operations columns exist so the
-- session's counters can be recomputed from the rows instead of incremented,
-- which is what makes a replayed boundary write idempotent. result_json holds
-- the result itself (stage, error, committed/removed/remaining/recovery,
-- inventory sync), and a component with no row is pending: nothing has run
-- for it yet.
CREATE TABLE IF NOT EXISTS execution_component_results (
    execution_id TEXT NOT NULL REFERENCES plan_executions(execution_id) ON DELETE CASCADE,
    component_index INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    completed_operations INTEGER NOT NULL DEFAULT 0,
    result_json TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (execution_id, component_index)
);

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
-- entries.path is the primary key, so its unique index already serves every
-- equality and range lookup; the duplicate on the same column only added a
-- second index to maintain on every inventory write.
DROP INDEX IF EXISTS idx_entries_path;
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
CREATE INDEX IF NOT EXISTS idx_conversion_steps_plan ON conversion_steps(plan_id);
CREATE INDEX IF NOT EXISTS idx_plan_roots_plan ON plan_roots(plan_id);
CREATE INDEX IF NOT EXISTS idx_conversion_components_plan ON conversion_components(plan_id);
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
	if _, err := db.Exec(`INSERT OR IGNORE INTO policy_slots (slot_index) VALUES (1), (2), (3)`); err != nil {
		return err
	}
	// The marker is written once, when the database is created. An existing
	// marker is never rewritten: a different version is refused before this
	// point, never upgraded in place.
	_, err := db.Exec(
		`INSERT OR IGNORE INTO schema_meta (key, value) VALUES ('schema_version', '` + schemaVersion + `')`,
	)
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
