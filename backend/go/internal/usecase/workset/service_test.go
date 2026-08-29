package workset

import (
	"context"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

const timeFmt = "2006-01-02T15:04:05.999999999Z07:00"

// newSvc builds a repo + workset service on a temp DB with a config dir.
func newSvc(t *testing.T, concurrency int) *serviceImpl {
	t.Helper()
	tmp := t.TempDir()
	dbPath := tmp + "/test.db"
	repo, err := sqlite.NewRepository(dbPath)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return NewService(repo, tmp, concurrency).(*serviceImpl)
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

func insertEntry(t *testing.T, repo *sqlite.Repository, path, root string, size, mtime int64) {
	t.Helper()
	now := time.Now().Format(timeFmt)
	parent := "/"
	if len(path) > 1 {
		parent = path[:len(path)-len("file.mp3")]
	}
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?)
	`, path, root, parent, "file.mp3", size, mtime, now, now)
	if err != nil {
		t.Fatalf("insert entry: %v", err)
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

	if _, err := svc.CreateWorkset(ctx, CreateRequest{LibraryID: "lib-1", Title: "", FolderIDs: []string{"f-a"}}); err == nil {
		t.Fatal("empty title should fail")
	}
	if _, err := svc.CreateWorkset(ctx, CreateRequest{LibraryID: "lib-1", Title: "ok", FolderIDs: []string{"f-a", "f-a"}}); err == nil {
		t.Fatal("duplicate folder should fail")
	}
	if _, err := svc.CreateWorkset(ctx, CreateRequest{LibraryID: "lib-1", Title: "ok", FolderIDs: []string{"nope"}}); err == nil {
		t.Fatal("unknown folder should fail")
	}
	if _, err := svc.CreateWorkset(ctx, CreateRequest{LibraryID: "nope", Title: "ok", FolderIDs: []string{"f-a"}}); err == nil {
		t.Fatal("unknown library should fail")
	}
}

func planWorkflowFixture() planusecase.Workflow {
	return planusecase.Workflow{
		SchemaVersion: 1,
		Steps: []planusecase.WorkflowStep{{
			StepType: planusecase.StepTypeReconcileAudio,
			Policy: planusecase.PolicySource{
				Kind: "preset", PresetName: "balanced", PresetVersion: 1,
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

	if _, err := svc.SaveDraft(ctx, id, SaveDraftRequest{Workflow: planWorkflowFixture(), IfMatchVersion: 1}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	view, _ := svc.GetWorkset(ctx, id)
	if view.Version != 2 {
		t.Fatalf("version = %d, want 2", view.Version)
	}
	// Same draft content saved again is a no-op hash but still bumps version
	// via full replacement semantics.
	if _, err := svc.SaveDraft(ctx, id, SaveDraftRequest{Workflow: planWorkflowFixture(), IfMatchVersion: 2}); err != nil {
		t.Fatalf("SaveDraft 2: %v", err)
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
