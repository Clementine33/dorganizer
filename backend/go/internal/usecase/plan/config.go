package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	appconfig "github.com/onsei/organizer/backend/internal/config"
)

// planConfig holds bitrate enrichment settings and the shared probe tool path.
type planConfig struct {
	FFprobePath string
	Bitrate     struct {
		BatchUpdate bool
	}
}

func defaultPlanConfig() planConfig {
	defaults := appconfig.DefaultAppConfig()
	out := planConfig{}
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

	out.Bitrate.BatchUpdate = cfg.Plan.Bitrate.BatchUpdate
	out.FFprobePath = cfg.Tools.FFprobePath
	return out, nil
}
