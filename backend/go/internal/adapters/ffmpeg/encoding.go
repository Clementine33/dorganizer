package ffmpeg

import (
	"fmt"
	"strconv"

	"github.com/onsei/organizer/backend/internal/conversion/execute"
	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
)

// encodedArgs maps one encoded codec onto its ffmpeg invocation. The codec set
// is enumerated rather than defaulted: a codec added to the profile vocabulary
// must be given its encoder here instead of silently becoming an MP3.
func encodedArgs(codec reconcile.Codec, bitrate string) (encodedAudio, error) {
	switch codec {
	case reconcile.CodecAac:
		// AAC stays bitrate-controlled CBR: never quality/VBR (-q:a).
		return encodedAudio{
			Codec:   "aac",
			Encoder: "aac",
			Mode:    reconcile.EncodingMode(codec),
			Args:    []string{"-c:a", "aac", "-b:a", bitrate, "-f", "ipod"},
		}, nil
	case reconcile.CodecMp3:
		return encodedAudio{
			Codec:   "mp3",
			Encoder: "libmp3lame",
			Mode:    reconcile.EncodingMode(codec),
			Args:    []string{"-c:a", "libmp3lame", "-b:a", bitrate, "-f", "mp3"},
		}, nil
	case reconcile.CodecOpus:
		// Opus is encoded as VBR (-b:a is its average target), which is what
		// keeps its files small on content that does not need the bitrate. Its
		// native 48 kHz also resamples a 44.1 kHz source; that is the format's
		// own behaviour, not a defect of the encode.
		return encodedAudio{
			Codec:   "opus",
			Encoder: "libopus",
			Mode:    reconcile.EncodingMode(codec),
			Args:    []string{"-c:a", "libopus", "-b:a", bitrate, "-f", "opus"},
		}, nil
	case reconcile.CodecWav, reconcile.CodecFlac:
		return encodedAudio{}, fmt.Errorf("lossless codec %s is not a bitrate target", codec)
	}
	return encodedAudio{}, fmt.Errorf("unsupported target codec %q", codec)
}

// encodedAudio is one ffmpeg invocation: the arguments, the codec ffprobe must
// report for the resulting file, and the encoder and rate-control mode a
// generation credential records beside it.
type encodedAudio struct {
	Codec   string
	Encoder string
	Mode    string
	Args    []string
}

// encodedTarget resolves the invocation of one encoded target without a probed
// source: the encoded arguments depend only on the codec and the bitrate, never
// on the media being read. It is the single place that invocation is derived,
// so a generation credential can never describe a different invocation than the
// one that ran.
func encodedTarget(codec reconcile.Codec, quality *reconcile.Quality) (encodedAudio, error) {
	if quality == nil || quality.Kind != reconcile.QualityBitrate || quality.Bitrate <= 0 {
		return encodedAudio{}, fmt.Errorf("positive bitrate target required")
	}
	return encodedArgs(codec, strconv.Itoa(quality.Bitrate)+"k")
}

// audioEncodingArgs preserves PCM precision, sample rate and channel count.
// Unsupported lossless representations fail before ffmpeg creates output.
func audioEncodingArgs(input execute.AudioStream, spec reconcile.AudioOutputSpec) (encodedAudio, error) {
	bits, _ := strconv.Atoi(input.RawBits)
	bits = max(bits, input.Bits)
	floating := false
	switch input.Codec {
	case "flac", "pcm_u8", "pcm_s16le", "pcm_s16be", "pcm_s24le", "pcm_s24be", "pcm_s32le", "pcm_s32be":
	case "pcm_f32le", "pcm_f32be", "pcm_f64le", "pcm_f64be":
		floating = true
	default:
		return encodedAudio{}, fmt.Errorf("unsupported lossless source codec: %s", input.Codec)
	}
	if bits <= 0 || bits > 64 {
		return encodedAudio{}, fmt.Errorf("unknown source precision")
	}
	switch spec.Codec {
	case reconcile.CodecWav:
		codec := "pcm_s16le"
		switch {
		case floating && bits > 32:
			codec = "pcm_f64le"
		case floating:
			codec = "pcm_f32le"
		case bits > 24:
			codec = "pcm_s32le"
		case bits > 16:
			codec = "pcm_s24le"
		}
		return encodedAudio{Codec: codec, Args: []string{"-c:a", codec, "-f", "wav"}}, nil
	case reconcile.CodecFlac:
		if floating || bits > 24 {
			return encodedAudio{}, fmt.Errorf("FLAC target cannot preserve source precision")
		}
		format := "s16"
		if bits > 16 {
			format = "s32"
		}
		return encodedAudio{Codec: "flac", Args: []string{"-c:a", "flac", "-sample_fmt", format, "-f", "flac"}}, nil
	case reconcile.CodecMp3, reconcile.CodecAac, reconcile.CodecOpus:
		return encodedTarget(spec.Codec, spec.Quality)
	default:
		return encodedAudio{}, fmt.Errorf("unsupported target codec %q", spec.Codec)
	}
}
