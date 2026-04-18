package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
)

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
