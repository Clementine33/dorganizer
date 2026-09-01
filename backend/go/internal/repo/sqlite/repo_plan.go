package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ==================== Plan CRUD Methods ====================

// ErrPlanNotFound is returned when a plan cannot be found.
var ErrPlanNotFound = errors.New("plan not found")

const planColumns = `plan_id, root_path, scan_root_path, library_id, plan_type, slim_mode, snapshot_token, status, plan_kind, workflow_schema_version, created_at`

// scanPlan scans one plan row (ordered per planColumns) into p.
func scanPlan(
	p *Plan,
	createdAtStr string,
	libraryID, slimMode sql.NullString,
	planKind string,
	workflowSchemaVersion int,
) {
	if libraryID.Valid {
		p.LibraryID = libraryID.String
	}
	if slimMode.Valid {
		p.SlimMode = &slimMode.String
	}
	p.PlanKind = planKind
	p.WorkflowSchemaVersion = workflowSchemaVersion
	p.CreatedAt = parseTimestamp(createdAtStr)
}

// CreatePlan inserts a new plan.
func (r *Repository) CreatePlan(p *Plan) error {
	var slimMode any
	if p.SlimMode != nil {
		slimMode = *p.SlimMode
	}
	var libraryID any
	if p.LibraryID != "" {
		libraryID = p.LibraryID
	}
	planKind := p.PlanKind
	if planKind == "" {
		planKind = "single_action"
	}
	_, err := r.db.Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, library_id, plan_type, slim_mode, snapshot_token, status, plan_kind, workflow_schema_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.PlanID, p.RootPath, p.ScanRootPath, libraryID, p.PlanType, slimMode, p.SnapshotToken, p.Status, planKind, p.WorkflowSchemaVersion, p.CreatedAt.Format(timeFormat))
	return err
}

// GetPlan retrieves a plan by ID.
func (r *Repository) GetPlan(planID string) (*Plan, error) {
	var p Plan
	var createdAtStr string
	var slimMode, libraryID sql.NullString
	var planKind string
	var workflowSchemaVersion int
	err := r.db.QueryRow(`
		SELECT `+planColumns+`
		FROM plans WHERE plan_id = ?
	`, planID).Scan(&p.PlanID, &p.RootPath, &p.ScanRootPath, &libraryID, &p.PlanType, &slimMode, &p.SnapshotToken, &p.Status, &planKind, &workflowSchemaVersion, &createdAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	scanPlan(&p, createdAtStr, libraryID, slimMode, planKind, workflowSchemaVersion)
	return &p, nil
}

// planOrderSQL orders by the absolute time of created_at. created_at is stored
// as RFC3339Nano with the writer's local offset, so a lexicographic string
// sort would misorder rows written under different UTC offsets; julianday()
// parses the ISO-8601 timestamps into a comparable instant.
const planOrderSQL = ` ORDER BY julianday(created_at) DESC, plan_id DESC`

// scanPlanRows scans one plan-rows result set into plans.
func scanPlanRows(rows *sql.Rows) ([]*Plan, error) {
	defer rows.Close()
	var plans []*Plan
	for rows.Next() {
		var p Plan
		var createdAtStr string
		var slimMode, libraryID sql.NullString
		var planKind string
		var workflowSchemaVersion int
		if err := rows.Scan(
			&p.PlanID,
			&p.RootPath,
			&p.ScanRootPath,
			&libraryID,
			&p.PlanType,
			&slimMode,
			&p.SnapshotToken,
			&p.Status,
			&planKind,
			&workflowSchemaVersion,
			&createdAtStr,
		); err != nil {
			return nil, err
		}
		scanPlan(&p, createdAtStr, libraryID, slimMode, planKind, workflowSchemaVersion)
		plans = append(plans, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return plans, nil
}

// ListPlansByRoot returns all plans for a root.
func (r *Repository) ListPlansByRoot(rootPath string) ([]*Plan, error) {
	rows, err := r.db.Query(`
		SELECT `+planColumns+`
		FROM plans WHERE root_path = ?`+planOrderSQL, rootPath)
	if err != nil {
		return nil, err
	}
	return scanPlanRows(rows)
}

// ListPlans returns standalone (non-workset) plans newest-first in a single
// SQL query. Workset-owned revision plans are excluded: they are discovered
// through the nested workset revision endpoints, and the legacy plan list must
// not silently duplicate them. When libraryID is non-nil only plans owned by
// that library are returned; otherwise all standalone plans (including legacy
// plans without ownership) are listed. Ordering and limiting happen in SQL.
func (r *Repository) ListPlans(libraryID *string, limit int) ([]*Plan, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT ` + planColumns + ` FROM plans WHERE workset_id = ''`
	var args []any
	if libraryID != nil {
		query += ` AND library_id = ?`
		args = append(args, *libraryID)
	}
	query += planOrderSQL + ` LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanPlanRows(rows)
}

// PlanDetail is a plan plus everything needed to rebuild the review page
// without touching retention-managed error_events.
type PlanDetail struct {
	Plan              Plan
	Items             []PlanItem
	FolderErrors      []PlanFolderError
	SuccessfulFolders []string
}

// GetPlanDetail returns a plan with its items, folder errors, and successful
// folders, each ordered deterministically.
func (r *Repository) GetPlanDetail(planID string) (*PlanDetail, error) {
	plan, err := r.GetPlan(planID)
	if err != nil {
		return nil, err
	}

	detail := &PlanDetail{Plan: *plan}

	itemRows, err := r.db.Query(`
		SELECT plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime
		FROM plan_items WHERE plan_id = ? ORDER BY item_index
	`, planID)
	if err != nil {
		return nil, err
	}
	for itemRows.Next() {
		var pi PlanItem
		var targetPath sql.NullString
		if err := itemRows.Scan(
			&pi.PlanID,
			&pi.ItemIndex,
			&pi.OpType,
			&pi.SourcePath,
			&targetPath,
			&pi.ReasonCode,
			&pi.PreconditionPath,
			&pi.PreconditionContentRev,
			&pi.PreconditionSize,
			&pi.PreconditionMtime,
		); err != nil {
			itemRows.Close()
			return nil, err
		}
		if targetPath.Valid {
			pi.TargetPath = &targetPath.String
		}
		detail.Items = append(detail.Items, pi)
	}
	itemRows.Close()
	if err := itemRows.Err(); err != nil {
		return nil, err
	}

	errRows, err := r.db.Query(`
		SELECT plan_id, error_index, folder_path, code, message, retryable
		FROM plan_errors WHERE plan_id = ? ORDER BY error_index
	`, planID)
	if err != nil {
		return nil, err
	}
	for errRows.Next() {
		var pe PlanFolderError
		var retryable int
		if err := errRows.Scan(
			&pe.PlanID,
			&pe.ErrorIndex,
			&pe.FolderPath,
			&pe.Code,
			&pe.Message,
			&retryable,
		); err != nil {
			errRows.Close()
			return nil, err
		}
		pe.Retryable = retryable == 1
		detail.FolderErrors = append(detail.FolderErrors, pe)
	}
	errRows.Close()
	if err := errRows.Err(); err != nil {
		return nil, err
	}

	folderRows, err := r.db.Query(`
		SELECT folder_path FROM plan_successful_folders WHERE plan_id = ? ORDER BY folder_index
	`, planID)
	if err != nil {
		return nil, err
	}
	for folderRows.Next() {
		var p string
		if err := folderRows.Scan(&p); err != nil {
			folderRows.Close()
			return nil, err
		}
		detail.SuccessfulFolders = append(detail.SuccessfulFolders, p)
	}
	folderRows.Close()
	if err := folderRows.Err(); err != nil {
		return nil, err
	}

	return detail, nil
}

// UpdatePlanStatus updates a plan's status.
func (r *Repository) UpdatePlanStatus(planID, status string) error {
	_, err := r.db.Exec("UPDATE plans SET status = ? WHERE plan_id = ?", status, planID)
	return err
}

// CreatePlanItem inserts a new plan item.
func (r *Repository) CreatePlanItem(pi *PlanItem) error {
	var targetPath any
	if pi.TargetPath != nil {
		targetPath = *pi.TargetPath
	}
	_, err := r.db.Exec(`
		INSERT INTO plan_items (plan_id, item_index, op_type, source_path, target_path, reason_code, precondition_path, precondition_content_rev, precondition_size, precondition_mtime)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, pi.PlanID, pi.ItemIndex, pi.OpType, pi.SourcePath, targetPath, pi.ReasonCode, pi.PreconditionPath, pi.PreconditionContentRev, pi.PreconditionSize, pi.PreconditionMtime)
	return err
}

// Precond represents entry preconditions for batch loading.
type Precond struct {
	ContentRev int
	Size       int64
	Mtime      int64
}

// LoadEntryPreconditionsBatchTx loads preconditions for multiple paths in a single transaction
// Uses chunked IN queries to avoid SQLite parameter limits (999 max).
func LoadEntryPreconditionsBatchTx(tx *sql.Tx, paths []string) (map[string]Precond, error) {
	result := make(map[string]Precond, len(paths))

	const chunkSize = 999 // SQLite max host parameters

	for start := 0; start < len(paths); start += chunkSize {
		end := min(start+chunkSize, len(paths))
		chunk := paths[start:end]

		if len(chunk) == 0 {
			continue
		}

		// Build IN clause with placeholders
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, path := range chunk {
			placeholders[i] = "?"
			args[i] = path
		}

		query := "SELECT path, COALESCE(content_rev, 0), COALESCE(size, 0), COALESCE(mtime, 0) FROM entries WHERE path IN (" +
			strings.Join(
				placeholders,
				",",
			) +
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

// CreatePlanTx inserts a new plan within an existing transaction.
func CreatePlanTx(tx *sql.Tx, p *Plan) error {
	var slimMode any
	if p.SlimMode != nil {
		slimMode = *p.SlimMode
	}
	var libraryID any
	if p.LibraryID != "" {
		libraryID = p.LibraryID
	}
	_, err := tx.Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, library_id, plan_type, slim_mode, snapshot_token, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.PlanID, p.RootPath, p.ScanRootPath, libraryID, p.PlanType, slimMode, p.SnapshotToken, p.Status, p.CreatedAt.Format(timeFormat))
	return err
}

// CreatePlanFolderErrorsBatchTx persists folder-scoped plan errors atomically
// with the rest of the plan.
func CreatePlanFolderErrorsBatchTx(tx *sql.Tx, planID string, errs []PlanFolderError) error {
	if len(errs) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`
		INSERT INTO plan_errors (plan_id, error_index, folder_path, code, message, retryable)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare plan error insert: %w", err)
	}
	defer stmt.Close()
	for _, e := range errs {
		retryable := 0
		if e.Retryable {
			retryable = 1
		}
		if _, err := stmt.Exec(planID, e.ErrorIndex, e.FolderPath, e.Code, e.Message, retryable); err != nil {
			return fmt.Errorf("insert plan error %q: %w", e.FolderPath, err)
		}
	}
	return nil
}

// CreatePlanSuccessfulFoldersBatchTx persists the folders that analyzed
// cleanly for a plan.
func CreatePlanSuccessfulFoldersBatchTx(tx *sql.Tx, planID string, folders []string) error {
	if len(folders) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`
		INSERT INTO plan_successful_folders (plan_id, folder_index, folder_path)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare successful folder insert: %w", err)
	}
	defer stmt.Close()
	for i, f := range folders {
		if _, err := stmt.Exec(planID, i, f); err != nil {
			return fmt.Errorf("insert successful folder %q: %w", f, err)
		}
	}
	return nil
}

// IsPlanIDConflictError checks if an error is a plan ID conflict error.
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
// Uses chunked inserts with prepared statements for efficiency.
func CreatePlanItemsBatchTx(tx *sql.Tx, planID string, items []PlanItem) error {
	if len(items) == 0 {
		return nil
	}

	const chunkSize = 500 // Balance between performance and parameter limits

	for start := 0; start < len(items); start += chunkSize {
		end := min(start+chunkSize, len(items))
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
			var targetPath any
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

// ListPlanItems returns all items for a plan.
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
		if err := rows.Scan(
			&pi.PlanID,
			&pi.ItemIndex,
			&pi.OpType,
			&pi.SourcePath,
			&targetPath,
			&pi.ReasonCode,
			&pi.PreconditionPath,
			&pi.PreconditionContentRev,
			&pi.PreconditionSize,
			&pi.PreconditionMtime,
		); err != nil {
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
