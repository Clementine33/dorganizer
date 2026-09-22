package execute

import (
	"context"

	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
)

// AudioStream is the probed shape of one audio stream: what the pipeline reads
// a staged output back as before it commits it.
type AudioStream struct {
	Codec      string `json:"codec_name"`
	SampleRate string `json:"sample_rate"`
	Channels   int    `json:"channels"`
	Bits       int    `json:"bits_per_sample"`
	RawBits    string `json:"bits_per_raw_sample"`
	Duration   string `json:"duration"`
}

// Encoder is the media tool the component pipeline is written against: it
// probes files and writes one frozen target specification. It is a port, not a
// command — everything ffmpeg is made of (tool paths, arguments, the checks a
// written file passes) belongs to the adapter that implements it.
type Encoder interface {
	// Encode writes one output at a new absolute staging path and leaves a
	// readable, spec-conforming file there; a failure leaves the partial output
	// to the caller.
	Encode(ctx context.Context, src, dst string, spec reconcile.AudioOutputSpec) error
	// Check reports both tools being available before any filesystem changes.
	Check() error
	// Probe reads the first audio stream of one file.
	Probe(ctx context.Context, path string) (AudioStream, error)
	// Version reports the encoder's own version line, the diagnostic a
	// generation credential records; an unavailable tool reports an empty
	// string rather than failing the generation it describes.
	Version(ctx context.Context) string
	// TargetFacts names the encoder binary and the rate-control mode one
	// encoded target is written with: what a generation credential records, and
	// what makes a record written under other settings no evidence for this one.
	TargetFacts(spec reconcile.AudioOutputSpec) (encoder, mode string, err error)
}

// EncoderFactory builds the encoder one tools configuration names. The pipeline
// asks for it when it prepares a unit, so the tool paths in force are the ones
// configured at that moment.
type EncoderFactory func(ToolsConfig) Encoder
