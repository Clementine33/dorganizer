package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// TestScanRootCtxEmitsProgressAndCounts verifies ScanRootCtx reports
// throttled progress via WithProgress: at least one callback, monotonically
// non-decreasing FilesScanned, and a final count equal to the number of
// audio files scanned.
func TestScanRootCtxEmitsProgressAndCounts(t *testing.T) {
	const n = 20
	tmp := t.TempDir()
	for i := range n {
		album := filepath.Join(tmp, fmt.Sprintf("album-%02d", i))
		if err := os.MkdirAll(album, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", album, err)
		}
		if err := os.WriteFile(filepath.Join(album, "song.wav"), []byte("dummy"), 0644); err != nil {
			t.Fatalf("write %s: %v", album, err)
		}
	}

	mock := &MockRepository{MergeResult: n}
	svc := NewScannerService(mock)

	var progress []Progress
	scanID, err := svc.ScanRootCtx(context.Background(), tmp, WithProgress(func(p Progress) {
		progress = append(progress, p)
	}))
	if err != nil {
		t.Fatalf("ScanRootCtx failed: %v", err)
	}
	if scanID == "" {
		t.Error("expected non-empty scan ID")
	}

	if len(progress) == 0 {
		t.Fatal("expected at least one progress callback")
	}

	last := 0
	for _, p := range progress {
		if p.FilesScanned < last {
			t.Fatalf("progress FilesScanned not monotonically non-decreasing: %v", progress)
		}
		last = p.FilesScanned
	}

	if last != n {
		t.Errorf("expected final FilesScanned=%d, got %d", n, last)
	}
}

// TestScanRootCtxCancellationCleansStaging verifies mid-walk cancellation:
// ScanRootCtx returns context.Canceled, the scan session is marked canceled
// with error_code SCAN_CANCELLED, and no entries_staging rows are left
// behind for the session.
func TestScanRootCtxCancellationCleansStaging(t *testing.T) {
	const files = 2000
	tmp := t.TempDir()
	for i := range files {
		dir := filepath.Join(tmp, fmt.Sprintf("d%03d", i%50))
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("song%d.wav", i)), []byte("dummy"), 0644); err != nil {
			t.Fatalf("write song%d.wav: %v", i, err)
		}
	}

	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	defer repo.Close()

	svc := NewScannerService(NewSQLiteRepositoryAdapter(repo))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cancelOnce := false
	_, err = svc.ScanRootCtx(ctx, tmp, WithProgress(func(Progress) {
		if !cancelOnce {
			cancelOnce = true
			cancel()
		}
	}))
	if err == nil {
		t.Fatal("expected error when context is canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	var status, errorCode string
	if err := repo.DB().
		QueryRow("SELECT status, error_code FROM scan_sessions ORDER BY started_at DESC LIMIT 1").
		Scan(&status, &errorCode); err != nil {
		t.Fatalf("query scan session: %v", err)
	}
	if status != "canceled" {
		t.Errorf("expected session status %q, got %q", "canceled", status)
	}
	if errorCode != "SCAN_CANCELLED" {
		t.Errorf("expected error_code %q, got %q", "SCAN_CANCELLED", errorCode)
	}

	var stagingCount int
	if err := repo.DB().QueryRow("SELECT COUNT(*) FROM entries_staging").Scan(&stagingCount); err != nil {
		t.Fatalf("count staging rows: %v", err)
	}
	if stagingCount != 0 {
		t.Errorf("expected no leftover entries_staging rows, got %d", stagingCount)
	}
}
