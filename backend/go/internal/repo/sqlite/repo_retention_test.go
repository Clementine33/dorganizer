package sqlite //nolint:testpackage // white-box tests exercise unexported internals

import (
	"testing"
	"time"
)

func TestRepository_RunRetentionCleanup_DeletesOnlyOlderThanCutoff(t *testing.T) {
	repo := newTestRepository(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	oldTime := cutoff.Add(-24 * time.Hour) // 2025-05-31
	newTime := cutoff.Add(24 * time.Hour)  // 2025-06-02

	oldScan := &ScanSession{
		SessionID: "scan-old",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "completed",
		StartedAt: oldTime,
	}
	if err := repo.CreateScanSession(oldScan); err != nil {
		t.Fatalf("create old scan session: %v", err)
	}

	newScan := &ScanSession{
		SessionID: "scan-new",
		RootPath:  "/music",
		Kind:      "full",
		Status:    "completed",
		StartedAt: newTime,
	}
	if err := repo.CreateScanSession(newScan); err != nil {
		t.Fatalf("create new scan session: %v", err)
	}

	stats, err := repo.RunRetentionCleanup(cutoff)
	if err != nil {
		t.Fatalf("RunRetentionCleanup: %v", err)
	}
	if stats.DeletedScanSessions != 1 {
		t.Errorf("DeletedScanSessions = %d, want 1", stats.DeletedScanSessions)
	}

	var scanCount int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM scan_sessions").Scan(&scanCount); err != nil {
		t.Fatalf("count scan_sessions: %v", err)
	}
	if scanCount != 1 {
		t.Errorf("scan_sessions remaining = %d, want 1", scanCount)
	}

	var retainedSession string
	if err := repo.db.QueryRow("SELECT session_id FROM scan_sessions").Scan(&retainedSession); err != nil {
		t.Fatalf("select retained scan session: %v", err)
	}
	if retainedSession != "scan-new" {
		t.Errorf("retained scan session = %q, want scan-new", retainedSession)
	}
}
