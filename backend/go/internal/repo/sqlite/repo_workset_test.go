package sqlite

import (
	"errors"
	"testing"
	"time"
)

func newWorksetFixture(t *testing.T, libID, title string) (*Workset, []WorksetMember, WorksetDraft) {
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
			MemberIndex: 0,
			RelPath:     "albumA",
			FolderID:    "f-a",
			FolderPath:  "/music/albumA",
			FolderName:  "albumA",
		},
		{
			WorksetID:   w.ID,
			MemberIndex: 1,
			RelPath:     "albumB",
			FolderID:    "f-b",
			FolderPath:  "/music/albumB",
			FolderName:  "albumB",
		},
	}
	draft := WorksetDraft{
		WorksetID:             w.ID,
		WorkflowSchemaVersion: 1,
		StepsJSON:             `[{"step_type":"reconcile_audio_outputs"}]`,
		DraftHash:             "hash-1",
		UpdatedAt:             now,
	}
	return w, members, draft
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

	w, members, draft := newWorksetFixture(t, "lib-1", "one")
	if err := repo.CreateWorkset(w, members, draft); err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}

	got, err := repo.GetWorkset(w.ID)
	if err != nil {
		t.Fatalf("GetWorkset: %v", err)
	}
	if got.Title != "one" || got.Version != 1 || got.LibraryID != "lib-1" {
		t.Fatalf("unexpected workset: %+v", got)
	}

	membersGot, err := repo.ListWorksetMembers(w.ID)
	if err != nil {
		t.Fatalf("ListWorksetMembers: %v", err)
	}
	if len(membersGot) != 2 || membersGot[0].RelPath != "albumA" || membersGot[1].RelPath != "albumB" {
		t.Fatalf("unexpected members: %+v", membersGot)
	}

	draftGot, err := repo.GetWorksetDraft(w.ID)
	if err != nil || draftGot == nil {
		t.Fatalf("GetWorksetDraft: %v %v", draftGot, err)
	}
	if draftGot.DraftHash != "hash-1" {
		t.Fatalf("draft hash = %q", draftGot.DraftHash)
	}
}

func TestCreateWorksetIdempotency(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w1, members, draft := newWorksetFixture(t, "lib-1", "dup")
	w1.CreationIdemKey = "idem-1"
	if err := repo.CreateWorkset(w1, members, draft); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Same key on a second workset must conflict.
	w2, members2, draft2 := newWorksetFixture(t, "lib-1", "dup2")
	w2.CreationIdemKey = "idem-1"
	if err := repo.CreateWorkset(w2, members2, draft2); !errors.Is(err, ErrWorksetIdemConflict) {
		t.Fatalf("second create err = %v, want ErrWorksetIdemConflict", err)
	}

	// Replay by key returns the original workset.
	got, err := repo.GetWorksetByCreationIdemKey("idem-1")
	if err != nil || got == nil {
		t.Fatalf("replay by key: %v %v", got, err)
	}
	if got.ID != w1.ID {
		t.Fatalf("replay returned %q, want %q", got.ID, w1.ID)
	}
}

func TestClearExpiredWorksetIdemKey(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, draft := newWorksetFixture(t, "lib-1", "expire")
	w.CreationIdemKey = "idem-old"
	if err := repo.CreateWorkset(w, members, draft); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Backdate the workset beyond the 30-day window.
	old := time.Now().Add(-31 * 24 * time.Hour)
	if _, err := repo.db.Exec(
		"UPDATE worksets SET created_at = ? WHERE id = ?",
		old.Format(timeFormat),
		w.ID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	if err := repo.ClearExpiredWorksetIdemKey(w.ID, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatalf("ClearExpiredWorksetIdemKey: %v", err)
	}
	got, err := repo.GetWorksetByCreationIdemKey("idem-old")
	if err != nil {
		t.Fatalf("GetWorksetByCreationIdemKey: %v", err)
	}
	if got != nil {
		t.Fatal("expired idem key still resolves")
	}

	// A fresh key inside the window must not be cleared.
	if err := repo.ClearExpiredWorksetIdemKey(w.ID, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatalf("ClearExpiredWorksetIdemKey (fresh): %v", err)
	}
}

func TestWorksetVersionGuard(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, draft := newWorksetFixture(t, "lib-1", "vguard")
	if err := repo.CreateWorkset(w, members, draft); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Stale version must conflict.
	if err := repo.UpdateWorksetTitle(w.ID, "renamed", 99, time.Now()); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version err = %v, want ErrVersionConflict", err)
	}

	// Correct version bumps to 2.
	if err := repo.UpdateWorksetTitle(w.ID, "renamed", 1, time.Now()); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, _ := repo.GetWorkset(w.ID)
	if got.Version != 2 || got.Title != "renamed" {
		t.Fatalf("after rename: %+v", got)
	}

	// Draft save bumps version and replaces content atomically.
	if err := repo.UpdateWorksetDraft(w.ID, 1, `[{"step_type":"x"}]`, "hash-2", 2, time.Now()); err != nil {
		t.Fatalf("draft save: %v", err)
	}
	got, _ = repo.GetWorkset(w.ID)
	if got.Version != 3 {
		t.Fatalf("after draft save version = %d, want 3", got.Version)
	}
	d, _ := repo.GetWorksetDraft(w.ID)
	if d.StepsJSON != `[{"step_type":"x"}]` || d.DraftHash != "hash-2" {
		t.Fatalf("draft after save: %+v", d)
	}
}

func TestWorksetListPagination(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	now := time.Now()
	for i := range 3 {
		w, members, draft := newWorksetFixture(t, "lib-1", string(rune('a'+i)))
		w.ID = "ws-list-" + string(rune('a'+i))
		w.CreatedAt = now.Add(time.Duration(i) * time.Second)
		w.UpdatedAt = now.Add(time.Duration(i) * time.Second)
		members[0].WorksetID = w.ID
		members[1].WorksetID = w.ID
		draft.WorksetID = w.ID
		if err := repo.CreateWorkset(w, members, draft); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	page, err := repo.ListWorksets("", "", 2, "", true)
	if err != nil {
		t.Fatalf("ListWorksets: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("page size = %d, want 2", len(page))
	}
	// Newest first (created later = later updated_at).
	if page[0].ID != "ws-list-c" || page[1].ID != "ws-list-b" {
		t.Fatalf("order: %s, %s", page[0].ID, page[1].ID)
	}

	// Next page via keyset cursor.
	next, err := repo.ListWorksets(page[1].UpdatedAt.UTC().Format(timeFormat), page[1].ID, 2, "", true)
	if err != nil {
		t.Fatalf("ListWorksets next: %v", err)
	}
	if len(next) != 1 || next[0].ID != "ws-list-a" {
		t.Fatalf("next page: %+v", next)
	}
}

func TestWorksetRevisionLifecycle(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, draft := newWorksetFixture(t, "lib-1", "rev")
	if err := repo.CreateWorkset(w, members, draft); err != nil {
		t.Fatalf("create: %v", err)
	}

	now := time.Now()
	g := &PlanGeneration{
		GenerationID:   "gen-1",
		WorksetID:      w.ID,
		IdempotencyKey: "idem-gen-1",
		RequestHash:    "req-hash-1",
		TotalRoots:     2,
		CreatedAt:      now,
	}
	if err := repo.CreateGeneration(g); err != nil {
		t.Fatalf("CreateGeneration: %v", err)
	}

	err := repo.PersistWorksetRevision(g.GenerationID, w.ID, now, WorksetRevisionPersist{
		PlanID:          "plan-1",
		RootPath:        "/music/albumA + /music/albumB",
		SnapshotToken:   "snap-1",
		LibraryID:       "lib-1",
		DraftHash:       "hash-1",
		MemberHash:      "members-1",
		WorksetsVersion: 1,
		Steps: []WorkflowStepRecord{{
			StepIndex: 0, StepType: "reconcile_audio_outputs", Status: "ok",
		}},
		Roots: []WorkflowRootRecord{
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
		t.Fatalf("PersistWorksetRevision: %v", err)
	}

	// Generation completed and revision promoted.
	genGot, _ := repo.GetGeneration("gen-1")
	if genGot.Status != GenStatusCompleted || genGot.RevisionID != "plan-1" {
		t.Fatalf("generation: %+v", genGot)
	}
	wsGot, _ := repo.GetWorkset(w.ID)
	if wsGot.CurrentRevisionID != "plan-1" || wsGot.Version != 2 {
		t.Fatalf("workset after promote: %+v", wsGot)
	}

	rev, err := repo.GetWorksetRevision(w.ID, "plan-1")
	if err != nil {
		t.Fatalf("GetWorksetRevision: %v", err)
	}
	if rev.RevisionIndex != 1 || rev.DraftHash != "hash-1" {
		t.Fatalf("revision: %+v", rev)
	}

	// A second revision gets index 2.
	g2 := &PlanGeneration{GenerationID: "gen-2", WorksetID: w.ID, CreatedAt: now.Add(time.Second)}
	if err := repo.CreateGeneration(g2); err != nil {
		t.Fatalf("CreateGeneration 2: %v", err)
	}
	if err := repo.PersistWorksetRevision(g2.GenerationID, w.ID, now.Add(time.Second), WorksetRevisionPersist{
		PlanID:          "plan-2",
		RootPath:        "/music/albumA",
		SnapshotToken:   "snap-2",
		LibraryID:       "lib-1",
		DraftHash:       "hash-2",
		MemberHash:      "members-1",
		WorksetsVersion: 2,
		Steps:           []WorkflowStepRecord{{StepIndex: 0, StepType: "reconcile_audio_outputs", Status: "ok"}},
		Roots: []WorkflowRootRecord{
			{
				RootIndex:            0,
				RootPath:             "/music/albumA",
				RootIdentity:         "albumA",
				InventoryFingerprint: "fp-a2",
				EntryCount:           3,
			},
		},
	}); err != nil {
		t.Fatalf("PersistWorksetRevision 2: %v", err)
	}
	rev2, _ := repo.GetWorksetRevision(w.ID, "plan-2")
	if rev2.RevisionIndex != 2 {
		t.Fatalf("revision 2 index = %d, want 2", rev2.RevisionIndex)
	}

	// History listing newest-first.
	revs, err := repo.ListWorksetRevisions(w.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListWorksetRevisions: %v", err)
	}
	if len(revs) != 2 || revs[0].PlanID != "plan-2" || revs[1].PlanID != "plan-1" {
		t.Fatalf("revision history: %+v", revs)
	}
}

func TestGenerationIdempotencyAndCancel(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, draft := newWorksetFixture(t, "lib-1", "gen")
	if err := repo.CreateWorkset(w, members, draft); err != nil {
		t.Fatalf("create: %v", err)
	}

	now := time.Now()
	g1 := &PlanGeneration{GenerationID: "gen-1", WorksetID: w.ID, IdempotencyKey: "key-1", CreatedAt: now}
	if err := repo.CreateGeneration(g1); err != nil {
		t.Fatalf("create gen: %v", err)
	}

	// Same workset+key conflict.
	g2 := &PlanGeneration{GenerationID: "gen-2", WorksetID: w.ID, IdempotencyKey: "key-1", CreatedAt: now}
	if err := repo.CreateGeneration(g2); !errors.Is(err, ErrGenerationIdemConflict) {
		t.Fatalf("dup key err = %v, want ErrGenerationIdemConflict", err)
	}

	// Same key on a different workset is allowed.
	now2 := time.Now()
	_, err := repo.db.Exec(`
		INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at)
		VALUES ('lib-2', 'lib-2', '/music2', '/music2', ?, ?)
	`, now2.Format(timeFormat), now2.Format(timeFormat))
	if err != nil {
		t.Fatalf("insert library 2: %v", err)
	}
	wOther, membersOther, draftOther := newWorksetFixture(t, "lib-2", "other")
	wOther.ID = "ws-other"
	membersOther[0].WorksetID = wOther.ID
	membersOther[1].WorksetID = wOther.ID
	draftOther.WorksetID = wOther.ID
	if err := repo.CreateWorkset(wOther, membersOther, draftOther); err != nil {
		t.Fatalf("create other: %v", err)
	}
	g3 := &PlanGeneration{GenerationID: "gen-3", WorksetID: wOther.ID, IdempotencyKey: "key-1", CreatedAt: now}
	if err := repo.CreateGeneration(g3); err != nil {
		t.Fatalf("cross-workset same key: %v", err)
	}

	// Queued cancel is synchronous terminal.
	if err := repo.CancelGeneration("gen-1"); err != nil {
		t.Fatalf("CancelGeneration: %v", err)
	}
	got, _ := repo.GetGeneration("gen-1")
	if got.Status != GenStatusCanceled {
		t.Fatalf("after cancel: %+v", got)
	}

	// A running cancel sets the flag; worker completes it.
	claimed, err := repo.NextQueuedGeneration()
	if err != nil {
		t.Fatalf("NextQueuedGeneration: %v", err)
	}
	if claimed == nil || claimed.GenerationID != "gen-3" {
		t.Fatalf("claim = %+v", claimed)
	}
	if err := repo.CancelGeneration("gen-3"); err != nil {
		t.Fatalf("cancel running: %v", err)
	}
	running, _ := repo.GetGeneration("gen-3")
	if running.Status != GenStatusRunning || !running.CancelRequested {
		t.Fatalf("running cancel flag: %+v", running)
	}
	if err := repo.CompleteGenerationCanceled("gen-3"); err != nil {
		t.Fatalf("CompleteGenerationCanceled: %v", err)
	}
	canceled, _ := repo.GetGeneration("gen-3")
	if canceled.Status != GenStatusCanceled {
		t.Fatalf("after worker cancel: %+v", canceled)
	}
}

func TestGenerationFailureAndInterrupt(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	w, members, draft := newWorksetFixture(t, "lib-1", "fail")
	if err := repo.CreateWorkset(w, members, draft); err != nil {
		t.Fatalf("create: %v", err)
	}

	now := time.Now()
	g := &PlanGeneration{GenerationID: "gen-1", WorksetID: w.ID, CreatedAt: now}
	if err := repo.CreateGeneration(g); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.MarkGenerationFailed("gen-1", "ROOT_COLLECT_FAILED", "collect failed"); err != nil {
		t.Fatalf("MarkGenerationFailed: %v", err)
	}
	got, _ := repo.GetGeneration("gen-1")
	if got.Status != GenStatusFailed || got.ErrorCode != "ROOT_COLLECT_FAILED" {
		t.Fatalf("failed gen: %+v", got)
	}

	// Failed rows no longer block a same-key retry (partial index excludes).
	g2 := &PlanGeneration{GenerationID: "gen-2", WorksetID: w.ID, IdempotencyKey: "key-r", CreatedAt: now}
	if err := repo.CreateGeneration(g2); err != nil {
		t.Fatalf("create retry: %v", err)
	}
	if err := repo.InterruptStaleGenerations(); err != nil {
		t.Fatalf("InterruptStaleGenerations: %v", err)
	}
	got2, _ := repo.GetGeneration("gen-2")
	if got2.Status != GenStatusInterrupted {
		t.Fatalf("after interrupt: %+v", got2)
	}
	// gen-1 was already terminal; interrupt must not change it.
	got1, _ := repo.GetGeneration("gen-1")
	if got1.Status != GenStatusFailed {
		t.Fatalf("terminal row mutated: %+v", got1)
	}
}

func TestListPlansExcludesWorksetPlans(t *testing.T) {
	repo := newTestRepository(t)
	insertLibrary(t, repo, "lib-1")

	// Standalone plan.
	if err := repo.CreatePlan(
		&Plan{
			PlanID:                "plan-standalone",
			RootPath:              "/music",
			ScanRootPath:          "/music",
			PlanType:              "workflow",
			SnapshotToken:         "s",
			Status:                "ready",
			PlanKind:              "workflow",
			WorkflowSchemaVersion: 1,
			CreatedAt:             time.Now(),
		},
	); err != nil {
		t.Fatalf("create standalone: %v", err)
	}
	// Workset revision plan (workset_id set directly, association inserted later).
	if _, err := repo.db.Exec(`
		INSERT INTO plans (plan_id, root_path, scan_root_path, plan_type, snapshot_token, status, plan_kind, workflow_schema_version, workset_id, created_at)
		VALUES ('plan-ws', '/music/albumA', '/music/albumA', 'workflow', 's2', 'ready', 'workflow', 1, 'ws-1', ?)
	`, time.Now().Format(timeFormat)); err != nil {
		t.Fatalf("insert workset plan: %v", err)
	}

	plans, err := repo.ListPlans(nil, 100)
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(plans) != 1 || plans[0].PlanID != "plan-standalone" {
		t.Fatalf("standalone list: %+v", plans)
	}
}
