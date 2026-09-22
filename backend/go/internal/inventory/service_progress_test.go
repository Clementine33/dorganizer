package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/adapters/filesystem"
	"github.com/onsei/organizer/backend/internal/inventory"
)

// TestScanRootCtxEmitsProgressAndCounts verifies ScanRootCtx reports
// throttled progress via inventory.WithProgress: at least one callback, monotonically
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

	store := &fakeStagingStore{MergeResult: n}
	svc := inventory.NewPipeline(store, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	var progress []inventory.Progress
	scanID, err := svc.ScanRootCtx(context.Background(), tmp, inventory.WithProgress(func(p inventory.Progress) {
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

// TestScanRootCtxCancellationCleansStagingAndSkipsTheMerge verifies mid-walk
// cancellation end to end on the pipeline's own seam: ScanRootCtx returns
// context.Canceled, the scan session ends canceled with error_code
// SCAN_CANCELLED, staging rows are cleaned up, and the merge never runs — a
// canceled scan must not publish a partial inventory.
func TestScanRootCtxCancellationCleansStagingAndSkipsTheMerge(t *testing.T) {
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

	// A session of this size walks several batches, so cancellation lands
	// mid-pipeline rather than after everything was staged.
	store := &fakeStagingStore{WriteBatch: 1000}
	svc := inventory.NewPipeline(store, filesystem.WalkRootEntriesParallel, filesystem.WalkFolderEntries)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cancelOnce := false
	_, err := svc.ScanRootCtx(ctx, tmp, inventory.WithProgress(func(inventory.Progress) {
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

	if len(store.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(store.Sessions))
	}
	session := store.Sessions[0]
	if session.Status != "canceled" {
		t.Errorf("expected session status %q, got %q", "canceled", session.Status)
	}
	if session.ErrorCode != "SCAN_CANCELLED" {
		t.Errorf("expected error_code %q, got %q", "SCAN_CANCELLED", session.ErrorCode)
	}

	if len(store.StagingEntries) != 0 {
		t.Errorf("expected no leftover staging rows, got %d", len(store.StagingEntries))
	}
	if store.MergeCalls != 0 {
		t.Errorf("a canceled scan merged %d time(s); it must not publish a partial inventory", store.MergeCalls)
	}
}
