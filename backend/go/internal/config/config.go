package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PruneConfig defines prune-related settings in config.json. LiteralTags is
// the maintained initial classifier tag list (matched as case-insensitive
// literal substrings); it seeds new workset drafts only.
type PruneConfig struct {
	LiteralTags []string `json:"literal_tags"`
}

// LoadPruneLiteralTags reads the maintained initial literal tag list from
// config.json (prune.literal_tags). A missing/unreadable file yields an empty
// set — there is deliberately no compiled-in fallback.
func LoadPruneLiteralTags(configDir string) []string {
	if configDir == "" {
		return nil
	}
	cfgPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil
	}
	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return normalizeTags(cfg.Prune.LiteralTags)
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]string, len(tags))
	for _, raw := range tags {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; !ok {
			seen[key] = trimmed
		}
	}
	out := make([]string, 0, len(seen))
	for _, t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// ToolsConfig defines encoder tool settings in config.json.
type ToolsConfig struct {
	Encoder  string `json:"encoder"`
	QAACPath string `json:"qaac_path"`
	LAMEPath string `json:"lame_path"`
}

// ExecuteConfig defines execution-related settings in config.json.
type ExecuteConfig struct {
	MaxIOWorkers int                   `json:"max_io_workers"`
	Precheck     ExecutePrecheckConfig `json:"precheck"`
}

type ExecutePrecheckConfig struct {
	ConcurrentStat bool `json:"concurrent_stat"`
}

type PlanConfig struct {
	Slim        PlanSlimConfig        `json:"slim"`
	RootResolve PlanRootResolveConfig `json:"root_resolve"`
	Bitrate     PlanBitrateConfig     `json:"bitrate"`
}

type PlanSlimConfig struct {
	RequireScope bool `json:"require_scope"`
}

type PlanRootResolveConfig struct {
	Batch bool `json:"batch"`
}

type PlanBitrateConfig struct {
	BatchUpdate bool `json:"batch_update"`
}

// WorksetConfig defines workset-generation settings in config.json.
type WorksetConfig struct {
	GenerationConcurrency int `json:"generation_concurrency"`
}

// AppConfig represents the full application configuration from config.json.
type AppConfig struct {
	Prune   PruneConfig   `json:"prune"`
	Tools   ToolsConfig   `json:"tools"`
	Execute ExecuteConfig `json:"execute"`
	Plan    PlanConfig    `json:"plan"`
	Workset WorksetConfig `json:"workset"`
}

func DefaultAppConfig() AppConfig {
	return AppConfig{
		Execute: ExecuteConfig{
			MaxIOWorkers: 4,
			Precheck: ExecutePrecheckConfig{
				ConcurrentStat: false,
			},
		},
		Plan: PlanConfig{
			Slim: PlanSlimConfig{
				RequireScope: true,
			},
			RootResolve: PlanRootResolveConfig{
				Batch: true,
			},
			Bitrate: PlanBitrateConfig{
				BatchUpdate: true,
			},
		},
		Workset: WorksetConfig{
			GenerationConcurrency: 2,
		},
	}
}
