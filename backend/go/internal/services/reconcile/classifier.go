package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
)

// NewRegexClassifier builds an immutable regex classifier over the normalized
// root-relative path of each audio entry. The pattern and version are part of
// the definition hash: editing the pattern must produce a new version.
func NewRegexClassifier(name string, version int, pattern string) (Classifier, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Classifier{}, fmt.Errorf("compile classifier pattern: %w", err)
	}
	hash := sha256.Sum256([]byte(name + "\x00" + fmt.Sprint(version) + "\x00" + pattern))
	return Classifier{
		Name:    name,
		Version: version,
		Pattern: pattern,
		Hash:    hex.EncodeToString(hash[:]),
		Regex:   re,
	}, nil
}

// Classify returns the partition for a root-relative path. matched is the
// classifier match; unmatched is its complement.
func (c Classifier) Classify(relPath string) Partition {
	if c.Regex != nil && c.Regex.MatchString(relPath) {
		return PartitionMatched
	}
	return PartitionUnmatched
}

// ValidatePolicy checks the structural shape of a resolved policy. Errors here
// are request-validation errors (no Plan is created), not media conflicts.
func ValidatePolicy(p Policy) error {
	const schema = 1
	if p.SchemaVersion != schema {
		return fmt.Errorf("unsupported policy schema version %d", p.SchemaVersion)
	}
	if p.Classifier.Name == "" || p.Classifier.Version <= 0 {
		return fmt.Errorf("policy requires a named, versioned classifier")
	}
	for name, profile := range map[string]DesiredProfile{
		"matched":   p.Matched,
		"unmatched": p.Unmatched,
	} {
		if profile.Lossless == nil && profile.Encoded == nil {
			return fmt.Errorf("policy %s profile must declare at least one output", name)
		}
		if err := validateProfileOutput(profile.Lossless); err != nil {
			return fmt.Errorf("policy %s lossless output: %w", name, err)
		}
		if err := validateProfileOutput(profile.Encoded); err != nil {
			return fmt.Errorf("policy %s encoded output: %w", name, err)
		}
	}
	return nil
}

func validateProfileOutput(spec *AudioOutputSpec) error {
	if spec == nil {
		return nil
	}
	if spec.Codec == CodecWav || spec.Codec == CodecFlac {
		if spec.Quality != nil {
			return fmt.Errorf("lossless output %s must not declare bitrate quality", spec.Codec)
		}
		return nil
	}
	if spec.Codec == CodecMp3 || spec.Codec == CodecAac {
		if spec.Quality == nil || spec.Quality.Kind != QualityBitrate || spec.Quality.Bitrate <= 0 {
			return fmt.Errorf("encoded output %s requires a positive bitrate quality", spec.Codec)
		}
		return nil
	}
	return fmt.Errorf("unsupported output codec %q", spec.Codec)
}
