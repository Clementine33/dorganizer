package reconcile

import "fmt"

// Presets are compiled-in immutable policy sources. Preset content never
// changes in place; edits produce a new version. Plans persist the resolved
// policy snapshot, so preset evolution never rewrites a created Plan.
type Preset struct {
	Name    string
	Version int
	Policy  Policy
}

func mp3Quality() *Quality {
	return &Quality{Kind: QualityBitrate, Bitrate: 320}
}

// Presets registry (v1).
var Presets = []Preset{
	{
		Name:    "balanced",
		Version: 1,
		Policy: Policy{
			SchemaVersion: 1,
			Classifier:    ClassifierRef{Name: "effect-direction", Version: 1},
			Matched: DesiredProfile{
				Lossless: &AudioOutputSpec{Codec: CodecWav},
				Encoded:  &AudioOutputSpec{Codec: CodecMp3, Quality: mp3Quality()},
			},
			Unmatched: DesiredProfile{
				Lossless: &AudioOutputSpec{Codec: CodecWav},
				Encoded:  &AudioOutputSpec{Codec: CodecMp3, Quality: mp3Quality()},
			},
		},
	},
	{
		Name:    "compact",
		Version: 1,
		Policy: Policy{
			SchemaVersion: 1,
			Classifier:    ClassifierRef{Name: "effect-direction", Version: 1},
			Matched: DesiredProfile{
				Encoded: &AudioOutputSpec{Codec: CodecMp3, Quality: mp3Quality()},
			},
			Unmatched: DesiredProfile{
				Encoded: &AudioOutputSpec{Codec: CodecMp3, Quality: mp3Quality()},
			},
		},
	},
	{
		Name:    "archive",
		Version: 1,
		Policy: Policy{
			SchemaVersion: 1,
			Classifier:    ClassifierRef{Name: "effect-direction", Version: 1},
			Matched: DesiredProfile{
				Lossless: &AudioOutputSpec{Codec: CodecWav},
			},
			Unmatched: DesiredProfile{
				Lossless: &AudioOutputSpec{Codec: CodecWav},
			},
		},
	},
}

// ResolvePreset returns a copy of a compiled-in preset policy.
func ResolvePreset(name string, version int) (Policy, bool) {
	for _, p := range Presets {
		if p.Name == name && p.Version == version {
			return p.Policy, true
		}
	}
	return Policy{}, false
}

func init() {
	// Guard against malformed registry edits at startup.
	for _, p := range Presets {
		if err := ValidatePolicy(p.Policy); err != nil {
			panic(fmt.Sprintf("preset %s@%d invalid: %v", p.Name, p.Version, err))
		}
	}
}
