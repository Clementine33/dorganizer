package workset_test

import (
	"testing"
	"time"
)

// TestRevisionComponentRootOwnership covers the stable component→root
// ownership contract through the operation-scoped revision detail: every
// component maps to the plan root it was discovered under, and a re-read
// returns the identical ownership table.
func TestRevisionComponentRootOwnership(t *testing.T) {
	f := newFixture(t)
	ids := f.standardLibrary("albumA", "albumB")

	// albumA holds a wav+mp3 pair (one matched component), albumB a lone wav
	// (one unmatched component). Parent directories are required for grouping.
	f.insertNestedDir("/music/albumA/wav")
	f.insertNestedDir("/music/albumA/mp3")
	f.insertAudioEntry("/music/albumA/wav/test1.wav", "/music/albumA", 1024, 1000)
	f.insertAudioEntry("/music/albumA/mp3/test1.mp3", "/music/albumA", 2048, 1000)
	f.insertNestedDir("/music/albumB/wav")
	f.insertAudioEntry("/music/albumB/wav/track.wav", "/music/albumB", 4096, 1000)

	ws := f.createCurrent("双专辑", ids...)
	gen := f.runGeneration(ws.WorksetID, f.operation(ws.WorksetID).Version)
	if gen.Status != "completed" || gen.RevisionID == "" {
		t.Fatalf("generation did not complete: %+v", gen)
	}

	rev, err := f.svc.GetRevision(f.ctx, ws.WorksetID, "conversion", gen.RevisionID)
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

	// Re-read returns the identical immutable snapshot.
	again, err := f.svc.GetRevision(f.ctx, ws.WorksetID, "conversion", gen.RevisionID)
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
	if len(again.Members) != len(rev.Members) {
		t.Fatalf("frozen member table changed on re-read: %d vs %d", len(again.Members), len(rev.Members))
	}
}

// insertNestedDir inserts a directory row for the planner's component
// grouping.
func (f *fixture) insertNestedDir(path string) {
	f.t.Helper()
	now := time.Now().Format(timeFmt)
	f.exec(`
		INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, created_at, updated_at)
		VALUES (?, '/music', ?, ?, 1, 0, ?, ?, ?)
	`, path, path, "dir", now, now, now)
}
