package execute_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	for _, codec := range []reconcile.Codec{
		reconcile.CodecWav, reconcile.CodecFlac, reconcile.CodecMp3, reconcile.CodecAac, reconcile.CodecOpus,
	} {
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
			if codec == reconcile.CodecOpus {
				// VBR: the average follows the content, not the setting — this
				// fixture's noise averages ~0.75x of it. What matters here is that
				// the file carries a measurable average at all (Ogg declares no
				// stream bitrate); the planner does not judge an Opus file by it.
				container := probeContainerBitrate(t, dst)
				if container < 192000*30/100 || container > 192000*130/100 {
					t.Fatalf("opus container average out of plausible range: %d", container)
				}
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

// TestFFmpegKeepsTags pins what a conversion does to a file's metadata: every
// target carries the source's tags across, wherever that container keeps them
// (MP3, MP4 and FLAC on the format, Ogg/Opus on the stream). Embedded pictures
// are deliberately out of scope: the workflow's first source is WAV, which
// cannot hold one, so no target gets container-specific handling for it.
func TestFFmpegKeepsTags(t *testing.T) {
	encoder, wav := audioFixture(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "tagged.flac")
	runFFmpeg(t,
		"-i", wav,
		"-metadata", "title=Tïtle 標題", "-metadata", "artist=Ärtist", "-metadata", "album=Álbum",
		"-metadata", "track=7", "-c:a", "flac", src,
	)
	if tags := probeTags(t, src); tags["title"] != "Tïtle 標題" || tags["artist"] != "Ärtist" {
		t.Fatalf("fixture lost its tags: %v", tags)
	}

	for _, codec := range []reconcile.Codec{
		reconcile.CodecMp3, reconcile.CodecAac, reconcile.CodecFlac, reconcile.CodecWav, reconcile.CodecOpus,
	} {
		t.Run(string(codec), func(t *testing.T) {
			spec := reconcile.AudioOutputSpec{Codec: codec}
			if !spec.Lossless() {
				spec.Quality = &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 192}
			}
			dst := filepath.Join(t.TempDir(), "output"+reconcile.ExtForCodec(codec))
			if err := encoder.Encode(t.Context(), src, dst, spec); err != nil {
				t.Fatal(err)
			}
			tags := probeTags(t, dst)
			if tags["title"] != "Tïtle 標題" || tags["artist"] != "Ärtist" || tags["album"] != "Álbum" {
				t.Fatalf("tags did not survive: %v", tags)
			}
		})
	}
}

func runFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	//nolint:gosec // The tool path and every argument come from this test's own fixtures.
	cmd := exec.CommandContext(t.Context(), "ffmpeg", append([]string{"-nostdin", "-v", "error", "-y"}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v: %s", args, err, output)
	}
}

// probeTags reads the tags ffprobe reports, wherever the container keeps them:
// MP3, MP4 and FLAC carry them on the format, Ogg/Opus on the stream.
func probeTags(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error",
		"-show_entries", "format_tags:stream_tags", "-of", "json", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Format struct {
			Tags map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			Tags map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	tags := map[string]string{}
	for key, value := range result.Format.Tags {
		tags[strings.ToLower(key)] = value
	}
	for _, stream := range result.Streams {
		for key, value := range stream.Tags {
			if _, exists := tags[strings.ToLower(key)]; !exists {
				tags[strings.ToLower(key)] = value
			}
		}
	}
	return tags
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

func probeContainerBitrate(t *testing.T, path string) int64 {
	t.Helper()
	data, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error",
		"-show_entries", "format=bit_rate", "-of", "json", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Format struct {
			Bitrate string `json:"bit_rate"`
		} `json:"format"`
	}
	if unmarshalErr := json.Unmarshal(data, &result); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	bitrate, parseErr := strconv.ParseInt(result.Format.Bitrate, 10, 64)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	return bitrate
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
