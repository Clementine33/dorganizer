package execute_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/onsei/organizer/backend/internal/services/execute"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

func audioFixture(t *testing.T) (execute.FFmpeg, string) {
	t.Helper()
	encoder := execute.FFmpeg{}
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

func TestFFmpegEncodesFrozenTargets(t *testing.T) {
	encoder, src := audioFixture(t)
	before, readErr := os.ReadFile(src)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, codec := range []reconcile.Codec{reconcile.CodecWav, reconcile.CodecFlac, reconcile.CodecMp3, reconcile.CodecAac} {
		t.Run(string(codec), func(t *testing.T) {
			spec := reconcile.AudioOutputSpec{Codec: codec}
			if !spec.Lossless() {
				spec.Quality = &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192}
			}
			dst := filepath.Join(t.TempDir(), "output.stage") // explicit muxer, not suffix guessing
			if err := encoder.Encode(t.Context(), src, dst, spec); err != nil {
				t.Fatal(err)
			}
			assertEncodedStream(t, probeStream(t, dst), codec, spec)
			legacyDst := filepath.Join(t.TempDir(), "legacy"+reconcile.ExtForCodec(codec))
			if err := execute.NewToolRunner(execute.ToolsConfig{}).Convert(src, legacyDst); err != nil {
				t.Fatalf("legacy toolrunner: %v", err)
			}
			if err := encoder.Encode(t.Context(), src, dst, spec); err == nil {
				t.Fatal("must not replace an existing target")
			}
		})
	}
	after, err := os.ReadFile(src)
	if err != nil || string(before) != string(after) {
		t.Fatalf("source changed: %v", err)
	}
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
}

func TestFFmpegRefusesInvalidAndCanceledConversion(t *testing.T) {
	encoder, src := audioFixture(t)
	spec := reconcile.AudioOutputSpec{Codec: reconcile.CodecAac}
	dst := filepath.Join(t.TempDir(), "invalid.m4a")
	if err := encoder.Encode(t.Context(), src, dst, spec); err == nil {
		t.Fatal("missing bitrate accepted")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("invalid request created output: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	spec.Quality = &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 256}
	if err := encoder.Encode(ctx, src, dst, spec); err == nil {
		t.Fatal("cancellation ignored")
	}
	if err := encoder.Encode(t.Context(), src, src, spec); err == nil {
		t.Fatal("source overwrite accepted")
	}
	if err := (execute.FFmpeg{Path: filepath.Join(t.TempDir(), "missing")}).Check(); err == nil {
		t.Fatal("missing tool accepted")
	}
}
