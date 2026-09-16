package reconcile

import "fmt"

// Reason code exclusive to the available_sources (relaxed) mode: an existing
// file kept because the declared shape of its stem cannot be reached. Strict
// planning turns the same input into a block instead.
const ReasonUnmetTarget = "UNMET_TARGET"

// Summary reason for relaxed outcomes: a plan can complete with unmet targets
// and still be reviewable/actionable, but the count distinguishes it from full
// satisfaction.
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
//  1. The declared profile is the stem's final shape: satisfied outputs are
//     kept, missing ones are materialized from the stem's source, and every
//     other observed file — including the source a declared output was encoded
//     from, and any output of a lane the profile does not declare — is
//     obsolete once those replacements commit.
//  2. A stem that cannot reach the declared shape is left exactly as it is and
//     recorded as an unmet target: no generation, no cleanup. Neither mode
//     weakens the rule that nothing is removed without a replacement.
//  3. Files whose quality cannot be verified (an unprobed bitrate) are never
//     counted as satisfying a target; where a source exists they are rebuilt.
//  4. No fake upgrades: lossless is never generated from lossy media, and a
//     codec change is never a lossy-to-lossy re-encode.
//  5. Source ambiguity and target-path conflicts still block the affected
//     component; relaxed mode never weakens these safety checks.
//  6. A partition with no files produces no component (unchanged) — that is
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

// lenientStem applies the relaxed decision table to one Variant Group. The
// declared profile is what the stem must end up holding, or the stem is left
// untouched and reported as unmet.
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

	losslessKeep := declaredLossless(profile.Lossless, losslessFiles)
	encodedKeep, encodedBelow := declaredEncoded(profile.Encoded, encodedFiles)
	needsOutput := (profile.Lossless != nil && len(losslessKeep) == 0) ||
		(profile.Encoded != nil && len(encodedKeep) == 0)

	sources := qualifiedSources(losslessFiles, profile)
	if needsOutput && len(sources) > 1 {
		d.blockCode = ReasonSourceAmbiguous
		d.blockMsg = fmt.Sprintf("stem %s has multiple equivalent lossless sources", g.Stem)
		d.keep(g.Stem, ReasonSourceAmbiguous)
		return d
	}
	var src *GroupedFile
	if len(sources) == 1 {
		s := sources[0]
		src = &s
	}
	if needsOutput && src == nil {
		// The declared shape is out of reach: every observed file stays and the
		// stem is reported as an unmet target. Removing what the profile does
		// not want would leave the stem with less than it has, not with what
		// was asked for.
		for _, f := range g.Files {
			d.keep(f.PathPosix, ReasonUnmetTarget)
		}
		return d
	}

	// The stem can reach its declared shape.
	kept := map[string]bool{}
	for _, f := range losslessKeep {
		kept[f.PathPosix] = true
		d.keep(f.PathPosix, ReasonKeepLosslessTarget)
	}
	for _, f := range encodedKeep {
		kept[f.PathPosix] = true
		d.keep(f.PathPosix, ReasonKeepEncodedSatisfied)
	}
	if profile.Lossless != nil && len(losslessKeep) == 0 {
		target := sameStemPath(src.PathPosix, ExtForCodec(profile.Lossless.Codec))
		d.encode(src.PathPosix, target, ReasonMaterializeLossless)
	}
	replaced := ""
	if profile.Encoded != nil && len(encodedKeep) == 0 {
		replaced = sameStemPath(src.PathPosix, ExtForCodec(profile.Encoded.Codec))
		if len(encodedBelow) == 1 {
			// A single below-target variant of the right codec is replaced in
			// place, so its path is not also queued for removal.
			replaced = sameStemPath(encodedBelow[0].PathPosix, ExtForCodec(profile.Encoded.Codec))
		}
		if _, taken := occupied[replaced]; taken && !stemOwns(g, replaced) {
			d.blockCode = ReasonTargetPathConflict
			d.blockMsg = fmt.Sprintf("stem %s target path %s is occupied by another entry", d.stem, replaced)
			return d
		}
		d.encode(src.PathPosix, replaced, ReasonMaterializeEncoded)
	}
	for _, f := range g.Files {
		if kept[f.PathPosix] || f.PathPosix == replaced {
			continue
		}
		reason := ReasonObsoleteEncoded
		if f.Lossless {
			reason = ReasonObsoleteLossless
		}
		d.del(f.PathPosix, reason)
	}
	return d
}

// declaredLossless returns the observed files that already are the declared
// lossless output; empty when the profile declares no lossless lane.
func declaredLossless(spec *AudioOutputSpec, losslessFiles []GroupedFile) []GroupedFile {
	if spec == nil {
		return nil
	}
	var keep []GroupedFile
	for _, f := range losslessFiles {
		if f.Codec == spec.Codec {
			keep = append(keep, f)
		}
	}
	return keep
}

// declaredEncoded splits the observed files of the declared encoded codec into
// the ones that already meet the target quality and the ones below it. Files
// of another codec are neither: the declared shape has no place for them.
func declaredEncoded(spec *AudioOutputSpec, encodedFiles []GroupedFile) (satisfied, below []GroupedFile) {
	if spec == nil {
		return nil, nil
	}
	for _, f := range encodedFiles {
		if f.Codec != spec.Codec {
			continue
		}
		if satisfiedEncoded(f, spec) {
			satisfied = append(satisfied, f)
		} else {
			below = append(below, f)
		}
	}
	return satisfied, below
}

// qualifiedSources returns the observed lossless files that may serve as this
// stem's source, in priority order: the declared lossless codec when the
// profile wants one, then WAV, then FLAC. It is deliberately independent of
// whether a lossless output is declared — a WAV is the source of an only-AAC
// target too. More than one candidate at the winning codec is ambiguous.
//
// Only lossless media qualify: a lossy file is never re-encoded, so a
// 256 kbps MP3 is no source for a 256 kbps AAC target.
func qualifiedSources(losslessFiles []GroupedFile, profile DesiredProfile) []GroupedFile {
	codecs := make([]Codec, 0, 3)
	if profile.Lossless != nil {
		codecs = append(codecs, profile.Lossless.Codec)
	}
	codecs = append(codecs, CodecWav, CodecFlac)
	for _, codec := range codecs {
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
