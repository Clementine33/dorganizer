package conversion

import "github.com/onsei/organizer/backend/internal/conversion/execute"

// PlanConfig is the planning side of the process configuration: the probe tool
// a pass measures with, and the write shape it persists the measurements in.
type PlanConfig struct {
	FFprobePath string
	// BatchUpdate selects the chunked, transactional write shape for probed
	// bitrates over the per-row one.
	BatchUpdate bool
}

// Settings is the process configuration the conversion task works from. Every
// read is a fresh look at the configuration rather than a value captured at
// startup: the tools an execution prepares with are the ones configured when it
// runs, and a configuration edited between two sessions is seen by the second.
//
// None of the reads fails. A configuration that is missing, unreadable or
// unparsable answers the defaults, because none of these entries can be failed
// by it: a planning pass runs on the default write shape rather than not
// running, a draft seeds an empty tag set rather than refusing to be created,
// and execution falls back to PATH rather than to no encoder. A caller that
// wants to tell the user the configuration is broken has to look at the file
// itself.
type Settings interface {
	// Plan reads the planning configuration (plan.bitrate.batch_update,
	// tools.ffprobe_path).
	Plan() PlanConfig
	// PruneLiteralTags reads the classifier tag literals a draft seeds from and
	// the tag library offers as its defaults (prune.literal_tags).
	PruneLiteralTags() []string
	// Tools reads the encoder tool paths (tools.ffmpeg_path, tools.ffprobe_path);
	// an empty path means PATH.
	Tools() execute.ToolsConfig
}
