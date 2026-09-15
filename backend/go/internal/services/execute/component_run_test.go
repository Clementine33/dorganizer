package execute_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/services/execute"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// newAudioRoot returns a member root holding one real 24-bit PCM WAV source.
func newAudioRoot(t *testing.T) (string, string) {
	t.Helper()
	_, source := audioFixture(t)
	return filepath.Dir(source), source
}

// mustFFmpeg runs ffmpeg with the given arguments and fails the test on error.
func mustFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	//nolint:gosec // fixed ffmpeg binary with test-controlled arguments
	command := exec.CommandContext(t.Context(), "ffmpeg", append([]string{"-nostdin", "-v", "error", "-y"}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, output)
	}
}

// freezeFile records the (path, size, mtime) tuple the planner persists.
func freezeFile(t *testing.T, filePath string) reconcile.FileTuple {
	t.Helper()
	info, err := os.Lstat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	return reconcile.FileTuple{Path: filepath.ToSlash(filePath), Size: info.Size(), Mtime: info.ModTime().Unix()}
}

// encodeOp builds one frozen materialize operation.
func encodeOp(componentID, source, target string) reconcile.Operation {
	return reconcile.Operation{
		Kind:        reconcile.OpKindEncode,
		Phase:       reconcile.PhaseMaterializeOutputs,
		ComponentID: componentID,
		VariantStem: "track",
		SourcePath:  filepath.ToSlash(source),
		TargetPath:  filepath.ToSlash(target),
	}
}

// removeOp builds one frozen obsolete-removal operation.
func removeOp(componentID, source string, dependsOn ...string) reconcile.Operation {
	depends := make([]string, 0, len(dependsOn))
	for _, dep := range dependsOn {
		depends = append(depends, filepath.ToSlash(dep))
	}
	return reconcile.Operation{
		Kind:        reconcile.OpKindRemoveObsolete,
		Phase:       reconcile.PhaseRemoveObsoleteAudio,
		ComponentID: componentID,
		VariantStem: "track",
		SourcePath:  filepath.ToSlash(source),
		DependsOn:   depends,
	}
}

// componentFixture assembles one frozen component around the given files and
// operations.
func componentFixture(files []reconcile.FileTuple, ops ...reconcile.Operation) reconcile.ComponentOutcome {
	return reconcile.ComponentOutcome{
		ComponentID: "cmp-1",
		Partition:   reconcile.PartitionMatched,
		Status:      reconcile.StatusOK,
		Files:       files,
		Operations:  ops,
	}
}

// declaredReplacement marks a target as the plan's declared in-place
// replacement, matching how the planner records a rebuilt encoded output.
func declaredReplacement(target string) []reconcile.VariantDecision {
	return []reconcile.VariantDecision{{
		Stem: "track",
		Decisions: []reconcile.FileDecision{{
			Path:       filepath.ToSlash(target),
			Resolution: reconcile.ResolutionEncode,
			TargetPath: filepath.ToSlash(target),
		}},
	}}
}

// runRequest is the standard soft-mode request for one component.
func runRequest(
	root string,
	component reconcile.ComponentOutcome,
	specs reconcile.DesiredProfile,
	mode execute.DeleteMode,
) execute.ComponentRunRequest {
	return execute.ComponentRunRequest{Root: root, Component: component, Specs: specs, DeleteMode: mode}
}

// errorCode extracts the stable code of a ComponentError.
func errorCode(t *testing.T, err error) string {
	t.Helper()
	var componentErr *execute.ComponentError
	if !errors.As(err, &componentErr) {
		t.Fatalf("error is not a *ComponentError: %v", err)
	}
	return componentErr.Code
}

// tempFiles lists staging leftovers (.tmp. and .tmp-old. names) under root.
func tempFiles(t *testing.T, root string) []string {
	t.Helper()
	var leftovers []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.Contains(d.Name(), ".tmp") {
			leftovers = append(leftovers, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return leftovers
}

func readBytes(t *testing.T, filePath string) []byte {
	t.Helper()
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeBytes(t *testing.T, filePath string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestComponentRun_MaterializesFrozenTargets(t *testing.T) {
	cases := []struct {
		name     string
		codec    reconcile.Codec
		bitrate  int
		target   string
		lossless bool
	}{
		{name: "mp3", codec: reconcile.CodecMp3, bitrate: 192, target: "track.mp3"},
		{name: "aac", codec: reconcile.CodecAac, bitrate: 192, target: "track.m4a"},
		{name: "flac", codec: reconcile.CodecFlac, target: "track.flac", lossless: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root, source := newAudioRoot(t)
			target := filepath.Join(root, testCase.target)
			spec := reconcile.AudioOutputSpec{Codec: testCase.codec}
			profile := reconcile.DesiredProfile{}
			if testCase.lossless {
				profile.Lossless = &spec
			} else {
				spec.Quality = &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: testCase.bitrate}
				profile.Encoded = &spec
			}
			before := readBytes(t, source)
			result, err := execute.RunComponent(t.Context(), runRequest(
				root,
				componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, encodeOp("cmp-1", source, target)),
				profile,
				execute.DeleteModeSoft,
			))
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if result.Status != execute.ComponentStatusSucceeded || result.Stage != "" {
				t.Fatalf("status = %s, stage = %s", result.Status, result.Stage)
			}
			if !slices.Equal(result.Committed, []string{filepath.ToSlash(target)}) {
				t.Fatalf("committed = %v", result.Committed)
			}
			if len(result.Remaining) != 0 || len(result.Recovery) != 0 || len(result.Removed) != 0 {
				t.Fatalf("unexpected facts: %+v", result)
			}
			assertEncodedStream(t, probeStream(t, target), testCase.codec, spec)
			if string(readBytes(t, source)) != string(before) {
				t.Fatal("source changed")
			}
			if leftovers := tempFiles(t, root); len(leftovers) != 0 {
				t.Fatalf("staging leftovers: %v", leftovers)
			}
		})
	}

	t.Run("wav precision", func(t *testing.T) {
		root, wav := newAudioRoot(t)
		source := filepath.Join(root, "track24.flac")
		mustFFmpeg(t, "-i", wav, "-c:a", "flac", source)
		target := filepath.Join(root, "track24.wav")
		spec := reconcile.AudioOutputSpec{Codec: reconcile.CodecWav}
		result, err := execute.RunComponent(t.Context(), runRequest(
			root,
			componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, encodeOp("cmp-1", source, target)),
			reconcile.DesiredProfile{Lossless: &spec},
			execute.DeleteModeSoft,
		))
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if result.Status != execute.ComponentStatusSucceeded {
			t.Fatalf("status = %s", result.Status)
		}
		assertEncodedStream(t, probeStream(t, target), reconcile.CodecWav, spec)
	})
}

func TestComponentRun_SecondOutputFailureKeepsOldAudio(t *testing.T) {
	root, wav := newAudioRoot(t)
	first := filepath.Join(root, "a.mp3")
	broken := filepath.Join(root, "broken.flac")
	writeBytes(t, broken, []byte(strings.Repeat("\xaa", 8192)))
	second := filepath.Join(root, "b.flac")
	obsolete := filepath.Join(root, "legacy.flac")
	mustFFmpeg(t, "-i", wav, "-c:a", "flac", obsolete)

	encoded := reconcile.AudioOutputSpec{
		Codec:   reconcile.CodecMp3,
		Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192},
	}
	lossless := reconcile.AudioOutputSpec{Codec: reconcile.CodecFlac}
	component := componentFixture(
		[]reconcile.FileTuple{freezeFile(t, wav), freezeFile(t, broken), freezeFile(t, obsolete)},
		encodeOp("cmp-1", wav, first),
		encodeOp("cmp-1", broken, second),
		removeOp("cmp-1", obsolete, first),
	)
	result, err := execute.RunComponent(t.Context(), runRequest(
		root, component, reconcile.DesiredProfile{Lossless: &lossless, Encoded: &encoded}, execute.DeleteModeSoft,
	))
	if err == nil {
		t.Fatal("expected the second output to fail")
	}
	if code := errorCode(t, err); code != execute.ComponentCodeEncodeFailed {
		t.Fatalf("code = %s", code)
	}
	if result.Status != execute.ComponentStatusFailed || result.Stage != execute.ComponentStageMaterialize {
		t.Fatalf("status = %s, stage = %s", result.Status, result.Stage)
	}
	if len(result.Committed) != 0 || len(result.Removed) != 0 {
		t.Fatalf("nothing may commit when materialization fails: %+v", result)
	}
	wantRemaining := []string{filepath.ToSlash(first), filepath.ToSlash(second), filepath.ToSlash(obsolete)}
	if !slices.Equal(result.Remaining, wantRemaining) {
		t.Fatalf("remaining = %v, want %v", result.Remaining, wantRemaining)
	}
	for _, path := range []string{first, second} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("%s must not exist", path)
		}
	}
	if _, statErr := os.Stat(obsolete); statErr != nil {
		t.Fatalf("old audio was deleted: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "Delete")); !os.IsNotExist(statErr) {
		t.Fatal("no recovery directory may be created on a failed run")
	}
	if leftovers := tempFiles(t, root); len(leftovers) != 0 {
		t.Fatalf("staging leftovers: %v", leftovers)
	}
}

func TestComponentRun_SamePathReplacementPreservesOldOutput(t *testing.T) {
	root, wav := newAudioRoot(t)
	target := filepath.Join(root, "track.mp3")
	mustFFmpeg(t, "-i", wav, "-c:a", "libmp3lame", "-b:a", "96k", target)
	oldBytes := readBytes(t, target)
	obsolete := filepath.Join(root, "legacy.flac")
	mustFFmpeg(t, "-i", wav, "-c:a", "flac", obsolete)

	encoded := reconcile.AudioOutputSpec{
		Codec:   reconcile.CodecMp3,
		Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192},
	}
	component := componentFixture(
		[]reconcile.FileTuple{freezeFile(t, wav), freezeFile(t, target), freezeFile(t, obsolete)},
		encodeOp("cmp-1", wav, target),
		removeOp("cmp-1", obsolete, target),
	)
	component.Variants = declaredReplacement(target)
	result, err := execute.RunComponent(t.Context(), runRequest(
		root, component, reconcile.DesiredProfile{Encoded: &encoded}, execute.DeleteModeSoft,
	))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !slices.Equal(result.Committed, []string{filepath.ToSlash(target)}) {
		t.Fatalf("committed = %v", result.Committed)
	}
	// The replaced target now holds the new output and was not deleted.
	assertEncodedStream(t, probeStream(t, target), reconcile.CodecMp3, encoded)
	replacedCopy := filepath.Join(root, "Delete", "track.mp3")
	if string(readBytes(t, replacedCopy)) != string(oldBytes) {
		t.Fatal("old output content is not preserved under Delete/")
	}
	removedCopy := filepath.Join(root, "Delete", "legacy.flac")
	if _, statErr := os.Stat(removedCopy); statErr != nil {
		t.Fatalf("obsolete file was not recovered: %v", statErr)
	}
	wantRecovery := []string{filepath.ToSlash(replacedCopy), filepath.ToSlash(removedCopy)}
	if !slices.Equal(result.Recovery, wantRecovery) {
		t.Fatalf("recovery = %v, want %v", result.Recovery, wantRecovery)
	}
}

func TestComponentRun_SoftDeleteNeverOverwritesRecovery(t *testing.T) {
	root := t.TempDir()
	obsolete := filepath.Join(root, "obs.mp3")
	writeBytes(t, obsolete, []byte("orphan audio"))
	earlier := filepath.Join(root, "Delete", "obs.mp3")
	if err := os.MkdirAll(filepath.Dir(earlier), 0o755); err != nil {
		t.Fatal(err)
	}
	writeBytes(t, earlier, []byte("earlier recovery"))

	component := componentFixture([]reconcile.FileTuple{freezeFile(t, obsolete)}, removeOp("cmp-1", obsolete))
	result, err := execute.RunComponent(t.Context(), runRequest(
		root, component, reconcile.DesiredProfile{}, execute.DeleteModeSoft,
	))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !slices.Equal(result.Removed, []string{filepath.ToSlash(obsolete)}) {
		t.Fatalf("removed = %v", result.Removed)
	}
	if string(readBytes(t, earlier)) != "earlier recovery" {
		t.Fatal("an existing recovery file was overwritten")
	}
	unique := filepath.Join(root, "Delete", "obs.1.mp3")
	if !slices.Equal(result.Recovery, []string{filepath.ToSlash(unique)}) {
		t.Fatalf("recovery = %v", result.Recovery)
	}
	if string(readBytes(t, unique)) != "orphan audio" {
		t.Fatal("recovered content lost")
	}
}

func TestComponentRun_HardDeleteRemovesOnlyListedFiles(t *testing.T) {
	root := t.TempDir()
	obsolete := filepath.Join(root, "obs.mp3")
	companion := filepath.Join(root, "companion.mp3")
	note := filepath.Join(root, "notes.txt")
	writeBytes(t, obsolete, []byte("obsolete"))
	writeBytes(t, companion, []byte("keep me"))
	writeBytes(t, note, []byte("not audio"))

	component := componentFixture([]reconcile.FileTuple{freezeFile(t, obsolete)}, removeOp("cmp-1", obsolete))
	result, err := execute.RunComponent(t.Context(), runRequest(
		root, component, reconcile.DesiredProfile{}, execute.DeleteModeHard,
	))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, statErr := os.Lstat(obsolete); !os.IsNotExist(statErr) {
		t.Fatal("listed obsolete file must be hard-deleted")
	}
	if string(readBytes(t, companion)) != "keep me" {
		t.Fatal("unlisted audio was touched")
	}
	if string(readBytes(t, note)) != "not audio" {
		t.Fatal("non-audio file was touched")
	}
	if _, statErr := os.Lstat(filepath.Join(root, "Delete")); !os.IsNotExist(statErr) {
		t.Fatal("hard mode must not create a recovery directory")
	}
	if len(result.Removed) != 1 || len(result.Recovery) != 0 {
		t.Fatalf("facts = %+v", result)
	}
}

func TestComponentRun_DeleteOnlyNeedsNoEncodeTools(t *testing.T) {
	root := t.TempDir()
	obsolete := filepath.Join(root, "obs.mp3")
	writeBytes(t, obsolete, []byte("obsolete"))
	component := componentFixture([]reconcile.FileTuple{freezeFile(t, obsolete)}, removeOp("cmp-1", obsolete))
	request := runRequest(root, component, reconcile.DesiredProfile{}, execute.DeleteModeSoft)
	request.Tools = execute.ToolsConfig{
		FFmpegPath:  filepath.Join(root, "missing-ffmpeg"),
		FFprobePath: filepath.Join(root, "missing-ffprobe"),
	}
	result, err := execute.RunComponent(t.Context(), request)
	if err != nil {
		t.Fatalf("delete-only run must not need ffmpeg: %v", err)
	}
	if result.Status != execute.ComponentStatusSucceeded || len(result.Removed) != 1 {
		t.Fatalf("facts = %+v", result)
	}
}

func TestComponentRun_ZeroOperationsSucceeds(t *testing.T) {
	root := t.TempDir()
	request := runRequest(root, componentFixture(nil), reconcile.DesiredProfile{}, execute.DeleteModeSoft)
	request.Tools = execute.ToolsConfig{
		FFmpegPath:  filepath.Join(root, "missing-ffmpeg"),
		FFprobePath: filepath.Join(root, "missing-ffprobe"),
	}
	result, err := execute.RunComponent(t.Context(), request)
	if err != nil {
		t.Fatalf("zero-operation run must succeed without tools: %v", err)
	}
	if result.Status != execute.ComponentStatusSucceeded || result.Stage != "" || len(result.Remaining) != 0 {
		t.Fatalf("facts = %+v", result)
	}
}

func TestComponentRun_SoftDeleteWithoutRootRefused(t *testing.T) {
	cases := map[string]string{
		"empty root":   "",
		"missing root": filepath.Join(t.TempDir(), "missing"),
	}
	for name, rootArg := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			obsolete := filepath.Join(root, "obs.mp3")
			writeBytes(t, obsolete, []byte("obsolete"))
			component := componentFixture([]reconcile.FileTuple{freezeFile(t, obsolete)}, removeOp("cmp-1", obsolete))
			_, err := execute.RunComponent(t.Context(), runRequest(
				rootArg, component, reconcile.DesiredProfile{}, execute.DeleteModeSoft,
			))
			if err == nil {
				t.Fatal("soft deletion without a usable root must fail")
			}
			if code := errorCode(t, err); code != execute.ComponentCodeInvalidRequest {
				t.Fatalf("code = %s", code)
			}
			if _, statErr := os.Stat(obsolete); statErr != nil {
				t.Fatal("no fallback to hard deletion is allowed")
			}
		})
	}
}

func TestComponentRun_SoftDeletePreservesRelativePath(t *testing.T) {
	root := t.TempDir()
	obsolete := filepath.Join(root, "album", "disc", "obs.mp3")
	if err := os.MkdirAll(filepath.Dir(obsolete), 0o755); err != nil {
		t.Fatal(err)
	}
	writeBytes(t, obsolete, []byte("nested obsolete"))

	component := componentFixture([]reconcile.FileTuple{freezeFile(t, obsolete)}, removeOp("cmp-1", obsolete))
	result, err := execute.RunComponent(t.Context(), runRequest(
		root, component, reconcile.DesiredProfile{}, execute.DeleteModeSoft,
	))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := filepath.Join(root, "Delete", "album", "disc", "obs.mp3")
	if !slices.Equal(result.Recovery, []string{filepath.ToSlash(want)}) {
		t.Fatalf("recovery = %v, want %s", result.Recovery, want)
	}
	if string(readBytes(t, want)) != "nested obsolete" {
		t.Fatal("recovered content lost")
	}
	if _, statErr := os.Stat(obsolete); !os.IsNotExist(statErr) {
		t.Fatal("obsolete file still in place")
	}
}

func TestComponentRun_CanceledBeforeStart(t *testing.T) {
	root, source := newAudioRoot(t)
	target := filepath.Join(root, "track.mp3")
	spec := reconcile.AudioOutputSpec{
		Codec:   reconcile.CodecMp3,
		Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192},
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := execute.RunComponent(ctx, runRequest(
		root,
		componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, encodeOp("cmp-1", source, target)),
		reconcile.DesiredProfile{Encoded: &spec},
		execute.DeleteModeSoft,
	))
	if err == nil {
		t.Fatal("canceled request must not run")
	}
	if !errors.Is(err, context.Canceled) || errorCode(t, err) != execute.ComponentCodeCanceled {
		t.Fatalf("err = %v", err)
	}
	if result.Status != execute.ComponentStatusCanceled || result.Stage != "" || len(result.Remaining) != 0 {
		t.Fatalf("facts = %+v", result)
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatal("canceled run must not create outputs")
	}
	if leftovers := tempFiles(t, root); len(leftovers) != 0 {
		t.Fatalf("staging leftovers: %v", leftovers)
	}
}
