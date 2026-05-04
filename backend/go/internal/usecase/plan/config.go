package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	appconfig "github.com/onsei/organizer/backend/internal/config"
	"github.com/onsei/organizer/backend/internal/services/execute"
)

// planConfig mirrors grpc.planConfig for usecase-internal config loading.
type planConfig struct {
	Slim struct {
		RequireScope bool
	}
	RootResolve struct {
		Batch bool
	}
	Bitrate struct {
		BatchUpdate bool
	}
}

func defaultPlanConfig() planConfig {
	defaults := appconfig.DefaultAppConfig()
	out := planConfig{}
	out.Slim.RequireScope = defaults.Plan.Slim.RequireScope
	out.RootResolve.Batch = defaults.Plan.RootResolve.Batch
	out.Bitrate.BatchUpdate = defaults.Plan.Bitrate.BatchUpdate
	return out
}

func getPlanConfig(configDir string) (planConfig, error) {
	out := defaultPlanConfig()

	cfgPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, fmt.Errorf("read config file: %w", err)
	}

	cfg := appconfig.DefaultAppConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return out, fmt.Errorf("parse config JSON: %w", err)
	}

	out.Slim.RequireScope = cfg.Plan.Slim.RequireScope
	out.RootResolve.Batch = cfg.Plan.RootResolve.Batch
	out.Bitrate.BatchUpdate = cfg.Plan.Bitrate.BatchUpdate
	return out, nil
}

func getPruneRegexPattern(configDir string) (string, error) {
	cfgPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", fmt.Errorf("read config file: %w", err)
	}

	var cfg appconfig.AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse config JSON: %w", err)
	}

	if cfg.Prune.RegexPattern == "" {
		return "", fmt.Errorf("prune regex_pattern is empty in config")
	}

	return cfg.Prune.RegexPattern, nil
}

func getToolsConfig(configDir string) (execute.ToolsConfig, error) {
	cfgPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return execute.ToolsConfig{}, nil
		}
		return execute.ToolsConfig{}, fmt.Errorf("read config file: %w", err)
	}

	cfg := appconfig.DefaultAppConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return execute.ToolsConfig{}, fmt.Errorf("parse config JSON: %w", err)
	}

	return execute.ToolsConfig{
		Encoder:  cfg.Tools.Encoder,
		QAACPath: cfg.Tools.QAACPath,
		LAMEPath: cfg.Tools.LAMEPath,
	}, nil
}
