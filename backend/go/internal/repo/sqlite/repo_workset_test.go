package sqlite //nolint:testpackage // white-box tests exercise unexported internals

import (
	"errors"
	"testing"
	"time"
)

// newOperationFixture builds one workset with its conversion operation and
// seeded sparse draft for repository-level tests.
func newOperationFixture(
	t *testing.T,
	libID, title string,
) (*Workset, []WorksetMember, Operation, OperationDraft) {
	t.Helper()
	now := time.Now()
	w := &Workset{
		ID:          "ws-test-" + title,
		Title:       title,
		LibraryID:   libID,
		RootPath:    "/music",
		RootPathKey: "/music",
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	members := []WorksetMember{
		{
			WorksetID:   w.ID,
			MemberID:    "m-test-a-" + title,
			MemberIndex: 0,
			RelPath:     "albumA",
			FolderID:    "f-a",
			FolderPath:  "/music/albumA",
			FolderName:  "albumA",
		},
		{
			WorksetID:   w.ID,
			MemberID:    "m-test-b-" + title,
			MemberIndex: 1,
			RelPath:     "albumB",
			FolderID:    "f-b",
			FolderPath:  "/music/albumB",
			FolderName:  "albumB",
		},
	}
	op := Operation{
		WorksetID:     w.ID,
		OperationType: "conversion",
		Version:       1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	draft := OperationDraft{
		WorksetID:     w.ID,
		OperationType: "conversion",
		SchemaVersion: 1,
		DraftJSON:     `{"schema_version":1,"matched":{},"unmatched":{}}`,
		DraftHash:     "hash-1",
		UpdatedAt:     now,
	}
	return w, members, op, draft
}

func insertLibrary(t *testing.T, repo *Repository, id string) {
	t.Helper()
	now := time.Now().Format(timeFormat)
	_, err := repo.db.Exec(`
		INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, "lib-"+id, "/music", "/music", now, now)
	if err != nil {
		t.Fatalf("insert library: %v", err)
	}
}

func TestWorksetCRUD(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, op, draft := newOperationFixture(t, "lib-1", "crud")
	if err := repo.CreateWorkset(w, members, []Operation{op}, []OperationDraft{draft}); err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}

	got, err := repo.GetWorkset(w.ID)
	if err != nil {
		t.Fatalf("GetWorkset: %v", err)
	}
	if got.Title != "crud" || got.Version != 1 || got.LibraryID != "lib-1" {
		t.Fatalf("workset: %+v", got)
	}

	gotMembers, err := repo.ListWorksetMembers(w.ID)
	if err != nil {
		t.Fatalf("ListWorksetMembers: %v", err)
	}
	if len(gotMembers) != 2 || gotMembers[0].MemberID != "m-test-a-crud" {
		t.Fatalf("members: %+v", gotMembers)
	}

	// The operation and its draft exist from the same commit.
	gotOp, err := repo.GetOperation(w.ID, "conversion")
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if gotOp.Version != 1 || gotOp.CurrentRevisionID != "" {
		t.Fatalf("operation: %+v", gotOp)
	}
	gotDraft, err := repo.GetOperationDraft(w.ID, "conversion")
	if err != nil || gotDraft == nil {
		t.Fatalf("GetOperationDraft: %+v err=%v", gotDraft, err)
	}
	if gotDraft.DraftHash != "hash-1" {
		t.Fatalf("draft: %+v", gotDraft)
	}
	ops, err := repo.ListOperations(w.ID)
	if err != nil || len(ops) != 1 {
		t.Fatalf("ListOperations: %+v err=%v", ops, err)
	}

	if _, err := repo.GetOperation(w.ID, "rename"); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("unknown operation type must be not-found, got %v", err)
	}
}

func TestCreateWorksetIdempotency(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, op, draft := newOperationFixture(t, "lib-1", "idem")
	w.CreationIdemKey = "key-1"
	if err := repo.CreateWorkset(w, members, []Operation{op}, []OperationDraft{draft}); err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	replayed, err := repo.GetWorksetByCreationIdemKey("key-1")
	if err != nil || replayed == nil || replayed.ID != w.ID {
		t.Fatalf("idempotency lookup: %+v err=%v", replayed, err)
	}

	w2, members2, op2, draft2 := newOperationFixture(t, "lib-1", "idem2")
	w2.CreationIdemKey = "key-1"
	if err := repo.CreateWorkset(
		w2,
		members2,
		[]Operation{op2},
		[]OperationDraft{draft2},
	); !errors.Is(
		err,
		ErrWorksetIdemConflict,
	) {
		t.Fatalf("duplicate creation key must conflict, got %v", err)
	}

	if _, err := repo.GetWorksetByCreationIdemKey("missing"); err != nil {
		t.Fatalf("missing key must return nil, got %v", err)
	}
}

func TestClearExpiredWorksetIdemKey(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, op, draft := newOperationFixture(t, "lib-1", "expire")
	w.CreationIdemKey = "key-old"
	w.CreatedAt = time.Now().Add(-60 * 24 * time.Hour)
	w.UpdatedAt = w.CreatedAt
	if err := repo.CreateWorkset(w, members, []Operation{op}, []OperationDraft{draft}); err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	if err := repo.ClearExpiredWorksetIdemKey(w.ID, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatalf("ClearExpiredWorksetIdemKey: %v", err)
	}
	cleared, err := repo.GetWorkset(w.ID)
	if err != nil {
		t.Fatalf("GetWorkset: %v", err)
	}
	if cleared.CreationIdemKey != "" {
		t.Fatalf("expired key not cleared: %q", cleared.CreationIdemKey)
	}
}

func TestWorksetVersionGuard(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, op, draft := newOperationFixture(t, "lib-1", "guard")
	if err := repo.CreateWorkset(w, members, []Operation{op}, []OperationDraft{draft}); err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	if err := repo.RenameWorkset(w.ID, "新名字", 1, time.Now()); err != nil {
		t.Fatalf("RenameWorkset: %v", err)
	}
	renamed, _ := repo.GetWorkset(w.ID)
	if renamed.Title != "新名字" || renamed.Version != 2 {
		t.Fatalf("rename result: %+v", renamed)
	}
	if err := repo.RenameWorkset(w.ID, "又改名", 1, time.Now()); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale rename must conflict, got %v", err)
	}
	if err := repo.RenameWorkset("ws-missing", "x", 1, time.Now()); !errors.Is(err, ErrWorksetNotFound) {
		t.Fatalf("missing workset must be not-found, got %v", err)
	}
	// A rename never advanced the operation version.
	after, _ := repo.GetOperation(w.ID, "conversion")
	if after.Version != 1 {
		t.Fatalf("rename advanced the operation version: %d", after.Version)
	}
}

func TestOperationDraftSaveVersionGuard(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, op, draft := newOperationFixture(t, "lib-1", "draft")
	if err := repo.CreateWorkset(w, members, []Operation{op}, []OperationDraft{draft}); err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	if err := repo.SaveOperationDraft(w.ID, "conversion", 1, `{"a":1}`, "hash-2", 1, time.Now()); err != nil {
		t.Fatalf("SaveOperationDraft: %v", err)
	}
	got, _ := repo.GetOperation(w.ID, "conversion")
	if got.Version != 2 {
		t.Fatalf("operation version after save = %d, want 2", got.Version)
	}
	saved, _ := repo.GetOperationDraft(w.ID, "conversion")
	if saved.DraftJSON != `{"a":1}` || saved.DraftHash != "hash-2" {
		t.Fatalf("draft after save: %+v", saved)
	}
	if err := repo.SaveOperationDraft(
		w.ID,
		"conversion",
		1,
		`{"a":2}`,
		"hash-3",
		1,
		time.Now(),
	); !errors.Is(
		err,
		ErrVersionConflict,
	) {
		t.Fatalf("stale draft save must conflict, got %v", err)
	}
	if err := repo.SaveOperationDraft(
		w.ID,
		"rename",
		1,
		`{}`,
		"hash",
		1,
		time.Now(),
	); !errors.Is(
		err,
		ErrOperationNotFound,
	) {
		t.Fatalf("unknown operation must be not-found, got %v", err)
	}
}

//nolint:funlen // full revision lifecycle in one database fixture
func TestOperationRevisionLifecycle(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, op, draft := newOperationFixture(t, "lib-1", "rev")
	if err := repo.CreateWorkset(w, members, []Operation{op}, []OperationDraft{draft}); err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}

	now := time.Now()
	g := &PlanGeneration{
		GenerationID:   "gen-1",
		WorksetID:      w.ID,
		OperationType:  "conversion",
		IdempotencyKey: "idem-gen-1",
		RequestHash:    "req-hash-1",
		TotalRoots:     2,
		CreatedAt:      now,
	}
	if err := repo.CreateGeneration(g); err != nil {
		t.Fatalf("CreateGeneration: %v", err)
	}

	err := repo.PersistOperationRevision(g.GenerationID, w.ID, "conversion", now, OperationRevisionPersist{
		PlanID:           "plan-1",
		RootPath:         "/music/albumA + /music/albumB",
		SnapshotToken:    "snap-1",
		LibraryID:        "lib-1",
		DraftHash:        "hash-1",
		MemberHash:       "members-1",
		OperationVersion: 1,
		ExcludedScope:    "m-test-b-rev",
		DraftSnapshot:    `{"schema_version":1}`,
		Steps: []PlanStepRecord{{
			StepIndex: 0, StepType: "reconcile_audio_outputs", Status: "ok",
		}},
		Roots: []PlanRootRecord{
			{
				RootIndex:            0,
				RootPath:             "/music/albumA",
				RootIdentity:         "albumA",
				InventoryFingerprint: "fp-a",
				EntryCount:           2,
			},
			{
				RootIndex:            1,
				RootPath:             "/music/albumB",
				RootIdentity:         "albumB",
				InventoryFingerprint: "fp-b",
				EntryCount:           1,
			},
		},
	})
	if err != nil {
		t.Fatalf("PersistOperationRevision: %v", err)
	}

	genGot, _ := repo.GetGeneration("gen-1")
	if genGot.Status != GenStatusCompleted || genGot.RevisionID != "plan-1" {
		t.Fatalf("generation: %+v", genGot)
	}
	// Promotion advances the OPERATION version, never the workset metadata one.
	opAfter, _ := repo.GetOperation(w.ID, "conversion")
	if opAfter.CurrentRevisionID != "plan-1" || opAfter.Version != 2 {
		t.Fatalf("operation after promote: %+v", opAfter)
	}
	wsAfter, _ := repo.GetWorkset(w.ID)
	if wsAfter.Version != 1 {
		t.Fatalf("revision promotion must not advance the workset version: %d", wsAfter.Version)
	}

	rev, err := repo.GetOperationRevision(w.ID, "conversion", "plan-1")
	if err != nil {
		t.Fatalf("GetOperationRevision: %v", err)
	}
	if rev.RevisionIndex != 1 || rev.DraftHash != "hash-1" || rev.ExcludedScope != "m-test-b-rev" ||
		rev.DraftSnapshot != `{"schema_version":1}` || rev.OperationVersion != 1 {
		t.Fatalf("revision: %+v", rev)
	}
	if _, revErr := repo.GetOperationRevision(w.ID, "rename", "plan-1"); !errors.Is(revErr, ErrRevisionNotFound) {
		t.Fatalf("unknown operation type must not resolve a revision, got %v", err)
	}

	// A second revision gets index 2.
	g2 := &PlanGeneration{
		GenerationID:  "gen-2",
		WorksetID:     w.ID,
		OperationType: "conversion",
		CreatedAt:     now.Add(time.Second),
	}
	if createErr := repo.CreateGeneration(g2); createErr != nil {
		t.Fatalf("CreateGeneration 2: %v", createErr)
	}
	if persistErr := repo.PersistOperationRevision(
		g2.GenerationID,
		w.ID,
		"conversion",
		now.Add(time.Second),
		OperationRevisionPersist{
			PlanID:           "plan-2",
			RootPath:         "/music/albumA",
			SnapshotToken:    "snap-2",
			LibraryID:        "lib-1",
			DraftHash:        "hash-2",
			MemberHash:       "members-1",
			OperationVersion: 2,
			Steps:            []PlanStepRecord{{StepIndex: 0, StepType: "reconcile_audio_outputs", Status: "ok"}},
			Roots: []PlanRootRecord{
				{
					RootIndex:            0,
					RootPath:             "/music/albumA",
					RootIdentity:         "albumA",
					InventoryFingerprint: "fp-a2",
					EntryCount:           3,
				},
			},
		},
	); persistErr != nil {
		t.Fatalf("PersistOperationRevision 2: %v", persistErr)
	}
	rev2, _ := repo.GetOperationRevision(w.ID, "conversion", "plan-2")
	if rev2.RevisionIndex != 2 {
		t.Fatalf("revision 2 index = %d, want 2", rev2.RevisionIndex)
	}

	revs, err := repo.ListOperationRevisions(w.ID, "conversion", 0, 10)
	if err != nil {
		t.Fatalf("ListOperationRevisions: %v", err)
	}
	if len(revs) != 2 || revs[0].PlanID != "plan-2" || revs[1].PlanID != "plan-1" {
		t.Fatalf("revision history: %+v", revs)
	}
	page, err := repo.ListOperationRevisions(w.ID, "conversion", 2, 10)
	if err != nil || len(page) != 1 || page[0].PlanID != "plan-1" {
		t.Fatalf("revision page: %+v err=%v", page, err)
	}
}

func TestGenerationIdempotencyAndCancel(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, op, draft := newOperationFixture(t, "lib-1", "gen")
	if err := repo.CreateWorkset(w, members, []Operation{op}, []OperationDraft{draft}); err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	now := time.Now()
	gen := &PlanGeneration{
		GenerationID:   "gen-a",
		WorksetID:      w.ID,
		OperationType:  "conversion",
		IdempotencyKey: "same-key",
		RequestHash:    "hash-a",
		TotalRoots:     2,
		CreatedAt:      now,
	}
	if err := repo.CreateGeneration(gen); err != nil {
		t.Fatalf("CreateGeneration: %v", err)
	}
	// The same key is free for another operation of the same workset.
	other := &PlanGeneration{
		GenerationID:   "gen-other-op",
		WorksetID:      w.ID,
		OperationType:  "rename",
		IdempotencyKey: "same-key",
		RequestHash:    "hash-b",
		CreatedAt:      now,
	}
	if err := repo.CreateGeneration(other); err != nil {
		t.Fatalf("a different operation must be able to reuse the key: %v", err)
	}
	// Reusing it inside the same operation conflicts.
	dup := &PlanGeneration{
		GenerationID:   "gen-b",
		WorksetID:      w.ID,
		OperationType:  "conversion",
		IdempotencyKey: "same-key",
		CreatedAt:      now,
	}
	if err := repo.CreateGeneration(dup); !errors.Is(err, ErrGenerationIdemConflict) {
		t.Fatalf("duplicate key in one operation must conflict, got %v", err)
	}

	found, err := repo.GetGenerationByOperationKey(w.ID, "conversion", "same-key")
	if err != nil || found == nil || found.GenerationID != "gen-a" {
		t.Fatalf("key lookup: %+v err=%v", found, err)
	}
	if miss, keyErr := repo.GetGenerationByOperationKey(w.ID, "rename", "other-key"); keyErr != nil || miss != nil {
		t.Fatalf("unknown key must return nil: %+v err=%v", miss, keyErr)
	}

	// One active session per operation; the other operation is unaffected.
	active, err := repo.GetActiveGenerationForOperation(w.ID, "conversion")
	if err != nil || active == nil || active.GenerationID != "gen-a" {
		t.Fatalf("active session: %+v err=%v", active, err)
	}
	if err := repo.CancelGeneration("gen-a"); err != nil {
		t.Fatalf("CancelGeneration: %v", err)
	}
	canceled, _ := repo.GetGeneration("gen-a")
	if canceled.Status != GenStatusCanceled {
		t.Fatalf("canceled status: %q", canceled.Status)
	}
	if remaining, _ := repo.GetActiveGenerationForOperation(w.ID, "conversion"); remaining != nil {
		t.Fatalf("canceled session still active: %+v", remaining)
	}
	latest, _ := repo.LatestGenerationForOperation(w.ID, "conversion")
	if latest == nil || latest.GenerationID != "gen-a" {
		t.Fatalf("latest session: %+v", latest)
	}
	// Cancel is idempotent on a terminal row.
	if err := repo.CancelGeneration("gen-a"); err != nil {
		t.Fatalf("second cancel: %v", err)
	}
}

func TestGenerationFailureAndInterrupt(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, op, draft := newOperationFixture(t, "lib-1", "fail")
	if err := repo.CreateWorkset(w, members, []Operation{op}, []OperationDraft{draft}); err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	now := time.Now()
	gen := &PlanGeneration{GenerationID: "gen-fail", WorksetID: w.ID, OperationType: "conversion", CreatedAt: now}
	if err := repo.CreateGeneration(gen); err != nil {
		t.Fatalf("CreateGeneration: %v", err)
	}
	if err := repo.MarkGenerationFailed("gen-fail", "GENERATION_FAILED", "boom"); err != nil {
		t.Fatalf("MarkGenerationFailed: %v", err)
	}
	failed, _ := repo.GetGeneration("gen-fail")
	if failed.Status != GenStatusFailed || failed.ErrorCode != "GENERATION_FAILED" {
		t.Fatalf("failed generation: %+v", failed)
	}
	// A failed session is not an active one.
	if active, _ := repo.GetActiveGenerationForOperation(w.ID, "conversion"); active != nil {
		t.Fatalf("failed session still active: %+v", active)
	}

	// Startup interruption releases stale queued/running rows only.
	stale := &PlanGeneration{GenerationID: "gen-stale", WorksetID: w.ID, OperationType: "conversion", CreatedAt: now}
	if err := repo.CreateGeneration(stale); err != nil {
		t.Fatalf("CreateGeneration stale: %v", err)
	}
	if err := repo.InterruptStaleGenerations(); err != nil {
		t.Fatalf("InterruptStaleGenerations: %v", err)
	}
	interrupted, _ := repo.GetGeneration("gen-stale")
	if interrupted.Status != GenStatusInterrupted {
		t.Fatalf("stale session status: %q", interrupted.Status)
	}
	stillFailed, _ := repo.GetGeneration("gen-fail")
	if stillFailed.Status != GenStatusFailed {
		t.Fatalf("terminal row was touched: %q", stillFailed.Status)
	}
}
