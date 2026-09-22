package settings

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/onsei/organizer/backend/internal/conversion"
	"github.com/onsei/organizer/backend/internal/conversion/execute"
)

// Reader reads the configuration the conversion task works from: it implements
// conversion.Settings over the config.json in one config directory. Every call
// reads the file, so a configuration edited between two sessions is picked up
// by the second, and none of the defaults are captured at startup.
type Reader struct{ configDir string }

// NewReader roots a reader at one config directory.
func NewReader(configDir string) Reader { return Reader{configDir: configDir} }

// Plan reads the planning keys (plan.bitrate.batch_update, tools.ffprobe_path).
// A missing file is not a failure — the compiled-in defaults are the answer —
// and neither is one that cannot be read or parsed: a plan is never failed by
// its configuration, and it never runs on a half-read one either.
func (r Reader) Plan() conversion.PlanConfig {
	defaults := DefaultAppConfig()
	out := conversion.PlanConfig{BatchUpdate: defaults.Plan.Bitrate.BatchUpdate}

	var cfg AppConfig
	if !r.decode(&cfg) {
		return out
	}
	out.BatchUpdate = cfg.Plan.Bitrate.BatchUpdate
	out.FFprobePath = cfg.Tools.FFprobePath
	return out
}

// PruneLiteralTags reads the maintained initial literal tag list
// (prune.literal_tags): what a new draft seeds from, and what the tag library
// offers as its defaults. A missing/unreadable file yields an empty set — there
// is deliberately no compiled-in fallback.
func (r Reader) PruneLiteralTags() []string {
	var cfg AppConfig
	if !r.decode(&cfg) {
		return nil
	}
	return normalizeTags(cfg.Prune.LiteralTags)
}

// Tools reads the encoder tool paths (tools.ffmpeg_path, tools.ffprobe_path).
// An unreadable file yields the zero value, which means PATH.
func (r Reader) Tools() execute.ToolsConfig {
	var cfg AppConfig
	if !r.decode(&cfg) {
		return execute.ToolsConfig{}
	}
	return execute.ToolsConfig{
		FFmpegPath:  cfg.Tools.FFmpegPath,
		FFprobePath: cfg.Tools.FFprobePath,
	}
}

// decode loads config.json into cfg, reporting whether there was a readable one
// to load. "There is no configuration" and "there is a broken one" are the same
// answer here: every read above treats the file as optional.
func (r Reader) decode(cfg *AppConfig) bool {
	if r.configDir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(r.configDir, "config.json"))
	if err != nil {
		return false
	}
	return json.Unmarshal(data, cfg) == nil
}
