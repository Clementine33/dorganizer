package sqlite //nolint:testpackage // white-box tests exercise unexported internals

import (
	"testing"
	"time"
)

//nolint:gocyclo,funlen // retention scenario with many cases
func TestRepository_RunRetentionCleanup_DeletesOnlyOlderThanCutoff(t *testing.T) {
	repo := newTestRepository(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	oldTime := cutoff.Add(-24 * time.Hour) // 2025-05-31
	newTime := cutoff.Add(24 * time.Hour)  // 2025-06-02

	// --- Insert old and new error_events ---
	oldErr := &ErrorEvent{
		Scope:     "scan",
		RootPath:  "/music",
		Code:      "OLD_ERR",
		Message:   "old error",
		Retryable: false,
	}
	if err := repo.CreateErrorEvent(oldErr); err != nil {
		t.Fatalf("create old error event: %v", err)
	}
	// Patch created_at to old time
	_, err := repo.db.Exec("UPDATE error_events SET created_at = ? WHERE id = ?", oldTime.Format(timeFormat), oldErr.ID)
	if err != nil {
		t.Fatalf("patch old error event time: %v", err)
	}

	newErr := &ErrorEvent{
		Scope:     "scan",
		RootPath:  "/music",
		Code:      "NEW_ERR",
		Message:   "new error",
		Retryable: false,
	}
	if createErr := repo.CreateErrorEvent(newErr); createErr != nil {
		t.Fatalf("create new error event: %v", createErr)
	}
	_, err = repo.db.Exec("UPDATE error_events SET created_at = ? WHERE id = ?", newTime.Format(timeFormat), newErr.ID)
	if err != nil {
		t.Fatalf("patch new error event time: %v", err)
	}

	// --- Insert old and new scan_sessions ---
	oldScan := &ScanSession{
		SessionID: "scan-old",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "completed",
		StartedAt: oldTime,
	}
	if createErr := repo.CreateScanSession(oldScan); createErr != nil {
		t.Fatalf("create old scan session: %v", createErr)
	}

	newScan := &ScanSession{
		SessionID: "scan-new",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "completed",
		StartedAt: newTime,
	}
	if createErr := repo.CreateScanSession(newScan); createErr != nil {
		t.Fatalf("create new scan session: %v", createErr)
	}

	// --- Insert old and new plans (with execute_sessions to validate cascade) ---
	oldPlan := &Plan{
		PlanID:        "plan-old",
		RootPath:      "/music",
		ScanRootPath:  "/music",
		PlanType:      "slim",
		SnapshotToken: "tok-old",
		Status:        "executed",
		CreatedAt:     oldTime,
	}
	if createErr := repo.CreatePlan(oldPlan); createErr != nil {
		t.Fatalf("create old plan: %v", createErr)
	}

	// Plan items on old plan — should cascade-delete with the plan
	oldPlanItem := &PlanItem{
		PlanID:                 "plan-old",
		ItemIndex:              0,
		OpType:                 "delete",
		SourcePath:             "/music/old-file.wav",
		ReasonCode:             "redundant",
		PreconditionPath:       "/music/old-file.wav",
		PreconditionContentRev: 1,
		PreconditionSize:       1000,
		PreconditionMtime:      1000000,
	}
	if createErr := repo.CreatePlanItem(oldPlanItem); createErr != nil {
		t.Fatalf("create old plan item: %v", createErr)
	}

	// Execute session on old plan — should cascade-delete with the plan
	oldExec := &ExecuteSession{
		SessionID: "exec-old",
		PlanID:    "plan-old",
		RootPath:  "/music",
		Status:    "completed",
		StartedAt: oldTime,
	}
	if createErr := repo.CreateExecuteSession(oldExec); createErr != nil {
		t.Fatalf("create old execute session: %v", createErr)
	}

	newPlan := &Plan{
		PlanID:        "plan-new",
		RootPath:      "/music",
		ScanRootPath:  "/music",
		PlanType:      "slim",
		SnapshotToken: "tok-new",
		Status:        "ready",
		CreatedAt:     newTime,
	}
	if createErr := repo.CreatePlan(newPlan); createErr != nil {
		t.Fatalf("create new plan: %v", createErr)
	}

	// Plan items on new plan — should survive cleanup
	newTargetPath := "/music/new-file.mp3"
	newPlanItem := &PlanItem{
		PlanID:                 "plan-new",
		ItemIndex:              0,
		OpType:                 "convert_and_delete",
		SourcePath:             "/music/new-file.wav",
		TargetPath:             &newTargetPath,
		ReasonCode:             "redundant",
		PreconditionPath:       "/music/new-file.wav",
		PreconditionContentRev: 2,
		PreconditionSize:       2000,
		PreconditionMtime:      2000000,
	}
	if createErr := repo.CreatePlanItem(newPlanItem); createErr != nil {
		t.Fatalf("create new plan item: %v", createErr)
	}

	// Execute session on new plan — should survive cleanup
	newExec := &ExecuteSession{
		SessionID: "exec-new",
		PlanID:    "plan-new",
		RootPath:  "/music",
		Status:    "completed",
		StartedAt: newTime,
	}
	if createErr := repo.CreateExecuteSession(newExec); createErr != nil {
		t.Fatalf("create new execute session: %v", createErr)
	}

	// --- Run cleanup ---
	stats, err := repo.RunRetentionCleanup(cutoff)
	if err != nil {
		t.Fatalf("RunRetentionCleanup: %v", err)
	}

	// --- Assert stats ---
	if stats.DeletedErrorEvents != 1 {
		t.Errorf("DeletedErrorEvents = %d, want 1", stats.DeletedErrorEvents)
	}
	if stats.DeletedScanSessions != 1 {
		t.Errorf("DeletedScanSessions = %d, want 1", stats.DeletedScanSessions)
	}
	if stats.DeletedPlans != 1 {
		t.Errorf("DeletedPlans = %d, want 1", stats.DeletedPlans)
	}

	// --- Verify remaining rows ---
	var errCount int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM error_events").Scan(&errCount); err != nil {
		t.Fatalf("count error_events: %v", err)
	}
	if errCount != 1 {
		t.Errorf("error_events remaining = %d, want 1", errCount)
	}

	var scanCount int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM scan_sessions").Scan(&scanCount); err != nil {
		t.Fatalf("count scan_sessions: %v", err)
	}
	if scanCount != 1 {
		t.Errorf("scan_sessions remaining = %d, want 1", scanCount)
	}

	var planCount int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM plans").Scan(&planCount); err != nil {
		t.Fatalf("count plans: %v", err)
	}
	if planCount != 1 {
		t.Errorf("plans remaining = %d, want 1", planCount)
	}

	// Verify cascade: execute_sessions for old plan should be gone, new one remains
	var execCount int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM execute_sessions").Scan(&execCount); err != nil {
		t.Fatalf("count execute_sessions: %v", err)
	}
	if execCount != 1 {
		t.Errorf("execute_sessions remaining = %d, want 1 (old cascaded, new retained)", execCount)
	}

	// Verify the retained execute session is the new one
	var retainedExecID string
	if err := repo.db.QueryRow("SELECT session_id FROM execute_sessions").Scan(&retainedExecID); err != nil {
		t.Fatalf("select retained execute session: %v", err)
	}
	if retainedExecID != "exec-new" {
		t.Errorf("retained execute session = %q, want exec-new", retainedExecID)
	}

	// Verify cascade: plan_items for old plan should be gone, new one remains
	var planItemCount int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM plan_items").Scan(&planItemCount); err != nil {
		t.Fatalf("count plan_items: %v", err)
	}
	if planItemCount != 1 {
		t.Errorf("plan_items remaining = %d, want 1 (old cascaded, new retained)", planItemCount)
	}

	// Verify the retained plan item belongs to the new plan
	var retainedPlanID string
	if err := repo.db.QueryRow("SELECT plan_id FROM plan_items").Scan(&retainedPlanID); err != nil {
		t.Fatalf("select retained plan item: %v", err)
	}
	if retainedPlanID != "plan-new" {
		t.Errorf("retained plan item plan_id = %q, want plan-new", retainedPlanID)
	}
}
