package sqlite

import (
	"testing"
	"time"
)

func TestRepository_CreateAndListErrorEvents_PathNullableRoundTrip(t *testing.T) {
	repo := newTestRepository(t)

	errEvent1 := &ErrorEvent{
		Scope:     "scan",
		RootPath:  "/music",
		Path:      nil,
		Code:      "SCAN_FAILED",
		Message:   "Permission denied accessing directory",
		Retryable: true,
	}

	if err := repo.CreateErrorEvent(errEvent1); err != nil {
		t.Fatalf("CreateErrorEvent failed: %v", err)
	}

	events, err := repo.ListErrorEventsByRoot("/music")
	if err != nil {
		t.Fatalf("ListErrorEventsByRoot failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	var event1 *ErrorEvent
	for _, e := range events {
		if e.ID == errEvent1.ID {
			event1 = e
			break
		}
	}
	if event1 == nil {
		t.Fatalf("event with ID %d not found", errEvent1.ID)
	}
	if event1.Path != nil {
		t.Errorf("expected path nil, got %s", *event1.Path)
	}
	if event1.Code != "SCAN_FAILED" {
		t.Errorf("expected code SCAN_FAILED, got %s", event1.Code)
	}
	if event1.Message != "Permission denied accessing directory" {
		t.Errorf("expected message 'Permission denied accessing directory', got %s", event1.Message)
	}
	if event1.Retryable != true {
		t.Errorf("expected retryable true, got %v", event1.Retryable)
	}
	if event1.Scope != "scan" {
		t.Errorf("expected scope scan, got %s", event1.Scope)
	}
	if event1.RootPath != "/music" {
		t.Errorf("expected root_path /music, got %s", event1.RootPath)
	}

	pathVal := "/music/corrupt.mp3"
	errEvent2 := &ErrorEvent{
		Scope:     "scan",
		RootPath:  "/music",
		Path:      &pathVal,
		Code:      "FILE_UNREADABLE",
		Message:   "Cannot read file",
		Retryable: false,
	}

	if err := repo.CreateErrorEvent(errEvent2); err != nil {
		t.Fatalf("CreateErrorEvent failed: %v", err)
	}

	events, err = repo.ListErrorEventsByRoot("/music")
	if err != nil {
		t.Fatalf("ListErrorEventsByRoot failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	var event2 *ErrorEvent
	for _, e := range events {
		if e.ID == errEvent2.ID {
			event2 = e
			break
		}
	}
	if event2 == nil {
		t.Fatalf("event with ID %d not found", errEvent2.ID)
	}
	if event2.Path == nil {
		t.Errorf("expected path '/music/corrupt.mp3', got nil")
	} else if *event2.Path != "/music/corrupt.mp3" {
		t.Errorf("expected path '/music/corrupt.mp3', got %s", *event2.Path)
	}
	if event2.Code != "FILE_UNREADABLE" {
		t.Errorf("expected code FILE_UNREADABLE, got %s", event2.Code)
	}
	if event2.Message != "Cannot read file" {
		t.Errorf("expected message 'Cannot read file', got %s", event2.Message)
	}
	if event2.Retryable != false {
		t.Errorf("expected retryable false, got %v", event2.Retryable)
	}
	if event2.Scope != "scan" {
		t.Errorf("expected scope scan, got %s", event2.Scope)
	}
	if event2.RootPath != "/music" {
		t.Errorf("expected root_path /music, got %s", event2.RootPath)
	}
}

func TestRepository_DeleteErrorEventsOlderThanTx_RespectsCutoffBoundary(t *testing.T) {
	repo := newTestRepository(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	oldTime := cutoff.Add(-1 * time.Second) // strictly before cutoff
	newTime := cutoff                       // exactly at cutoff (>= cutoff, should be kept)

	// Create old error event
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
	_, err := repo.db.Exec("UPDATE error_events SET created_at = ? WHERE id = ?", oldTime.Format(timeFormat), oldErr.ID)
	if err != nil {
		t.Fatalf("patch old error event time: %v", err)
	}

	// Create new error event (at cutoff, should be retained)
	newErr := &ErrorEvent{
		Scope:     "scan",
		RootPath:  "/music",
		Code:      "NEW_ERR",
		Message:   "new error",
		Retryable: false,
	}
	if err := repo.CreateErrorEvent(newErr); err != nil {
		t.Fatalf("create new error event: %v", err)
	}
	_, err = repo.db.Exec("UPDATE error_events SET created_at = ? WHERE id = ?", newTime.Format(timeFormat), newErr.ID)
	if err != nil {
		t.Fatalf("patch new error event time: %v", err)
	}

	// Run delete within a transaction
	tx, err := repo.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	deleted, err := repo.DeleteErrorEventsOlderThanTx(tx, cutoff)
	if err != nil {
		tx.Rollback()
		t.Fatalf("DeleteErrorEventsOlderThanTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if deleted != 1 {
		t.Errorf("deleted count = %d, want 1", deleted)
	}

	// Verify only the new event remains
	var count int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM error_events").Scan(&count); err != nil {
		t.Fatalf("count error_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("error_events remaining = %d, want 1", count)
	}

	var retainedCode string
	if err := repo.db.QueryRow("SELECT code FROM error_events").Scan(&retainedCode); err != nil {
		t.Fatalf("select retained event: %v", err)
	}
	if retainedCode != "NEW_ERR" {
		t.Errorf("retained event code = %q, want NEW_ERR", retainedCode)
	}
}
