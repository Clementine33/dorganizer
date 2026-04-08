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
