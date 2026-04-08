package analyze

import (
	"path"
	"regexp"
	"strings"
	"time"
)

// PruneTarget specifies what to delete
type PruneTarget string

const (
	PruneTargetLossless PruneTarget = "lossless" // wav, flac
	PruneTargetLossy    PruneTarget = "lossy"    // mp3, aac, m4a
	PruneTargetBoth     PruneTarget = "both"     // all audio
)

// PruneError represents an error during prune analysis
type PruneError struct {
	Path    string
	Code    string
	Message string
}

// AnalyzePrune analyzes files for prune operations.
// Returns operations and any errors (e.g., invalid pattern).
// Filters entries by regex pattern and target type (lossless/lossy/both).
func AnalyzePrune(entries []Entry, pattern string, target PruneTarget) ([]Operation, []PruneError) {
	// Compile regex once
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, []PruneError{{Path: "", Code: "INVALID_PATTERN", Message: err.Error()}}
	}

	// Pre-allocate with reasonable capacity
	ops := make([]Operation, 0, len(entries))

	// Single pass: evaluate each entry for pruning
	for _, e := range entries {
		// Check pattern match
		if !re.MatchString(e.PathPosix) {
			continue
		}

		// Parse file info
		ext := strings.ToLower(path.Ext(e.PathPosix))
		isLossless := ext == ".wav" || ext == ".flac"
		isLossy := ext == ".mp3" || ext == ".aac" || ext == ".m4a"

		// Check target match
		var matchesTarget bool
		switch target {
		case PruneTargetLossless:
			matchesTarget = isLossless
		case PruneTargetLossy:
			matchesTarget = isLossy
		case PruneTargetBoth:
			matchesTarget = isLossless || isLossy
		}

		if !matchesTarget {
			continue
		}

		// Add delete operation
		op := Operation{
			Type:       OpTypeDelete,
			SourcePath: e.PathPosix,
			Reason:     "PRUNE_MATCH",
		}
		ops = append(ops, op)
	}

	return ops, nil
}

// MatchByPattern filters entries matching a regex pattern
func MatchByPattern(entries []Entry, pattern *regexp.Regexp, _ string) []Entry {
	var matches []Entry
	for _, e := range entries {
		if pattern.MatchString(e.PathPosix) {
			matches = append(matches, e)
		}
	}
	return matches
}

// GeneratePrunePlan generates a prune plan for matched entries
func GeneratePrunePlan(matches []Entry, reason string) Plan {
	plan := Plan{
		PlanID:        generatePlanID(),
		SnapshotToken: generateSnapshotToken(),
		Operations:    make([]Operation, 0, len(matches)),
	}

	for _, e := range matches {
		plan.Operations = append(plan.Operations, Operation{
			Type:       OpTypeDelete,
			SourcePath: e.PathPosix,
			Reason:     reason,
		})
	}

	return plan
}

func generatePlanID() string {
	return "plan-" + time.Now().Format("20060102150405")
}

func generateSnapshotToken() string {
	return "snapshot-" + time.Now().Format("20060102150405.000000")
}
