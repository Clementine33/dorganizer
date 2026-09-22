package execute_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/onsei/organizer/backend/internal/adapters/ffmpeg"
	"github.com/onsei/organizer/backend/internal/conversion/execute"
	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
)

func requireStage(t *testing.T, err error, stage string) {
	t.Helper()
	var componentErr *execute.ComponentError
	if !errors.As(err, &componentErr) {
		t.Fatalf("error is not a *ComponentError: %v", err)
	}
	if componentErr.Stage != stage {
		t.Fatalf("stage = %s, want %s", componentErr.Stage, stage)
	}
}

func mp3Spec() reconcile.AudioOutputSpec {
	return reconcile.AudioOutputSpec{
		Codec:   reconcile.CodecMp3,
		Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192},
	}
}

// TestPreparedComponent_CommitKeepsFrozenOrderWhenEncodesFinishReversed drives
// the split API the way a session pool does: two encodes overlap, the second
// one finishes first, and the commit still lands them in frozen order.
func TestPreparedComponent_CommitKeepsFrozenOrderWhenEncodesFinishReversed(t *testing.T) {
	root, wav := newAudioRoot(t)
	first := filepath.Join(root, "a.mp3")
	second := filepath.Join(root, "b.mp3")
	spec := mp3Spec()

	encoder := ffmpeg.New(execute.ToolsConfig{})
	secondDone := make(chan struct{})
	kit := execute.ComponentRunTestKit{Encode: func(
		ctx context.Context, src, dst string, outSpec reconcile.AudioOutputSpec,
	) error {
		if strings.Contains(filepath.Base(dst), "a.mp3") {
			<-secondDone // the first target finishes after the second one
		} else {
			defer close(secondDone)
		}
		return encoder.Encode(ctx, src, dst, outSpec)
	}}
	prepared, err := execute.PrepareComponentWithTestKit(t.Context(), runRequest(
		root,
		componentFixture(
			[]reconcile.FileTuple{freezeFile(t, wav)},
			encodeOp("cmp-1", wav, first),
			encodeOp("cmp-1", wav, second),
		),
		reconcile.DesiredProfile{Encoded: &spec},
		execute.DeleteModeSoft,
	), kit)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prepared.Encodes() != 2 {
		t.Fatalf("encodes = %d, want 2", prepared.Encodes())
	}

	var wg sync.WaitGroup
	errs := make([]error, prepared.Encodes())
	finished := make(chan int, prepared.Encodes())
	for i := range prepared.Encodes() {
		wg.Go(func() {
			errs[i] = prepared.EncodeOne(t.Context(), i)
			finished <- i
		})
	}
	wg.Wait()
	close(finished)
	var order []int
	for i := range finished {
		order = append(order, i)
	}
	if !slices.Equal(order, []int{1, 0}) {
		t.Fatalf("encode completion order = %v, want the second output first", order)
	}
	for i, encErr := range errs {
		if encErr != nil {
			t.Fatalf("encode %d: %v", i, encErr)
		}
	}

	result, err := prepared.Commit(t.Context())
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if result.Status != execute.ComponentStatusSucceeded {
		t.Fatalf("status = %s", result.Status)
	}
	want := []string{filepath.ToSlash(first), filepath.ToSlash(second)}
	if !slices.Equal(result.Committed, want) {
		t.Fatalf("committed = %v, want the frozen order %v", result.Committed, want)
	}
	for _, path := range want {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("%s is missing: %v", path, statErr)
		}
	}
	if leftovers := tempFiles(t, root); len(leftovers) != 0 {
		t.Fatalf("staging leftovers: %v", leftovers)
	}
}

// TestPreparedComponent_DiscardAfterEncodeFailure covers the cleanup a failed
// component owes the disk and the facts it reports: nothing committed, every
// frozen target still to do, no staging file left behind.
func TestPreparedComponent_DiscardAfterEncodeFailure(t *testing.T) {
	root, wav := newAudioRoot(t)
	first := filepath.Join(root, "a.mp3")
	broken := filepath.Join(root, "broken.flac")
	writeBytes(t, broken, []byte(strings.Repeat("\xaa", 8192)))
	second := filepath.Join(root, "b.flac")
	encoded := mp3Spec()
	lossless := reconcile.AudioOutputSpec{Codec: reconcile.CodecFlac}

	prepared, err := execute.PrepareComponentWithTestKit(t.Context(), runRequest(
		root,
		componentFixture(
			[]reconcile.FileTuple{freezeFile(t, wav), freezeFile(t, broken)},
			encodeOp("cmp-1", wav, first),
			encodeOp("cmp-1", broken, second),
		),
		reconcile.DesiredProfile{Lossless: &lossless, Encoded: &encoded},
		execute.DeleteModeSoft,
	), execute.ComponentRunTestKit{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := prepared.EncodeOne(t.Context(), 0); err != nil {
		t.Fatalf("first encode: %v", err)
	}
	if leftovers := tempFiles(t, root); len(leftovers) != 1 {
		t.Fatalf("staged temp = %v, want one before the discard", leftovers)
	}
	encErr := prepared.EncodeOne(t.Context(), 1)
	if encErr == nil {
		t.Fatal("expected the broken source to fail")
	}
	if code := errorCode(t, encErr); code != execute.ComponentCodeEncodeFailed {
		t.Fatalf("code = %s", code)
	}
	requireStage(t, encErr, execute.ComponentStageMaterialize)

	result := prepared.Discard(encErr)
	if result.Status != execute.ComponentStatusFailed || result.Stage != execute.ComponentStageMaterialize {
		t.Fatalf("status = %s, stage = %s", result.Status, result.Stage)
	}
	wantRemaining := []string{filepath.ToSlash(first), filepath.ToSlash(second)}
	if !slices.Equal(result.Remaining, wantRemaining) {
		t.Fatalf("remaining = %v, want %v", result.Remaining, wantRemaining)
	}
	if len(result.Committed) != 0 || len(result.Removed) != 0 || len(result.Recovery) != 0 {
		t.Fatalf("facts = %+v", result)
	}
	for _, path := range []string{first, second} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("%s must not exist", path)
		}
	}
	if leftovers := tempFiles(t, root); len(leftovers) != 0 {
		t.Fatalf("staging leftovers after the discard: %v", leftovers)
	}
}

// TestPreparedComponent_DiscardAfterCancellation reports a canceled stop with
// every target still to do and the staged temp cleaned.
func TestPreparedComponent_DiscardAfterCancellation(t *testing.T) {
	root, wav := newAudioRoot(t)
	first := filepath.Join(root, "a.mp3")
	second := filepath.Join(root, "b.mp3")
	spec := mp3Spec()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	encoder := ffmpeg.New(execute.ToolsConfig{})
	kit := execute.ComponentRunTestKit{Encode: func(
		encCtx context.Context, src, dst string, outSpec reconcile.AudioOutputSpec,
	) error {
		err := encoder.Encode(encCtx, src, dst, outSpec)
		cancel()
		return err
	}}
	prepared, err := execute.PrepareComponentWithTestKit(ctx, runRequest(
		root,
		componentFixture(
			[]reconcile.FileTuple{freezeFile(t, wav)},
			encodeOp("cmp-1", wav, first),
			encodeOp("cmp-1", wav, second),
		),
		reconcile.DesiredProfile{Encoded: &spec},
		execute.DeleteModeSoft,
	), kit)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if encErr := prepared.EncodeOne(ctx, 0); encErr != nil {
		t.Fatalf("first encode: %v", encErr)
	}
	encErr := prepared.EncodeOne(ctx, 1)
	if encErr == nil {
		t.Fatal("expected the canceled encode to stop")
	}
	if !errors.Is(encErr, context.Canceled) || errorCode(t, encErr) != execute.ComponentCodeCanceled {
		t.Fatalf("err = %v", encErr)
	}

	result := prepared.Discard(encErr)
	if result.Status != execute.ComponentStatusCanceled || result.Stage != execute.ComponentStageMaterialize {
		t.Fatalf("status = %s, stage = %s", result.Status, result.Stage)
	}
	wantRemaining := []string{filepath.ToSlash(first), filepath.ToSlash(second)}
	if !slices.Equal(result.Remaining, wantRemaining) {
		t.Fatalf("remaining = %v, want %v", result.Remaining, wantRemaining)
	}
	if leftovers := tempFiles(t, root); len(leftovers) != 0 {
		t.Fatalf("staging leftovers after the discard: %v", leftovers)
	}
}

// TestPreparedComponent_DiscardWithoutCause reports a canceled stop for a
// component that never started encoding.
func TestPreparedComponent_DiscardWithoutCause(t *testing.T) {
	root, wav := newAudioRoot(t)
	target := filepath.Join(root, "a.mp3")
	spec := mp3Spec()
	prepared, err := execute.PrepareComponentWithTestKit(t.Context(), runRequest(
		root,
		componentFixture(
			[]reconcile.FileTuple{freezeFile(t, wav)},
			encodeOp("cmp-1", wav, target),
		),
		reconcile.DesiredProfile{Encoded: &spec},
		execute.DeleteModeSoft,
	), execute.ComponentRunTestKit{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	result := prepared.Discard(nil)
	if result.Status != execute.ComponentStatusCanceled || result.Stage != "" {
		t.Fatalf("status = %s, stage = %s", result.Status, result.Stage)
	}
	if !slices.Equal(result.Remaining, []string{filepath.ToSlash(target)}) {
		t.Fatalf("remaining = %v", result.Remaining)
	}
}

// TestPreparedComponent_DiscardReportsLeftoverTemp covers a cleanup that could
// not remove its own staging file: the path is reported, never dropped.
func TestPreparedComponent_DiscardReportsLeftoverTemp(t *testing.T) {
	root, wav := newAudioRoot(t)
	target := filepath.Join(root, "a.mp3")
	spec := mp3Spec()
	kit := execute.ComponentRunTestKit{Remove: func(p string) error {
		if strings.Contains(filepath.Base(p), ".tmp.") {
			return errors.New("injected cleanup failure")
		}
		return os.Remove(p)
	}}
	prepared, err := execute.PrepareComponentWithTestKit(t.Context(), runRequest(
		root,
		componentFixture(
			[]reconcile.FileTuple{freezeFile(t, wav)},
			encodeOp("cmp-1", wav, target),
		),
		reconcile.DesiredProfile{Encoded: &spec},
		execute.DeleteModeSoft,
	), kit)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if err := prepared.EncodeOne(t.Context(), 0); err != nil {
		t.Fatalf("encode: %v", err)
	}
	result := prepared.Discard(nil)
	if len(result.Recovery) != 1 || !strings.Contains(result.Recovery[0], ".tmp.") {
		t.Fatalf("recovery = %v, want the leftover temp", result.Recovery)
	}
}

// TestPreparedComponent_ConcurrentEncodeFailureLeavesNothingStaged runs one
// failing encode beside a healthy one and discards the component: no staged
// temp and no target may survive, and the failure belongs to materialize.
func TestPreparedComponent_ConcurrentEncodeFailureLeavesNothingStaged(t *testing.T) {
	root, wav := newAudioRoot(t)
	first := filepath.Join(root, "a.mp3")
	broken := filepath.Join(root, "broken.flac")
	writeBytes(t, broken, []byte(strings.Repeat("\xaa", 8192)))
	second := filepath.Join(root, "b.mp3")
	spec := mp3Spec()

	prepared, err := execute.PrepareComponentWithTestKit(t.Context(), runRequest(
		root,
		componentFixture(
			[]reconcile.FileTuple{freezeFile(t, wav), freezeFile(t, broken)},
			encodeOp("cmp-1", wav, first),
			encodeOp("cmp-1", broken, second),
		),
		reconcile.DesiredProfile{Encoded: &spec},
		execute.DeleteModeSoft,
	), execute.ComponentRunTestKit{})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, prepared.Encodes())
	for i := range prepared.Encodes() {
		wg.Go(func() { errs[i] = prepared.EncodeOne(t.Context(), i) })
	}
	wg.Wait()
	if errs[0] != nil {
		t.Fatalf("healthy encode: %v", errs[0])
	}
	if errs[1] == nil {
		t.Fatal("expected the broken source to fail")
	}
	if code := errorCode(t, errs[1]); code != execute.ComponentCodeEncodeFailed {
		t.Fatalf("code = %s", code)
	}
	requireStage(t, errs[1], execute.ComponentStageMaterialize)

	result := prepared.Discard(errs[1])
	if result.Status != execute.ComponentStatusFailed || result.Stage != execute.ComponentStageMaterialize {
		t.Fatalf("status = %s, stage = %s", result.Status, result.Stage)
	}
	if leftovers := tempFiles(t, root); len(leftovers) != 0 {
		t.Fatalf("staging leftovers: %v", leftovers)
	}
	for _, path := range []string{first, second} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("%s must not exist", path)
		}
	}
}
