package conversion //nolint:testpackage // white-box tests exercise unexported internals

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
)

var mp3Fixture = sync.OnceValues(func() ([]byte, error) {
	return exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:a", "libmp3lame", "-b:a", "128k", "-f", "mp3", "pipe:1").Output()
})

func writeTestMP3Frame(t *testing.T, path string) {
	t.Helper()
	data, err := mp3Fixture()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSelectScopedProbeCandidates_OnlyScopedMissingMP3AndAAC(t *testing.T) {
	entries := []reconcile.AudioEntry{
		{PathPosix: "/scope/a.mp3", Bitrate: 0},
		{PathPosix: "/scope/b.mp3", Bitrate: 128000},
		{PathPosix: "/scope/c.flac", Bitrate: 0},
		{PathPosix: "/scope/d.MP3", Bitrate: 0},
		{PathPosix: "/scope/e.aac", Bitrate: 0},
		{PathPosix: "/scope/f.M4A", Bitrate: 0},
		{PathPosix: "/scope/g.m4a", Bitrate: 256000},
	}

	idx := selectScopedProbeCandidates(entries)
	if len(idx) != 4 {
		t.Fatalf("expected 4 scoped probe candidates, got %d", len(idx))
	}
	if idx[0] != 0 || idx[1] != 3 || idx[2] != 4 || idx[3] != 5 {
		t.Fatalf("unexpected candidate indexes: got %v want [0 3 4 5]", idx)
	}
}

func TestEnrichMissing_OnlyPersistsScopedEntries(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-analyze-bitrate-scope-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	const scopedTotal = 120
	scopedEntries := make([]reconcile.AudioEntry, 0, scopedTotal)

	for i := range scopedTotal {
		p := filepath.Join(tmpDir, fmt.Sprintf("in-scope-%03d.mp3", i))
		writeTestMP3Frame(t, p)
		pPosix := filepath.ToSlash(p)
		_, err = repo.DB().Exec(`
			INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
			VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, NULL)
		`, pPosix, filepath.ToSlash(tmpDir), 1234567890)
		if err != nil {
			t.Fatalf("failed to insert scoped entry: %v", err)
		}
		scopedEntries = append(scopedEntries, reconcile.AudioEntry{PathPosix: pPosix, Bitrate: 0, Format: "audio/mpeg"})
	}

	outOfScopePath := filepath.Join(tmpDir, "out-of-scope.mp3")
	writeTestMP3Frame(t, outOfScopePath)
	_, err = repo.DB().Exec(`
		INSERT INTO entries (path, root_path, is_dir, size, format, content_rev, mtime, bitrate)
		VALUES (?, ?, 0, 1000, 'audio/mpeg', 1, ?, NULL)
	`, filepath.ToSlash(outOfScopePath), filepath.ToSlash(tmpDir), 1234567890)
	if err != nil {
		t.Fatalf("failed to insert out-of-scope entry: %v", err)
	}

	a := newBitrateAnalyzer(repo, "")
	if err := a.enrichMissing(context.Background(), scopedEntries, true); err != nil {
		t.Fatalf("expected enrich scoped entries bitrate success, got %v", err)
	}

	var scopedUpdated int
	if err := repo.DB().
		QueryRow("SELECT COUNT(1) FROM entries WHERE path LIKE ? AND COALESCE(bitrate,0) > 0", filepath.ToSlash(filepath.Join(tmpDir, "in-scope-"))+"%").
		Scan(&scopedUpdated); err != nil {
		t.Fatalf("failed to count scoped updated bitrates: %v", err)
	}
	if scopedUpdated != scopedTotal {
		t.Fatalf("expected %d scoped bitrates updated, got %d", scopedTotal, scopedUpdated)
	}

	var outOfScopeBitrate int64
	if err := repo.DB().
		QueryRow("SELECT COALESCE(bitrate, 0) FROM entries WHERE path = ?", filepath.ToSlash(outOfScopePath)).
		Scan(&outOfScopeBitrate); err != nil {
		t.Fatalf("failed to read out-of-scope bitrate: %v", err)
	}
	if outOfScopeBitrate != 0 {
		t.Fatalf("expected out-of-scope bitrate to remain 0, got %d", outOfScopeBitrate)
	}
}

func TestEnrichMissing_ReturnsPersistError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "onsei-test-analyze-bitrate-enrich-error-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	repo, err := sqlite.NewRepository(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to create repo: %v", err)
	}
	defer repo.Close()

	mp3Path := filepath.Join(tmpDir, "track.mp3")
	writeTestMP3Frame(t, mp3Path)

	if _, dropErr := repo.DB().Exec("DROP TABLE entries"); dropErr != nil {
		t.Fatalf("failed to drop entries table: %v", dropErr)
	}

	a := newBitrateAnalyzer(repo, "")
	err = a.enrichMissing(context.Background(),
		[]reconcile.AudioEntry{{PathPosix: filepath.ToSlash(mp3Path), Bitrate: 0, Format: "audio/mpeg"}}, true,
	)
	if err == nil {
		t.Fatal("expected enrich error, got nil")
	}
	if !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("expected no such table error, got %v", err)
	}
}

func TestEnrichMissingWithBatchOption_DoesNotEmitGlobalLogMetrics(t *testing.T) {
	var buf bytes.Buffer
	oldOut := log.Writer()
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(oldOut)
		log.SetFlags(oldFlags)
		log.SetPrefix(oldPrefix)
	}()

	a := &bitrateAnalyzer{}
	err := a.enrichMissing(context.Background(),
		[]reconcile.AudioEntry{{PathPosix: "/scope/a.flac", Bitrate: 0, Format: "audio/flac"}},
		true,
	)
	if err != nil {
		t.Fatalf("expected enrich success, got %v", err)
	}

	if got := strings.TrimSpace(buf.String()); got != "" {
		t.Fatalf("expected no global log output, got %q", got)
	}
}
