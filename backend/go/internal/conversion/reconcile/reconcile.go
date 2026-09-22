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

// bitrateSatisfactionTolerance mirrors the historical threshold semantics: a
// 320 kbps target accepts an observed bitrate >= 319 kbps (319000 bps), because
// header-level probes can under-report a rate slightly. It applies to the codecs
// compared on a floor rather than a band (MP3, and Opus's measurement entry).
const bitrateSatisfactionTolerance = 1000

// The acceptance band of an AAC target, as a percentage of it. ffmpeg's native
// AAC encoder delivers its target exactly while the target is within reach
// (measured: 128k and 192k both land at 1.00x), and saturates near 222 kbps for
// stereo 44.1 kHz above that (asked for 256k it writes ~0.86x, for 320k ~0.65x).
// A tight band therefore only applies while the encoder can honour the target;
// above that the floor has to admit what it can actually produce, or every plan
// would rebuild its own output forever. The ceiling is the target's own meaning:
// a file well above the declared shape is replaced like a file below it.
const (
	aacTightFloor     = 95
	aacSaturatedFloor = 60
	aacCeiling        = 110
	// aacExactAbove is where "the encoder can honour the target" stops.
	aacExactAbove = 192000
)

// measuredBitrateSatisfies is the first entry of the encoded-lane gate: a file
// whose own measured rate already answers the target is not touched, whoever
// wrote it. The bands follow what this toolchain's encoders actually write. An
// unknown rate (0) never satisfies: it is not evidence of anything.
func measuredBitrateSatisfies(bitrate int64, spec *AudioOutputSpec) bool {
	if spec.Quality == nil || spec.Quality.Bitrate <= 0 {
		return false
	}
	target := int64(spec.Quality.Bitrate) * 1000
	switch spec.Codec {
	case CodecMp3:
		return bitrate >= target-bitrateSatisfactionTolerance
	case CodecAac:
		floor := int64(aacTightFloor)
		if target > aacExactAbove {
			floor = aacSaturatedFloor
		}
		return bitrate >= target*floor/100 && bitrate <= target*aacCeiling/100
	case CodecOpus:
		// Opus is written VBR, so its average is a fact about the content; a rate
		// at or above the target can only mean the content needed that much, and
		// one below it decides nothing (see satisfiedEncoded).
		return bitrate >= target-bitrateSatisfactionTolerance
	case CodecWav, CodecFlac:
		// Lossless targets carry no bitrate; the lane rules decide those.
		return false
	}
	return false
}

// satisfiedEncoded reports whether an observed encoded variant satisfies the
// target spec. Two entries accept a file, in this order:
//
//  1. Its own measured rate already answers the target — the first entry, and
//     all MP3 ever needs: MP3 is written CBR and lands exactly on its setting, so
//     the number leaves nothing to ask.
//  2. A generation credential proves this app wrote the file for exactly this
//     target and that the bytes are still the ones it wrote. This is what settles
//     the codecs a measurement cannot judge: Opus VBR (the same 160k setting
//     averages 0.01x on silence, 0.68x on dense noise, 1.13x on sparse material)
//     and AAC above the point where the encoder saturates.
//
// A file neither entry accepts is unconfirmed, never silently adequate. That is
// the whole claim: a measured rate is not evidence of quality either — a
// low-bitrate file re-encoded to a high target would not regain what it lost.
func satisfiedEncoded(f GroupedFile, spec *AudioOutputSpec) bool {
	if f.Codec != spec.Codec {
		return false
	}
	if measuredBitrateSatisfies(f.Bitrate, spec) {
		return true
	}
	if spec.Codec == CodecMp3 {
		return false
	}
	return f.Generated.Matches(*spec, f.Size, f.Mtime)
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
// -> components -> variant groups -> desired-state reconciliation. Both modes
// share this skeleton, its ordering and its summary; the policy mode selects
// only the per-component decision table: reconcileComponent for "" / strict
// (historical behavior), lenientComponent in mode.go for available_sources.
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

	planComponent := reconcileComponent
	if in.Policy.Mode == ModeAvailableSources {
		planComponent = lenientComponent
	}
	for _, part := range []Partition{PartitionMatched, PartitionUnmatched} {
		profile := ProfileFor(in.Policy, part)
		for _, comp := range BuildComponents(partitioned[part]) {
			res.Components = append(res.Components, planComponent(root, part, profile, comp, occupied))
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
		// UNMET_TARGET decisions are emitted by the available_sources table
		// only, so the strict path always adds zero here.
		summary.UnmetTargets += countUnmetTargets(c)
	}
	switch {
	case summary.BlockedCount > 0 && summary.OperationCount > 0:
		summary.SummaryReason = ReasonPartial
	case summary.BlockedCount > 0:
		summary.SummaryReason = ReasonBlocked
	case summary.UnmetTargets > 0:
		summary.SummaryReason = ReasonUnmetTargets
	case summary.OperationCount > 0:
		summary.SummaryReason = ReasonActionable
	default:
		summary.SummaryReason = ReasonNoMatch
	}
	res.Summary = summary
	return res, nil
}

// newComponentOutcome opens the shared per-component skeleton both decision
// tables build on: identity, partition, status, and the observed files as
// sorted tuples.
func newComponentOutcome(root string, partition Partition, comp Component) ComponentOutcome {
	out := ComponentOutcome{
		ComponentID: ComponentID(root, partition, comp),
		Partition:   partition,
		Status:      StatusOK,
	}
	for _, f := range comp.Files {
		out.Files = append(out.Files, FileTuple{Path: f.PathPosix, Size: f.Size, Mtime: f.Mtime})
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out
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

	encodedTarget       []GroupedFile
	encodedKeep         []GroupedFile
	encodedObsolete     []GroupedFile
	encodedNeedsRebuild bool
	encodedTargetPath   string
}

//nolint:gocognit,gocyclo,cyclop,funlen // per-lane decision table (lossless/encoded/ops); each case is a straight-line rule
func reconcileComponent(
	root string,
	partition Partition,
	profile DesiredProfile,
	comp Component,
	occupied map[string]struct{},
) ComponentOutcome {
	out := newComponentOutcome(root, partition, comp)

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
			switch {
			case len(targetCodec) > 0:
				p.losslessKeep = targetCodec
				for _, f := range losslessFiles {
					if f.Codec != profile.Lossless.Codec {
						p.losslessObsolete = append(p.losslessObsolete, f)
					}
				}
			case len(losslessFiles) > 0:
				if len(candidates) == 0 {
					p.losslessBlocked = ReasonSourceMissing
				} else {
					src := candidates[0]
					p.losslessEncodeSrc = &src
					p.losslessObsolete = losslessFiles
				}
			default:
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
					block(
						p.losslessBlocked,
						"stem %s cannot satisfy lossless %s target",
						p.stem,
						profile.Lossless.Codec,
					)
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
					// A candidate of the declared codec that no credential confirms
					// is a different fact from a stem holding nothing to build from,
					// and it is the one the review can act on: without a lossless
					// source there is nothing to regenerate it from, and re-encoding
					// a lossy file would not restore what it lost.
					if len(p.encodedTarget) > 0 {
						block(
							ReasonConformanceUnconfirmed,
							"stem %s holds an unconfirmed %s target and no lossless source to regenerate it from",
							p.stem,
							profile.Encoded.Codec,
						)
						break
					}
					block(
						ReasonSourceMissing,
						"stem %s cannot rebuild encoded target without a lossless source",
						p.stem,
					)
					break
				}
				switch len(p.encodedTarget) {
				case 1:
					p.encodedTargetPath = p.encodedTarget[0].PathPosix
				case 0:
					p.encodedTargetPath = sameStemPath(p.source, ExtForCodec(profile.Encoded.Codec))
					if _, taken := occupied[p.encodedTargetPath]; taken {
						if owner[p.encodedTargetPath] != p.stem {
							block(
								ReasonTargetPathConflict,
								"stem %s target path %s is occupied by another entry",
								p.stem,
								p.encodedTargetPath,
							)
							break
						}
					}
				default:
					block(
						ReasonTargetPathAmbiguous,
						"stem %s has multiple %s variants; cannot choose an encode target",
						p.stem,
						profile.Encoded.Codec,
					)
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
			v.Decisions = append(
				v.Decisions,
				FileDecision{Path: f.PathPosix, Resolution: ResolutionKeep, ReasonCode: ReasonKeepLosslessTarget},
			)
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
				v.Decisions = append(
					v.Decisions,
					FileDecision{Path: f.PathPosix, Resolution: ResolutionKeep, ReasonCode: ReasonKeepEncodedSatisfied},
				)
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
			v.Decisions = append(
				v.Decisions,
				FileDecision{Path: f.PathPosix, Resolution: ResolutionDelete, ReasonCode: code},
			)
		}
		for _, f := range p.losslessObsolete {
			v.Decisions = append(
				v.Decisions,
				FileDecision{Path: f.PathPosix, Resolution: ResolutionDelete, ReasonCode: ReasonObsoleteLossless},
			)
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
