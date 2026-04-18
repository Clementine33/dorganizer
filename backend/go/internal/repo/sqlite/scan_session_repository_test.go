package sqlite

import (
	"testing"
	"time"
)

func TestRepository_ScanSessionMethods(t *testing.T) {
	repo := newTestRepository(t)

	session := &ScanSession{
		SessionID: "scan-001",
		RootPath:  "/music",
		ScopePath: nil,
		Kind:      "full",
		Status:    "running",
		StartedAt: time.Now(),
	}

	if err := repo.CreateScanSession(session); err != nil {
		t.Fatalf("CreateScanSession failed: %v", err)
	}

	fetched, err := repo.GetScanSession("scan-001")
	if err != nil {
		t.Fatalf("GetScanSession failed: %v", err)
	}
	if fetched.Status != "running" {
		t.Errorf("expected status running, got %s", fetched.Status)
	}

	if err := repo.UpdateScanSessionStatus("scan-001", "completed", "", ""); err != nil {
		t.Fatalf("UpdateScanSessionStatus failed: %v", err)
	}

	fetched, err = repo.GetScanSession("scan-001")
	if err != nil {
		t.Fatalf("GetScanSession failed: %v", err)
	}
	if fetched.Status != "completed" {
		t.Errorf("expected status completed, got %s", fetched.Status)
	}
}

func TestRepository_CreateAndGetScanSession_ScopePathNullableRoundTrip(t *testing.T) {
	repo := newTestRepository(t)

	session1 := &ScanSession{
		SessionID: "scan-full",
		RootPath:  "/music",
		ScopePath: nil,
		Kind:      "full",
		Status:    "running",
		StartedAt: time.Now(),
	}

	if err := repo.CreateScanSession(session1); err != nil {
		t.Fatalf("CreateScanSession failed: %v", err)
	}

	fetched1, err := repo.GetScanSession("scan-full")
	if err != nil {
		t.Fatalf("GetScanSession failed: %v", err)
	}
	if fetched1.ScopePath != nil {
		t.Errorf("expected scope_path nil for full scan, got %s", *fetched1.ScopePath)
	}

	scopePathVal := "/music/albums"
	session2 := &ScanSession{
		SessionID: "scan-folder",
		RootPath:  "/music",
		ScopePath: &scopePathVal,
		Kind:      "folder",
		Status:    "running",
		StartedAt: time.Now(),
	}

	if err := repo.CreateScanSession(session2); err != nil {
		t.Fatalf("CreateScanSession failed: %v", err)
	}

	fetched2, err := repo.GetScanSession("scan-folder")
	if err != nil {
		t.Fatalf("GetScanSession failed: %v", err)
	}
	if fetched2.ScopePath == nil {
		t.Errorf("expected scope_path '/music/albums', got nil")
	} else if *fetched2.ScopePath != "/music/albums" {
		t.Errorf("expected scope_path '/music/albums', got %s", *fetched2.ScopePath)
	}
}

func TestRepository_DeleteScanSessionsOlderThanTx_COALESCEFinishedAt(t *testing.T) {
	repo := newTestRepository(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	oldTime := cutoff.Add(-24 * time.Hour) // before cutoff
	newTime := cutoff.Add(24 * time.Hour)  // after cutoff

	// 1) Completed scan with old finished_at → should be deleted
	completedOld := &ScanSession{
		SessionID: "scan-completed-old",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "completed",
		StartedAt: oldTime.Add(-1 * time.Hour),
	}
	if err := repo.CreateScanSession(completedOld); err != nil {
		t.Fatalf("create completed-old scan: %v", err)
	}
	// Set finished_at to old time via status update, then patch to exact oldTime
	if err := repo.UpdateScanSessionStatus("scan-completed-old", "completed", "", ""); err != nil {
		t.Fatalf("update status: %v", err)
	}
	_, err := repo.db.Exec("UPDATE scan_sessions SET finished_at = ? WHERE session_id = ?", oldTime.Format(timeFormat), "scan-completed-old")
	if err != nil {
		t.Fatalf("patch finished_at: %v", err)
	}

	// 2) Running scan with old started_at → should be deleted (COALESCE(finished_at, started_at) falls back to started_at)
	runningOld := &ScanSession{
		SessionID: "scan-running-old",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "running",
		StartedAt: oldTime,
	}
	if err := repo.CreateScanSession(runningOld); err != nil {
		t.Fatalf("create running-old scan: %v", err)
	}

	// 3) Newer scan → should be retained
	newerScan := &ScanSession{
		SessionID: "scan-new",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "completed",
		StartedAt: newTime,
	}
	if err := repo.CreateScanSession(newerScan); err != nil {
		t.Fatalf("create new scan: %v", err)
	}
	if err := repo.UpdateScanSessionStatus("scan-new", "completed", "", ""); err != nil {
		t.Fatalf("update new scan status: %v", err)
	}

	// 4) Old started_at but new finished_at → COALESCE(finished_at, started_at) picks finished_at → retained
	coalesceScan := &ScanSession{
		SessionID: "scan-coalesce-precedence",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "completed",
		StartedAt: oldTime, // started_at < cutoff
	}
	if err := repo.CreateScanSession(coalesceScan); err != nil {
		t.Fatalf("create coalesce-precedence scan: %v", err)
	}
	if err := repo.UpdateScanSessionStatus("scan-coalesce-precedence", "completed", "", ""); err != nil {
		t.Fatalf("update coalesce-precedence scan status: %v", err)
	}
	_, err = repo.db.Exec("UPDATE scan_sessions SET finished_at = ? WHERE session_id = ?", newTime.Format(timeFormat), "scan-coalesce-precedence")
	if err != nil {
		t.Fatalf("patch coalesce-precedence finished_at: %v", err)
	}

	// Run delete within a transaction
	tx, err := repo.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	deleted, err := repo.DeleteScanSessionsOlderThanTx(tx, cutoff)
	if err != nil {
		tx.Rollback()
		t.Fatalf("DeleteScanSessionsOlderThanTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if deleted != 2 {
		t.Errorf("deleted count = %d, want 2", deleted)
	}

	// Verify the two retained scans remain
	var count int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM scan_sessions").Scan(&count); err != nil {
		t.Fatalf("count scan_sessions: %v", err)
	}
	if count != 2 {
		t.Fatalf("scan_sessions remaining = %d, want 2", count)
	}

	rows, err := repo.db.Query("SELECT session_id FROM scan_sessions ORDER BY session_id")
	if err != nil {
		t.Fatalf("query retained scans: %v", err)
	}
	defer rows.Close()

	var retained []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan retained id: %v", err)
		}
		retained = append(retained, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration: %v", err)
	}

	wantRetained := map[string]bool{"scan-new": true, "scan-coalesce-precedence": true}
	for _, id := range retained {
		if !wantRetained[id] {
			t.Errorf("unexpected retained scan %q", id)
		}
		delete(wantRetained, id)
	}
	for id := range wantRetained {
		t.Errorf("missing expected retained scan %q", id)
	}

	// Explicitly verify COALESCE precedence: old started_at + new finished_at → retained
	var coalesceExists bool
	if err := repo.db.QueryRow("SELECT EXISTS(SELECT 1 FROM scan_sessions WHERE session_id = 'scan-coalesce-precedence')").Scan(&coalesceExists); err != nil {
		t.Fatalf("check coalesce-precedence row: %v", err)
	}
	if !coalesceExists {
		t.Error("scan-coalesce-precedence row was deleted; COALESCE(finished_at, started_at) should have chosen finished_at (new) over started_at (old)")
	}
}
