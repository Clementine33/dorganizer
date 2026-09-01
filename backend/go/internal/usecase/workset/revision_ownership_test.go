package workset //nolint:testpackage // white-box tests exercise unexported internals

import (
	"context"
	"testing"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// TestRevisionComponentRootOwnership covers the stable component→root
// ownership contract end to end: create a workset over two album folders,
// generate a revision synchronously via the dispatcher, then assert that
// GetRevision maps every component to the correct planning root and that a
// re-read returns the identical ownership table.
//
//nolint:funlen // long ownership scenario
func TestRevisionComponentRootOwnership(t *testing.T) {
	svc := newSvc(t, 1)
	repo := svc.repo
	insertLibraryRow(t, repo, "lib-1", "Onsei", "/music")
	insertFolder(t, repo, "lib-1", "f-a", "/music/albumA", "albumA", "albumA")
	insertFolder(t, repo, "lib-1", "f-b", "/music/albumB", "albumB", "albumB")

	// Seed scanned entries: albumA has one wav+mp3 pair (matched component),
	// albumB has a lone wav (unmatched component). Parent dirs are required for
	// the planner's component grouping.
	insertDirEntry(t, repo, "/music/albumA")
	insertDirEntry(t, repo, "/music/albumA/wav")
	insertDirEntry(t, repo, "/music/albumA/mp3")
	insertAudioEntry(t, repo, "/music/albumA/wav/test1.wav", 0)
	insertAudioEntry(t, repo, "/music/albumA/mp3/test1.mp3", 192000)
	insertDirEntry(t, repo, "/music/albumB")
	insertDirEntry(t, repo, "/music/albumB/wav")
	insertAudioEntry(t, repo, "/music/albumB/wav/track.wav", 0)

	ctx := context.Background()
	res, err := svc.CreateWorkset(ctx, CreateRequest{
		LibraryID: "lib-1", Title: "双专辑", FolderIDs: []string{"f-a", "f-b"}, IdempotencyKey: "own-1",
	})
	if err != nil {
		t.Fatalf("CreateWorkset: %v", err)
	}
	id := res.Workset.WorksetID

	if _, saveErr := svc.SaveDraft(
		ctx,
		id,
		SaveDraftRequest{Workflow: planWorkflowFixture(), IfMatchVersion: 1},
	); saveErr != nil {
		t.Fatalf("SaveDraft: %v", saveErr)
	}
	view, _ := svc.GetWorkset(ctx, id)
	if view.Version != 2 {
		t.Fatalf("version = %d, want 2", view.Version)
	}

	got, err := svc.StartGeneration(
		ctx,
		id,
		StartGenerationRequest{ExpectedDraftVersion: 2, IdempotencyKey: "own-gen-1"},
	)
	if err != nil {
		t.Fatalf("StartGeneration: %v", err)
	}
	if !got.Created {
		t.Fatalf("expected a fresh session, got %+v", got)
	}

	// The dispatcher was never started, so the queued session is still ours to
	// run synchronously — deterministic, no polling.
	gen, err := repo.GetGeneration(got.Generation.GenerationID)
	if err != nil {
		t.Fatalf("GetGeneration: %v", err)
	}
	svc.dispatcher.execute(gen)

	gen, err = repo.GetGeneration(got.Generation.GenerationID)
	if err != nil {
		t.Fatalf("re-read generation: %v", err)
	}
	if gen.Status != "completed" || gen.RevisionID == "" {
		t.Fatalf("generation did not complete: status=%q rev=%q err=%q/%q",
			gen.Status, gen.RevisionID, gen.ErrorCode, gen.ErrorMessage)
	}

	rev, err := svc.GetRevision(ctx, id, gen.RevisionID)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if len(rev.Roots) != 2 {
		t.Fatalf("roots = %d, want 2", len(rev.Roots))
	}
	if rev.Roots[0].RootPath != "/music/albumA" || rev.Roots[1].RootPath != "/music/albumB" {
		t.Fatalf("root order mismatch: %+v", rev.Roots)
	}
	if len(rev.ComponentRoots) == 0 {
		t.Fatal("component_roots missing")
	}
	// albumA (matched: wav+mp3 pair) and albumB (unmatched: lone wav) each
	// contribute at least one component; both must map to their own root.
	byRoot := map[int]int{}
	for _, cr := range rev.ComponentRoots {
		if cr.ComponentID == "" {
			t.Fatalf("empty component_id in ownership table: %+v", rev.ComponentRoots)
		}
		if cr.RootIndex < 0 || cr.RootIndex > 1 {
			t.Fatalf("component %s maps to unknown root %d", cr.ComponentID, cr.RootIndex)
		}
		byRoot[cr.RootIndex]++
	}
	if byRoot[0] == 0 || byRoot[1] == 0 {
		t.Fatalf("expected components under both roots, got %+v", byRoot)
	}

	// Re-read must return the identical ownership table (immutable snapshot).
	again, err := svc.GetRevision(ctx, id, gen.RevisionID)
	if err != nil {
		t.Fatalf("GetRevision again: %v", err)
	}
	if len(again.ComponentRoots) != len(rev.ComponentRoots) {
		t.Fatalf(
			"ownership table length changed on re-read: %d vs %d",
			len(again.ComponentRoots),
			len(rev.ComponentRoots),
		)
	}
	for i := range rev.ComponentRoots {
		if again.ComponentRoots[i] != rev.ComponentRoots[i] {
			t.Fatalf("ownership row %d changed: %+v vs %+v", i, again.ComponentRoots[i], rev.ComponentRoots[i])
		}
	}
}

func insertDirEntry(t *testing.T, repo *sqlite.Repository, path string) {
	t.Helper()
	now := time.Now().Format(timeFmt)
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, created_at, updated_at)
		VALUES (?, '/music', ?, ?, 1, 0, ?, ?, ?)
	`, path, path, "dir", now, now, now)
	if err != nil {
		t.Fatalf("insert dir entry %s: %v", path, err)
	}
}

func insertAudioEntry(t *testing.T, repo *sqlite.Repository, path string, bitrate int64) {
	t.Helper()
	now := time.Now()
	_, err := repo.DB().Exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, bitrate, format, created_at, updated_at)
		VALUES (?, '/music', ?, ?, 0, 1024, ?, ?, 'mp3', ?, ?)
	`, path, path, "file", now.Unix(), bitrate, now.Format(timeFmt), now.Format(timeFmt))
	if err != nil {
		t.Fatalf("insert audio entry %s: %v", path, err)
	}
}
