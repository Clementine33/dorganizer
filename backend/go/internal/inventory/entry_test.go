package inventory_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/filesystem"
	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/admission"
	"github.com/onsei/organizer/backend/internal/inventory"
)

// fakeScanStore answers the one question the scanning entry asks storage:
// whether an execution is running against a root.
type fakeScanStore struct {
	executing bool
	err       error
	asked     []string
}

func (f *fakeScanStore) HasActiveExecutionForRoot(rootPath string) (bool, error) {
	f.asked = append(f.asked, rootPath)
	return f.executing, f.err
}

// newEntry wires the scanning entry over a real pipeline and a real store, the
// way the process does, and returns what the entry recorded on the library row.
func newEntry(t *testing.T, store inventory.Store) (inventory.Service, *[]string) {
	t.Helper()
	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "entry.db"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	var recorded []string
	svc := inventory.NewService(
		inventory.NewPipeline(
			sqlite.NewScanStaging(repo),
			filesystem.WalkRootEntriesParallel,
			filesystem.WalkFolderEntries,
		),
		store,
		nil,
		func(_, status, message string, _ time.Time) error {
			recorded = append(recorded, status+":"+message)
			return nil
		},
	)
	return svc, &recorded
}

// TestScanValidatesTheRoot pins the validation contract: a missing root, a
// non-directory root and an empty root are told apart by code, and each answers
// exactly one terminal error event before returning.
func TestScanValidatesTheRoot(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "somefile.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	svc, recorded := newEntry(t, &fakeScanStore{})

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
			var events []inventory.Event
			_, err := svc.Scan(
				context.Background(),
				inventory.Request{LibraryID: "lib-1", RootPath: tc.root},
				func(ev inventory.Event) {
					events = append(events, ev)
				},
			)
			scanErr, ok := inventory.AsError(err)
			if !ok {
				t.Fatalf("expected *inventory.Error, got %T: %v", err, err)
			}
			if scanErr.Code != tc.code {
				t.Errorf("code = %q, want %q", scanErr.Code, tc.code)
			}
			if len(events) != 1 || events[0].Type != "error" {
				t.Errorf("expected a single error event, got %+v", events)
			}
		})
	}
	if len(*recorded) != len(cases) {
		t.Fatalf("library records = %v, want one per refused scan", *recorded)
	}
	for _, rec := range *recorded {
		if rec[:len("failed")] != "failed" {
			t.Errorf("a refused scan recorded %q, want a failed outcome", rec)
		}
	}
}

// TestScanEmitsEventsAndRecordsTheOutcome covers the happy path: started ->
// progress -> completed in that order, a result that matches the events, and a
// completed outcome on the library row.
func TestScanEmitsEventsAndRecordsTheOutcome(t *testing.T) {
	const n = 10
	dir := t.TempDir()
	for i := range n {
		if err := os.WriteFile(filepath.Join(dir, "song"+string(rune('a'+i))+".wav"), []byte("x"), 0644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	svc, recorded := newEntry(t, &fakeScanStore{})

	var events []inventory.Event
	result, err := svc.Scan(
		context.Background(),
		inventory.Request{LibraryID: "lib-1", RootPath: dir},
		func(ev inventory.Event) {
			events = append(events, ev)
		},
	)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if len(events) < 2 || events[0].Type != "started" || events[len(events)-1].Type != "completed" {
		t.Fatalf("events = %+v, want started ... completed", events)
	}
	if result.ScanID == "" || events[len(events)-1].ScanID != result.ScanID {
		t.Fatalf(
			"the completed event must carry the result's scan id (%+v vs %q)",
			events[len(events)-1],
			result.ScanID,
		)
	}
	if result.FilesScanned != n {
		t.Errorf("files scanned = %d, want %d", result.FilesScanned, n)
	}
	if len(*recorded) != 1 || (*recorded)[0] != "completed:" {
		t.Fatalf("library records = %v, want one completed outcome", *recorded)
	}
}

// TestAdmissionChecksAreNotSharedAcrossScanKinds pins the difference the two
// scan entries have: a full scan refuses while an execution is running (it
// rewrites the inventory that execution validates against), while a member
// refresh never asks — it reads one subtree, and the page refresh route must
// keep working during an execution.
func TestAdmissionChecksAreNotSharedAcrossScanKinds(t *testing.T) {
	store := &fakeScanStore{executing: true}
	svc, _ := newEntry(t, store)

	_, err := svc.AdmitLibraryScan("/music")
	scanErr, ok := inventory.AsError(err)
	if !ok || scanErr.Code != "EXECUTION_IN_PROGRESS" {
		t.Fatalf("full scan admission = %v, want EXECUTION_IN_PROGRESS while an execution runs", err)
	}

	release, err := svc.AdmitMemberRefresh()
	if err != nil {
		t.Fatalf("a member refresh must be admitted during an execution: %v", err)
	}
	release()
	if len(store.asked) != 1 {
		t.Fatalf("store asked %v, want the execution check for the full scan only", store.asked)
	}
}

// TestAdmissionRefusalPropagatesTheGateError pins two things at once: two scans
// may run together (the gate counts them and only excludes file management and
// maintenance), and while direct file management holds the slot the refusal
// crosses the seam as the gate's own type — the HTTP layer maps it to 409 BUSY,
// which only works while it is not wrapped in a scan error.
func TestAdmissionRefusalPropagatesTheGateError(t *testing.T) {
	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatalf("open repository: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	gate := admission.NewGate(nil)
	svc := inventory.NewService(
		inventory.NewPipeline(
			sqlite.NewScanStaging(repo),
			filesystem.WalkRootEntriesParallel,
			filesystem.WalkFolderEntries,
		),
		&fakeScanStore{},
		gate,
		nil,
	)

	// Two scans run beside each other: the gate counts them and excludes only
	// file management and maintenance.
	first, err := svc.AdmitMemberRefresh()
	if err != nil {
		t.Fatalf("first refresh admission: %v", err)
	}
	second, err := svc.AdmitMemberRefresh()
	if err != nil {
		t.Fatalf("a second scan runs beside the first: %v", err)
	}

	// While file management holds the exclusive slot every scan is refused, and
	// the refusal keeps the gate's type: the HTTP layer maps it to 409 BUSY,
	// which only works while it is not wrapped in a scan error.
	if _, busyErr := gate.BeginManual(); busyErr == nil {
		t.Fatal("file management must wait for the running scans")
	}
	first()
	second()

	manual, err := gate.BeginManual()
	if err != nil {
		t.Fatalf("take the file-management slot once the scans released it: %v", err)
	}
	defer manual()

	if _, err = svc.AdmitMemberRefresh(); !admission.IsBusy(err) {
		t.Fatalf("a refresh while file management runs must be refused as busy, got %v", err)
	}
	if _, err = svc.AdmitLibraryScan("/music"); !admission.IsBusy(err) {
		t.Fatalf("a library scan while file management runs must be refused as busy, got %v", err)
	}
}

// TestRefreshMemberValidatesItsPaths keeps the refresh's own validation
// contract: it reports what is wrong with the member path instead of scanning
// whatever it was handed.
func TestRefreshMemberValidatesItsPaths(t *testing.T) {
	svc, _ := newEntry(t, &fakeScanStore{})
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cases := []struct {
		name   string
		folder string
		root   string
		code   string
	}{
		{name: "missing-args", folder: "", root: "", code: "MEMBER_PATH_REQUIRED"},
		{name: "missing", folder: filepath.Join(dir, "nope"), root: dir, code: "MEMBER_PATH_NOT_FOUND"},
		{name: "file", folder: filePath, root: dir, code: "MEMBER_PATH_NOT_DIRECTORY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.RefreshMember(context.Background(), tc.folder, tc.root)
			scanErr, ok := inventory.AsError(err)
			if !ok {
				t.Fatalf("expected *inventory.Error, got %T: %v", err, err)
			}
			if scanErr.Code != tc.code {
				t.Errorf("code = %q, want %q", scanErr.Code, tc.code)
			}
			if errors.Is(err, context.Canceled) {
				t.Error("a refused refresh is not a cancellation")
			}
		})
	}
}
