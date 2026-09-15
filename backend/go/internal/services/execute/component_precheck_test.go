package execute_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onsei/organizer/backend/internal/services/execute"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

//nolint:funlen // declarative rejection matrix; splitting would fragment the shared fixture harness
func TestComponentRun_PrecheckRejections(t *testing.T) {
	mp3 := reconcile.AudioOutputSpec{
		Codec:   reconcile.CodecMp3,
		Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192},
	}
	mp3Profile := reconcile.DesiredProfile{Encoded: &mp3}
	wavSpec := reconcile.AudioOutputSpec{Codec: reconcile.CodecWav}

	base := func(t *testing.T) (root, source, target string) {
		t.Helper()
		root, source = newAudioRoot(t)
		target = filepath.Join(root, "track.mp3")
		return root, source, target
	}

	testCases := []struct {
		name  string
		want  string
		build func(t *testing.T) execute.ComponentRunRequest
	}{
		{
			name: "blocked component",
			want: execute.ComponentCodeBlocked,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				component := componentFixture(
					[]reconcile.FileTuple{freezeFile(t, source)},
					encodeOp("cmp-1", source, target),
				)
				component.Status = reconcile.StatusBlocked
				return runRequest(root, component, mp3Profile, execute.DeleteModeSoft)
			},
		},
		{
			name: "unknown operation kind",
			want: execute.ComponentCodeUnsupportedOp,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				op := encodeOp("cmp-1", source, target)
				op.Kind = "frobnicate"
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, op),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "wrong operation phase",
			want: execute.ComponentCodeUnsupportedOp,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				op := encodeOp("cmp-1", source, target)
				op.Phase = reconcile.PhaseRemoveObsoleteAudio
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, op),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "removal with a target path",
			want: execute.ComponentCodeUnsupportedOp,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				op := removeOp("cmp-1", source)
				op.TargetPath = filepath.ToSlash(target)
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, op),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "encode without a target",
			want: execute.ComponentCodeUnsupportedOp,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				op := encodeOp("cmp-1", source, target)
				op.TargetPath = ""
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, op),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "no effective target spec",
			want: execute.ComponentCodeUnsupportedOp,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, encodeOp("cmp-1", source, target)),
					reconcile.DesiredProfile{},
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "encoded spec without bitrate",
			want: execute.ComponentCodeInvalidRequest,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, encodeOp("cmp-1", source, target)),
					reconcile.DesiredProfile{Encoded: &reconcile.AudioOutputSpec{Codec: reconcile.CodecMp3}},
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "unsupported spec codec",
			want: execute.ComponentCodeInvalidRequest,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, encodeOp("cmp-1", source, target)),
					reconcile.DesiredProfile{Encoded: &reconcile.AudioOutputSpec{Codec: "ogg"}},
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "duplicate materialize target",
			want: execute.ComponentCodeConflict,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				return runRequest(
					root,
					componentFixture(
						[]reconcile.FileTuple{freezeFile(t, source)},
						encodeOp("cmp-1", source, target),
						encodeOp("cmp-1", source, target),
					),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "source equals target",
			want: execute.ComponentCodeConflict,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, _ := base(t)
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, encodeOp("cmp-1", source, source)),
					reconcile.DesiredProfile{Lossless: &wavSpec},
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "source is another operation's target",
			want: execute.ComponentCodeConflict,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				middle := filepath.Join(root, "middle.mp3")
				return runRequest(
					root,
					componentFixture(
						[]reconcile.FileTuple{freezeFile(t, source)},
						encodeOp("cmp-1", source, middle),
						encodeOp("cmp-1", middle, target),
					),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "removal collides with a target",
			want: execute.ComponentCodeConflict,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				return runRequest(
					root,
					componentFixture(
						[]reconcile.FileTuple{freezeFile(t, source)},
						encodeOp("cmp-1", source, target),
						removeOp("cmp-1", target),
					),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "removal collides with a source",
			want: execute.ComponentCodeConflict,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				return runRequest(
					root,
					componentFixture(
						[]reconcile.FileTuple{freezeFile(t, source)},
						encodeOp("cmp-1", source, target),
						removeOp("cmp-1", source),
					),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "duplicate removal",
			want: execute.ComponentCodeConflict,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, _ := base(t)
				return runRequest(
					root,
					componentFixture(
						[]reconcile.FileTuple{freezeFile(t, source)},
						removeOp("cmp-1", source),
						removeOp("cmp-1", source),
					),
					reconcile.DesiredProfile{},
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "dependency is not a materialize target",
			want: execute.ComponentCodeInvalidDependency,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, _ := base(t)
				return runRequest(
					root,
					componentFixture(
						[]reconcile.FileTuple{freezeFile(t, source)},
						removeOp("cmp-1", source, filepath.Join(root, "unknown.mp3")),
					),
					reconcile.DesiredProfile{},
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "operation from another component",
			want: execute.ComponentCodeUnsupportedOp,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				op := encodeOp("other", source, target)
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, op),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "unsupported delete mode",
			want: execute.ComponentCodeInvalidRequest,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root := t.TempDir()
				return runRequest(root, componentFixture(nil), reconcile.DesiredProfile{}, "")
			},
		},
		{
			name: "missing root",
			want: execute.ComponentCodeInvalidRequest,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				_ = root
				return runRequest(
					"",
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, encodeOp("cmp-1", source, target)),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "root is not a directory",
			want: execute.ComponentCodeInvalidRequest,
			build: func(t *testing.T) execute.ComponentRunRequest {
				_, source, _ := base(t)
				return runRequest(source, componentFixture(nil), reconcile.DesiredProfile{}, execute.DeleteModeSoft)
			},
		},
		{
			name: "source outside the root",
			want: execute.ComponentCodePathUnsafe,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, _, target := base(t)
				_, outsideSource := newAudioRoot(t)
				return runRequest(
					root,
					componentFixture(
						[]reconcile.FileTuple{freezeFile(t, outsideSource)},
						encodeOp("cmp-1", outsideSource, target),
					),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "target outside the root",
			want: execute.ComponentCodePathUnsafe,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, _ := base(t)
				outsideTarget := filepath.Join(t.TempDir(), "evil.mp3")
				return runRequest(
					root,
					componentFixture(
						[]reconcile.FileTuple{freezeFile(t, source)},
						encodeOp("cmp-1", source, outsideTarget),
					),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "symlinked directory in the target path",
			want: execute.ComponentCodePathUnsafe,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, _ := base(t)
				link := filepath.Join(root, "link")
				if err := os.Symlink(t.TempDir(), link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				return runRequest(
					root,
					componentFixture(
						[]reconcile.FileTuple{freezeFile(t, source)},
						encodeOp("cmp-1", source, filepath.Join(link, "new.mp3")),
					),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "symlinked source",
			want: execute.ComponentCodePathUnsafe,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, _, target := base(t)
				targetFile := filepath.Join(t.TempDir(), "real.flac")
				writeBytes(t, targetFile, []byte("real"))
				link := filepath.Join(root, "evil.flac")
				if err := os.Symlink(targetFile, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, link)}, encodeOp("cmp-1", link, target)),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "existing target without a replacement declaration",
			want: execute.ComponentCodeConflict,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				writeBytes(t, target, []byte("existing"))
				return runRequest(
					root,
					componentFixture(
						[]reconcile.FileTuple{freezeFile(t, source), freezeFile(t, target)},
						encodeOp("cmp-1", source, target),
					),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "occupied fresh target",
			want: execute.ComponentCodeConflict,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				writeBytes(t, target, []byte("occupied"))
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, encodeOp("cmp-1", source, target)),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "source changed since planning",
			want: execute.ComponentCodeFileChanged,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				tuple := freezeFile(t, source)
				writeBytes(t, source, append(readBytes(t, source), 0))
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{tuple}, encodeOp("cmp-1", source, target)),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "source missing since planning",
			want: execute.ComponentCodeFileChanged,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				tuple := freezeFile(t, source)
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				return runRequest(
					root,
					componentFixture([]reconcile.FileTuple{tuple}, encodeOp("cmp-1", source, target)),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "source not part of the frozen component",
			want: execute.ComponentCodeFileChanged,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				return runRequest(
					root,
					componentFixture(nil, encodeOp("cmp-1", source, target)),
					mp3Profile,
					execute.DeleteModeSoft,
				)
			},
		},
		{
			name: "encode tools unavailable",
			want: execute.ComponentCodeToolsUnavailable,
			build: func(t *testing.T) execute.ComponentRunRequest {
				root, source, target := base(t)
				request := runRequest(
					root,
					componentFixture([]reconcile.FileTuple{freezeFile(t, source)}, encodeOp("cmp-1", source, target)),
					mp3Profile,
					execute.DeleteModeSoft,
				)
				request.Tools = execute.ToolsConfig{
					FFmpegPath:  filepath.Join(root, "missing-ffmpeg"),
					FFprobePath: filepath.Join(root, "missing-ffprobe"),
				}
				return request
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := testCase.build(t)
			result, err := execute.RunComponent(t.Context(), request)
			if err == nil {
				t.Fatal("expected a precheck rejection")
			}
			if got := errorCode(t, err); got != testCase.want {
				t.Fatalf("code = %s, want %s", got, testCase.want)
			}
			if result.Status != execute.ComponentStatusFailed || result.Stage != execute.ComponentStagePrecheck {
				t.Fatalf("facts = %+v", result)
			}
		})
	}
}
