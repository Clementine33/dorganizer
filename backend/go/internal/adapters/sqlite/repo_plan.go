package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/onsei/organizer/backend/internal/workset"
)

// ==================== Policy slots ====================

// PolicySlotRow is one of the three fixed global policy slots. PolicyJSON is
// empty while the slot is unconfigured.
type PolicySlotRow struct {
	SlotIndex  int
	Name       string
	PolicyJSON string
	UpdatedAt  time.Time
}

// GetPolicySlots returns slots 1..3 in order; missing rows materialize as
// empty slots so the fixed cardinality holds even against a hand-truncated
// table.
func (r *Repository) GetPolicySlots() ([]*PolicySlotRow, error) {
	rows, err := r.db.Query(`
		SELECT slot_index, name, COALESCE(policy_json, ''), COALESCE(updated_at, '')
		FROM policy_slots ORDER BY slot_index
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byIndex := map[int]*PolicySlotRow{}
	var maxSeen int
	for rows.Next() {
		var slot PolicySlotRow
		var updatedAt string
		if err := rows.Scan(&slot.SlotIndex, &slot.Name, &slot.PolicyJSON, &updatedAt); err != nil {
			return nil, err
		}
		slot.UpdatedAt = parseTimestamp(updatedAt)
		byIndex[slot.SlotIndex] = &slot
		if slot.SlotIndex > maxSeen {
			maxSeen = slot.SlotIndex
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if maxSeen < 3 {
		maxSeen = 3
	}
	out := make([]*PolicySlotRow, 0, maxSeen)
	for i := 1; i <= maxSeen; i++ {
		if s, ok := byIndex[i]; ok {
			out = append(out, s)
		} else {
			out = append(out, &PolicySlotRow{SlotIndex: i})
		}
	}
	return out, nil
}

// GetPolicySlot fetches one slot by index; nil when out of range.
func (r *Repository) GetPolicySlot(slotIndex int) (*PolicySlotRow, error) {
	if slotIndex < 1 || slotIndex > 3 {
		return nil, nil
	}
	var slot PolicySlotRow
	var updatedAt string
	err := r.db.QueryRow(`
		SELECT slot_index, name, COALESCE(policy_json, ''), COALESCE(updated_at, '')
		FROM policy_slots WHERE slot_index = ?
	`, slotIndex).Scan(&slot.SlotIndex, &slot.Name, &slot.PolicyJSON, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &PolicySlotRow{SlotIndex: slotIndex}, nil
		}
		return nil, err
	}
	slot.UpdatedAt = parseTimestamp(updatedAt)
	return &slot, nil
}

// UpdatePolicySlot overwrites an existing slot's name and policy. Rows.Count
// must be exactly 1; a zero affected count means the slot row is missing,
// which violates the storage invariant and surfaces as an error.
func (r *Repository) UpdatePolicySlot(slotIndex int, name, policyJSON string) error {
	if slotIndex < 1 || slotIndex > 3 {
		return fmt.Errorf("policy slot index %d outside 1..3", slotIndex)
	}
	res, err := r.db.Exec(`
		UPDATE policy_slots SET name = ?, policy_json = ?, updated_at = ? WHERE slot_index = ?
	`, name, policyJSON, time.Now().Format(timeFormat), slotIndex)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("policy slot %d row missing (storage invariant violated)", slotIndex)
	}
	return nil
}

// ==================== plan persistence ====================

const planColumns = `plan_id, root_path, scan_root_path, library_id, snapshot_token, status, task_kind, task_schema_version, created_at`

// CreatePlanTx persists one plan and all of its payload, root and unit
// snapshots in one transaction so a partial plan is never visible. worksetID
// is the record that owns the plan: plans are linked to their record by that
// column alone (there is no foreign key), so every cleanup path deletes them
// explicitly and every insert has to fill it in.
func CreatePlanTx(
	db *sql.DB,
	planID string,
	taskKind string,
	taskSchemaVersion int,
	rootPath string,
	snapshotToken string,
	libraryID string,
	worksetID string,
	steps []workset.PlanStepRecord,
	roots []workset.PlanRootRecord,
	components []workset.PlanComponentRecord,
) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin plan tx: %w", err)
	}
	defer tx.Rollback()

	if err := InsertPlanTx(
		tx,
		planID,
		taskKind,
		taskSchemaVersion,
		rootPath,
		snapshotToken,
		libraryID,
		worksetID,
		steps,
		roots,
		components,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit plan tx: %w", err)
	}
	return nil
}

// InsertPlanTx is the transaction-scoped form of
// CreatePlanTx for callers that must persist the plan snapshot inside
// a larger atomic transaction (the workset generation completion path writes
// the plan, its revision association, and the current-revision promotion in
// one commit).
func InsertPlanTx(
	tx *sql.Tx,
	planID string,
	taskKind string,
	taskSchemaVersion int,
	rootPath string,
	snapshotToken string,
	libraryID string,
	worksetID string,
	steps []workset.PlanStepRecord,
	roots []workset.PlanRootRecord,
	components []workset.PlanComponentRecord,
) error {
	var libID any
	if libraryID != "" {
		libID = libraryID
	}
	if _, err := tx.Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, library_id, snapshot_token, status, task_kind, task_schema_version, workset_id, created_at)
		VALUES (?, ?, ?, ?, ?, 'ready', ?, ?, ?, ?)
	`, planID, rootPath, rootPath, libID, snapshotToken,
		taskKind, taskSchemaVersion, worksetID, time.Now().Format(timeFormat)); err != nil {
		return fmt.Errorf("insert plan: %w", err)
	}

	for _, s := range steps {
		if _, err := tx.Exec(
			`
			INSERT INTO conversion_steps
			(plan_id, step_index, step_type, status,
			 policy_schema_version, policy_json, policy_hash, classifier_pattern, classifier_hash, step_summary_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			planID,
			s.StepIndex,
			s.StepType,
			s.Status,
			s.PolicySchemaVersion,
			s.PolicyJSON,
			s.PolicyHash,
			s.ClassifierTags,
			s.ClassifierHash,
			s.StepSummaryJSON,
		); err != nil {
			return fmt.Errorf("insert plan payload: %w", err)
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
			return fmt.Errorf("insert plan root: %w", err)
		}
	}

	for _, c := range components {
		if _, err := tx.Exec(`
			INSERT INTO conversion_components
			(plan_id, step_index, component_index, component_id, root_index, partition, status, reason_code, outcome_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, planID, c.StepIndex, c.ComponentIndex, c.ComponentID, c.RootIndex, c.Partition, c.Status, c.ReasonCode, c.OutcomeJSON); err != nil {
			return fmt.Errorf("insert plan unit: %w", err)
		}
	}

	return nil
}

// scanPlanRow scans one plan row (planColumns order).
func scanPlanRow(
	p *workset.Plan,
	createdAtStr string,
	libraryID sql.NullString,
	taskKind string,
	taskSchemaVersion int,
) {
	if libraryID.Valid {
		p.LibraryID = libraryID.String
	}
	p.TaskKind = taskKind
	p.TaskSchemaVersion = taskSchemaVersion
	p.CreatedAt = parseTimestamp(createdAtStr)
}

// GetPlanDetail reconstructs a plan's persisted review payload without
// consulting live task state.
func (r *Repository) GetPlanDetail(planID string) (*workset.PlanDetail, error) {
	var p workset.Plan
	var createdAt string
	var libraryID sql.NullString
	var taskKind string
	var taskSchemaVersion int
	err := r.db.QueryRow(`
		SELECT `+planColumns+`
		FROM plans WHERE plan_id = ?
	`, planID).Scan(&p.PlanID, &p.RootPath, &p.ScanRootPath, &libraryID, &p.SnapshotToken, &p.Status, &taskKind, &taskSchemaVersion, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, workset.ErrPlanNotFound
		}
		return nil, err
	}
	scanPlanRow(&p, createdAt, libraryID, taskKind, taskSchemaVersion)

	detail := &workset.PlanDetail{Plan: p}

	steps, err := loadPlanSteps(r.db, planID)
	if err != nil {
		return nil, err
	}
	detail.Steps = steps

	roots, err := loadPlanRoots(r.db, planID)
	if err != nil {
		return nil, err
	}
	detail.Roots = roots

	components, err := loadPlanComponents(r.db, planID)
	if err != nil {
		return nil, err
	}
	detail.Components = components

	return detail, nil
}

func loadPlanSteps(db *sql.DB, planID string) ([]workset.PlanStepRecord, error) {
	stepRows, err := db.Query(`
		SELECT step_index, step_type, status,
		       policy_schema_version, policy_json, policy_hash, classifier_pattern, classifier_hash, step_summary_json
		FROM conversion_steps WHERE plan_id = ? ORDER BY step_index
	`, planID)
	if err != nil {
		return nil, err
	}
	defer stepRows.Close()
	var steps []workset.PlanStepRecord
	for stepRows.Next() {
		var s workset.PlanStepRecord
		if scanErr := stepRows.Scan(
			&s.StepIndex,
			&s.StepType,
			&s.Status,
			&s.PolicySchemaVersion,
			&s.PolicyJSON,
			&s.PolicyHash,
			&s.ClassifierTags,
			&s.ClassifierHash,
			&s.StepSummaryJSON,
		); scanErr != nil {
			return nil, scanErr
		}
		steps = append(steps, s)
	}
	return steps, stepRows.Err()
}

func loadPlanRoots(db *sql.DB, planID string) ([]workset.PlanRootRecord, error) {
	rootRows, err := db.Query(`
		SELECT root_index, root_path, root_identity, inventory_fingerprint, entry_count, root_status, root_error_code, root_error_message
		FROM plan_roots WHERE plan_id = ? ORDER BY root_index
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rootRows.Close()
	var roots []workset.PlanRootRecord
	for rootRows.Next() {
		var rec workset.PlanRootRecord
		if scanErr := rootRows.Scan(
			&rec.RootIndex,
			&rec.RootPath,
			&rec.RootIdentity,
			&rec.InventoryFingerprint,
			&rec.EntryCount,
			&rec.RootStatus,
			&rec.RootErrorCode,
			&rec.RootErrorMessage,
		); scanErr != nil {
			return nil, scanErr
		}
		roots = append(roots, rec)
	}
	return roots, rootRows.Err()
}

func loadPlanComponents(db *sql.DB, planID string) ([]workset.PlanComponentRecord, error) {
	compRows, err := db.Query(`
		SELECT step_index, component_index, component_id, root_index, partition, status, reason_code, outcome_json
		FROM conversion_components WHERE plan_id = ? ORDER BY component_index
	`, planID)
	if err != nil {
		return nil, err
	}
	defer compRows.Close()
	var comps []workset.PlanComponentRecord
	for compRows.Next() {
		var c workset.PlanComponentRecord
		if err := compRows.Scan(
			&c.StepIndex,
			&c.ComponentIndex,
			&c.ComponentID,
			&c.RootIndex,
			&c.Partition,
			&c.Status,
			&c.ReasonCode,
			&c.OutcomeJSON,
		); err != nil {
			return nil, err
		}
		comps = append(comps, c)
	}
	return comps, compRows.Err()
}

// GetPlanRoots returns the persisted planning roots of a plan
// plan in root_index order.
func (r *Repository) GetPlanRoots(planID string) ([]workset.PlanRootRecord, error) {
	rows, err := r.db.Query(`
		SELECT root_index, root_path, root_identity, inventory_fingerprint, entry_count, root_status, root_error_code, root_error_message
		FROM plan_roots WHERE plan_id = ? ORDER BY root_index
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []workset.PlanRootRecord
	for rows.Next() {
		var rec workset.PlanRootRecord
		if err := rows.Scan(
			&rec.RootIndex,
			&rec.RootPath,
			&rec.RootIdentity,
			&rec.InventoryFingerprint,
			&rec.EntryCount,
			&rec.RootStatus,
			&rec.RootErrorCode,
			&rec.RootErrorMessage,
		); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
