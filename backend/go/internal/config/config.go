package config

// PruneConfig defines prune-related settings in config.json.
type PruneConfig struct {
	RegexPattern string `json:"regex_pattern"`
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

// AppConfig represents the full application configuration from config.json.
type AppConfig struct {
	Prune   PruneConfig   `json:"prune"`
	Tools   ToolsConfig   `json:"tools"`
	Execute ExecuteConfig `json:"execute"`
	Plan    PlanConfig    `json:"plan"`
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
	}
}
