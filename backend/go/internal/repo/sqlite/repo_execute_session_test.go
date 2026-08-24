package sqlite

import (
	"testing"
	"time"
)

func TestRepository_ExecuteSessionMethods(t *testing.T) {
	repo := newTestRepository(t)

	plan := &Plan{
		PlanID:        "plan-001",
		RootPath:      "/music",
		PlanType:      "slim",
		SnapshotToken: "snap-123",
		Status:        "ready",
		CreatedAt:     time.Now(),
	}
	if err := repo.CreatePlan(plan); err != nil {
		t.Fatalf("CreatePlan failed: %v", err)
	}

	execSession := &ExecuteSession{
		SessionID: "exec-001",
		PlanID:    "plan-001",
		RootPath:  "/music",
		Status:    "running",
		StartedAt: time.Now(),
	}

	if err := repo.CreateExecuteSession(execSession); err != nil {
		t.Fatalf("CreateExecuteSession failed: %v", err)
	}

	fetched, err := repo.GetExecuteSession("exec-001")
	if err != nil {
		t.Fatalf("GetExecuteSession failed: %v", err)
	}
	if fetched.Status != "running" {
		t.Errorf("expected status running, got %s", fetched.Status)
	}

	if err := repo.UpdateExecuteSessionStatus("exec-001", "completed", "", ""); err != nil {
		t.Fatalf("UpdateExecuteSessionStatus failed: %v", err)
	}

	fetched, err = repo.GetExecuteSession("exec-001")
	if err != nil {
		t.Fatalf("GetExecuteSession failed: %v", err)
	}
	if fetched.Status != "completed" {
		t.Errorf("expected status completed, got %s", fetched.Status)
	}
}

func TestRepository_DeletePlansOlderThanTx_CascadesExecuteSessions(t *testing.T) {
	repo := newTestRepository(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	oldTime := cutoff.Add(-24 * time.Hour)
	newTime := cutoff.Add(24 * time.Hour)

	// Old plan — should be deleted
	oldPlan := &Plan{
		PlanID:        "plan-old",
		RootPath:      "/music",
		ScanRootPath:  "/music",
		PlanType:      "slim",
		SnapshotToken: "tok-old",
		Status:        "executed",
		CreatedAt:     oldTime,
	}
	if err := repo.CreatePlan(oldPlan); err != nil {
		t.Fatalf("create old plan: %v", err)
	}

	// Execute session under old plan — should cascade-delete
	oldExec := &ExecuteSession{
		SessionID: "exec-old",
		PlanID:    "plan-old",
		RootPath:  "/music",
		Status:    "completed",
		StartedAt: oldTime,
	}
	if err := repo.CreateExecuteSession(oldExec); err != nil {
		t.Fatalf("create old execute session: %v", err)
	}

	// New plan — should be retained
	newPlan := &Plan{
		PlanID:        "plan-new",
		RootPath:      "/music",
		ScanRootPath:  "/music",
		PlanType:      "slim",
		SnapshotToken: "tok-new",
		Status:        "ready",
		CreatedAt:     newTime,
	}
	if err := repo.CreatePlan(newPlan); err != nil {
		t.Fatalf("create new plan: %v", err)
	}

	// Execute session under new plan — should survive
	newExec := &ExecuteSession{
		SessionID: "exec-new",
		PlanID:    "plan-new",
		RootPath:  "/music",
		Status:    "running",
		StartedAt: newTime,
	}
	if err := repo.CreateExecuteSession(newExec); err != nil {
		t.Fatalf("create new execute session: %v", err)
	}

	// Run delete within a transaction
	tx, err := repo.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	deleted, err := repo.DeletePlansOlderThanTx(tx, cutoff)
	if err != nil {
		tx.Rollback()
		t.Fatalf("DeletePlansOlderThanTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if deleted != 1 {
		t.Errorf("deleted plans count = %d, want 1", deleted)
	}

	// Verify plans
	var planCount int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM plans").Scan(&planCount); err != nil {
		t.Fatalf("count plans: %v", err)
	}
	if planCount != 1 {
		t.Errorf("plans remaining = %d, want 1", planCount)
	}

	// Verify execute_sessions: old cascaded, new retained
	var execCount int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM execute_sessions").Scan(&execCount); err != nil {
		t.Fatalf("count execute_sessions: %v", err)
	}
	if execCount != 1 {
		t.Errorf("execute_sessions remaining = %d, want 1", execCount)
	}

	var retainedExecID string
	if err := repo.db.QueryRow("SELECT session_id FROM execute_sessions").Scan(&retainedExecID); err != nil {
		t.Fatalf("select retained execute session: %v", err)
	}
	if retainedExecID != "exec-new" {
		t.Errorf("retained execute session = %q, want exec-new", retainedExecID)
	}
}
