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

type StemGroup struct {
	Stem  string
	Files []FileEntry
}

func buildStemGroups(comp Component) []StemGroup {
	byStem := make(map[string][]FileEntry)
	for _, f := range comp.FileEntries {
		byStem[f.Stem] = append(byStem[f.Stem], f)
	}

	stems := make([]string, 0, len(byStem))
	for stem := range byStem {
		stems = append(stems, stem)
	}
	sort.Strings(stems)

	groups := make([]StemGroup, 0, len(stems))
	for _, stem := range stems {
		groups = append(groups, StemGroup{Stem: stem, Files: byStem[stem]})
	}
	return groups
}

func componentPathHint(comp Component) string {
	if len(comp.FileEntries) == 0 {
		return ""
	}
	return comp.FileEntries[0].ParentPath
}

func validateStemGroups(comp Component, groups []StemGroup) []PruneError {
	pathHint := componentPathHint(comp)
	var errs []PruneError

	for _, group := range groups {
		losslessCount := 0
		lossyCount := 0
		lossyExts := map[string]struct{}{}

		for _, f := range group.Files {
			if f.IsLossless {
				losslessCount++
			}
			if f.IsLossy {
				lossyCount++
				lossyExts[f.Ext] = struct{}{}
			}
		}

		switch {
		case losslessCount == 0 && lossyCount >= 1 && len(lossyExts) == 1:
			continue
		case losslessCount == 1 && lossyCount == 1 && len(lossyExts) == 1:
			continue
		case losslessCount > 1:
			errs = append(errs, PruneError{Path: pathHint, Code: "SLIM_STEM_MULTI_LOSSLESS", Message: fmt.Sprintf("found multiple lossless files for stem: %s", group.Stem)})
		case losslessCount == 1 && lossyCount > 1:
			errs = append(errs, PruneError{Path: pathHint, Code: "SLIM_STEM_LOSSLESS_WITH_MULTI_LOSSY", Message: fmt.Sprintf("found 1 lossless and multiple lossy files for stem: %s", group.Stem)})
		case lossyCount >= 2 && len(lossyExts) > 1:
			errs = append(errs, PruneError{Path: pathHint, Code: "SLIM_STEM_LOSSY_MIXED_FORMATS", Message: fmt.Sprintf("found mixed lossy formats for stem: %s", group.Stem)})
		}
	}

	return errs
}

func isLosslessOnlyComponent(groups []StemGroup) bool {
	hasLossless := false
	for _, group := range groups {
		for _, f := range group.Files {
			if f.IsLossy {
				return false
			}
			if f.IsLossless {
				hasLossless = true
			}
		}
	}
	return hasLossless
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

func DetermineBranch(groups []StemGroup, checkUnknownBitrate bool) BranchResult {
	hasLossy := false
	hasLossless := false
	hasLowLossy := false
	hasUnknownLossyBitrate := false
	var allFiles []FileEntry

	for _, group := range groups {
		for _, f := range group.Files {
			allFiles = append(allFiles, f)
			if f.IsLossless {
				hasLossless = true
			}
			if !f.IsLossy {
				continue
			}
			hasLossy = true
			if checkUnknownBitrate && f.Ext == ".mp3" && f.Bitrate == 0 {
				hasUnknownLossyBitrate = true
				continue
			}
			if f.Ext == ".mp3" && f.Bitrate < BitrateThreshold {
				hasLowLossy = true
			}
		}
	}

	source := preferredLossless(allFiles)
	if hasUnknownLossyBitrate && hasLossless {
		return BranchResult{BranchType: BranchUnknown, Reason: "lossy bitrate unknown", HasUnknown: true}
	}
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

func GenerateOperations(groups []StemGroup, branch BranchResult) []Operation {
	var ops []Operation

	switch branch.BranchType {
	case BranchConvert:
		sourceByStem := map[string]string{}
		for _, group := range groups {
			source := preferredLossless(group.Files)
			if source == "" {
				continue
			}
			sourceByStem[group.Stem] = source
			ops = append(ops, Operation{Type: OpTypeConvert, SourcePath: source, TargetPath: convertPath(source), Reason: branch.Reason})
		}
		for _, group := range groups {
			if sourceByStem[group.Stem] == "" {
				continue
			}
			for _, f := range group.Files {
				if f.IsLossy {
					ops = append(ops, Operation{Type: OpTypeDelete, SourcePath: f.PathPosix, Reason: branch.Reason})
				}
			}
		}
	case BranchDeleteLossless:
		for _, group := range groups {
			hasLossy := false
			for _, f := range group.Files {
				if f.IsLossy {
					hasLossy = true
					break
				}
			}

			if hasLossy {
				for _, f := range group.Files {
					if f.IsLossless {
						ops = append(ops, Operation{Type: OpTypeDelete, SourcePath: f.PathPosix, Reason: branch.Reason})
					}
				}
				continue
			}

			source := preferredLossless(group.Files)
			if source == "" {
				continue
			}
			ops = append(ops, Operation{Type: OpTypeConvert, SourcePath: source, TargetPath: convertPath(source), Reason: branch.Reason})
			for _, f := range group.Files {
				if f.IsLossless && f.PathPosix != source {
					ops = append(ops, Operation{Type: OpTypeDelete, SourcePath: f.PathPosix, Reason: branch.Reason})
				}
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
		groups := buildStemGroups(c)
		if errs := validateStemGroups(c, groups); len(errs) > 0 {
			plan.Errors = append(plan.Errors, errs...)
			continue
		}

		branch := DetermineBranch(groups, true)
		if branch.BranchType == BranchUnknown {
			plan.Errors = append(plan.Errors, PruneError{Path: componentPathHint(c), Code: "SLIM_GROUP_SKIPPED_BITRATE_UNKNOWN", Message: "component skipped because required mp3 bitrate is unknown"})
			continue
		}

		plan.Operations = append(plan.Operations, GenerateOperations(groups, branch)...)
	}
	return plan
}

func AnalyzeSlimMode1(entries []Entry, purePattern *regexp.Regexp) Plan {
	entries = filterEntriesByPurePattern(entries, purePattern)
	plan := Plan{Operations: make([]Operation, 0)}
	components := BuildComponents(entries)

	for _, c := range components {
		groups := buildStemGroups(c)
		if errs := validateStemGroups(c, groups); len(errs) > 0 {
			plan.Errors = append(plan.Errors, errs...)
			continue
		}
		if isLosslessOnlyComponent(groups) {
			plan.Errors = append(plan.Errors, PruneError{Path: componentPathHint(c), Code: "SLIM_MODE1_LOSSLESS_ONLY", Message: "component has only lossless files"})
			continue
		}

		branch := DetermineBranch(groups, false)
		if branch.BranchType == BranchSkip {
			continue
		}
		plan.Operations = append(plan.Operations, GenerateOperations(groups, BranchResult{BranchType: BranchDeleteLossless, Reason: "mode1 keep lossy"})...)
	}

	return plan
}
