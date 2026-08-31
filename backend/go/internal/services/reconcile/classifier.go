package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// NormalizeTags canonicalizes a user-entered literal tag set: trim each tag,
// drop empties, de-duplicate case-insensitively, and sort for a stable hash.
// The first spelling encountered is kept; sorting makes the order irrelevant.
func NormalizeTags(raw []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// NewTagClassifier builds an immutable literal classifier from already
// normalized tags. Every tag is matched as a literal (non-regex) substring;
// QuoteMeta neutralizes regex metacharacters and the (?i) wrapper makes the
// whole alternation case-insensitive in one compiled pass.
func NewTagClassifier(tags []string) (Classifier, error) {
	if len(tags) == 0 {
		return Classifier{}, fmt.Errorf("classifier requires at least one tag")
	}
	quoted := make([]string, len(tags))
	for i, t := range tags {
		quoted[i] = regexp.QuoteMeta(t)
	}
	matcher, err := regexp.Compile(`(?i)(?:` + strings.Join(quoted, `|`) + `)`)
	if err != nil {
		return Classifier{}, fmt.Errorf("compile tag matcher: %w", err)
	}
	hash := sha256.Sum256([]byte(strings.Join(tags, "\x00")))
	return Classifier{
		Tags:    tags,
		Hash:    hex.EncodeToString(hash[:]),
		Matcher: matcher,
	}, nil
}

// Classify returns the partition for a root-relative path. matched is the
// classifier match (UI: 无音效); unmatched is its complement (UI: 有音效).
func (c Classifier) Classify(relPath string) Partition {
	if c.Matcher != nil && c.Matcher.MatchString(relPath) {
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
	if len(NormalizeTags(p.ClassifierTags)) == 0 {
		return fmt.Errorf("policy requires at least one non-empty classifier tag")
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

// ResolveClassifier normalizes a policy's raw tag input and compiles the
// resolved classifier. This is the single classifier authority for draft
// saves, slot updates, and planning runs.
func ResolveClassifier(rawTags []string) (Classifier, error) {
	tags := NormalizeTags(rawTags)
	if len(tags) == 0 {
		return Classifier{}, fmt.Errorf("policy requires at least one non-empty classifier tag")
	}
	return NewTagClassifier(tags)
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
