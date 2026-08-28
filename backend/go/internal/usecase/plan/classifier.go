package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	appconfig "github.com/onsei/organizer/backend/internal/config"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// EffectClassifierName is the canonical content-classifier for the first
// workflow release. The v1 definition freezes the installation's current
// prune regex; later edits produce new immutable versions.
const EffectClassifierName = "effect-direction"
const EffectClassifierVersion = 1

// DefaultEffectClassifierPattern is the bootstrap pattern used when no
// config.json exists yet (mirrors config.json.template). It is the seed for
// classifier v1 only; it is never mutated in place.
const DefaultEffectClassifierPattern = `(?:[Ss][Ee]|音)(?:[な無]し|[Cc][Uu][Tt])|(?:[Nn][Oo]|无|無)(?:[Ss][Ee]|音效|效果音)|(?:反転)`

// loadClassifierPattern reads the frozen classifier seed from config.json,
// falling back to the bootstrap default when no config file exists.
func loadClassifierPattern(configDir string) string {
	cfgPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return DefaultEffectClassifierPattern
	}
	var cfg appconfig.AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultEffectClassifierPattern
	}
	if cfg.Prune.RegexPattern == "" {
		return DefaultEffectClassifierPattern
	}
	return cfg.Prune.RegexPattern
}

// ensureClassifier persists the immutable classifier definition when the
// version does not exist yet.
func (s *serviceImpl) ensureClassifier(configDir string) error {
	pattern := loadClassifierPattern(configDir)
	classifier, err := reconcile.NewRegexClassifier(EffectClassifierName, EffectClassifierVersion, pattern)
	if err != nil {
		return NewError(ErrKindInvalidArgument, "INVALID_CLASSIFIER_PATTERN", fmt.Sprintf("invalid classifier pattern: %v", err), err)
	}
	err = s.repo.EnsureClassifier(sqlite.ClassifierRow{
		Name:      classifier.Name,
		Version:   classifier.Version,
		Kind:      "regex",
		Pattern:   classifier.Pattern,
		Hash:      classifier.Hash,
		CreatedAt: time.Now(),
	})
	if err != nil {
		return NewError(ErrKindInternal, "CLASSIFIER_PERSIST_FAILED", fmt.Sprintf("ensure classifier: %v", err), err)
	}
	return nil
}

// resolveClassifier loads an immutable classifier by ref, bootstrapping the
// v1 definition from config when the version is absent.
func (s *serviceImpl) resolveClassifier(configDir string, ref reconcile.ClassifierRef) (*reconcile.Classifier, error) {
	if ref.Name == EffectClassifierName && ref.Version == EffectClassifierVersion {
		if err := s.ensureClassifier(configDir); err != nil {
			return nil, err
		}
	}
	row, err := s.repo.LoadClassifier(ref.Name, ref.Version)
	if err != nil {
		return nil, NewError(ErrKindInternal, "CLASSIFIER_LOAD_FAILED", fmt.Sprintf("load classifier: %v", err), err)
	}
	if row == nil {
		return nil, NewError(ErrKindInvalidArgument, "UNKNOWN_CLASSIFIER", fmt.Sprintf("classifier %s@%d not found", ref.Name, ref.Version), nil)
	}
	classifier, err := reconcile.NewRegexClassifier(row.Name, row.Version, row.Pattern)
	if err != nil {
		return nil, NewError(ErrKindInternal, "CLASSIFIER_INVALID", fmt.Sprintf("classifier %s@%d invalid: %v", row.Name, row.Version, err), err)
	}
	return &classifier, nil
}

// resolvePolicy resolves a tagged policy source (preset or inline) into the
// fully-resolved policy plus its immutable classifier.
func (s *serviceImpl) resolvePolicy(configDir string, source PolicySource) (reconcile.Policy, *reconcile.Classifier, error) {
	var policy reconcile.Policy
	switch source.Kind {
	case "preset":
		resolved, ok := reconcile.ResolvePreset(source.PresetName, source.PresetVersion)
		if !ok {
			return policy, nil, NewError(ErrKindInvalidArgument, "UNKNOWN_PRESET", fmt.Sprintf("preset %s@%d not found", source.PresetName, source.PresetVersion), nil)
		}
		policy = resolved
	case "inline":
		if source.InlinePolicy == nil {
			return policy, nil, NewError(ErrKindInvalidArgument, "INVALID_POLICY", "inline policy is required", nil)
		}
		policy = *source.InlinePolicy
	default:
		return policy, nil, NewError(ErrKindInvalidArgument, "INVALID_POLICY_SOURCE", fmt.Sprintf("unsupported policy source kind %q", source.Kind), nil)
	}

	if err := reconcile.ValidatePolicy(policy); err != nil {
		return policy, nil, NewError(ErrKindInvalidArgument, "INVALID_POLICY", err.Error(), err)
	}
	classifier, err := s.resolveClassifier(configDir, policy.Classifier)
	if err != nil {
		return policy, nil, err
	}
	return policy, classifier, nil
}
