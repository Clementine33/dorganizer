package execute

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
)

// audioExtensions are the audio extensions observed in components and emitted
// as targets. Removal refuses anything else, so an operation can never delete
// a non-audio file.
var audioExtensions = map[string]bool{
	".wav": true, ".flac": true, ".mp3": true, ".m4a": true, ".aac": true,
}

// plannedEncode is one validated materialize operation. Paths are native
// absolute filesystem paths; temp is set when materialization starts.
type plannedEncode struct {
	source  string
	target  string
	spec    reconcile.AudioOutputSpec
	replace bool
	temp    string
}

// plannedRemove is one validated obsolete-removal operation with its frozen
// dependency list in persisted form.
type plannedRemove struct {
	source    string
	dependsOn []string
}

// plannedComponent is a frozen component that passed the full precheck.
type plannedComponent struct {
	absRoot  string
	realRoot string
	// recoveryRoot is the base soft removals are made relative to; it may be
	// above absRoot (the library root), never below it.
	recoveryRoot string
	encodes      []plannedEncode
	removes      []plannedRemove
}

// nativePath returns the filesystem form of a path as persisted.
func nativePath(p string) string { return filepath.Clean(filepath.FromSlash(p)) }

// posixForm returns the persisted display form of a filesystem path.
func posixForm(p string) string { return path.Clean(filepath.ToSlash(p)) }

// componentError builds a stage-scoped failure with its gate code.
func componentError(stage, code, p, message string, err error) *ComponentError {
	return &ComponentError{Stage: stage, Code: code, Path: p, Message: message, Err: err}
}

// precheckComponentRequest validates the whole component before any file is
// created: operation kinds and phases, effective target specs, dependency
// integrity, source/target conflicts, root boundaries and the disk facts of
// every referenced file. Only then may materialization create temporary files.
func precheckComponentRequest(req ComponentRunRequest) (*plannedComponent, *ComponentError) {
	plan, cerr := newPlannedComponent(req)
	if cerr != nil {
		return nil, cerr
	}
	specs, cerr := encodeSpecsByExtension(req.Specs)
	if cerr != nil {
		return nil, cerr
	}
	if cerr := plan.collectOperations(req.Component, specs); cerr != nil {
		return nil, cerr
	}
	if cerr := plan.validateConflicts(); cerr != nil {
		return nil, cerr
	}
	if cerr := plan.validateDependencies(); cerr != nil {
		return nil, cerr
	}
	if cerr := plan.checkPaths(); cerr != nil {
		return nil, cerr
	}
	if cerr := plan.checkDiskFacts(req.Component); cerr != nil {
		return nil, cerr
	}
	if len(plan.encodes) > 0 {
		if err := newFFmpeg(req.Tools).Check(); err != nil {
			return nil, componentError(ComponentStagePrecheck, ComponentCodeToolsUnavailable, "",
				"encode tools unavailable", err)
		}
	}
	return plan, nil
}

// newPlannedComponent validates the root and the component identity.
func newPlannedComponent(req ComponentRunRequest) (*plannedComponent, *ComponentError) {
	if req.DeleteMode != DeleteModeSoft && req.DeleteMode != DeleteModeHard {
		return nil, componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			fmt.Sprintf("unsupported delete mode %q", req.DeleteMode), nil)
	}
	if req.Root == "" {
		return nil, componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			"member root is required", nil)
	}
	absRoot := filepath.Clean(filepath.FromSlash(req.Root))
	if !filepath.IsAbs(absRoot) {
		return nil, componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			fmt.Sprintf("member root must be absolute: %s", req.Root), nil)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			"member root does not resolve", err)
	}
	info, err := os.Lstat(realRoot)
	if err != nil {
		return nil, componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			"cannot inspect member root", err)
	}
	if !info.IsDir() {
		return nil, componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			"member root is not a directory", nil)
	}
	plan := &plannedComponent{absRoot: absRoot, realRoot: realRoot}
	recoveryRoot, rerr := resolveRecoveryRoot(req, absRoot)
	if rerr != nil {
		return nil, rerr
	}
	plan.recoveryRoot = recoveryRoot
	if cerr := plan.validateComponent(req.Component); cerr != nil {
		return nil, cerr
	}
	return plan, nil
}

// resolveRecoveryRoot resolves the base soft removals are made relative to. It
// defaults to the member root and must be an existing directory: a recovery
// copy is never written through a path that cannot be resolved.
func resolveRecoveryRoot(req ComponentRunRequest, absRoot string) (string, *ComponentError) {
	if req.RecoveryRoot == "" {
		return absRoot, nil
	}
	recoveryRoot := filepath.Clean(filepath.FromSlash(req.RecoveryRoot))
	if !filepath.IsAbs(recoveryRoot) {
		return "", componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			fmt.Sprintf("recovery root must be absolute: %s", req.RecoveryRoot), nil)
	}
	resolved, err := filepath.EvalSymlinks(recoveryRoot)
	if err != nil {
		return "", componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			"recovery root does not resolve", err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			"cannot inspect recovery root", err)
	}
	if !info.IsDir() {
		return "", componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			"recovery root is not a directory", nil)
	}
	return recoveryRoot, nil
}

// validateComponent refuses blocked or malformed components.
func (p *plannedComponent) validateComponent(c reconcile.ComponentOutcome) *ComponentError {
	if c.ComponentID == "" {
		return componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			"component id is required", nil)
	}
	switch c.Status {
	case reconcile.StatusOK:
		return nil
	case reconcile.StatusBlocked:
		return componentError(ComponentStagePrecheck, ComponentCodeBlocked, "",
			"component is blocked and cannot be executed", nil)
	default:
		return componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
			fmt.Sprintf("unknown component status %q", c.Status), nil)
	}
}

// encodeSpecsByExtension maps the effective target specs to their canonical
// target extensions. Every materialize target must resolve to exactly one.
func encodeSpecsByExtension(profile reconcile.DesiredProfile) (map[string]reconcile.AudioOutputSpec, *ComponentError) {
	specs := make(map[string]reconcile.AudioOutputSpec, 2)
	for _, spec := range []*reconcile.AudioOutputSpec{profile.Lossless, profile.Encoded} {
		if spec == nil {
			continue
		}
		ext := reconcile.ExtForCodec(spec.Codec)
		if ext == "" {
			return nil, componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
				fmt.Sprintf("unsupported target codec %q", spec.Codec), nil)
		}
		if _, duplicate := specs[ext]; duplicate {
			return nil, componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
				fmt.Sprintf("ambiguous target spec for %s", ext), nil)
		}
		if !spec.Lossless() && (spec.Quality == nil ||
			spec.Quality.Kind != reconcile.QualityBitrate || spec.Quality.Bitrate <= 0) {
			return nil, componentError(ComponentStagePrecheck, ComponentCodeInvalidRequest, "",
				fmt.Sprintf("encoded target spec %s requires a positive bitrate", spec.Codec), nil)
		}
		specs[ext] = *spec
	}
	return specs, nil
}

// collectOperations validates every frozen operation and records it.
func (p *plannedComponent) collectOperations(
	c reconcile.ComponentOutcome, specs map[string]reconcile.AudioOutputSpec,
) *ComponentError {
	for i, op := range c.Operations {
		if op.ComponentID != c.ComponentID {
			return componentError(ComponentStagePrecheck, ComponentCodeUnsupportedOp, op.SourcePath,
				fmt.Sprintf("operation %d belongs to component %q", i, op.ComponentID), nil)
		}
		switch op.Kind {
		case reconcile.OpKindEncode:
			if cerr := p.collectEncode(op, specs, i); cerr != nil {
				return cerr
			}
		case reconcile.OpKindRemoveObsolete:
			if cerr := p.collectRemove(op, i); cerr != nil {
				return cerr
			}
		default:
			return componentError(ComponentStagePrecheck, ComponentCodeUnsupportedOp, op.SourcePath,
				fmt.Sprintf("operation %d has unsupported kind %q", i, op.Kind), nil)
		}
	}
	return nil
}

func (p *plannedComponent) collectEncode(
	op reconcile.Operation, specs map[string]reconcile.AudioOutputSpec, index int,
) *ComponentError {
	if op.Phase != reconcile.PhaseMaterializeOutputs {
		return componentError(ComponentStagePrecheck, ComponentCodeUnsupportedOp, op.TargetPath,
			fmt.Sprintf("operation %d has phase %q; want %q", index, op.Phase, reconcile.PhaseMaterializeOutputs), nil)
	}
	if op.SourcePath == "" || op.TargetPath == "" {
		return componentError(ComponentStagePrecheck, ComponentCodeUnsupportedOp, op.TargetPath,
			fmt.Sprintf("operation %d must reference a source and a target", index), nil)
	}
	target := nativePath(op.TargetPath)
	source := nativePath(op.SourcePath)
	spec, ok := specs[strings.ToLower(filepath.Ext(target))]
	if !ok {
		return componentError(ComponentStagePrecheck, ComponentCodeUnsupportedOp, op.TargetPath,
			fmt.Sprintf("no effective target spec for extension %s", filepath.Ext(op.TargetPath)), nil)
	}
	if source == target {
		return componentError(ComponentStagePrecheck, ComponentCodeConflict, op.TargetPath,
			"operation source and target are the same path", nil)
	}
	p.encodes = append(p.encodes, plannedEncode{source: source, target: target, spec: spec})
	return nil
}

func (p *plannedComponent) collectRemove(op reconcile.Operation, index int) *ComponentError {
	if op.Phase != reconcile.PhaseRemoveObsoleteAudio {
		return componentError(ComponentStagePrecheck, ComponentCodeUnsupportedOp, op.SourcePath,
			fmt.Sprintf("operation %d has phase %q; want %q", index, op.Phase, reconcile.PhaseRemoveObsoleteAudio), nil)
	}
	if op.SourcePath == "" || op.TargetPath != "" {
		return componentError(ComponentStagePrecheck, ComponentCodeUnsupportedOp, op.SourcePath,
			fmt.Sprintf("operation %d must reference only an obsolete source file", index), nil)
	}
	source := nativePath(op.SourcePath)
	if !audioExtensions[strings.ToLower(filepath.Ext(source))] {
		return componentError(ComponentStagePrecheck, ComponentCodeUnsupportedOp, op.SourcePath,
			"refusing to remove a non-audio file", nil)
	}
	remove := plannedRemove{source: source, dependsOn: make([]string, 0, len(op.DependsOn))}
	for _, dep := range op.DependsOn {
		remove.dependsOn = append(remove.dependsOn, posixForm(nativePath(dep)))
	}
	p.removes = append(p.removes, remove)
	return nil
}

// validateConflicts refuses duplicate targets, operation sources that are also
// targets, obsolete files that are also materialize targets, and duplicate
// removals.
//
// A removal of an encode source is deliberately allowed: materialization reads
// every source before the removal stage runs, and each removal carries the
// component's materialized targets as dependencies, so the file is only
// removed once its replacement has landed. That is the shape the planner emits
// when a declared output replaces the lossless source it was encoded from.
func (p *plannedComponent) validateConflicts() *ComponentError {
	targets := make(map[string]bool, len(p.encodes))
	for _, enc := range p.encodes {
		target := posixForm(enc.target)
		if targets[target] {
			return componentError(ComponentStagePrecheck, ComponentCodeConflict, target,
				"duplicate materialize target", nil)
		}
		targets[target] = true
	}
	for _, enc := range p.encodes {
		source := posixForm(enc.source)
		if targets[source] {
			return componentError(ComponentStagePrecheck, ComponentCodeConflict, source,
				"an operation source is also a materialize target", nil)
		}
	}
	removed := make(map[string]bool, len(p.removes))
	for _, rem := range p.removes {
		source := posixForm(rem.source)
		if targets[source] {
			return componentError(ComponentStagePrecheck, ComponentCodeConflict, source,
				"an obsolete file is also a materialize target", nil)
		}
		if removed[source] {
			return componentError(ComponentStagePrecheck, ComponentCodeConflict, source,
				"duplicate obsolete removal", nil)
		}
		removed[source] = true
	}
	return nil
}

// validateDependencies refuses removals whose dependency is not a materialize
// target of this component. A removal without dependencies (a delete-only
// component) is valid.
func (p *plannedComponent) validateDependencies() *ComponentError {
	targets := make(map[string]bool, len(p.encodes))
	for _, enc := range p.encodes {
		targets[posixForm(enc.target)] = true
	}
	for _, rem := range p.removes {
		for _, dep := range rem.dependsOn {
			if !targets[dep] {
				return componentError(ComponentStagePrecheck, ComponentCodeInvalidDependency, posixForm(rem.source),
					fmt.Sprintf("dependency %s is not a materialize target of this component", dep), nil)
			}
		}
	}
	return nil
}
