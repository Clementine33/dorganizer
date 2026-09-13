package reconcile

import (
	"path"
	"sort"
	"strings"
)

// codecByExt maps recognized audio extensions to codec families.
var codecByExt = map[string]Codec{
	".wav":  CodecWav,
	".flac": CodecFlac,
	".mp3":  CodecMp3,
	".aac":  CodecAac,
	".m4a":  CodecAac,
}

// parseFile derives grouping facts from a stored entry. The second return
// value reports whether the entry is a recognized audio file (sidecars and
// unknown extensions are not audio candidates).
func parseFile(e AudioEntry) (GroupedFile, bool) {
	ext := strings.ToLower(path.Ext(e.PathPosix))
	codec, ok := codecByExt[ext]
	if !ok {
		return GroupedFile{}, false
	}
	base := path.Base(e.PathPosix)
	stem := base
	if len(base) > len(ext) && strings.EqualFold(base[len(base)-len(ext):], ext) {
		stem = base[:len(base)-len(ext)]
	}
	// Safe normalization only: Unicode case folding for the identity stem.
	// No guessed suffix stripping (e.g. [320], (Instrumental)).
	return GroupedFile{
		AudioEntry: e,
		ParentPath: path.Dir(e.PathPosix),
		Stem:       strings.ToLower(stem),
		Ext:        ext,
		Codec:      codec,
		Lossless:   IsLosslessCodec(codec),
	}, true
}

// BuildComponents discovers connected audio sets using the intentional
// same-parent OR same-stem transitive relation. Same stems across directories
// pair (mp3/00.mp3 <-> wav/00.wav); unrelated stems in one directory join for
// structure discovery. Non-audio candidates never enter components.
func BuildComponents(entries []AudioEntry) []Component {
	files := make([]GroupedFile, 0, len(entries))
	for _, e := range entries {
		fe, ok := parseFile(e)
		if !ok {
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
			comp.Files = append(comp.Files, f)

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

		sort.Slice(comp.Files, func(a, b int) bool {
			return comp.Files[a].PathPosix < comp.Files[b].PathPosix
		})
		components = append(components, comp)
	}
	return components
}

// StemGroups re-partitions one component by normalized stem, deterministically
// ordered.
func (c Component) StemGroups() []StemGroup {
	byStem := make(map[string][]GroupedFile)
	for _, f := range c.Files {
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

// ParentDirs returns the sorted unique parent directories of the component,
// used as the structural anchor for the component identity.
func (c Component) ParentDirs() []string {
	seen := map[string]struct{}{}
	for _, f := range c.Files {
		seen[f.ParentPath] = struct{}{}
	}
	dirs := make([]string, 0, len(seen))
	for d := range seen {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// AudioEntries filters and converts stored entries to grouped files.
func AudioEntries(entries []AudioEntry) []AudioEntry {
	out := make([]AudioEntry, 0, len(entries))
	for _, e := range entries {
		if _, ok := parseFile(e); ok {
			out = append(out, e)
		}
	}
	return out
}
