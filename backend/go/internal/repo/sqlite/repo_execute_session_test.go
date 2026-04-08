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
