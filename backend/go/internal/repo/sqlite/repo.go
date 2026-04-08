package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

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
	PlanType      string  // slim, prune, single_delete, single_convert
	SlimMode      *string // nullable: 1, 2, or nil
	SnapshotToken string
	Status        string // ready, executed, stale, canceled
	CreatedAt     time.Time
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

// ==================== Repository ====================

type Repository struct {
	db *sql.DB
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

	return &Repository{db: db}, nil
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

-- Persisted plans
CREATE TABLE IF NOT EXISTS plans (
    plan_id TEXT PRIMARY KEY,
    root_path TEXT NOT NULL,
    scan_root_path TEXT NOT NULL DEFAULT '',
    plan_type TEXT NOT NULL,
    slim_mode TEXT,
    snapshot_token TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ready',
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
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

-- Staging indexes
CREATE INDEX IF NOT EXISTS idx_staging_session ON entries_staging(session_id);
CREATE INDEX IF NOT EXISTS idx_entries_staging_status ON entries_staging(status);

-- Scan session indexes
CREATE INDEX IF NOT EXISTS idx_scan_sessions_root ON scan_sessions(root_path);
CREATE INDEX IF NOT EXISTS idx_scan_sessions_status ON scan_sessions(status);

-- Plan indexes
CREATE INDEX IF NOT EXISTS idx_plans_root ON plans(root_path);
CREATE INDEX IF NOT EXISTS idx_plans_status ON plans(status);
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

// Ensure DBPath creates the database file at given path
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

// ==================== Plan CRUD Methods ====================

// CreatePlan inserts a new plan
func (r *Repository) CreatePlan(p *Plan) error {
	var slimMode interface{}
	if p.SlimMode != nil {
		slimMode = *p.SlimMode
	}
	_, err := r.db.Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, p.PlanID, p.RootPath, p.ScanRootPath, p.PlanType, slimMode, p.SnapshotToken, p.Status, p.CreatedAt.Format(timeFormat))
	return err
}

// GetPlan retrieves a plan by ID
func (r *Repository) GetPlan(planID string) (*Plan, error) {
	var p Plan
	var createdAtStr string
	var slimMode sql.NullString
	err := r.db.QueryRow(`
		SELECT plan_id, root_path, scan_root_path, plan_type, slim_mode, snapshot_token, status, created_at
		FROM plans WHERE plan_id = ?
	`, planID).Scan(&p.PlanID, &p.RootPath, &p.ScanRootPath, &p.PlanType, &slimMode, &p.SnapshotToken, &p.Status, &createdAtStr)
	if err != nil {
		return nil, err
	}
	if slimMode.Valid {
		p.SlimMode = &slimMode.String
	}
	p.CreatedAt = parseTimestamp(createdAtStr)
	return &p, nil
}

// ListPlansByRoot returns all plans for a root
func (r *Repository) ListPlansByRoot(rootPath string) ([]*Plan, error) {
	rows, err := r.db.Query(`
		SELECT plan_id, root_path, scan_root_path, plan_type, slim_mode, snapshot_token, status, created_at
		FROM plans WHERE root_path = ? ORDER BY created_at DESC
	`, rootPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []*Plan
	for rows.Next() {
		var p Plan
		var createdAtStr string
		var slimMode sql.NullString
		if err := rows.Scan(&p.PlanID, &p.RootPath, &p.ScanRootPath, &p.PlanType, &slimMode, &p.SnapshotToken, &p.Status, &createdAtStr); err != nil {
			return nil, err
		}
		if slimMode.Valid {
			p.SlimMode = &slimMode.String
		}
		p.CreatedAt = parseTimestamp(createdAtStr)
		plans = append(plans, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return plans, nil
}

// UpdatePlanStatus updates a plan's status
func (r *Repository) UpdatePlanStatus(planID, status string) error {
	_, err := r.db.Exec("UPDATE plans SET status = ? WHERE plan_id = ?", status, planID)
	return err
}

// CreatePlanItem inserts a new plan item
func (r *Repository) CreatePlanItem(pi *PlanItem) error {
	var targetPath interface{}
	if pi.TargetPath != nil {
		targetPath = *pi.TargetPath
	}
	_, err := r.db.Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, pi.PlanID, pi.ItemIndex, pi.OpType, pi.SourcePath, targetPath, pi.ReasonCode, pi.PreconditionPath, pi.PreconditionContentRev, pi.PreconditionSize, pi.PreconditionMtime)
	return err
}

// Precond represents entry preconditions for batch loading
type Precond struct {
	ContentRev int
	Size       int64
	Mtime      int64
}

// LoadEntryPreconditionsBatchTx loads preconditions for multiple paths in a single transaction
// Uses chunked IN queries to avoid SQLite parameter limits (999 max)
func LoadEntryPreconditionsBatchTx(tx *sql.Tx, paths []string) (map[string]Precond, error) {
	result := make(map[string]Precond, len(paths))

	const chunkSize = 999 // SQLite max host parameters

	for start := 0; start < len(paths); start += chunkSize {
		end := start + chunkSize
		if end > len(paths) {
			end = len(paths)
		}
		chunk := paths[start:end]

		if len(chunk) == 0 {
			continue
		}

		// Build IN clause with placeholders
		placeholders := make([]string, len(chunk))
		args := make([]interface{}, len(chunk))
		for i, path := range chunk {
			placeholders[i] = "?"
			args[i] = path
		}

		query := "SELECT path, COALESCE(content_rev, 0), COALESCE(size, 0), COALESCE(mtime, 0) FROM entries WHERE path IN (" +
			strings.Join(placeholders, ",") +
			")"

		rows, err := tx.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("batch precondition query failed: %w", err)
		}

		for rows.Next() {
			var path string
			var p Precond
			if err := rows.Scan(&path, &p.ContentRev, &p.Size, &p.Mtime); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan batch precondition failed: %w", err)
			}
			result[path] = p
		}

		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("batch precondition rows error: %w", err)
		}
		rows.Close()
	}

	// Fill in zero values for paths not found in entries table
	for _, path := range paths {
		if _, ok := result[path]; !ok {
			result[path] = Precond{ContentRev: 0, Size: 0, Mtime: 0}
		}
	}

	return result, nil
}

// CreatePlanTx inserts a new plan within an existing transaction
func CreatePlanTx(tx *sql.Tx, p *Plan) error {
	var slimMode interface{}
	if p.SlimMode != nil {
		slimMode = *p.SlimMode
	}
	_, err := tx.Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, p.PlanID, p.RootPath, p.ScanRootPath, p.PlanType, slimMode, p.SnapshotToken, p.Status, p.CreatedAt.Format(timeFormat))
	return err
}

// IsPlanIDConflictError checks if an error is a plan ID conflict error
func IsPlanIDConflictError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	// SQLite constraint violation for PRIMARY KEY
	return strings.Contains(errStr, "constraint") &&
		(strings.Contains(errStr, "primary key") || strings.Contains(errStr, "unique"))
}

// CreatePlanItemsBatchTx inserts multiple plan items within a single transaction
// Uses chunked inserts with prepared statements for efficiency
func CreatePlanItemsBatchTx(tx *sql.Tx, planID string, items []PlanItem) error {
	if len(items) == 0 {
		return nil
	}

	const chunkSize = 500 // Balance between performance and parameter limits

	for start := 0; start < len(items); start += chunkSize {
		end := start + chunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[start:end]

		if len(chunk) == 0 {
			continue
		}

		// Use a single prepared statement for this chunk
		stmt, err := tx.Prepare(`
			INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("prepare batch insert statement failed: %w", err)
		}

		for _, item := range chunk {
			var targetPath interface{}
			if item.TargetPath != nil {
				targetPath = *item.TargetPath
			}

			_, err := stmt.Exec(
				planID,
				item.ItemIndex,
				item.OpType,
				item.SourcePath,
				targetPath,
				item.ReasonCode,
				item.PreconditionPath,
				item.PreconditionContentRev,
				item.PreconditionSize,
				item.PreconditionMtime,
			)
			if err != nil {
				stmt.Close()
				return fmt.Errorf("batch insert plan item failed: %w", err)
			}
		}

		if err := stmt.Close(); err != nil {
			return fmt.Errorf("close batch insert statement failed: %w", err)
		}
	}

	return nil
}

// ListPlanItems returns all items for a plan
func (r *Repository) ListPlanItems(planID string) ([]*PlanItem, error) {
	rows, err := r.db.Query(`
		SELECT plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime
		FROM plan_items WHERE plan_id = ? ORDER BY item_index
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*PlanItem
	for rows.Next() {
		var pi PlanItem
		var targetPath sql.NullString
		if err := rows.Scan(&pi.PlanID, &pi.ItemIndex, &pi.OpType, &pi.SourcePath, &targetPath, &pi.ReasonCode, &pi.PreconditionPath, &pi.PreconditionContentRev, &pi.PreconditionSize, &pi.PreconditionMtime); err != nil {
			return nil, err
		}
		if targetPath.Valid {
			pi.TargetPath = &targetPath.String
		}
		items = append(items, &pi)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// ==================== Scan Session Methods ====================

// CreateScanSession creates a new scan session
func (r *Repository) CreateScanSession(s *ScanSession) error {
	var scopePath interface{}
	if s.ScopePath != nil {
		scopePath = *s.ScopePath
	}
	_, err := r.db.Exec(`
		INSERT INTO scan_sessions (session_id, root_path, scope_path, kind, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, s.SessionID, s.RootPath, scopePath, s.Kind, s.Status, s.StartedAt.Format(timeFormat))
	return err
}

// GetScanSession retrieves a scan session by ID
func (r *Repository) GetScanSession(sessionID string) (*ScanSession, error) {
	var s ScanSession
	var startedAtStr string
	var finishedAtStr, errorCode, errorMessage, scopePath sql.NullString
	err := r.db.QueryRow(`
		SELECT session_id, root_path, scope_path, kind, status, error_code, error_message, started_at, finished_at
		FROM scan_sessions WHERE session_id = ?
	`, sessionID).Scan(&s.SessionID, &s.RootPath, &scopePath, &s.Kind, &s.Status, &errorCode, &errorMessage, &startedAtStr, &finishedAtStr)
	if err != nil {
		return nil, err
	}
	if scopePath.Valid {
		s.ScopePath = &scopePath.String
	}
	s.ErrorCode = errorCode.String
	s.ErrorMessage = errorMessage.String
	s.StartedAt = parseTimestamp(startedAtStr)
	if finishedAtStr.Valid && finishedAtStr.String != "" {
		s.FinishedAt = parseTimestamp(finishedAtStr.String)
	}
	return &s, nil
}

// UpdateScanSessionStatus updates scan session status
func (r *Repository) UpdateScanSessionStatus(sessionID, status, errorCode, errorMessage string) error {
	finishedAt := "NULL"
	if status == "completed" || status == "failed" || status == "canceled" {
		finishedAt = "datetime('now')"
	}
	if finishedAt == "NULL" {
		_, err := r.db.Exec(`
			UPDATE scan_sessions SET status = ?, error_code = ?, error_message = ? WHERE session_id = ?
		`, status, errorCode, errorMessage, sessionID)
		return err
	}
	_, err := r.db.Exec(`
		UPDATE scan_sessions SET status = ?, error_code = ?, error_message = ?, finished_at = datetime('now') WHERE session_id = ?
	`, status, errorCode, errorMessage, sessionID)
	return err
}

// ListScanSessionsByRoot returns scan sessions for a root
func (r *Repository) ListScanSessionsByRoot(rootPath string) ([]*ScanSession, error) {
	rows, err := r.db.Query(`
		SELECT session_id, root_path, scope_path, kind, status, started_at, finished_at
		FROM scan_sessions WHERE root_path = ? ORDER BY started_at DESC
	`, rootPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*ScanSession
	for rows.Next() {
		var s ScanSession
		var startedAtStr string
		var finishedAtStr, scopePath sql.NullString
		if err := rows.Scan(&s.SessionID, &s.RootPath, &scopePath, &s.Kind, &s.Status, &startedAtStr, &finishedAtStr); err != nil {
			return nil, err
		}
		if scopePath.Valid {
			s.ScopePath = &scopePath.String
		}
		s.StartedAt = parseTimestamp(startedAtStr)
		if finishedAtStr.Valid && finishedAtStr.String != "" {
			s.FinishedAt = parseTimestamp(finishedAtStr.String)
		}
		sessions = append(sessions, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

// ==================== Execute Session Methods ====================

// CreateExecuteSession creates a new execute session
func (r *Repository) CreateExecuteSession(e *ExecuteSession) error {
	_, err := r.db.Exec(`
		INSERT INTO execute_sessions (session_id, plan_id, root_path, status, started_at)
		VALUES (?, ?, ?, ?, ?)
	`, e.SessionID, e.PlanID, e.RootPath, e.Status, e.StartedAt.Format(timeFormat))
	return err
}

// GetExecuteSession retrieves an execute session by ID
func (r *Repository) GetExecuteSession(sessionID string) (*ExecuteSession, error) {
	var e ExecuteSession
	var startedAtStr string
	var finishedAtStr, errorCode, errorMessage sql.NullString
	err := r.db.QueryRow(`
		SELECT session_id, plan_id, root_path, status, started_at, finished_at, error_code, error_message
		FROM execute_sessions WHERE session_id = ?
	`, sessionID).Scan(&e.SessionID, &e.PlanID, &e.RootPath, &e.Status, &startedAtStr, &finishedAtStr, &errorCode, &errorMessage)
	if err != nil {
		return nil, err
	}
	e.ErrorCode = errorCode.String
	e.ErrorMessage = errorMessage.String
	e.StartedAt = parseTimestamp(startedAtStr)
	if finishedAtStr.Valid && finishedAtStr.String != "" {
		e.FinishedAt = parseTimestamp(finishedAtStr.String)
	}
	return &e, nil
}

// UpdateExecuteSessionStatus updates execute session status
func (r *Repository) UpdateExecuteSessionStatus(sessionID, status, errorCode, errorMessage string) error {
	_, err := r.db.Exec(`
		UPDATE execute_sessions SET status = ?, error_code = ?, error_message = ?, finished_at = datetime('now') WHERE session_id = ?
	`, status, errorCode, errorMessage, sessionID)
	return err
}

// ListExecuteSessionsByPlan returns execute sessions for a plan
func (r *Repository) ListExecuteSessionsByPlan(planID string) ([]*ExecuteSession, error) {
	rows, err := r.db.Query(`
		SELECT session_id, plan_id, root_path, status, started_at, finished_at
		FROM execute_sessions WHERE plan_id = ? ORDER BY started_at DESC
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*ExecuteSession
	for rows.Next() {
		var e ExecuteSession
		var startedAtStr string
		var finishedAtStr sql.NullString
		if err := rows.Scan(&e.SessionID, &e.PlanID, &e.RootPath, &e.Status, &startedAtStr, &finishedAtStr); err != nil {
			return nil, err
		}
		e.StartedAt = parseTimestamp(startedAtStr)
		if finishedAtStr.Valid && finishedAtStr.String != "" {
			e.FinishedAt = parseTimestamp(finishedAtStr.String)
		}
		sessions = append(sessions, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

// ==================== Error Event Methods ====================

// CreateErrorEvent logs an error event
func (r *Repository) CreateErrorEvent(e *ErrorEvent) error {
	retryable := 0
	if e.Retryable {
		retryable = 1
	}
	var path interface{}
	if e.Path != nil {
		path = *e.Path
	}
	result, err := r.db.Exec(`
		INSERT INTO error_events (scope, root_path, path, code, message, retryable)
		VALUES (?, ?, ?, ?, ?, ?)
	`, e.Scope, e.RootPath, path, e.Code, e.Message, retryable)
	if err != nil {
		return err
	}
	id, _ := result.LastInsertId()
	e.ID = id
	return nil
}

// ListErrorEventsByRoot returns error events for a root
func (r *Repository) ListErrorEventsByRoot(rootPath string) ([]*ErrorEvent, error) {
	rows, err := r.db.Query(`
		SELECT id, scope, root_path, path, code, message, retryable, created_at
		FROM error_events WHERE root_path = ? ORDER BY id DESC
	`, rootPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*ErrorEvent
	for rows.Next() {
		var e ErrorEvent
		var retryable int
		var createdAtStr string
		var path sql.NullString
		if err := rows.Scan(&e.ID, &e.Scope, &e.RootPath, &path, &e.Code, &e.Message, &retryable, &createdAtStr); err != nil {
			return nil, err
		}
		if path.Valid {
			e.Path = &path.String
		}
		e.Retryable = retryable == 1
		e.CreatedAt = parseTimestamp(createdAtStr)
		events = append(events, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}
