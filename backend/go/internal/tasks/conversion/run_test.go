package conversion_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	"github.com/onsei/organizer/backend/internal/tasks/conversion"
	"github.com/onsei/organizer/backend/internal/workset"
)

// seedRootEntries writes an RJ-like tree into the entries table: two content
// partitions (SEあり unmatched / SEなし matched) each with wav+mp3 codec
// lanes, 2 tracks each, all mp3 at 320 kbps so the balanced policy is fully
// satisfied (no disk reads; bitrate enrichment is skipped for non-zero rates).
func seedRootEntries(t *testing.T, repo *sqlite.Repository) {
	t.Helper()
	type row struct {
		path, parent, name, format string
		size, mtime, bitrate       int64
	}
	rows := []row{
		{"/music", "", "music", "", 0, 0, 0},
		{"/music/SEあり", "/music", "SEあり", "", 0, 0, 0},
		{"/music/SEあり/wav", "/music/SEあり", "wav", "", 0, 0, 0},
		{"/music/SEあり/mp3", "/music/SEあり", "mp3", "", 0, 0, 0},
		{"/music/SEなし", "/music", "SEなし", "", 0, 0, 0},
		{"/music/SEなし/wav", "/music/SEなし", "wav", "", 0, 0, 0},
		{"/music/SEなし/mp3", "/music/SEなし", "mp3", "", 0, 0, 0},
	}
	for _, p := range []string{"SEあり", "SEなし"} {
		for _, n := range []string{"00", "01"} {
			rows = append(
				rows,
				row{
					"/music/" + p + "/wav/" + n + ".wav",
					"/music/" + p + "/wav",
					n + ".wav",
					"wav",
					100000,
					1700000000,
					0,
				},
				row{
					"/music/" + p + "/mp3/" + n + ".mp3",
					"/music/" + p + "/mp3",
					n + ".mp3",
					"mpeg",
					12000,
					1700000000,
					320000,
				},
			)
		}
	}
	for _, r := range rows {
		isDir := 0
		if r.size == 0 && r.bitrate == 0 && r.format == "" {
			isDir = 1
		}
		_, err := repo.DB().Exec(`
			INSERT INTO entries (path, root_path, parent_path, name, is_dir, size, mtime, bitrate, format, content_rev)
			VALUES (?, '/music', ?, ?, ?, ?, ?, ?, ?, 1)
		`, r.path, r.parent, r.name, isDir, r.size, r.mtime, r.bitrate, r.format)
		if err != nil {
			t.Fatalf("seed entry %s: %v", r.path, err)
		}
	}
}

// balancedPolicy is the balanced output shape (wav + mp3@320 in both
// partitions) with the given classifier tags.
func balancedPolicy() reconcile.Policy {
	profile := reconcile.DesiredProfile{
		Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
		Encoded: &reconcile.AudioOutputSpec{
			Codec:   reconcile.CodecMp3,
			Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320},
		},
	}
	return reconcile.Policy{
		SchemaVersion:  1,
		ClassifierTags: []string{"SEなし"},
		Matched:        profile,
		Unmatched:      profile,
	}
}

func TestPlanBalancedSatisfied(t *testing.T) {
	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	defer repo.Close()
	seedRootEntries(t, repo)

	policy := balancedPolicy()
	res, err := conversion.Plan(context.Background(), repo, "", conversion.Input{
		Policy: policy,
		Roots:  []conversion.RootInput{{Path: "/music", Policy: policy}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if res.Summary.SummaryReason != "NO_MATCH" {
		t.Fatalf("summary = %q, want NO_MATCH (balanced satisfied)", res.Summary.SummaryReason)
	}
	if res.Status != "ok" {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	if len(res.Components) != 2 {
		t.Fatalf("components = %d, want 2 partitions", len(res.Components))
	}
	for _, c := range res.Components {
		if c.Outcome.Status != "ok" {
			t.Fatalf("component %s status = %s: %s", c.Outcome.ComponentID, c.Outcome.Status, c.Outcome.Message)
		}
		if len(c.Outcome.Operations) != 0 {
			t.Fatalf("component %s should have no operations", c.Outcome.ComponentID)
		}
	}

	// The frozen input facts a revision persists: one root with its live
	// inventory fingerprint and count, plus the classifier tag snapshot.
	if len(res.Roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(res.Roots))
	}
	if res.Roots[0].Count != 8 {
		t.Fatalf("entry count = %d, want 8 audio entries", res.Roots[0].Count)
	}
	if res.Roots[0].InventoryFingerprint == "" {
		t.Fatal("inventory fingerprint must be part of the snapshot")
	}
	if res.ClassifierTags == "" {
		t.Fatal("classifier tag snapshot must be part of the snapshot")
	}
}

func TestPlanInvalidPolicy(t *testing.T) {
	repo, err := sqlite.NewRepository(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	defer repo.Close()
	seedRootEntries(t, repo)

	policy := balancedPolicy()
	policy.SchemaVersion = 99
	_, err = conversion.Plan(context.Background(), repo, "", conversion.Input{
		Policy: policy,
		Roots:  []conversion.RootInput{{Path: "/music", Policy: policy}},
	})
	if err == nil {
		t.Fatal("expected error for unsupported policy schema version")
	}
	planErr, ok := workset.AsError(err)
	if !ok || planErr.Code != "INVALID_POLICY" {
		t.Fatalf("error = %v, want INVALID_POLICY", err)
	}
}
