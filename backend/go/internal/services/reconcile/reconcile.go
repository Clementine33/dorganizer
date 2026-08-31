package reconcile

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Stable decision reason codes (machine identity, not prose).
const (
	ReasonKeepLosslessTarget   = "KEEP_LOSSLESS_TARGET"
	ReasonKeepEncodedSatisfied = "KEEP_ENCODED_SATISFIED"
	ReasonMaterializeLossless  = "MATERIALIZE_LOSSLESS"
	ReasonMaterializeEncoded   = "MATERIALIZE_ENCODED"
	ReasonObsoleteLossless     = "OBSOLETE_LOSSLESS"
	ReasonObsoleteEncoded      = "OBSOLETE_ENCODED"
	ReasonReplacedEncoded      = "REPLACED_ENCODED"
)

// mp3SatisfactionTolerance mirrors the historical threshold semantics: a 320
// kbps target accepts an observed bitrate >= 319 kbps (319000 bps), because
// header-level probes can under-report VBR averages slightly.
const mp3SatisfactionTolerance = 1000

// satisfiedEncoded reports whether an observed encoded variant satisfies the
// target spec using observable facts only. MP3 quality is verifiable via the
// probed bitrate; AAC/M4A quality cannot be probed with the v1 tooling, so an
// AAC file is never assumed adequate — the encoded lane must rebuild from a
// qualified lossless source or block (never silently keep an unverifiable
// file as satisfying a quality target).
func satisfiedEncoded(f GroupedFile, spec *AudioOutputSpec) bool {
	if f.Codec != spec.Codec {
		return false
	}
	if spec.Codec == CodecAac {
		return false
	}
	// CodecMp3
	return f.Bitrate >= int64(spec.Quality.Bitrate)*1000-mp3SatisfactionTolerance
}

// sameStemPath derives a target path beside the given source path, preserving
// the source's display stem and replacing the extension.
func sameStemPath(sourcePosix, targetExt string) string {
	dir := path.Dir(sourcePosix)
	base := path.Base(sourcePosix)
	stem := strings.TrimSuffix(base, path.Ext(base))
	return path.Join(dir, stem+targetExt)
}

// Reconcile plans the audio step for one planning root: classify -> partition
// -> components -> variant groups -> desired-state reconciliation.
func Reconcile(in ReconcileInput) (ReconcileResult, error) {
	if err := ValidatePolicy(in.Policy); err != nil {
		return ReconcileResult{}, err
	}
	if in.Classifier.Matcher == nil {
		return ReconcileResult{}, fmt.Errorf("classifier is not resolved")
	}
	root := strings.TrimSuffix(in.RootPath, "/")
	if root == "" || root == "." {
		return ReconcileResult{}, fmt.Errorf("planning root is required")
	}

	audio := AudioEntries(in.Entries)
	digest, count := InventoryFingerprint(audio)
	res := ReconcileResult{Digest: digest, Count: count}

	// Every entry path (audio or not) may block a fallback target path.
	occupied := make(map[string]struct{}, len(in.Entries))
	for _, e := range in.Entries {
		occupied[e.PathPosix] = struct{}{}
	}

	partitioned := map[Partition][]AudioEntry{PartitionMatched: {}, PartitionUnmatched: {}}
	for _, e := range audio {
		if !strings.HasPrefix(e.PathPosix, root+"/") {
			continue // outside the planning root; callers must not pass these
		}
		rel := strings.TrimPrefix(e.PathPosix, root+"/")
		partition := in.Classifier.Classify(rel)
		partitioned[partition] = append(partitioned[partition], e)
	}

	for _, part := range []Partition{PartitionMatched, PartitionUnmatched} {
		profile := in.Policy.Matched
		if part == PartitionUnmatched {
			profile = in.Policy.Unmatched
		}
		for _, comp := range BuildComponents(partitioned[part]) {
			outcome := reconcileComponent(root, part, profile, comp, occupied)
			res.Components = append(res.Components, outcome)
		}
	}

	sort.Slice(res.Components, func(i, j int) bool {
		if res.Components[i].Partition != res.Components[j].Partition {
			return res.Components[i].Partition < res.Components[j].Partition
		}
		return res.Components[i].ComponentID < res.Components[j].ComponentID
	})

	summary := StepSummary{ComponentCount: len(res.Components)}
	for _, c := range res.Components {
		summary.OperationCount += len(c.Operations)
		if c.Status == StatusBlocked {
			summary.BlockedCount++
			summary.ErrorCount++
		}
	}
	switch {
	case summary.BlockedCount > 0 && summary.OperationCount > 0:
		summary.SummaryReason = ReasonPartial
	case summary.BlockedCount > 0:
		summary.SummaryReason = ReasonBlocked
	case summary.OperationCount > 0:
		summary.SummaryReason = ReasonActionable
	default:
		summary.SummaryReason = ReasonNoMatch
	}
	res.Summary = summary
	return res, nil
}

type groupPlan struct {
	stem  string
	files []GroupedFile

	source          string
	sourceAmbiguous bool

	losslessKeep      []GroupedFile
	losslessEncodeSrc *GroupedFile
	losslessObsolete  []GroupedFile
	losslessBlocked   string // reason code when the lane cannot be satisfied

	encodedTarget         []GroupedFile
	encodedKeep           []GroupedFile
	encodedObsolete       []GroupedFile
	encodedNeedsRebuild   bool
	encodedQualityUnknown bool
	encodedTargetPath     string
}

func reconcileComponent(root string, partition Partition, profile DesiredProfile, comp Component, occupied map[string]struct{}) ComponentOutcome {
	out := ComponentOutcome{
		ComponentID: ComponentID(root, partition, comp),
		Partition:   partition,
		Status:      StatusOK,
	}
	for _, f := range comp.Files {
		out.Files = append(out.Files, FileTuple{Path: f.PathPosix, Size: f.Size, Mtime: f.Mtime})
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })

	groups := comp.StemGroups()
	owner := make(map[string]string, len(comp.Files))
	for _, g := range groups {
		for _, f := range g.Files {
			owner[f.PathPosix] = g.Stem
		}
	}

	plans := make([]groupPlan, 0, len(groups))
	for _, g := range groups {
		p := groupPlan{stem: g.Stem, files: g.Files}
		var losslessFiles, encodedFiles []GroupedFile
		for _, f := range g.Files {
			if f.Lossless {
				losslessFiles = append(losslessFiles, f)
			} else {
				encodedFiles = append(encodedFiles, f)
			}
		}

		// Qualified source selection from the Observed Inventory only: the
		// desired lossless codec if present, else WAV, then FLAC. A same-step
		// projected output is never a source.
		var candidates []GroupedFile
		if profile.Lossless != nil {
			for _, f := range losslessFiles {
				if f.Codec == profile.Lossless.Codec {
					candidates = append(candidates, f)
				}
			}
		}
		if len(candidates) == 0 {
			for _, f := range losslessFiles {
				if f.Codec == CodecWav {
					candidates = append(candidates, f)
				}
			}
		}
		if len(candidates) == 0 {
			for _, f := range losslessFiles {
				if f.Codec == CodecFlac {
					candidates = append(candidates, f)
				}
			}
		}
		if len(candidates) > 1 {
			p.sourceAmbiguous = true
		} else if len(candidates) == 1 {
			p.source = candidates[0].PathPosix
		}

		// Lossless lane (per Variant Group; no batch re-encode of adequate
		// lossless).
		if profile.Lossless != nil {
			var targetCodec []GroupedFile
			for _, f := range losslessFiles {
				if f.Codec == profile.Lossless.Codec {
					targetCodec = append(targetCodec, f)
				}
			}
			if len(targetCodec) > 0 {
				p.losslessKeep = targetCodec
				for _, f := range losslessFiles {
					if f.Codec != profile.Lossless.Codec {
						p.losslessObsolete = append(p.losslessObsolete, f)
					}
				}
			} else if len(losslessFiles) > 0 {
				if len(candidates) == 0 {
					p.losslessBlocked = ReasonSourceMissing
				} else {
					src := candidates[0]
					p.losslessEncodeSrc = &src
					p.losslessObsolete = losslessFiles
				}
			} else {
				p.losslessBlocked = ReasonLosslessUnfulfillable
			}
		} else {
			p.losslessObsolete = losslessFiles
		}

		// Encoded lane: per-group satisfaction facts; the component decides
		// KEEP_ALL vs REBUILD_ALL afterwards.
		if profile.Encoded != nil {
			for _, f := range encodedFiles {
				if f.Codec == profile.Encoded.Codec {
					p.encodedTarget = append(p.encodedTarget, f)
				} else {
					p.encodedObsolete = append(p.encodedObsolete, f)
				}
			}
			var below []GroupedFile
			for _, f := range p.encodedTarget {
				if satisfiedEncoded(f, profile.Encoded) {
					p.encodedKeep = append(p.encodedKeep, f)
				} else {
					below = append(below, f)
					if f.Codec == CodecMp3 && f.Bitrate == 0 {
						p.encodedQualityUnknown = true
					}
				}
			}
			if len(p.encodedTarget) == 0 || len(below) > 0 {
				p.encodedNeedsRebuild = true
				p.encodedKeep = nil // satisfied files are replaced by the batch
				p.encodedObsolete = append(p.encodedObsolete, below...)
			}
		} else {
			p.encodedObsolete = encodedFiles
		}
		plans = append(plans, p)
	}

	// Component fail-closed. A blocked component emits zero executable
	// operations but retains review decisions with the reason.
	block := func(reason, format string, args ...any) {
		out.Status = StatusBlocked
		out.ReasonCode = reason
		out.Message = fmt.Sprintf(format, args...)
	}
	isBlocked := func() bool { return out.Status == StatusBlocked }

	// Lossless lane decision.
	if profile.Lossless != nil {
		if !isBlocked() {
			for _, p := range plans {
				if p.losslessBlocked != "" {
					block(p.losslessBlocked, "stem %s cannot satisfy lossless %s target", p.stem, profile.Lossless.Codec)
					break
				}
			}
		}
		lane := LaneDecision{Lane: LaneLossless}
		switch {
		case isBlocked():
			lane.Decision = LaneBlocked
			lane.ReasonCode = out.ReasonCode
		case losslessRebuildNeeded(plans):
			lane.Decision = LaneRebuild
		default:
			lane.Decision = LaneKeep
		}
		out.Lanes = append(out.Lanes, lane)
	}

	// Encoded lane: component-wide KEEP_ALL / REBUILD_ALL.
	if profile.Encoded != nil {
		rebuildNeeded := false
		for _, p := range plans {
			if p.encodedNeedsRebuild {
				rebuildNeeded = true
				break
			}
		}
		if rebuildNeeded && !isBlocked() {
			// Consistency rule: the whole encoded lane is rebuilt from each
			// group's observed lossless source; any group without a qualified
			// source blocks the component.
			for i := range plans {
				p := &plans[i]
				if p.sourceAmbiguous {
					block(ReasonSourceAmbiguous, "stem %s has multiple equivalent lossless sources", p.stem)
					break
				}
				if p.source == "" {
					reason := ReasonSourceMissing
					if p.encodedQualityUnknown && len(p.encodedTarget) > 0 {
						reason = ReasonQualityUnknown
					}
					block(reason, "stem %s cannot rebuild encoded target without a lossless source", p.stem)
					break
				}
				switch len(p.encodedTarget) {
				case 1:
					p.encodedTargetPath = p.encodedTarget[0].PathPosix
				case 0:
					p.encodedTargetPath = sameStemPath(p.source, ExtForCodec(profile.Encoded.Codec))
					if _, taken := occupied[p.encodedTargetPath]; taken {
						if owner[p.encodedTargetPath] != p.stem {
							block(ReasonTargetPathConflict, "stem %s target path %s is occupied by another entry", p.stem, p.encodedTargetPath)
							break
						}
					}
				default:
					block(ReasonTargetPathAmbiguous, "stem %s has multiple %s variants; cannot choose an encode target", p.stem, profile.Encoded.Codec)
					break
				}
				if isBlocked() {
					break
				}
			}
		}

		lane := LaneDecision{Lane: LaneEncoded}
		switch {
		case isBlocked():
			lane.Decision = LaneBlocked
			lane.ReasonCode = out.ReasonCode
		case rebuildNeeded:
			lane.Decision = LaneRebuildAll
		default:
			lane.Decision = LaneKeepAll
		}
		out.Lanes = append(out.Lanes, lane)
	}

	// Per-file decisions (kept even for blocked components, projected
	// inventory and operations only for actionable ones).
	rebuildAll := profile.Encoded != nil && encodedLaneDecision(out) == LaneRebuildAll
	for _, p := range plans {
		v := VariantDecision{Stem: p.stem}

		for _, f := range p.losslessKeep {
			v.Decisions = append(v.Decisions, FileDecision{Path: f.PathPosix, Resolution: ResolutionKeep, ReasonCode: ReasonKeepLosslessTarget})
		}
		if p.losslessEncodeSrc != nil {
			target := sameStemPath(p.losslessEncodeSrc.PathPosix, ExtForCodec(profile.Lossless.Codec))
			v.Decisions = append(v.Decisions, FileDecision{
				Path: p.losslessEncodeSrc.PathPosix, Resolution: ResolutionEncode,
				ReasonCode: ReasonMaterializeLossless, TargetPath: target,
			})
		}

		if rebuildAll {
			// The whole encoded lane is rebuilt; the selected target path is
			// a staged replacement, so kept-encoded decisions are dropped.
			for _, f := range p.encodedKeep {
				if f.PathPosix == p.encodedTargetPath {
					continue // represented by the encode decision
				}
			}
			if p.encodedTargetPath != "" {
				v.Decisions = append(v.Decisions, FileDecision{
					Path: p.encodedTargetPath, Resolution: ResolutionEncode,
					ReasonCode: ReasonMaterializeEncoded, TargetPath: p.encodedTargetPath,
				})
			}
		} else {
			for _, f := range p.encodedKeep {
				v.Decisions = append(v.Decisions, FileDecision{Path: f.PathPosix, Resolution: ResolutionKeep, ReasonCode: ReasonKeepEncodedSatisfied})
			}
		}
		for _, f := range p.encodedObsolete {
			if p.encodedTargetPath != "" && f.PathPosix == p.encodedTargetPath {
				continue // replaced in place
			}
			code := ReasonObsoleteEncoded
			if f.Lossless {
				code = ReasonObsoleteLossless
			}
			v.Decisions = append(v.Decisions, FileDecision{Path: f.PathPosix, Resolution: ResolutionDelete, ReasonCode: code})
		}
		for _, f := range p.losslessObsolete {
			v.Decisions = append(v.Decisions, FileDecision{Path: f.PathPosix, Resolution: ResolutionDelete, ReasonCode: ReasonObsoleteLossless})
		}
		v.Decisions = dedupeDecisions(v.Decisions)
		out.Variants = append(out.Variants, v)
	}

	if isBlocked() {
		return out
	}

	// Operations: materialization + removal. All removal operations depend on
	// the component's materialized targets committing first.
	var materializeTargets []string
	seenTargets := map[string]bool{}
	for _, p := range plans {
		if p.losslessEncodeSrc != nil {
			tp := sameStemPath(p.losslessEncodeSrc.PathPosix, ExtForCodec(profile.Lossless.Codec))
			materializeTargets = appendUnique(materializeTargets, seenTargets, tp)
			out.Operations = append(out.Operations, Operation{
				Kind: OpKindEncode, Phase: PhaseMaterializeOutputs,
				ComponentID: out.ComponentID, VariantStem: p.stem,
				SourcePath: p.losslessEncodeSrc.PathPosix, TargetPath: tp,
			})
		}
		if p.encodedTargetPath != "" {
			materializeTargets = appendUnique(materializeTargets, seenTargets, p.encodedTargetPath)
			out.Operations = append(out.Operations, Operation{
				Kind: OpKindEncode, Phase: PhaseMaterializeOutputs,
				ComponentID: out.ComponentID, VariantStem: p.stem,
				SourcePath: p.source, TargetPath: p.encodedTargetPath,
			})
		}
	}
	for _, p := range plans {
		for _, f := range append(append([]GroupedFile{}, p.losslessObsolete...), p.encodedObsolete...) {
			if p.encodedTargetPath != "" && f.PathPosix == p.encodedTargetPath {
				continue
			}
			out.Operations = append(out.Operations, Operation{
				Kind: OpKindRemoveObsolete, Phase: PhaseRemoveObsoleteAudio,
				ComponentID: out.ComponentID, VariantStem: p.stem,
				SourcePath: f.PathPosix, DependsOn: append([]string{}, materializeTargets...),
			})
		}
	}
	sortOperations(out.Operations)

	out.ProjectedInventory = projectedInventory(out)
	return out
}

func losslessRebuildNeeded(plans []groupPlan) bool {
	for _, p := range plans {
		if p.losslessEncodeSrc != nil {
			return true
		}
	}
	return false
}

func encodedLaneDecision(out ComponentOutcome) string {
	for _, lane := range out.Lanes {
		if lane.Lane == LaneEncoded {
			return lane.Decision
		}
	}
	return ""
}

func dedupeDecisions(in []FileDecision) []FileDecision {
	seen := map[string]bool{}
	out := make([]FileDecision, 0, len(in))
	for _, d := range in {
		if seen[d.Path] {
			continue
		}
		seen[d.Path] = true
		out = append(out, d)
	}
	return out
}

func appendUnique(list []string, seen map[string]bool, p string) []string {
	if seen[p] {
		return list
	}
	seen[p] = true
	return append(list, p)
}

func sortOperations(ops []Operation) {
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Phase != ops[j].Phase {
			return ops[i].Phase < ops[j].Phase
		}
		if ops[i].SourcePath != ops[j].SourcePath {
			return ops[i].SourcePath < ops[j].SourcePath
		}
		return ops[i].TargetPath < ops[j].TargetPath
	})
}

// projectedInventory is the exact final audio set: kept files plus
// materialized targets, deduplicated and sorted.
func projectedInventory(out ComponentOutcome) []string {
	seen := map[string]bool{}
	var paths []string
	for _, v := range out.Variants {
		for _, d := range v.Decisions {
			switch d.Resolution {
			case ResolutionKeep, ResolutionEncode:
				p := d.Path
				if d.Resolution == ResolutionEncode && d.TargetPath != "" {
					p = d.TargetPath
				}
				if !seen[p] {
					seen[p] = true
					paths = append(paths, p)
				}
			}
		}
	}
	sort.Strings(paths)
	return paths
}
