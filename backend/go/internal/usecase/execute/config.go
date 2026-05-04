package execute

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	appconfig "github.com/onsei/organizer/backend/internal/config"
	exesvc "github.com/onsei/organizer/backend/internal/services/execute"
)

// getToolsConfig reads the tools config from config.json.
func getToolsConfig(configDir string) (exesvc.ToolsConfig, error) {
	cfgPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return exesvc.ToolsConfig{}, nil
		}
		return exesvc.ToolsConfig{}, fmt.Errorf("read config file: %w", err)
	}

	cfg := appconfig.DefaultAppConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return exesvc.ToolsConfig{}, fmt.Errorf("parse config JSON: %w", err)
	}

	return exesvc.ToolsConfig{
		Encoder:  cfg.Tools.Encoder,
		QAACPath: cfg.Tools.QAACPath,
		LAMEPath: cfg.Tools.LAMEPath,
	}, nil
}

// getExecuteConfig reads the execute config from config.json.
func getExecuteConfig(configDir string) (exesvc.ExecuteConfig, error) {
	defaults := appconfig.DefaultAppConfig()
	fallback := exesvc.ExecuteConfig{
		MaxIOWorkers:           defaults.Execute.MaxIOWorkers,
		PrecheckConcurrentStat: defaults.Execute.Precheck.ConcurrentStat,
	}

	cfgPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fallback, nil
		}
		return fallback, fmt.Errorf("read config file: %w", err)
	}

	cfg := appconfig.DefaultAppConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fallback, fmt.Errorf("parse config JSON: %w", err)
	}

	workers := cfg.Execute.MaxIOWorkers
	if workers < 1 {
		workers = 4
	}

	return exesvc.ExecuteConfig{
		MaxIOWorkers:           workers,
		PrecheckConcurrentStat: cfg.Execute.Precheck.ConcurrentStat,
	}, nil
}
