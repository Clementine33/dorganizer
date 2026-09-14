package execute_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onsei/organizer/backend/internal/services/execute"
)

// TestToolRunnerAACBitrate checks the real adapter's subprocess arguments.
func TestToolRunnerAACBitrate(t *testing.T) { checkToolRunnerArguments(t, ".m4a", "aac", "256k") }

func TestToolRunnerMP3Bitrate(t *testing.T) {
	checkToolRunnerArguments(t, ".mp3", "libmp3lame", "320k")
}

func checkToolRunnerArguments(t *testing.T, extension, codec, bitrate string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("argument recorder uses a POSIX shell; real codec tests are portable")
	}
	_, src := audioFixture(t)
	dir := t.TempDir()
	recorder := filepath.Join(dir, "ffmpeg-recorder")
	logPath := filepath.Join(dir, "arguments")
	t.Setenv("ONSEI_TEST_FFMPEG_ARGS", logPath)
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$ONSEI_TEST_FFMPEG_ARGS\"\nexec ffmpeg \"$@\"\n"
	if err := os.WriteFile(recorder, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := execute.NewToolRunner(execute.ToolsConfig{FFmpegPath: recorder})
	if err := runner.Convert(src, filepath.Join(dir, "output"+extension)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	args := string(raw)
	for _, want := range []string{"-c:a\n" + codec + "\n", "-b:a\n" + bitrate + "\n"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %q in arguments: %s", want, args)
		}
	}
	if strings.Contains(args, "-q:a\n") || strings.Contains(args, "--cvbr\n") {
		t.Fatalf("unexpected VBR option: %s", args)
	}
}
