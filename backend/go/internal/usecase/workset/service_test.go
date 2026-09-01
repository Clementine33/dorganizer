package workset //nolint:testpackage // white-box tests exercise unexported internals

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

const timeFmt = "2006-01-02T15:04:05.999999999Z07:00"

// newSvc builds a repo + workset service on a temp DB with a config dir.
// A minimal config.json is written so seeded drafts carry a classifier tag.
func newSvc(t *testing.T, concurrency int) *serviceImpl {
	t.Helper()
	tmp := t.TempDir()
	dbPath := tmp + "/test.db"
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	cfg := `{"prune":{"literal_tags":["SEなし"]}}`
	if err := os.WriteFile(filepath.Join(tmp, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	svc, _ := NewService(repo, tmp, concurrency).(*serviceImpl)
	return svc
}

func insertLibraryRow(t *testing.T, repo *sqlite.Repository, id, name, root string) {
	t.Helper()
	now := time.Now().Format(timeFmt)
	_, err := repo.DB().Exec(`
		INSERT INTO libraries (id, name, root_path, root_path_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, name, root, root, now, now)
	if err != nil {
		t.Fatalf("insert library: %v", err)
	}
}

func insertFolder(t *testing.T, repo *sqlite.Repository, libID, id, path, rel, name string) {
	t.Helper()
	now := time.Now().Format(timeFmt)
	_, err := repo.DB().Exec(`
		INSERT INTO library_folders (id, library_id, path, name, relative_path, audio_file_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?)
	`, id, libID, path, name, rel, now, now)
	if err != nil {
		t.Fatalf("insert folder: %v", err)
	}
}

func TestCreateAndViewWorkset(t *testing.T) {
	svc := newSvc(t, 1)
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-a", "/music/albumA", "albumA", "albumA")

	ctx := context.Background()
	res, err := svc.CreateWorkset(ctx, CreateRequest{
		LibraryID: "lib-1", Title: "夏季整理", FolderIDs: []string{"f-a"}, IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	if !res.Created {
		t.Fatal("expected created=true")
	}
	if res.Workset.PlanningState != PlanningUnplanned {
		t.Fatalf("planning_state = %q, want unplanned", res.Workset.PlanningState)
	}
	if res.Workset.Version != 1 {
		t.Fatalf("version = %d, want 1", res.Workset.Version)
	}
	if len(res.Workset.Members) != 1 || res.Workset.Members[0].State != MemberPending {
		t.Fatalf("members: %+v", res.Workset.Members)
	}

	replay, err := svc.CreateWorkset(ctx, CreateRequest{
		LibraryID: "lib-1", Title: "夏季整理", FolderIDs: []string{"f-a"}, IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Created || replay.Workset.WorksetID != res.Workset.WorksetID {
		t.Fatalf("replay: created=%v id=%s", replay.Created, replay.Workset.WorksetID)
	}
}

func TestCreateWorksetValidation(t *testing.T) {
	svc := newSvc(t, 1)
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-a", "/music/albumA", "albumA", "albumA")
	ctx := context.Background()

	if _, err := svc.CreateWorkset(
		ctx,
		CreateRequest{LibraryID: "lib-1", Title: "", FolderIDs: []string{"f-a"}},
	); err == nil {
		t.Fatal("empty title should fail")
	}
	if _, err := svc.CreateWorkset(
		ctx,
		CreateRequest{LibraryID: "lib-1", Title: "ok", FolderIDs: []string{"f-a", "f-a"}},
	); err == nil {
		t.Fatal("duplicate folder should fail")
	}
	if _, err := svc.CreateWorkset(
		ctx,
		CreateRequest{LibraryID: "lib-1", Title: "ok", FolderIDs: []string{"nope"}},
	); err == nil {
		t.Fatal("unknown folder should fail")
	}
	if _, err := svc.CreateWorkset(
		ctx,
		CreateRequest{LibraryID: "nope", Title: "ok", FolderIDs: []string{"f-a"}},
	); err == nil {
		t.Fatal("unknown library should fail")
	}
}

func planWorkflowFixture() planusecase.Workflow {
	profile := reconcile.DesiredProfile{
		Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
		Encoded: &reconcile.AudioOutputSpec{
			Codec:   reconcile.CodecMp3,
			Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320},
		},
	}
	return planusecase.Workflow{
		SchemaVersion: 1,
		Steps: []planusecase.WorkflowStep{{
			StepType: planusecase.StepTypeReconcileAudio,
			Policy: planusecase.PolicySource{
				Kind: "inline",
				InlinePolicy: &reconcile.Policy{
					SchemaVersion:  1,
					ClassifierTags: []string{"SEなし"},
					Matched:        profile,
					Unmatched:      profile,
				},
			},
		}},
	}
}

func TestDraftSaveAndNeedsPlanning(t *testing.T) {
	svc := newSvc(t, 1)
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-a", "/music/albumA", "albumA", "albumA")
	ctx := context.Background()

	res, _ := svc.CreateWorkset(ctx, CreateRequest{LibraryID: "lib-1", Title: "t", FolderIDs: []string{"f-a"}})
	id := res.Workset.WorksetID

	if _, err := svc.SaveDraft(
		ctx,
		id,
		SaveDraftRequest{Workflow: planWorkflowFixture(), IfMatchVersion: 1},
	); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	view, _ := svc.GetWorkset(ctx, id)
	if view.Version != 2 {
		t.Fatalf("version = %d, want 2", view.Version)
	}
	// Same draft content saved again is a no-op hash but still bumps version
	// via full replacement semantics.
	if _, err := svc.SaveDraft(
		ctx,
		id,
		SaveDraftRequest{Workflow: planWorkflowFixture(), IfMatchVersion: 2},
	); err != nil {
		t.Fatalf("SaveDraft 2: %v", err)
	}
}

// incompletePolicyFixture keeps the output profiles but leaves the classifier
// tags empty — the normal "just created, not yet configured" draft state.
func incompletePolicyFixture() planusecase.Workflow {
	wf := planWorkflowFixture()
	policy := *wf.Steps[0].Policy.InlinePolicy
	policy.ClassifierTags = []string{}
	wf.Steps[0].Policy.InlinePolicy = &policy
	return wf
}

func TestDraftSaveAllowsIncompleteButGenerationRejects(t *testing.T) {
	svc := newSvc(t, 1)
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-a", "/music/albumA", "albumA", "albumA")
	ctx := context.Background()

	res, _ := svc.CreateWorkset(ctx, CreateRequest{LibraryID: "lib-1", Title: "t", FolderIDs: []string{"f-a"}})
	id := res.Workset.WorksetID

	// An incomplete policy is a legal editing state: saving it must succeed.
	if _, err := svc.SaveDraft(
		ctx,
		id,
		SaveDraftRequest{Workflow: incompletePolicyFixture(), IfMatchVersion: 1},
	); err != nil {
		t.Fatalf("SaveDraft of incomplete policy should be allowed: %v", err)
	}
	// But it cannot produce a revision: generation rejects synchronously.
	if _, err := svc.StartGeneration(ctx, id, StartGenerationRequest{}); err == nil {
		t.Fatal("StartGeneration with incomplete policy should fail")
	} else if werr, ok := AsError(err); !ok || werr.Code != "INVALID_POLICY" {
		t.Fatalf("want INVALID_POLICY, got %v", err)
	}
}

func TestStartGenerationRejectsScanAndActive(t *testing.T) {
	svc := newSvc(t, 1)
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-a", "/music/albumA", "albumA", "albumA")
	ctx := context.Background()

	res, _ := svc.CreateWorkset(ctx, CreateRequest{LibraryID: "lib-1", Title: "t", FolderIDs: []string{"f-a"}})
	id := res.Workset.WorksetID

	// Simulate an active scan row for the library root.
	now := time.Now().Format(timeFmt)
	if _, err := repo.DB().Exec(`
		INSERT INTO scan_sessions (session_id, root_path, kind, status, started_at)
		VALUES ('scan-1', '/music', 'full', 'running', ?)
	`, now); err != nil {
		t.Fatalf("insert scan: %v", err)
	}
	if _, err := svc.StartGeneration(ctx, id, StartGenerationRequest{}); err == nil {
		t.Fatal("start generation during scan should fail")
	} else if werr, ok := AsError(err); !ok || werr.Code != "SCAN_IN_PROGRESS" {
		t.Fatalf("expected SCAN_IN_PROGRESS, got %v", err)
	}

	// Complete the scan, then start a real generation.
	_, _ = repo.DB().Exec("UPDATE scan_sessions SET status='completed', finished_at=? WHERE session_id='scan-1'", now)
	got, err := svc.StartGeneration(ctx, id, StartGenerationRequest{})
	if err != nil {
		t.Fatalf("StartGeneration: %v", err)
	}
	if !got.Created {
		t.Fatalf("expected fresh session, got %+v", got)
	}

	// A second start must conflict (one active session per workset).
	if _, err := svc.StartGeneration(ctx, id, StartGenerationRequest{}); err == nil {
		t.Fatal("second start should fail with active session")
	} else if werr, ok := AsError(err); !ok || werr.Code != "GENERATION_IN_PROGRESS" {
		t.Fatalf("expected GENERATION_IN_PROGRESS, got %v", err)
	}
}

func TestOrphanedWorksetReadOnly(t *testing.T) {
	svc := newSvc(t, 1)
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-a", "/music/albumA", "albumA", "albumA")
	ctx := context.Background()

	res, _ := svc.CreateWorkset(ctx, CreateRequest{LibraryID: "lib-1", Title: "t", FolderIDs: []string{"f-a"}})
	id := res.Workset.WorksetID

	// Orphan by setting library_id NULL (the delete path does this).
	if _, err := repo.DB().Exec("UPDATE worksets SET library_id = NULL WHERE id = ?", id); err != nil {
		t.Fatalf("orphan: %v", err)
	}
	view, err := svc.GetWorkset(ctx, id)
	if err != nil {
		t.Fatalf("GetWorkset orphaned: %v", err)
	}
	if view.PlanningState != PlanningOrphaned {
		t.Fatalf("orphaned planning_state = %q", view.PlanningState)
	}
	if view.CurrentRevision != nil {
		t.Fatalf("orphaned with revision: %+v", view.CurrentRevision)
	}
	if _, err := svc.RenameWorkset(ctx, id, RenameRequest{Title: "x", IfMatchVersion: 1}); err == nil {
		t.Fatal("rename orphaned should fail")
	} else if werr, ok := AsError(err); !ok || werr.Code != "ORPHANED_WORKSET" {
		t.Fatalf("expected ORPHANED_WORKSET, got %v", err)
	}
}

func insertTestAudioEntry(t *testing.T, repo *sqlite.Repository, path, root string, size, mtime int64) {
	t.Helper()
	now := time.Now().Format(timeFmt)
	parent := "/"
	if len(path) > 1 {
		parent = filepath.Dir(path)
	}
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, 'mp3', ?, ?)
	`, path, root, parent, filepath.Base(path), size, mtime, now, now)
	if err != nil {
		t.Fatalf("insert audio entry: %v", err)
	}
}

func insertNonAudioEntry(t *testing.T, repo *sqlite.Repository, path, root string, size, mtime int64) {
	t.Helper()
	now := time.Now().Format(timeFmt)
	parent := "/"
	if len(path) > 1 {
		parent = filepath.Dir(path)
	}
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, format, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, 'txt', ?, ?)
	`, path, root, parent, filepath.Base(path), size, mtime, now, now)
	if err != nil {
		t.Fatalf("insert non-audio entry: %v", err)
	}
}

func TestStaleValidationBlockedAndSidecarIndependence(t *testing.T) {
	svc := newSvc(t, 1)
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-a", "/music/albumA", "albumA", "albumA")
	insertTestAudioEntry(t, repo, "/music/albumA/01.mp3", "/music/albumA", 1024, 1000)
	insertNonAudioEntry(t, repo, "/music/albumA/cover.jpg", "/music/albumA", 2048, 1000)

	ctx := context.Background()
	res, err := svc.CreateWorkset(ctx, CreateRequest{
		LibraryID: "lib-1", Title: "测试正交", FolderIDs: []string{"f-a"}, IdempotencyKey: "idem-stale-1",
	})
	if err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	id := res.Workset.WorksetID

	// 1. Generate revision v1.
	genRes, err := svc.StartGeneration(
		ctx,
		id,
		StartGenerationRequest{ExpectedDraftVersion: 1, IdempotencyKey: "gen-stale-1"},
	)
	if err != nil {
		t.Fatalf("StartGeneration: %v", err)
	}
	gen, err := repo.GetGeneration(genRes.Generation.GenerationID)
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	svc.dispatcher.execute(gen)

	view, err := svc.GetWorkset(ctx, id)
	if err != nil {
		t.Fatalf("GetWorkset: %v", err)
	}
	if view.CurrentRevision == nil {
		t.Fatal("expected current revision")
	}
	if view.CurrentRevision.ValidationState != ValidationValid || view.CurrentRevision.Stale == nil ||
		*view.CurrentRevision.Stale != false {
		t.Fatalf(
			"expected valid non-stale revision, got state=%q stale=%v",
			view.CurrentRevision.ValidationState,
			view.CurrentRevision.Stale,
		)
	}

	// 2. Modifying non-audio sidecar (cover.jpg mtime or add txt) does NOT mark stale.
	_, err = repo.DB().Exec("UPDATE entries SET mtime = mtime + 500 WHERE path = '/music/albumA/cover.jpg'")
	if err != nil {
		t.Fatalf("update sidecar mtime: %v", err)
	}
	insertNonAudioEntry(t, repo, "/music/albumA/notes.txt", "/music/albumA", 500, 1000)

	viewAfterSidecar, err := svc.GetWorkset(ctx, id)
	if err != nil {
		t.Fatalf("GetWorkset after sidecar change: %v", err)
	}
	if viewAfterSidecar.CurrentRevision.ValidationState != ValidationValid ||
		*viewAfterSidecar.CurrentRevision.Stale != false {
		t.Fatalf(
			"non-audio file change should NOT make revision stale, got state=%q stale=%v",
			viewAfterSidecar.CurrentRevision.ValidationState,
			viewAfterSidecar.CurrentRevision.Stale,
		)
	}

	// 3. Modifying audio file (size/mtime) DOES mark stale.
	_, err = repo.DB().Exec("UPDATE entries SET mtime = mtime + 100 WHERE path = '/music/albumA/01.mp3'")
	if err != nil {
		t.Fatalf("update audio mtime: %v", err)
	}
	viewAfterAudio, err := svc.GetWorkset(ctx, id)
	if err != nil {
		t.Fatalf("GetWorkset after audio change: %v", err)
	}
	if viewAfterAudio.CurrentRevision.ValidationState != ValidationStale ||
		*viewAfterAudio.CurrentRevision.Stale != true {
		t.Fatalf(
			"audio file change MUST make revision stale, got state=%q stale=%v",
			viewAfterAudio.CurrentRevision.ValidationState,
			viewAfterAudio.CurrentRevision.Stale,
		)
	}
}

func TestMissingRootRemainsMissingAndNotStaleUntilAudioAppears(t *testing.T) {
	svc := newSvc(t, 1)
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-missing", "/music/albumMissing", "albumMissing", "albumMissing")

	ctx := context.Background()
	res, err := svc.CreateWorkset(ctx, CreateRequest{
		LibraryID: "lib-1", Title: "缺失根测试", FolderIDs: []string{"f-missing"}, IdempotencyKey: "idem-missing-1",
	})
	if err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	id := res.Workset.WorksetID

	genRes, err := svc.StartGeneration(
		ctx,
		id,
		StartGenerationRequest{ExpectedDraftVersion: 1, IdempotencyKey: "gen-missing-1"},
	)
	if err != nil {
		t.Fatalf("StartGeneration: %v", err)
	}
	gen, err := repo.GetGeneration(genRes.Generation.GenerationID)
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	svc.dispatcher.execute(gen)

	view, err := svc.GetWorkset(ctx, id)
	if err != nil {
		t.Fatalf("GetWorkset: %v", err)
	}
	if view.CurrentRevision == nil {
		t.Fatal("expected current revision")
	}
	// Missing root is blocked/SOURCE_MISSING, but since no inventory changed, it is NOT stale.
	if view.CurrentRevision.BlockedCount == 0 {
		t.Fatalf("expected blocked count > 0, got %d", view.CurrentRevision.BlockedCount)
	}
	if view.CurrentRevision.ValidationState != ValidationValid || *view.CurrentRevision.Stale != false {
		t.Fatalf(
			"missing root with unchanged inventory must not be stale, got state=%q stale=%v",
			view.CurrentRevision.ValidationState,
			view.CurrentRevision.Stale,
		)
	}

	// When audio is scanned/added under that root, it becomes stale.
	insertTestAudioEntry(t, repo, "/music/albumMissing/01.mp3", "/music/albumMissing", 1024, 1000)
	viewAfterAudio, err := svc.GetWorkset(ctx, id)
	if err != nil {
		t.Fatalf("GetWorkset after audio appears: %v", err)
	}
	if viewAfterAudio.CurrentRevision.ValidationState != ValidationStale ||
		*viewAfterAudio.CurrentRevision.Stale != true {
		t.Fatalf(
			"after audio appears, revision should become stale, got state=%q stale=%v",
			viewAfterAudio.CurrentRevision.ValidationState,
			viewAfterAudio.CurrentRevision.Stale,
		)
	}
}
