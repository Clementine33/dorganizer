package execute_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/adapters/ffmpeg"
	"github.com/onsei/organizer/backend/internal/conversion/execute"
	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
)

func TestComponentRun_CommitFailureStopsCleanup(t *testing.T) {
	root, wav := newAudioRoot(t)
	first := filepath.Join(root, "a.mp3")
	second := filepath.Join(root, "b.mp3")
	obsolete := filepath.Join(root, "legacy.flac")
	mustFFmpeg(t, "-i", wav, "-c:a", "flac", obsolete)
	spec := reconcile.AudioOutputSpec{
		Codec:   reconcile.CodecMp3,
		Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192},
	}

	component := componentFixture(
		[]reconcile.FileTuple{freezeFile(t, wav), freezeFile(t, obsolete)},
		encodeOp("cmp-1", wav, first),
		encodeOp("cmp-1", wav, second),
		removeOp("cmp-1", obsolete, first, second),
	)
	kit := execute.ComponentRunTestKit{Rename: func(oldpath, newpath string) error {
		if strings.HasPrefix(filepath.Base(oldpath), "b.mp3.tmp.") {
			return errors.New("injected rename failure")
		}
		return os.Rename(oldpath, newpath)
	}}
	result, err := execute.RunComponentWithTestKit(t.Context(), runRequest(
		root, component, reconcile.DesiredProfile{Encoded: &spec}, execute.DeleteModeSoft,
	), kit)
	if err == nil {
		t.Fatal("expected the second commit to fail")
	}
	if code := errorCode(t, err); code != execute.ComponentCodeCommitFailed {
		t.Fatalf("code = %s", code)
	}
	if result.Status != execute.ComponentStatusFailed || result.Stage != execute.ComponentStageCommit {
		t.Fatalf("status = %s, stage = %s", result.Status, result.Stage)
	}
	if !slices.Equal(result.Committed, []string{filepath.ToSlash(first)}) {
		t.Fatalf("committed = %v", result.Committed)
	}
	if _, statErr := os.Stat(first); statErr != nil {
		t.Fatalf("committed output must survive: %v", statErr)
	}
	if _, statErr := os.Lstat(second); !os.IsNotExist(statErr) {
		t.Fatal("failed commit must not leave a target")
	}
	if _, statErr := os.Stat(obsolete); statErr != nil {
		t.Fatal("cleanup must not run after a failed commit")
	}
	wantRemaining := []string{filepath.ToSlash(second), filepath.ToSlash(obsolete)}
	if !slices.Equal(result.Remaining, wantRemaining) {
		t.Fatalf("remaining = %v, want %v", result.Remaining, wantRemaining)
	}
	if leftovers := tempFiles(t, root); len(leftovers) != 0 {
		t.Fatalf("staging leftovers: %v", leftovers)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "Delete")); !os.IsNotExist(statErr) {
		t.Fatal("no removal may start after a failed commit")
	}
}

func TestComponentRun_DeleteFailureStopsRemovals(t *testing.T) {
	root, wav := newAudioRoot(t)
	first := filepath.Join(root, "a.mp3")
	one := filepath.Join(root, "one.mp3")
	two := filepath.Join(root, "two.mp3")
	writeBytes(t, one, []byte("one"))
	writeBytes(t, two, []byte("two"))
	spec := reconcile.AudioOutputSpec{
		Codec:   reconcile.CodecMp3,
		Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192},
	}

	component := componentFixture(
		[]reconcile.FileTuple{freezeFile(t, wav), freezeFile(t, one), freezeFile(t, two)},
		encodeOp("cmp-1", wav, first),
		removeOp("cmp-1", one, first),
		removeOp("cmp-1", two, first),
	)
	kit := execute.ComponentRunTestKit{Rename: func(oldpath, newpath string) error {
		if strings.Contains(newpath, string(filepath.Separator)+"Delete"+string(filepath.Separator)) &&
			strings.HasPrefix(filepath.Base(newpath), "two.") {
			return errors.New("injected rename failure")
		}
		return os.Rename(oldpath, newpath)
	}}
	result, err := execute.RunComponentWithTestKit(t.Context(), runRequest(
		root, component, reconcile.DesiredProfile{Encoded: &spec}, execute.DeleteModeSoft,
	), kit)
	if err == nil {
		t.Fatal("expected the second removal to fail")
	}
	if code := errorCode(t, err); code != execute.ComponentCodeDeleteFailed {
		t.Fatalf("code = %s", code)
	}
	if result.Status != execute.ComponentStatusFailed || result.Stage != execute.ComponentStageRemove {
		t.Fatalf("status = %s, stage = %s", result.Status, result.Stage)
	}
	if !slices.Equal(result.Committed, []string{filepath.ToSlash(first)}) {
		t.Fatalf("committed = %v", result.Committed)
	}
	if !slices.Equal(result.Removed, []string{filepath.ToSlash(one)}) {
		t.Fatalf("removed = %v", result.Removed)
	}
	wantRecovery := []string{filepath.ToSlash(filepath.Join(root, "Delete", "one.mp3"))}
	if !slices.Equal(result.Recovery, wantRecovery) {
		t.Fatalf("recovery = %v", result.Recovery)
	}
	if string(readBytes(t, two)) != "two" {
		t.Fatal("the failed removal must keep its file")
	}
	if _, statErr := os.Stat(first); statErr != nil {
		t.Fatalf("valid new output must be kept: %v", statErr)
	}
	if !slices.Equal(result.Remaining, []string{filepath.ToSlash(two)}) {
		t.Fatalf("remaining = %v", result.Remaining)
	}
}

func TestComponentRun_ReplacementCleanupFailureReportsCommittedOutput(t *testing.T) {
	root, wav := newAudioRoot(t)
	target := filepath.Join(root, "track.mp3")
	mustFFmpeg(t, "-i", wav, "-c:a", "libmp3lame", "-b:a", "96k", target)
	spec := reconcile.AudioOutputSpec{
		Codec:   reconcile.CodecMp3,
		Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192},
	}
	component := componentFixture(
		[]reconcile.FileTuple{freezeFile(t, wav), freezeFile(t, target)},
		encodeOp("cmp-1", wav, target),
	)
	component.Variants = declaredReplacement(target)
	kit := execute.ComponentRunTestKit{Remove: func(p string) error {
		if strings.Contains(filepath.Base(p), ".tmp-old.") {
			return errors.New("injected cleanup failure")
		}
		return os.Remove(p)
	}}
	result, err := execute.RunComponentWithTestKit(t.Context(), runRequest(
		root, component, reconcile.DesiredProfile{Encoded: &spec}, execute.DeleteModeHard,
	), kit)
	if err == nil {
		t.Fatal("expected the replaced-old cleanup to fail")
	}
	if code := errorCode(t, err); code != execute.ComponentCodeCommitFailed {
		t.Fatalf("code = %s", code)
	}
	if result.Status != execute.ComponentStatusFailed || result.Stage != execute.ComponentStageCommit {
		t.Fatalf("status = %s, stage = %s", result.Status, result.Stage)
	}
	// The new output landed before the cleanup failed and must be reported as
	// committed, with the old copy kept and listed.
	if !slices.Equal(result.Committed, []string{filepath.ToSlash(target)}) {
		t.Fatalf("committed = %v", result.Committed)
	}
	if len(result.Remaining) != 0 {
		t.Fatalf("remaining = %v", result.Remaining)
	}
	if len(result.Recovery) != 1 || !strings.Contains(result.Recovery[0], ".tmp-old.") {
		t.Fatalf("recovery = %v", result.Recovery)
	}
	assertEncodedStream(t, probeStream(t, target), reconcile.CodecMp3, spec)
	leftovers := tempFiles(t, root)
	if len(leftovers) != 1 || filepath.ToSlash(leftovers[0]) != result.Recovery[0] {
		t.Fatalf("leftovers = %v, recovery = %v", leftovers, result.Recovery)
	}
}

func TestComponentRun_CancelStopsAtSafeBoundary(t *testing.T) {
	root, wav := newAudioRoot(t)
	first := filepath.Join(root, "a.mp3")
	second := filepath.Join(root, "b.mp3")
	spec := reconcile.AudioOutputSpec{
		Codec:   reconcile.CodecMp3,
		Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192},
	}

	ctx, cancel := context.WithCancel(t.Context())
	encoder := ffmpeg.New(execute.ToolsConfig{})
	kit := execute.ComponentRunTestKit{
		Encode: func(encCtx context.Context, src, dst string, outSpec reconcile.AudioOutputSpec) error {
			err := encoder.Encode(encCtx, src, dst, outSpec)
			cancel()
			return err
		},
	}
	result, err := execute.RunComponentWithTestKit(ctx, runRequest(
		root,
		componentFixture(
			[]reconcile.FileTuple{freezeFile(t, wav)},
			encodeOp("cmp-1", wav, first),
			encodeOp("cmp-1", wav, second),
		),
		reconcile.DesiredProfile{Encoded: &spec},
		execute.DeleteModeSoft,
	), kit)
	if err == nil {
		t.Fatal("expected cancellation")
	}
	if !errors.Is(err, context.Canceled) || errorCode(t, err) != execute.ComponentCodeCanceled {
		t.Fatalf("err = %v", err)
	}
	if result.Status != execute.ComponentStatusCanceled || result.Stage != execute.ComponentStageMaterialize {
		t.Fatalf("status = %s, stage = %s", result.Status, result.Stage)
	}
	if len(result.Committed) != 0 {
		t.Fatalf("committed = %v", result.Committed)
	}
	wantRemaining := []string{filepath.ToSlash(first), filepath.ToSlash(second)}
	if !slices.Equal(result.Remaining, wantRemaining) {
		t.Fatalf("remaining = %v", result.Remaining)
	}
	for _, path := range []string{first, second} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("%s must not exist", path)
		}
	}
	if leftovers := tempFiles(t, root); len(leftovers) != 0 {
		t.Fatalf("staging leftovers: %v", leftovers)
	}
}
