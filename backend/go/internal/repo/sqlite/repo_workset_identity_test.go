package sqlite //nolint:testpackage // white-box tests exercise unexported internals

import (
	"path/filepath"
	"testing"
	"time"
)

// TestMemberIdentityIsStableAcrossReopens pins the identity contract member
// navigation rests on: MemberID is assigned once at creation and never
// regenerated, and RelPath is the durable library-relative path, so reopening
// the database (or rescanning the library) cannot renumber what the workbench
// addresses.
func TestMemberIdentityIsStableAcrossReopens(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "identity.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}
	defer repo.Close()
	insertLibrary(t, repo, "lib-1")

	w, members, op, draft := newOperationFixture(t, "lib-1", "identity")
	if createErr := repo.ReplaceCurrentWorkset(
		w,
		members,
		[]Operation{op},
		[]OperationDraft{draft},
		"",
	); createErr != nil {
		t.Fatalf("ReplaceCurrentWorkset: %v", createErr)
	}
	first, err := repo.ListWorksetMembers(w.ID)
	if err != nil {
		t.Fatalf("ListWorksetMembers: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("members: %+v", first)
	}
	for _, m := range first {
		if m.MemberID == "" {
			t.Fatalf("member without identity: %+v", m)
		}
	}
	if first[0].MemberID == first[1].MemberID {
		t.Fatalf("member identities collide: %q", first[0].MemberID)
	}

	if closeErr := repo.Close(); closeErr != nil {
		t.Fatalf("close repo: %v", closeErr)
	}
	reopened, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	second, err := reopened.ListWorksetMembers(w.ID)
	if err != nil {
		t.Fatalf("reread members: %v", err)
	}
	if len(second) != len(first) {
		t.Fatalf("member count changed on reopen: %d -> %d", len(first), len(second))
	}
	for i := range first {
		if first[i].MemberID != second[i].MemberID {
			t.Fatalf("identity changed on reopen: %q -> %q", first[i].MemberID, second[i].MemberID)
		}
		if first[i].RelPath != second[i].RelPath {
			t.Fatalf("member order changed on reopen: %q -> %q", first[i].RelPath, second[i].RelPath)
		}
	}
}

// TestReplaceCurrentWorksetIsTheOnlyRecordForAPair pins the storage-level
// uniqueness of one record per (library, operation): the second write for the
// same pair must replace the first, not add another row.
func TestReplaceCurrentWorksetIsTheOnlyRecordForAPair(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	first, members, op, draft := newOperationFixture(t, "lib-1", "first")
	if err := repo.ReplaceCurrentWorkset(first, members, []Operation{op}, []OperationDraft{draft}, ""); err != nil {
		t.Fatalf("first record: %v", err)
	}
	second, members2, op2, draft2 := newOperationFixture(t, "lib-1", "second")
	if err := repo.ReplaceCurrentWorkset(
		second,
		members2,
		[]Operation{op2},
		[]OperationDraft{draft2},
		first.ID,
	); err != nil {
		t.Fatalf("replacement: %v", err)
	}

	current, err := repo.GetCurrentWorkset("lib-1", "conversion")
	if err != nil {
		t.Fatalf("GetCurrentWorkset: %v", err)
	}
	if current == nil || current.ID != second.ID {
		t.Fatalf("current record = %+v, want the replacement", current)
	}
	if _, gone := repo.GetWorkset(first.ID); gone == nil {
		t.Fatal("the replaced record must be gone, not orphaned")
	}
	var rows int
	if countErr := repo.DB().QueryRow(
		"SELECT COUNT(*) FROM worksets WHERE library_id = 'lib-1'",
	).Scan(&rows); countErr != nil {
		t.Fatalf("count records: %v", countErr)
	}
	if rows != 1 {
		t.Fatalf("expected exactly one record per (library, operation), got %d", rows)
	}
}

// TestReplaceCurrentWorksetRefusesAStaleExpectedRecord is the concurrent-replace
// contract: a caller that saw another record as current must not overwrite the
// one that replaced it.
func TestReplaceCurrentWorksetRefusesAStaleExpectedRecord(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	first, members, op, draft := newOperationFixture(t, "lib-1", "first")
	if err := repo.ReplaceCurrentWorkset(first, members, []Operation{op}, []OperationDraft{draft}, ""); err != nil {
		t.Fatalf("first record: %v", err)
	}
	second, members2, op2, draft2 := newOperationFixture(t, "lib-1", "second")
	// The caller still believes the library has no record: refuse.
	err := repo.ReplaceCurrentWorkset(second, members2, []Operation{op2}, []OperationDraft{draft2}, "")
	if err == nil {
		t.Fatal("a stale expected record must refuse the write")
	}
	current, _ := repo.GetCurrentWorkset("lib-1", "conversion")
	if current == nil || current.ID != first.ID {
		t.Fatalf("the newer record was overwritten: %+v", current)
	}
}

// TestReplaceCurrentWorksetRefusesABusyRecord keeps a queued session safe: a
// replacement must not strand a planning session that is about to run against
// the record being removed.
func TestReplaceCurrentWorksetRefusesABusyRecord(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	first, members, op, draft := newOperationFixture(t, "lib-1", "busy")
	if err := repo.ReplaceCurrentWorkset(first, members, []Operation{op}, []OperationDraft{draft}, ""); err != nil {
		t.Fatalf("first record: %v", err)
	}
	seedGeneration(t, repo, "gen-busy", first.ID)
	if busy, err := repo.HasActiveSession(); err != nil || !busy {
		t.Fatalf("HasActiveSession = %v, %v; want true", busy, err)
	}

	second, members2, op2, draft2 := newOperationFixture(t, "lib-1", "busy2")
	err := repo.ReplaceCurrentWorkset(second, members2, []Operation{op2}, []OperationDraft{draft2}, first.ID)
	if err == nil {
		t.Fatal("a record with a queued session must refuse the replacement")
	}
	if current, _ := repo.GetCurrentWorkset("lib-1", "conversion"); current == nil || current.ID != first.ID {
		t.Fatalf("the busy record was replaced anyway: %+v", current)
	}
}

// seedGeneration queues one planning session for a record.
func seedGeneration(t *testing.T, repo *Repository, genID, worksetID string) {
	t.Helper()
	now := time.Now()
	if err := repo.CreateGeneration(&PlanGeneration{
		GenerationID:  genID,
		WorksetID:     worksetID,
		OperationType: "conversion",
		Status:        GenStatusQueued,
		CreatedAt:     now,
	}); err != nil {
		t.Fatalf("CreateGeneration: %v", err)
	}
}
