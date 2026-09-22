package execute_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/onsei/organizer/backend/internal/adapters/ffmpeg"
	"github.com/onsei/organizer/backend/internal/conversion/execute"
	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
)

// The pipeline's tests assert what actually landed on disk. They build their
// source through ffmpeg and read it back with ffprobe directly, so a bug in the
// port's own probing cannot hide a wrong file from them.

func audioFixture(t *testing.T) (execute.Encoder, string) {
	t.Helper()
	encoder := ffmpeg.New(execute.ToolsConfig{})
	if err := encoder.Check(); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "音声 with spaces.wav")
	cmd := exec.CommandContext(
		t.Context(),
		"ffmpeg",
		"-nostdin",
		"-v",
		"error",
		"-f",
		"lavfi",
		"-i",
		"anoisesrc=duration=2:sample_rate=44100:seed=1",
		"-ac",
		"2",
		"-c:a",
		"pcm_s24le",
		src,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, output)
	}
	return encoder, src
}

type probedStream struct {
	Codec   string `json:"codec_name"`
	Bits    string `json:"bits_per_raw_sample"`
	Bitrate string `json:"bit_rate"`
}

func probeStream(t *testing.T, path string) probedStream {
	t.Helper()
	data, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-show_streams", "-of", "json", path).
		Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Streams []probedStream `json:"streams"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Streams) != 1 {
		t.Fatalf("streams: %s", data)
	}
	return result.Streams[0]
}

// assertEncodedStream checks the codec, precision and bitrate ffprobe reports.
func assertEncodedStream(
	t *testing.T,
	stream probedStream,
	codec reconcile.Codec,
	spec reconcile.AudioOutputSpec,
) {
	t.Helper()
	want := string(codec)
	if codec == reconcile.CodecWav {
		want = "pcm_s24le"
	}
	if stream.Codec != want {
		t.Fatalf("codec = %s, want %s", stream.Codec, want)
	}
	if spec.Lossless() && stream.Bits != "24" {
		t.Fatalf("lost precision: %s", stream.Codec)
	}
	if codec == reconcile.CodecAac {
		bitrate, err := strconv.Atoi(stream.Bitrate)
		if err != nil || bitrate < 175000 || bitrate > 210000 {
			t.Fatalf("AAC did not honor 192 kbps: %s", stream.Bitrate)
		}
	}
	if codec == reconcile.CodecMp3 && stream.Bitrate != "192000" {
		t.Fatalf("wrong CBR: %s", stream.Bitrate)
	}
	if codec == reconcile.CodecOpus {
		// Ogg declares no per-stream bitrate at all; the container's average is
		// the fact the planner reads, checked where the output path is known.
		if stream.Bitrate != "" {
			t.Fatalf("opus stream bitrate unexpectedly declared: %s", stream.Bitrate)
		}
	}
}
