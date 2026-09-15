package execute

import (
	"fmt"
	"strconv"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// encodedAudio is one ffmpeg invocation: the arguments plus the codec ffprobe
// must report for the resulting file.
type encodedAudio struct {
	Codec string
	Args  []string
}

// audioEncodingArgs preserves PCM precision, sample rate and channel count.
// Unsupported lossless representations fail before ffmpeg creates output.
func audioEncodingArgs(input audioStream, spec reconcile.AudioOutputSpec) (encodedAudio, error) {
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
	case reconcile.CodecMp3, reconcile.CodecAac:
		if spec.Quality == nil || spec.Quality.Kind != reconcile.QualityBitrate || spec.Quality.Bitrate <= 0 {
			return encodedAudio{}, fmt.Errorf("positive bitrate target required")
		}
		bitrate := strconv.Itoa(spec.Quality.Bitrate) + "k"
		if spec.Codec == reconcile.CodecAac {
			return encodedAudio{Codec: "aac", Args: []string{"-c:a", "aac", "-b:a", bitrate, "-f", "ipod"}}, nil
		}
		return encodedAudio{Codec: "mp3", Args: []string{"-c:a", "libmp3lame", "-b:a", bitrate, "-f", "mp3"}}, nil
	default:
		return encodedAudio{}, fmt.Errorf("unsupported target codec %q", spec.Codec)
	}
}
