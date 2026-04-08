package analyze

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	OpTypeConvert OpType = "convert"
	OpTypeDelete  OpType = "delete"
)

type OpType string

type Plan struct {
	PlanID        string
	SnapshotToken string
	Operations    []Operation
	Errors        []PruneError
}

type Operation struct {
	Type       OpType
	SourcePath string
	TargetPath string
	Reason     string
}

type Entry struct {
	PathPosix string
	FileSize  int64
	Bitrate   int64
	Format    string
}

type FileEntry struct {
	Entry
	ParentPath string
	Stem       string
	Ext        string
	IsLossless bool
	IsLossy    bool
}

type Component struct {
	Stem        string
	FileEntries []FileEntry
}

type BranchType string

const (
	BranchConvert        BranchType = "convert"
	BranchDeleteLossless BranchType = "delete_lossless"
	BranchUnknown        BranchType = "unknown"
	BranchSkip           BranchType = "skip"

	BitrateThreshold = 319000
)

type BranchResult struct {
	BranchType BranchType
	SourcePath string
	Reason     string
	HasUnknown bool
}

func toFileEntry(e Entry) FileEntry {
	ext := strings.ToLower(path.Ext(e.PathPosix))
	base := path.Base(e.PathPosix)
	stem := strings.TrimSuffix(base, ext)
	return FileEntry{
		Entry:      e,
		ParentPath: path.Dir(e.PathPosix),
		Stem:       stem,
		Ext:        ext,
		IsLossless: ext == ".wav" || ext == ".flac",
		IsLossy:    ext == ".mp3" || ext == ".aac" || ext == ".m4a",
	}
}

func isAudioCandidate(fe FileEntry) bool {
	// Slim grouping should ignore sidecars.
	if fe.Ext == ".vtt" || fe.Ext == ".lrc" {
		return false
	}
	return fe.IsLossless || fe.IsLossy
}

func BuildComponents(entries []Entry) []Component {
	files := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		fe := toFileEntry(e)
		if !isAudioCandidate(fe) {
			continue
		}
		files = append(files, fe)
	}
	if len(files) == 0 {
		return nil
	}

	byParent := map[string][]int{}
	byStem := map[string][]int{}
	for i, f := range files {
		byParent[f.ParentPath] = append(byParent[f.ParentPath], i)
		byStem[f.Stem] = append(byStem[f.Stem], i)
	}

	visited := make([]bool, len(files))
	components := make([]Component, 0)

	for i := range files {
		if visited[i] {
			continue
		}
		queue := []int{i}
		visited[i] = true
		comp := Component{}

		for len(queue) > 0 {
			idx := queue[0]
			queue = queue[1:]
			f := files[idx]
			comp.FileEntries = append(comp.FileEntries, f)

			for _, n := range byParent[f.ParentPath] {
				if !visited[n] {
					visited[n] = true
					queue = append(queue, n)
				}
			}
			for _, n := range byStem[f.Stem] {
				if !visited[n] {
					visited[n] = true
					queue = append(queue, n)
				}
			}
		}

		sort.Slice(comp.FileEntries, func(a, b int) bool {
			return comp.FileEntries[a].PathPosix < comp.FileEntries[b].PathPosix
		})
		components = append(components, comp)
	}
	return components
}

func preferredLossless(files []FileEntry) string {
	var wav, flac string
	for _, f := range files {
		if !f.IsLossless {
			continue
		}
		if f.Ext == ".wav" && wav == "" {
			wav = f.PathPosix
		}
		if f.Ext == ".flac" && flac == "" {
			flac = f.PathPosix
		}
	}
	if wav != "" {
		return wav
	}
	return flac
}

func DetermineBranch(comp Component) BranchResult {
	hasLossy := false
	hasLossless := false
	hasLowLossy := false

	for _, f := range comp.FileEntries {
		if f.IsLossless {
			hasLossless = true
		}
		if !f.IsLossy {
			continue
		}
		hasLossy = true
		// Unknown-bitrate skip is required for mp3 references in Mode II.
		if f.Ext == ".mp3" && f.Bitrate == 0 {
			return BranchResult{BranchType: BranchUnknown, Reason: "lossy bitrate unknown", HasUnknown: true}
		}
		if f.Ext == ".mp3" && f.Bitrate < BitrateThreshold {
			hasLowLossy = true
		}
	}

	source := preferredLossless(comp.FileEntries)
	if hasLossy && hasLowLossy {
		if !hasLossless {
			return BranchResult{BranchType: BranchSkip, Reason: "lossy below threshold but no lossless source"}
		}
		return BranchResult{BranchType: BranchConvert, SourcePath: source, Reason: "lossy bitrate below threshold"}
	}
	if hasLossy {
		if !hasLossless {
			return BranchResult{BranchType: BranchSkip, Reason: "no lossless source"}
		}
		return BranchResult{BranchType: BranchDeleteLossless, SourcePath: source, Reason: "all lossy bitrate >= threshold"}
	}
	if hasLossless {
		return BranchResult{BranchType: BranchConvert, SourcePath: source, Reason: "no lossy files, convert to lossy"}
	}

	return BranchResult{BranchType: BranchSkip, Reason: "no actionable files"}
}

func convertPath(pathPosix string) string {
	ext := path.Ext(pathPosix)
	return strings.TrimSuffix(pathPosix, ext) + ".m4a"
}

func GenerateOperations(comp Component, branch BranchResult) []Operation {
	var ops []Operation

	switch branch.BranchType {
	case BranchConvert:
		byStem := map[string][]FileEntry{}
		sourceByStem := map[string]string{}
		for _, f := range comp.FileEntries {
			byStem[f.Stem] = append(byStem[f.Stem], f)
		}
		for stem, files := range byStem {
			source := preferredLossless(files)
			if source == "" {
				continue
			}
			sourceByStem[stem] = source
			ops = append(ops, Operation{
				Type:       OpTypeConvert,
				SourcePath: source,
				TargetPath: convertPath(source),
				Reason:     branch.Reason,
			})
		}
		for _, f := range comp.FileEntries {
			if !f.IsLossy {
				continue
			}
			if sourceByStem[f.Stem] == "" {
				continue
			}
			ops = append(ops, Operation{Type: OpTypeDelete, SourcePath: f.PathPosix, Reason: branch.Reason})
		}
	case BranchDeleteLossless:
		byStem := map[string][]FileEntry{}
		for _, f := range comp.FileEntries {
			byStem[f.Stem] = append(byStem[f.Stem], f)
		}

		for _, files := range byStem {
			hasLossy := false
			for _, f := range files {
				if f.IsLossy {
					hasLossy = true
					break
				}
			}

			if hasLossy {
				for _, f := range files {
					if !f.IsLossless {
						continue
					}
					ops = append(ops, Operation{Type: OpTypeDelete, SourcePath: f.PathPosix, Reason: branch.Reason})
				}
				continue
			}

			source := preferredLossless(files)
			if source == "" {
				continue
			}

			ops = append(ops, Operation{
				Type:       OpTypeConvert,
				SourcePath: source,
				TargetPath: convertPath(source),
				Reason:     branch.Reason,
			})

			for _, f := range files {
				if !f.IsLossless || f.PathPosix == source {
					continue
				}
				ops = append(ops, Operation{Type: OpTypeDelete, SourcePath: f.PathPosix, Reason: branch.Reason})
			}
		}
	}

	return ops
}

func filterEntriesByPurePattern(entries []Entry, purePattern *regexp.Regexp) []Entry {
	if purePattern == nil {
		return entries
	}

	matched := MatchByPattern(entries, purePattern, "")
	if len(matched) == 0 {
		return entries
	}

	matchedPaths := make(map[string]struct{}, len(matched))
	for _, e := range matched {
		matchedPaths[e.PathPosix] = struct{}{}
	}

	filtered := make([]Entry, 0, len(entries)-len(matched))
	for _, e := range entries {
		if _, ok := matchedPaths[e.PathPosix]; ok {
			continue
		}
		filtered = append(filtered, e)
	}

	return filtered
}

func AnalyzeSlimMode2(entries []Entry, purePattern *regexp.Regexp) Plan {
	entries = filterEntriesByPurePattern(entries, purePattern)

	plan := Plan{Operations: make([]Operation, 0)}
	components := BuildComponents(entries)
	for _, c := range components {
		branch := DetermineBranch(c)
		if branch.BranchType == BranchUnknown {
			pathHint := ""
			if len(c.FileEntries) > 0 {
				pathHint = c.FileEntries[0].ParentPath
			}
			plan.Errors = append(plan.Errors, PruneError{
				Path:    pathHint,
				Code:    "SLIM_GROUP_SKIPPED_BITRATE_UNKNOWN",
				Message: "component skipped because required mp3 bitrate is unknown",
			})
		}

		// Check for SLIM_STEM_MATCH_GT2: more than 2 files with same stem
		stemCounts := make(map[string]int)
		for _, f := range c.FileEntries {
			stemCounts[f.Stem]++
		}
		for stem, count := range stemCounts {
			if count > 2 {
				pathHint := ""
				if len(c.FileEntries) > 0 {
					pathHint = c.FileEntries[0].ParentPath
				}
				plan.Errors = append(plan.Errors, PruneError{
					Path:    pathHint,
					Code:    "SLIM_STEM_MATCH_GT2",
					Message: fmt.Sprintf("found %d files with same stem: %s", count, stem),
				})
			}
		}

		plan.Operations = append(plan.Operations, GenerateOperations(c, branch)...)
	}
	return plan
}

func AnalyzeSlimMode1(entries []Entry, purePattern *regexp.Regexp) Plan {
	entries = filterEntriesByPurePattern(entries, purePattern)
	plan := Plan{Operations: make([]Operation, 0)}
	components := BuildComponents(entries)

	for _, c := range components {
		stemCounts := make(map[string]int)
		hasLossy := false
		hasLossless := false

		for _, f := range c.FileEntries {
			stemCounts[f.Stem]++
			if f.IsLossy {
				hasLossy = true
			}
			if f.IsLossless {
				hasLossless = true
			}
		}

		for stem, count := range stemCounts {
			if count > 2 {
				pathHint := ""
				if len(c.FileEntries) > 0 {
					pathHint = c.FileEntries[0].ParentPath
				}
				plan.Errors = append(plan.Errors, PruneError{
					Path:    pathHint,
					Code:    "SLIM_STEM_MATCH_GT2",
					Message: fmt.Sprintf("found %d files with same stem: %s", count, stem),
				})
				return plan
			}
		}

		if hasLossless && !hasLossy {
			pathHint := ""
			if len(c.FileEntries) > 0 {
				pathHint = c.FileEntries[0].ParentPath
			}
			plan.Errors = append(plan.Errors, PruneError{
				Path:    pathHint,
				Code:    "SLIM_MODE1_LOSSLESS_ONLY",
				Message: "component has only lossless files",
			})
			return plan
		}

		if hasLossy && !hasLossless {
			continue
		}

		plan.Operations = append(plan.Operations, GenerateOperations(c, BranchResult{BranchType: BranchDeleteLossless, Reason: "mode1 keep lossy"})...)
	}

	return plan
}
