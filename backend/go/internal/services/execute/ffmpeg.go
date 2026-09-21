package execute

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// FFmpeg encodes the frozen target specification, independently of global
// encoder preferences. Empty paths use the executables on PATH.
type FFmpeg struct {
	Path      string
	ProbePath string
}

func newFFmpeg(cfg ToolsConfig) FFmpeg {
	return FFmpeg{Path: cfg.FFmpegPath, ProbePath: cfg.FFprobePath}
}

func (f FFmpeg) paths() (string, string) {
	encoder, probe := f.Path, f.ProbePath
	if encoder == "" {
		encoder = "ffmpeg"
	}
	if probe == "" {
		probe = "ffprobe"
	}
	return encoder, probe
}

// Check verifies both tools before any filesystem changes.
func (f FFmpeg) Check() error {
	encoder, probe := f.paths()
	for _, tool := range []string{encoder, probe} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("tool unavailable %s: %w", tool, err)
		}
	}
	return nil
}

// ToolVersion reports the encoder's own version line, the diagnostic a
// generation credential records beside its facts: it names the binary that
// wrote a file without claiming it as part of the file's identity — a re-encode
// with a newer encoder is the same generation, an encode with other settings is
// not. An unavailable tool reports an empty string rather than failing the
// generation it describes.
func ToolVersion(ctx context.Context, cfg ToolsConfig) string {
	encoder, _ := newFFmpeg(cfg).paths()
	out, err := exec.CommandContext(ctx, encoder, "-version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
}

type audioStream struct {
	Codec      string `json:"codec_name"`
	SampleRate string `json:"sample_rate"`
	Channels   int    `json:"channels"`
	Bits       int    `json:"bits_per_sample"`
	RawBits    string `json:"bits_per_raw_sample"`
	Duration   string `json:"duration"`
}

func (f FFmpeg) probe(ctx context.Context, path string) (audioStream, error) {
	_, probe := f.paths()
	data, err := exec.CommandContext(ctx, probe, "-v", "error", "-select_streams", "a:0", "-show_streams", "-of", "json", path).
		Output()
	if err != nil {
		return audioStream{}, fmt.Errorf("probe %s: %w", path, err)
	}
	var result struct {
		Streams []audioStream `json:"streams"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return audioStream{}, err
	}
	if len(result.Streams) != 1 || result.Streams[0].Channels < 1 {
		return audioStream{}, fmt.Errorf("no audio stream: %s", path)
	}
	return result.Streams[0], nil
}

// Encode creates and decodes a staged output before its caller can commit it.
// The source's tags come with it; its embedded picture does not — the workflow's
// first source is WAV, which has none, so no container gets special handling.
// AAC uses bitrate-controlled CBR (-b:a), never quality/VBR (-q:a).
// The destination must be a new absolute staging path. Partial output on failure
// belongs to the caller; this adapter never commits, removes or replaces media.
func (f FFmpeg) Encode(ctx context.Context, src, dst string, spec reconcile.AudioOutputSpec) error {
	if !filepath.IsAbs(src) || !filepath.IsAbs(dst) || src == dst {
		return fmt.Errorf("distinct absolute source and destination paths required")
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("destination already exists: %s", dst)
	} else if !os.IsNotExist(err) {
		return err
	}
	input, err := f.probe(ctx, src)
	if err != nil {
		return err
	}
	codecArgs, err := audioEncodingArgs(input, spec)
	if err != nil {
		return err
	}
	args := []string{
		"-nostdin",
		"-hide_banner",
		"-loglevel",
		"error",
		"-xerror",
		"-n",
		"-i",
		src,
		"-map",
		"0:a:0",
		"-map_metadata",
		"0",
		"-vn",
	}
	args = append(args, codecArgs.Args...)
	encoder, _ := f.paths()
	if output, runErr := exec.CommandContext(ctx, encoder, append(args, dst)...).CombinedOutput(); runErr != nil {
		return fmt.Errorf("encode: %w: %s", runErr, output)
	}
	output, err := f.probe(ctx, dst)
	if err != nil {
		return err
	}
	if output.Codec != codecArgs.Codec {
		return fmt.Errorf("unexpected output codec: %s", output.Codec)
	}
	inputDuration, inputErr := strconv.ParseFloat(input.Duration, 64)
	outputDuration, outputErr := strconv.ParseFloat(output.Duration, 64)
	if inputErr != nil || outputErr != nil || inputDuration <= 0 || outputDuration <= 0 ||
		math.Abs(inputDuration-outputDuration) > 0.1 {
		return fmt.Errorf("output duration does not match source")
	}
	if output.Channels != input.Channels {
		return fmt.Errorf("output changed channel count")
	}
	// Opus is a 48 kHz format: a 44.1 kHz source is resampled by the codec
	// itself, which is the one sample-rate change that is the format's, not a
	// defect of the encode.
	if output.SampleRate != input.SampleRate && spec.Codec != reconcile.CodecOpus {
		return fmt.Errorf("output changed sample rate")
	}
	if output, err := exec.CommandContext(ctx, encoder, "-nostdin", "-v", "error", "-xerror", "-i", dst, "-map", "0:a:0", "-f", "null", "-").
		CombinedOutput(); err != nil {
		return fmt.Errorf("validate output: %w: %s", err, output)
	}
	return nil
}
