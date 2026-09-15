package plan_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	"github.com/onsei/organizer/backend/internal/usecase/plan"
)

func TestPlanProbesMissingBitrate(t *testing.T) {
	dir := t.TempDir()
	track := filepath.Join(dir, "track.mp3")
	output, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-f", "lavfi", "-i",
		"sine=frequency=440:duration=2", "-c:a", "libmp3lame", "-b:a", "320k", track).CombinedOutput()
	if err != nil {
		t.Fatalf("generate MP3: %v: %s", err, output)
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	probe, err = filepath.Abs(probe)
	if err != nil {
		t.Fatal(err)
	}
	// The explicit tool must work without ffprobe being discoverable on PATH.
	t.Setenv("PATH", t.TempDir())
	repo, err := sqlite.NewRepository(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	_, err = repo.DB().Exec(`INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 80000, 'mpeg', 1, 1, 0)`, filepath.ToSlash(track), filepath.ToSlash(dir))
	if err != nil {
		t.Fatal(err)
	}
	profile := reconcile.DesiredProfile{Encoded: &reconcile.AudioOutputSpec{
		Codec: reconcile.CodecMp3, Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320},
	}}
	policy := reconcile.Policy{
		SchemaVersion: 1, ClassifierTags: []string{"SEなし"}, Matched: profile, Unmatched: profile,
	}
	in := plan.Input{Policy: policy, Roots: []plan.RootInput{{Path: dir, Policy: policy}}}
	for _, batch := range []bool{true, false} {
		cfg, marshalErr := json.Marshal(map[string]any{
			"tools": map[string]string{"ffprobe_path": probe},
			"plan":  map[string]any{"bitrate": map[string]bool{"batch_update": batch}},
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		if _, resetErr := repo.DB().Exec("UPDATE entries SET bitrate = 0"); resetErr != nil {
			t.Fatal(resetErr)
		}
		res, runErr := plan.Plan(t.Context(), repo, dir, in)
		if runErr != nil {
			t.Fatal(runErr)
		}
		if res.Summary.OperationCount != 0 || res.Summary.BlockedCount != 0 {
			t.Fatalf("320k MP3 must satisfy target: %+v", res.Summary)
		}
		var bitrate int64
		if scanErr := repo.DB().QueryRow("SELECT bitrate FROM entries").Scan(&bitrate); scanErr != nil {
			t.Fatal(scanErr)
		}
		if bitrate != 320000 {
			t.Fatalf("persisted bitrate = %d, want 320000", bitrate)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = plan.Plan(ctx, repo, dir, in)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Plan: %v", err)
	}
}
