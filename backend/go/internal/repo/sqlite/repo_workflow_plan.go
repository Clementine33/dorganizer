package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ==================== Classifiers ====================

// ClassifierRow is one immutable named/versioned classifier definition.
type ClassifierRow struct {
	Name      string
	Version   int
	Kind      string
	Pattern   string
	Hash      string
	CreatedAt time.Time
}

// EnsureClassifier inserts a classifier definition when the version does not
// already exist. Existing versions are never mutated.
func (r *Repository) EnsureClassifier(row ClassifierRow) error {
	_, err := r.db.Exec(`
		INSERT OR IGNORE INTO classifiers (name, version, kind, pattern, hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, row.Name, row.Version, row.Kind, row.Pattern, row.Hash, row.CreatedAt.Format(timeFormat))
	return err
}

// LoadClassifier fetches an immutable classifier definition.
func (r *Repository) LoadClassifier(name string, version int) (*ClassifierRow, error) {
	var row ClassifierRow
	var createdAt string
	err := r.db.QueryRow(`
		SELECT name, version, kind, pattern, hash, created_at
		FROM classifiers WHERE name = ? AND version = ?
	`, name, version).Scan(&row.Name, &row.Version, &row.Kind, &row.Pattern, &row.Hash, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	row.CreatedAt = parseTimestamp(createdAt)
	return &row, nil
}

// ==================== Workflow plan persistence ====================

// WorkflowStepRecord is a persisted resolved workflow step snapshot.
type WorkflowStepRecord struct {
	StepIndex           int
	StepType            string
	Status              string
	PolicySourceKind    string
	PolicySourceName    string
	PolicySourceVersion int
	PolicySchemaVersion int
	PolicyJSON          string
	PolicyHash          string
	ClassifierName      string
	ClassifierVersion   int
	ClassifierPattern   string
	ClassifierHash      string
	StepSummaryJSON     string
}

// WorkflowRootRecord is a persisted planning root with its inventory
// fingerprint. RootStatus is "ok" for planned roots and "missing" for member
// folders whose subtree no longer exists; RootErrorCode/Message carry the
// stable machine outcome for missing roots (SOURCE_MISSING).
type WorkflowRootRecord struct {
	RootIndex            int
	RootPath             string
	RootIdentity         string
	InventoryFingerprint string
	EntryCount           int
	RootStatus           string
	RootErrorCode        string
	RootErrorMessage     string
}

// WorkflowComponentRecord is a persisted component outcome snapshot.
type WorkflowComponentRecord struct {
	StepIndex      int
	ComponentIndex int
	ComponentID    string
	RootIndex      int
	Partition      string
	Status         string
	ReasonCode     string
	OutcomeJSON    string
}

// WorkflowPlanDetail is the full persisted review payload for a workflow plan.
type WorkflowPlanDetail struct {
	Plan       Plan
	Steps      []WorkflowStepRecord
	Roots      []WorkflowRootRecord
	Components []WorkflowComponentRecord
}

const workflowPlanColumns = `plan_id, root_path, scan_root_path, library_id, plan_type, slim_mode, snapshot_token, status, plan_kind, workflow_schema_version, created_at`

// CreateWorkflowPlanTx persists a workflow plan and all of its step, root and
// component snapshots in one transaction so a partial plan is never visible.
func CreateWorkflowPlanTx(
	db *sql.DB,
	planID string,
	planType string,
	rootPath string,
	snapshotToken string,
	libraryID string,
	steps []WorkflowStepRecord,
	roots []WorkflowRootRecord,
	components []WorkflowComponentRecord,
) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin workflow plan tx: %w", err)
	}
	defer tx.Rollback()

	if err := InsertWorkflowPlanTx(tx, planID, planType, rootPath, snapshotToken, libraryID, steps, roots, components); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workflow plan tx: %w", err)
	}
	return nil
}

// InsertWorkflowPlanTx is the transaction-scoped form of
// CreateWorkflowPlanTx for callers that must persist the plan snapshot inside
// a larger atomic transaction (the workset generation completion path writes
// the plan, its revision association, and the current-revision promotion in
// one commit).
func InsertWorkflowPlanTx(
	tx *sql.Tx,
	planID string,
	planType string,
	rootPath string,
	snapshotToken string,
	libraryID string,
	steps []WorkflowStepRecord,
	roots []WorkflowRootRecord,
	components []WorkflowComponentRecord,
) error {
	var libID any
	if libraryID != "" {
		libID = libraryID
	}
	if _, err := tx.Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, library_id, plan_type, slim_mode, snapshot_token, status, plan_kind, workflow_schema_version, created_at)
		VALUES (?, ?, ?, ?, ?, NULL, ?, 'ready', 'workflow', 1, ?)
	`, planID, rootPath, rootPath, libID, planType, snapshotToken, time.Now().Format(timeFormat)); err != nil {
		return fmt.Errorf("insert workflow plan: %w", err)
	}

	for _, s := range steps {
		if _, err := tx.Exec(`
			INSERT INTO plan_workflow_steps
			(plan_id, step_index, step_type, status, policy_source_kind, policy_source_name, policy_source_version,
			 policy_schema_version, policy_json, policy_hash, classifier_name, classifier_version,
			 classifier_pattern, classifier_hash, step_summary_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, planID, s.StepIndex, s.StepType, s.Status, s.PolicySourceKind, s.PolicySourceName, s.PolicySourceVersion,
			s.PolicySchemaVersion, s.PolicyJSON, s.PolicyHash, s.ClassifierName, s.ClassifierVersion,
			s.ClassifierPattern, s.ClassifierHash, s.StepSummaryJSON); err != nil {
			return fmt.Errorf("insert workflow step: %w", err)
		}
	}

	for _, r := range roots {
		rootStatus := r.RootStatus
		if rootStatus == "" {
			rootStatus = "ok"
		}
		if _, err := tx.Exec(`
			INSERT INTO plan_roots (plan_id, root_index, root_path, root_identity, inventory_fingerprint, entry_count, root_status, root_error_code, root_error_message)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, planID, r.RootIndex, r.RootPath, r.RootIdentity, r.InventoryFingerprint, r.EntryCount, rootStatus, r.RootErrorCode, r.RootErrorMessage); err != nil {
			return fmt.Errorf("insert workflow root: %w", err)
		}
	}

	for _, c := range components {
		if _, err := tx.Exec(`
			INSERT INTO plan_components
			(plan_id, step_index, component_index, component_id, root_index, partition, status, reason_code, outcome_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, planID, c.StepIndex, c.ComponentIndex, c.ComponentID, c.RootIndex, c.Partition, c.Status, c.ReasonCode, c.OutcomeJSON); err != nil {
			return fmt.Errorf("insert workflow component: %w", err)
		}
	}

	return nil
}

// scanWorkflowPlanRow scans one plan row (workflowPlanColumns order).
func scanWorkflowPlanRow(p *Plan, createdAtStr string, libraryID, slimMode sql.NullString, planKind string, workflowSchemaVersion int) {
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

// GetWorkflowPlanDetail reconstructs a workflow plan review from persisted
// snapshots without consulting live policy/classifier state.
func (r *Repository) GetWorkflowPlanDetail(planID string) (*WorkflowPlanDetail, error) {
	var p Plan
	var createdAt string
	var slimMode, libraryID sql.NullString
	var planKind string
	var workflowSchemaVersion int
	err := r.db.QueryRow(`
		SELECT `+workflowPlanColumns+`
		FROM plans WHERE plan_id = ?
	`, planID).Scan(&p.PlanID, &p.RootPath, &p.ScanRootPath, &libraryID, &p.PlanType, &slimMode, &p.SnapshotToken, &p.Status, &planKind, &workflowSchemaVersion, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	scanWorkflowPlanRow(&p, createdAt, libraryID, slimMode, planKind, workflowSchemaVersion)

	detail := &WorkflowPlanDetail{Plan: p}

	stepRows, err := r.db.Query(`
		SELECT step_index, step_type, status, policy_source_kind, policy_source_name, policy_source_version,
		       policy_schema_version, policy_json, policy_hash, classifier_name, classifier_version,
		       classifier_pattern, classifier_hash, step_summary_json
		FROM plan_workflow_steps WHERE plan_id = ? ORDER BY step_index
	`, planID)
	if err != nil {
		return nil, err
	}
	defer stepRows.Close()
	for stepRows.Next() {
		var s WorkflowStepRecord
		if err := stepRows.Scan(&s.StepIndex, &s.StepType, &s.Status, &s.PolicySourceKind, &s.PolicySourceName, &s.PolicySourceVersion,
			&s.PolicySchemaVersion, &s.PolicyJSON, &s.PolicyHash, &s.ClassifierName, &s.ClassifierVersion,
			&s.ClassifierPattern, &s.ClassifierHash, &s.StepSummaryJSON); err != nil {
			return nil, err
		}
		detail.Steps = append(detail.Steps, s)
	}
	if err := stepRows.Err(); err != nil {
		return nil, err
	}

	rootRows, err := r.db.Query(`
		SELECT root_index, root_path, root_identity, inventory_fingerprint, entry_count, root_status, root_error_code, root_error_message
		FROM plan_roots WHERE plan_id = ? ORDER BY root_index
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rootRows.Close()
	for rootRows.Next() {
		var r WorkflowRootRecord
		if err := rootRows.Scan(&r.RootIndex, &r.RootPath, &r.RootIdentity, &r.InventoryFingerprint, &r.EntryCount, &r.RootStatus, &r.RootErrorCode, &r.RootErrorMessage); err != nil {
			return nil, err
		}
		detail.Roots = append(detail.Roots, r)
	}
	if err := rootRows.Err(); err != nil {
		return nil, err
	}

	compRows, err := r.db.Query(`
		SELECT step_index, component_index, component_id, root_index, partition, status, reason_code, outcome_json
		FROM plan_components WHERE plan_id = ? ORDER BY component_index
	`, planID)
	if err != nil {
		return nil, err
	}
	defer compRows.Close()
	for compRows.Next() {
		var c WorkflowComponentRecord
		if err := compRows.Scan(&c.StepIndex, &c.ComponentIndex, &c.ComponentID, &c.RootIndex, &c.Partition, &c.Status, &c.ReasonCode, &c.OutcomeJSON); err != nil {
			return nil, err
		}
		detail.Components = append(detail.Components, c)
	}
	if err := compRows.Err(); err != nil {
		return nil, err
	}

	return detail, nil
}

// GetWorkflowPlanRoots returns the persisted planning roots of a workflow
// plan in root_index order.
func (r *Repository) GetWorkflowPlanRoots(planID string) ([]WorkflowRootRecord, error) {
	rows, err := r.db.Query(`
		SELECT root_index, root_path, root_identity, inventory_fingerprint, entry_count, root_status, root_error_code, root_error_message
		FROM plan_roots WHERE plan_id = ? ORDER BY root_index
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkflowRootRecord
	for rows.Next() {
		var rec WorkflowRootRecord
		if err := rows.Scan(&rec.RootIndex, &rec.RootPath, &rec.RootIdentity, &rec.InventoryFingerprint, &rec.EntryCount, &rec.RootStatus, &rec.RootErrorCode, &rec.RootErrorMessage); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// GetPlanWorkflowSchema reports plan_kind and workflow schema version for the
// Execute boundary guard. PlanKind is "" and err is ErrPlanNotFound when the
// plan does not exist; other database failures are returned as errors so they
// are never misrouted as a missing plan.
func (r *Repository) GetPlanWorkflowSchema(planID string) (planKind string, schemaVersion int, err error) {
	err = r.db.QueryRow(`
		SELECT plan_kind, COALESCE(workflow_schema_version, 0) FROM plans WHERE plan_id = ?
	`, planID).Scan(&planKind, &schemaVersion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, ErrPlanNotFound
		}
		return "", 0, err
	}
	return planKind, schemaVersion, nil
}
