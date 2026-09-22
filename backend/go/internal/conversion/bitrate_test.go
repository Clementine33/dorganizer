package conversion //nolint:testpackage // white-box tests exercise unexported internals

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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

// TestEnrichMissing_HandsOnlyScopedEntriesToTheInventory pins what the analyzer
// persists: exactly the entries it was handed and probed, each with the rate it
// measured, in one call carrying the batch option through.
func TestEnrichMissing_HandsOnlyScopedEntriesToTheInventory(t *testing.T) {
	tmpDir := t.TempDir()

	const scopedTotal = 120
	scopedEntries := make([]reconcile.AudioEntry, 0, scopedTotal)
	wantPaths := make([]string, 0, scopedTotal)
	for i := range scopedTotal {
		p := filepath.Join(tmpDir, fmt.Sprintf("in-scope-%03d.mp3", i))
		writeTestMP3Frame(t, p)
		pPosix := filepath.ToSlash(p)
		scopedEntries = append(scopedEntries, reconcile.AudioEntry{PathPosix: pPosix, Bitrate: 0, Format: "audio/mpeg"})
		wantPaths = append(wantPaths, pPosix)
	}
	// A track this root never collected: it must never be written, whatever its
	// extension says, because the analyzer only persists what it was handed.
	outOfScopePath := filepath.ToSlash(filepath.Join(tmpDir, "out-of-scope.mp3"))
	writeTestMP3Frame(t, filepath.FromSlash(outOfScopePath))

	inv := &recordingInventory{}
	a := newBitrateAnalyzer(inv, "")
	if err := a.enrichMissing(context.Background(), scopedEntries, true); err != nil {
		t.Fatalf("enrich scoped entries: %v", err)
	}

	written := inv.written()
	if len(written) != scopedTotal {
		t.Fatalf("wrote %d entries, want %d", len(written), scopedTotal)
	}
	// Probing runs concurrently, so the writes arrive in completion order:
	// what is pinned is which paths were written, each exactly once.
	want := make(map[string]bool, len(wantPaths))
	for _, path := range wantPaths {
		want[path] = true
	}
	seen := make(map[string]int, len(written))
	for _, path := range written {
		seen[path]++
	}
	for path := range want {
		if seen[path] != 1 {
			t.Fatalf("path %q written %d times, want once", path, seen[path])
		}
	}
	if seen[outOfScopePath] != 0 {
		t.Fatalf("wrote %q, which was never handed to the analyzer", outOfScopePath)
	}
	for i, entry := range scopedEntries {
		if entry.Bitrate <= 0 {
			t.Fatalf("entry %d was handed to the writer without its probed rate", i)
		}
	}
	if len(inv.batch) != 1 || !inv.batch[0] {
		t.Fatalf("batch option reached the writer as %v, want one call with true", inv.batch)
	}
}

// TestEnrichMissing_ReturnsPersistError keeps a failing write visible: the
// planner must not report a successful pass over facts that were not stored.
func TestEnrichMissing_ReturnsPersistError(t *testing.T) {
	tmpDir := t.TempDir()
	mp3Path := filepath.Join(tmpDir, "track.mp3")
	writeTestMP3Frame(t, mp3Path)

	writeErr := errors.New("write failed")
	inv := &recordingInventory{writeErr: writeErr}
	a := newBitrateAnalyzer(inv, "")
	err := a.enrichMissing(context.Background(),
		[]reconcile.AudioEntry{{PathPosix: filepath.ToSlash(mp3Path), Bitrate: 0, Format: "audio/mpeg"}}, true,
	)
	if !errors.Is(err, writeErr) {
		t.Fatalf("enrich error = %v, want the writer's failure", err)
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
