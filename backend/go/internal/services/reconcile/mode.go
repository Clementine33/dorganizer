package reconcile

import "fmt"

// Reason codes exclusive to the available_sources (relaxed) mode. They mark
// acceptable outcomes that strict planning can never produce: existing files
// kept because a target cannot be completed (unmet targets), not blocked.
const (
	ReasonUnmetTarget      = "UNMET_TARGET"
	ReasonKeepUnverifiable = "KEEP_QUALITY_UNVERIFIABLE"
	ReasonKeepNoSource     = "KEEP_NO_QUALIFIED_SOURCE"
)

// Summary reason for relaxed outcomes: a plan can complete with unmet targets
// and still be reviewable/confirmable, so it is ACTIONABLE-like, but the
// count distinguishes it from full satisfaction.
const ReasonUnmetTargets = "UNMET_TARGETS"

// stemDecision is one stem's relaxed plan: reviewable decisions plus the
// encode operations this stem would run. blockCode is set only for the
// fail-closed checks relaxed mode keeps (ambiguity, path conflicts).
type stemDecision struct {
	stem      string
	decisions []FileDecision
	ops       []Operation
	targets   []string // materialized target paths of this stem's encodes
	blockCode string
	blockMsg  string
}

func (d *stemDecision) keep(path, reason string) {
	d.decisions = append(d.decisions, FileDecision{Path: path, Resolution: ResolutionKeep, ReasonCode: reason})
}

func (d *stemDecision) del(path, reason string) {
	d.decisions = append(d.decisions, FileDecision{Path: path, Resolution: ResolutionDelete, ReasonCode: reason})
}

func (d *stemDecision) encode(source, target, reason string) {
	d.decisions = append(d.decisions, FileDecision{
		Path: target, Resolution: ResolutionEncode, ReasonCode: reason, TargetPath: target,
	})
	d.ops = append(d.ops, Operation{
		Kind: OpKindEncode, Phase: PhaseMaterializeOutputs, VariantStem: d.stem,
		SourcePath: source, TargetPath: target,
	})
	d.targets = appendUnique(d.targets, map[string]bool{}, target)
}

// countUnmetTargets counts stems in one component kept with an unmet target.
func countUnmetTargets(c ComponentOutcome) int {
	n := 0
	for _, v := range c.Variants {
		for _, d := range v.Decisions {
			if d.ReasonCode == ReasonUnmetTarget {
				n++
				break
			}
		}
	}
	return n
}

// lenientComponent plans one component with the available_sources (relaxed)
// decision table (design 4.2), evaluated per Variant Group:
//
//  1. Satisfied outputs are kept, never rebuilt because another stem lacks a
//     source (per-group decisions, no component-wide REBUILD_ALL).
//  2. A unique qualified lossless source generates missing/below-target
//     encoded outputs.
//  3. No qualified source: the whole stem's existing files are kept, no
//     generation or cleanup operations for that stem, and the unmet target is
//     recorded.
//  4. Files whose quality cannot be verified (AAC, unknown-bitrate MP3) are
//     kept but never marked satisfied.
//  5. No fake upgrades: lossless is never generated from lossy media.
//  6. Source ambiguity and target-path conflicts still block the affected
//     component; relaxed mode never weakens these safety checks.
//  7. A partition with no files produces no component (unchanged) — that is
//     "no applicable files", not a source-missing block.
//
// The surrounding skeleton (validation, partitioning, component building,
// ordering, summary) lives in Reconcile.
func lenientComponent(
	root string,
	partition Partition,
	profile DesiredProfile,
	comp Component,
	occupied map[string]struct{},
) ComponentOutcome {
	out := newComponentOutcome(root, partition, comp)

	var materializeTargets []string
	seenTargets := map[string]bool{}

	for _, g := range comp.StemGroups() {
		d := lenientStem(g, profile, occupied)
		if d.blockCode != "" {
			out.Status = StatusBlocked
			out.ReasonCode = d.blockCode
			out.Message = d.blockMsg
			out.Variants = append(out.Variants, VariantDecision{Stem: d.stem, Decisions: d.decisions})
			// Fail closed: emit nothing executable for a blocked component.
			// Empty slice (not nil) so the JSON contract is always an array.
			out.Operations = []Operation{}
			return out
		}
		out.Variants = append(out.Variants, VariantDecision{Stem: d.stem, Decisions: dedupeDecisions(d.decisions)})
		for _, op := range d.ops {
			op.ComponentID = out.ComponentID
			out.Operations = append(out.Operations, op)
		}
		for _, t := range d.targets {
			materializeTargets = appendUnique(materializeTargets, seenTargets, t)
		}
	}

	// Removal operations: every deletion depends on the component's
	// materialized targets (no deletion without replacement success).
	for _, v := range out.Variants {
		for _, dec := range v.Decisions {
			if dec.Resolution != ResolutionDelete {
				continue
			}
			out.Operations = append(out.Operations, Operation{
				Kind: OpKindRemoveObsolete, Phase: PhaseRemoveObsoleteAudio,
				ComponentID: out.ComponentID, VariantStem: v.Stem,
				SourcePath: dec.Path, DependsOn: append([]string{}, materializeTargets...),
			})
		}
	}
	sortOperations(out.Operations)
	out.ProjectedInventory = projectedInventory(out)
	return out
}

// lenientStem applies the relaxed decision table to one Variant Group.
func lenientStem(g StemGroup, profile DesiredProfile, occupied map[string]struct{}) stemDecision {
	d := stemDecision{stem: g.Stem}
	var losslessFiles, encodedFiles []GroupedFile
	for _, f := range g.Files {
		if f.Lossless {
			losslessFiles = append(losslessFiles, f)
		} else {
			encodedFiles = append(encodedFiles, f)
		}
	}

	source := qualifiedSource(losslessFiles, profile)

	// Rule 6: source ambiguity blocks the affected component.
	if len(losslessFiles) > 0 && profile.Lossless != nil && len(source) > 1 {
		d.blockCode = ReasonSourceAmbiguous
		d.blockMsg = fmt.Sprintf("stem %s has multiple equivalent lossless sources", g.Stem)
		d.keep(g.Stem, ReasonSourceAmbiguous)
		return d
	}
	var src *GroupedFile
	if len(source) == 1 {
		s := source[0]
		src = &s
	}

	if profile.Lossless != nil {
		lenientLosslessLane(&d, losslessFiles, profile, src)
	}
	if profile.Encoded != nil {
		lenientEncodedLane(&d, g, encodedFiles, profile, src, occupied)
	}
	return d
}

// qualifiedSource mirrors strict source selection: the desired lossless codec,
// else WAV, then FLAC. Returns all qualifying candidates; relaxed mode treats
// >1 as ambiguous and uses exactly one.
func qualifiedSource(losslessFiles []GroupedFile, profile DesiredProfile) []GroupedFile {
	if profile.Lossless == nil {
		return nil
	}
	for _, codec := range []Codec{profile.Lossless.Codec, CodecWav, CodecFlac} {
		var candidates []GroupedFile
		for _, f := range losslessFiles {
			if f.Codec == codec {
				candidates = append(candidates, f)
			}
		}
		if len(candidates) > 0 {
			return candidates
		}
	}
	return nil
}

// lenientLosslessLane decides the lossless lane for one stem.
func lenientLosslessLane(d *stemDecision, losslessFiles []GroupedFile, profile DesiredProfile, src *GroupedFile) {
	var targetCodec []GroupedFile
	for _, f := range losslessFiles {
		if f.Codec == profile.Lossless.Codec {
			targetCodec = append(targetCodec, f)
		}
	}
	switch {
	case len(targetCodec) > 0:
		// Satisfied: keep target-codec files. Other lossless files (e.g. a
		// FLAC beside the target WAV) are kept too: no source-based cleanup
		// applies in relaxed mode without a completable replacement plan.
		for _, f := range targetCodec {
			d.keep(f.PathPosix, ReasonKeepLosslessTarget)
		}
		for _, f := range losslessFiles {
			if f.Codec != profile.Lossless.Codec {
				d.keep(f.PathPosix, ReasonKeepNoSource)
			}
		}
	case len(losslessFiles) > 0 && src != nil:
		// Rule 5 boundary: only a lossless source may materialize a lossless
		// target (candidates are lossless by construction).
		target := sameStemPath(src.PathPosix, ExtForCodec(profile.Lossless.Codec))
		d.encode(src.PathPosix, target, ReasonMaterializeLossless)
	case len(losslessFiles) > 0:
		// Lossless present but unusable as a source: keep, record unmet.
		for _, f := range losslessFiles {
			d.keep(f.PathPosix, ReasonUnmetTarget)
		}
	default:
		// No lossless file: the lane has no applicable file to keep, but the
		// lossless target itself is unmet (design 4.2: satisfied MP3 alone
		// keeps, WAV target unmet). Reviewable, non-executable marker.
		d.keep(d.stem, ReasonUnmetTarget)
	}
}

// lenientEncodedLane decides the encoded lane for one stem.
func lenientEncodedLane(
	d *stemDecision,
	g StemGroup,
	encodedFiles []GroupedFile,
	profile DesiredProfile,
	src *GroupedFile,
	occupied map[string]struct{},
) {
	var targetSatisfied, targetBelow, targetOther []GroupedFile
	for _, f := range encodedFiles {
		switch {
		case f.Codec != profile.Encoded.Codec:
			targetOther = append(targetOther, f)
		case satisfiedEncoded(f, profile.Encoded):
			targetSatisfied = append(targetSatisfied, f)
		default:
			targetBelow = append(targetBelow, f)
		}
	}

	switch {
	case len(targetSatisfied) > 0:
		// Rule 1: keep satisfied outputs, no rebuild.
		for _, f := range targetSatisfied {
			d.keep(f.PathPosix, ReasonKeepEncodedSatisfied)
		}
	case src != nil:
		// Rule 2: unique qualified lossless source generates the missing or
		// below-target output, beside the existing file where possible.
		target := sameStemPath(src.PathPosix, ExtForCodec(profile.Encoded.Codec))
		if len(targetBelow) == 1 {
			target = sameStemPath(targetBelow[0].PathPosix, ExtForCodec(profile.Encoded.Codec))
		}
		if _, taken := occupied[target]; taken && !stemOwns(g, target) {
			d.blockCode = ReasonTargetPathConflict
			d.blockMsg = fmt.Sprintf("stem %s target path %s is occupied by another entry", d.stem, target)
			return
		}
		d.encode(src.PathPosix, target, ReasonMaterializeEncoded)
		// Strict cleanup semantics apply to a completable stem: below-target
		// and other-codec files are obsolete because a replacement now
		// exists; deletions depend on that replacement succeeding.
		for _, f := range targetBelow {
			if f.PathPosix != target {
				d.del(f.PathPosix, ReasonReplacedEncoded)
			}
		}
		for _, f := range targetOther {
			d.del(f.PathPosix, ReasonObsoleteEncoded)
		}
	default:
		// Rule 3: no qualified source — keep the whole stem, record the
		// unmet target, no generation or cleanup for this stem.
		for _, f := range targetBelow {
			d.keep(f.PathPosix, ReasonUnmetTarget)
		}
		for _, f := range targetOther {
			// Rule 4: quality-unverifiable files are kept, never satisfied.
			d.keep(f.PathPosix, ReasonKeepUnverifiable)
		}
	}
}

// stemOwns reports whether the occupied path belongs to the stem's own files
// (replacement in place is fine; another entry's path is a conflict).
func stemOwns(g StemGroup, target string) bool {
	for _, f := range g.Files {
		if f.PathPosix == target {
			return true
		}
	}
	return false
}
