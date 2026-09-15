package plan //nolint:testpackage // white-box tests exercise unexported internals

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

func TestEnrichBitrateProbesAACContainers(t *testing.T) {
	for _, ext := range []string{".aac", ".M4A"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			track := filepath.Join(dir, "track"+ext)
			output, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-f", "lavfi", "-i",
				"sine=frequency=440:duration=2", "-c:a", "aac", "-b:a", "256k", track).CombinedOutput()
			if err != nil {
				t.Fatalf("generate AAC: %v: %s", err, output)
			}
			repo, err := sqlite.NewRepository(filepath.Join(dir, "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			track = filepath.ToSlash(track)
			_, err = repo.DB().Exec(
				`INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
 VALUES (?, ?, 0, 1000, 'aac', 1, 1, 0)`,
				track, filepath.ToSlash(dir),
			)
			if err != nil {
				t.Fatal(err)
			}
			entries := []reconcile.AudioEntry{{PathPosix: track}}
			a := newBitrateAnalyzer(repo, "")
			if err := a.enrichMissing(t.Context(), entries, true); err != nil {
				t.Fatal(err)
			}
			var bitrate int64
			if err := repo.DB().QueryRow("SELECT bitrate FROM entries").Scan(&bitrate); err != nil {
				t.Fatal(err)
			}
			// Native AAC's measured average need not equal the requested 256k.
			if bitrate <= 0 || entries[0].Bitrate != bitrate {
				t.Fatalf("AAC bitrate: entry=%d persisted=%d", entries[0].Bitrate, bitrate)
			}
			if err := os.Remove(filepath.FromSlash(track)); err != nil {
				t.Fatal(err)
			}
			if err := a.enrichMissing(t.Context(), entries, true); err != nil {
				t.Fatal(err)
			}
			if entries[0].Bitrate != bitrate {
				t.Fatal("cached AAC bitrate changed")
			}
		})
	}
}

func TestEnrichBitrateKeepsUnknownAndCachedValues(t *testing.T) {
	dir := t.TempDir()
	invalid := filepath.Join(dir, "invalid.mp3")
	if err := os.WriteFile(invalid, []byte("not audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{probe, filepath.Join(dir, "missing-ffprobe")} {
		entries := []reconcile.AudioEntry{
			{PathPosix: filepath.ToSlash(invalid)},
			{PathPosix: filepath.ToSlash(filepath.Join(dir, "missing.mp3"))},
			{PathPosix: filepath.ToSlash(invalid), Bitrate: 192000},
		}
		a := newBitrateAnalyzer(nil, tool)
		if enrichErr := a.enrichMissing(t.Context(), entries, true); enrichErr != nil {
			t.Fatal(enrichErr)
		}
		if entries[0].Bitrate != 0 || entries[1].Bitrate != 0 || entries[2].Bitrate != 192000 {
			t.Fatalf("unexpected bitrate changes: %+v", entries)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = newBitrateAnalyzer(nil, probe).enrichMissing(ctx,
		[]reconcile.AudioEntry{{PathPosix: filepath.ToSlash(invalid)}}, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled enrichment: %v", err)
	}
}
