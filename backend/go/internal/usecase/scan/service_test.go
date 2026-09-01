package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

func newTestRepo(t *testing.T, dir string) *sqlite.Repository {
	t.Helper()
	repo, err := sqlite.NewRepository(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

// TestScanServiceValidatesRoot verifies typed validation errors:
// empty root -> invalid_argument / ROOT_PATH_REQUIRED,
// nonexistent dir -> invalid_argument / ROOT_PATH_NOT_FOUND.
func TestScanServiceValidatesRoot(t *testing.T) {
	dir := t.TempDir()
	repo := newTestRepo(t, dir)
	svc := NewService(repo)

	_, err := svc.Scan(context.Background(), Request{RootPath: ""}, func(Event) {})
	var scanErr *Error
	if !errors.As(err, &scanErr) {
		t.Fatalf("expected *scan.Error for empty root, got %T: %v", err, err)
	}
	if scanErr.Kind != ErrKindInvalidArgument {
		t.Errorf("expected Kind=%q, got %q", ErrKindInvalidArgument, scanErr.Kind)
	}
	if scanErr.Code != "ROOT_PATH_REQUIRED" {
		t.Errorf("expected Code=ROOT_PATH_REQUIRED, got %q", scanErr.Code)
	}

	_, err = svc.Scan(context.Background(), Request{RootPath: filepath.Join(dir, "does-not-exist")}, func(Event) {})
	if !errors.As(err, &scanErr) {
		t.Fatalf("expected *scan.Error for nonexistent root, got %T: %v", err, err)
	}
	if scanErr.Kind != ErrKindInvalidArgument {
		t.Errorf("expected Kind=%q, got %q", ErrKindInvalidArgument, scanErr.Kind)
	}
	if scanErr.Code != "ROOT_PATH_NOT_FOUND" {
		t.Errorf("expected Code=ROOT_PATH_NOT_FOUND, got %q", scanErr.Code)
	}
}

// TestScanServiceEmitsErrorEventForInvalidRoots verifies validation failures
// are classified correctly (not-directory vs not-found vs required) and each
// emits a single terminal `error` event before returning.
func TestScanServiceEmitsErrorEventForInvalidRoots(t *testing.T) {
	dir := t.TempDir()
	repo := newTestRepo(t, dir)
	svc := NewService(repo)

	filePath := filepath.Join(dir, "somefile.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	cases := []struct {
		name string
		root string
		code string
	}{
		{name: "empty", root: "", code: "ROOT_PATH_REQUIRED"},
		{name: "missing", root: filepath.Join(dir, "does-not-exist"), code: "ROOT_PATH_NOT_FOUND"},
		{name: "file-as-root", root: filePath, code: "ROOT_PATH_NOT_DIRECTORY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var events []Event
			_, err := svc.Scan(context.Background(), Request{RootPath: tc.root}, func(ev Event) {
				events = append(events, ev)
			})
			var scanErr *Error
			if !errors.As(err, &scanErr) {
				t.Fatalf("expected *scan.Error, got %T: %v", err, err)
			}
			if scanErr.Code != tc.code {
				t.Errorf("expected Code=%q, got %q", tc.code, scanErr.Code)
			}
			if len(events) != 1 || events[0].Type != "error" {
				t.Errorf("expected a single error event, got %+v", events)
			}
		})
	}
}

// TestScanServiceEmitsEventsAndResult verifies the happy path over a real
// sqlite repo: started -> (progress) -> completed, scan_id non-empty and
// matching the result, final FilesScanned equal to the audio file count.
func TestScanServiceEmitsEventsAndResult(t *testing.T) {
	const n = 10
	dir := t.TempDir()
	repo := newTestRepo(t, dir)
	svc := NewService(repo)

	musicDir := filepath.Join(dir, "music")
	for i := range n {
		album := filepath.Join(musicDir, fmt.Sprintf("album-%02d", i))
		if err := os.MkdirAll(album, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", album, err)
		}
		if err := os.WriteFile(filepath.Join(album, "track.wav"), []byte("dummy"), 0644); err != nil {
			t.Fatalf("write track.wav in %s: %v", album, err)
		}
	}

	var events []Event
	result, err := svc.Scan(context.Background(), Request{RootPath: musicDir}, func(ev Event) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if result.ScanID == "" {
		t.Error("expected non-empty result scan_id")
	}
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}

	if events[0].Type != "started" {
		t.Errorf("expected first event type %q, got %q", "started", events[0].Type)
	}

	completed := events[len(events)-1]
	if completed.Type != "completed" {
		t.Errorf("expected last event type %q, got %q", "completed", completed.Type)
	}
	if completed.ScanID == "" {
		t.Error("expected completed event scan_id non-empty")
	}
	if completed.ScanID != result.ScanID {
		t.Errorf("expected completed event scan_id %q to match result %q", completed.ScanID, result.ScanID)
	}
	if result.FilesScanned != n {
		t.Errorf("expected FilesScanned=%d, got %d", n, result.FilesScanned)
	}

	// Progress events (when present) must sit between started and completed
	// and report monotonically non-decreasing FilesScanned.
	lastProgress := -1
	for _, ev := range events {
		if ev.Type == "progress" {
			if ev.FilesScanned < lastProgress {
				t.Fatalf("progress FilesScanned not monotonically non-decreasing: %+v", events)
			}
			lastProgress = ev.FilesScanned
		}
		if ev.Type != "progress" && ev.Type != "started" && ev.Type != "completed" {
			t.Errorf("unexpected event type %q", ev.Type)
		}
	}
}
