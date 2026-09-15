package execute

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// ToolsConfig represents the tools configuration for conversion.
type ToolsConfig struct {
	FFmpegPath  string
	FFprobePath string
}

// DeleteMode selects how obsolete files leave their place. Soft deletion keeps
// the legacy <root>/Delete/<relative path> recovery convention; it is never
// downgraded to a hard delete when the root is missing or unusable.
type DeleteMode string

const (
	DeleteModeSoft DeleteMode = "soft"
	DeleteModeHard DeleteMode = "hard"
)

// Component run stages, reported through ComponentRunResult.Stage.
const (
	ComponentStagePrecheck    = "precheck"
	ComponentStageMaterialize = "materialize"
	ComponentStageValidate    = "validate"
	ComponentStageCommit      = "commit"
	ComponentStageRemove      = "remove"
)

// Component run statuses.
const (
	ComponentStatusSucceeded = "succeeded"
	ComponentStatusFailed    = "failed"
	ComponentStatusCanceled  = "canceled"
)

// Component failure codes reported through ComponentError.Code. They name the
// gate that refused or interrupted the component, not a specific errno.
const (
	ComponentCodeInvalidRequest    = "COMPONENT_INVALID_REQUEST"
	ComponentCodeBlocked           = "COMPONENT_BLOCKED"
	ComponentCodeUnsupportedOp     = "COMPONENT_UNSUPPORTED_OPERATION"
	ComponentCodeInvalidDependency = "COMPONENT_INVALID_DEPENDENCY"
	ComponentCodeConflict          = "COMPONENT_TARGET_CONFLICT"
	ComponentCodeFileChanged       = "COMPONENT_FILE_CHANGED"
	ComponentCodePathUnsafe        = "COMPONENT_PATH_UNSAFE"
	ComponentCodeToolsUnavailable  = "COMPONENT_TOOLS_UNAVAILABLE"
	ComponentCodeEncodeFailed      = "COMPONENT_ENCODE_FAILED"
	ComponentCodeValidateFailed    = "COMPONENT_VALIDATE_FAILED"
	ComponentCodeCommitFailed      = "COMPONENT_COMMIT_FAILED"
	ComponentCodeDeleteFailed      = "COMPONENT_DELETE_FAILED"
	ComponentCodeCanceled          = "COMPONENT_CANCELED"
)

// ComponentRunRequest executes one frozen Component against its member root.
// The component, the effective target specs of its partition and the root are
// consumed exactly as frozen; no reconciliation or live draft state is
// consulted.
type ComponentRunRequest struct {
	Root       string
	Component  reconcile.ComponentOutcome
	Specs      reconcile.DesiredProfile
	DeleteMode DeleteMode
	Tools      ToolsConfig
}

// ComponentRunResult reports what actually happened to one Component. On a
// failed or canceled run the lists describe the partial outcome; file changes
// are never rolled back globally, so callers must reconcile these facts with
// the disk.
type ComponentRunResult struct {
	// Status is succeeded, failed or canceled.
	Status string
	// Stage is the stage the run stopped in; empty on success and when the
	// canceled request never entered a stage.
	Stage string
	// Committed are the target paths that now hold new outputs, in commit order.
	Committed []string
	// Removed are the obsolete paths removed from their place, in removal order.
	Removed []string
	// Remaining are the operation surfaces not completed, in frozen order:
	// target paths of uncommitted encodes, then paths of unremoved files.
	Remaining []string
	// Recovery lists paths that still hold media needing operator attention:
	// soft-delete destinations, replaced-old-file copies and temporary files
	// that could not be cleaned. All entries are persisted-form paths.
	Recovery []string
}

// ComponentError is the stable failure cause of a component run.
type ComponentError struct {
	Stage   string
	Code    string
	Path    string
	Message string
	Err     error
}

// Error implements error.
func (e *ComponentError) Error() string {
	if e.Err != nil {
		return e.Code + ": " + e.Message + ": " + e.Err.Error()
	}
	return e.Code + ": " + e.Message
}

// Unwrap returns the underlying cause.
func (e *ComponentError) Unwrap() error { return e.Err }

// componentToolkit is the component-run IO surface. Function fields keep every
// filesystem or encoder failure injectable without mocks.
type componentToolkit struct {
	encode func(ctx context.Context, src, dst string, spec reconcile.AudioOutputSpec) error
	rename func(oldpath, newpath string) error
	remove func(path string) error
}

// defaultComponentToolkit returns the real encoder and filesystem operations.
func defaultComponentToolkit(tools ToolsConfig) *componentToolkit {
	encoder := newFFmpeg(tools)
	return &componentToolkit{
		encode: encoder.Encode,
		rename: os.Rename,
		remove: os.Remove,
	}
}

// RunComponent executes one frozen Component: every output is materialized and
// validated before any commit, and every commit succeeds before any obsolete
// file is removed, so old audio is never cleaned while a replacement is still
// uncommitted. It takes no cross-run lock: callers must serialize runs over
// the same or overlapping roots (the execution session layer owns that).
func RunComponent(ctx context.Context, req ComponentRunRequest) (ComponentRunResult, error) {
	return runComponent(ctx, req, defaultComponentToolkit(req.Tools))
}

func runComponent(ctx context.Context, req ComponentRunRequest, tk *componentToolkit) (ComponentRunResult, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ComponentRunResult{Status: ComponentStatusCanceled}, canceledComponentError("", ctxErr)
	}

	plan, precheckErr := precheckComponentRequest(req)
	if precheckErr != nil {
		return ComponentRunResult{Status: ComponentStatusFailed, Stage: precheckErr.Stage}, precheckErr
	}

	encoder := newFFmpeg(req.Tools)
	soft := req.DeleteMode == DeleteModeSoft
	result := ComponentRunResult{Status: ComponentStatusSucceeded}

	// Materialize: encode every output into a task-owned temporary file in the
	// target directory, so the later commit is a same-filesystem rename.
	for i := range plan.encodes {
		enc := &plan.encodes[i]
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stopCanceled(plan, tk, result, ComponentStageMaterialize, ctxErr)
		}
		enc.temp = tempOutputPath(enc.target)
		if err := tk.encode(ctx, enc.source, enc.temp, enc.spec); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return stopCanceled(plan, tk, result, ComponentStageMaterialize, ctxErr)
			}
			return stopRun(plan, tk, result, ComponentStatusFailed, ComponentStageMaterialize, &ComponentError{
				Stage:   ComponentStageMaterialize,
				Code:    ComponentCodeEncodeFailed,
				Path:    posixForm(enc.target),
				Message: "encoding the frozen target failed",
				Err:     err,
			})
		}
	}

	// Validate: every staged output is re-probed on disk before any commit can
	// touch existing media.
	for i := range plan.encodes {
		enc := &plan.encodes[i]
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stopCanceled(plan, tk, result, ComponentStageValidate, ctxErr)
		}
		if err := validateStagedOutput(ctx, encoder, enc.temp, enc.spec); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return stopCanceled(plan, tk, result, ComponentStageValidate, ctxErr)
			}
			return stopRun(plan, tk, result, ComponentStatusFailed, ComponentStageValidate, &ComponentError{
				Stage:   ComponentStageValidate,
				Code:    ComponentCodeValidateFailed,
				Path:    posixForm(enc.target),
				Message: "staged output validation failed",
				Err:     err,
			})
		}
	}

	// Commit: outputs land only after every one of them is valid. A started
	// commit finishes before a cancellation is honored, so a recovery copy is
	// never destroyed halfway.
	for i := range plan.encodes {
		enc := &plan.encodes[i]
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stopCanceled(plan, tk, result, ComponentStageCommit, ctxErr)
		}
		recovery, committed, err := commitOutput(tk, plan.absRoot, enc, soft)
		result.Recovery = append(result.Recovery, recovery...)
		if committed {
			result.Committed = append(result.Committed, posixForm(enc.target))
		}
		if err != nil {
			return stopRun(plan, tk, result, ComponentStatusFailed, ComponentStageCommit, &ComponentError{
				Stage:   ComponentStageCommit,
				Code:    ComponentCodeCommitFailed,
				Path:    posixForm(enc.target),
				Message: "committing the output failed",
				Err:     err,
			})
		}
	}

	// Remove: obsolete audio is cleaned only after every commit succeeded.
	for i := range plan.removes {
		rem := &plan.removes[i]
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stopCanceled(plan, tk, result, ComponentStageRemove, ctxErr)
		}
		recoveryPath, err := removeObsolete(tk, plan.absRoot, rem.source, soft)
		if err != nil {
			return stopRun(plan, tk, result, ComponentStatusFailed, ComponentStageRemove, &ComponentError{
				Stage:   ComponentStageRemove,
				Code:    ComponentCodeDeleteFailed,
				Path:    posixForm(rem.source),
				Message: "removing an obsolete file failed",
				Err:     err,
			})
		}
		result.Removed = append(result.Removed, posixForm(rem.source))
		if recoveryPath != "" {
			result.Recovery = append(result.Recovery, recoveryPath)
		}
	}

	return result, nil
}

// stopRun finalizes a failed or canceled run: it records the stage and the
// not-completed operations and removes this run's unconsumed temporary
// outputs, reporting any leftover path that could not be cleaned.
func stopRun(
	plan *plannedComponent, tk *componentToolkit, result ComponentRunResult, status, stage string, cause error,
) (ComponentRunResult, error) {
	result.Status = status
	result.Stage = stage
	result.Remaining = remainingOps(plan, len(result.Committed), len(result.Removed))
	result.Recovery = append(result.Recovery, cleanupTemps(tk, plan)...)
	return result, cause
}

// stopCanceled finalizes a canceled run at a stage boundary.
func stopCanceled(
	plan *plannedComponent, tk *componentToolkit, result ComponentRunResult, stage string, ctxErr error,
) (ComponentRunResult, error) {
	return stopRun(plan, tk, result, ComponentStatusCanceled, stage, canceledComponentError(stage, ctxErr))
}

// remainingOps lists the operation surfaces not completed: the targets of
// uncommitted encodes followed by the paths of unremoved files, both in frozen
// order and persisted form.
func remainingOps(plan *plannedComponent, committed, removed int) []string {
	remaining := make([]string, 0, len(plan.encodes)-committed+len(plan.removes)-removed)
	for _, enc := range plan.encodes[committed:] {
		remaining = append(remaining, posixForm(enc.target))
	}
	for _, rem := range plan.removes[removed:] {
		remaining = append(remaining, posixForm(rem.source))
	}
	return remaining
}

// cleanupTemps removes this run's uncommitted temporary outputs. A path that
// cannot be cleaned is reported instead of silently dropped.
func cleanupTemps(tk *componentToolkit, plan *plannedComponent) []string {
	var left []string
	for i := range plan.encodes {
		temp := plan.encodes[i].temp
		if temp == "" {
			continue
		}
		if err := tk.remove(temp); err != nil && !errors.Is(err, fs.ErrNotExist) {
			left = append(left, posixForm(temp))
		}
	}
	return left
}

// validateStagedOutput re-probes a staged output before any commit: the file
// must still be a readable audio stream of the frozen codec class.
func validateStagedOutput(ctx context.Context, encoder FFmpeg, temp string, spec reconcile.AudioOutputSpec) error {
	stream, err := encoder.probe(ctx, temp)
	if err != nil {
		return fmt.Errorf("probe staged output: %w", err)
	}
	if !codecMatchesSpec(stream.Codec, spec) {
		return fmt.Errorf("staged output codec %q does not match target codec %q", stream.Codec, spec.Codec)
	}
	return nil
}

// codecMatchesSpec reports whether an ffprobe codec name belongs to the spec's
// codec class.
func codecMatchesSpec(codec string, spec reconcile.AudioOutputSpec) bool {
	switch spec.Codec {
	case reconcile.CodecWav:
		return strings.HasPrefix(codec, "pcm_")
	case reconcile.CodecFlac:
		return codec == "flac"
	case reconcile.CodecMp3:
		return codec == "mp3"
	case reconcile.CodecAac:
		return codec == "aac"
	}
	return false
}

// canceledComponentError builds the error of a canceled run.
func canceledComponentError(stage string, cause error) *ComponentError {
	return &ComponentError{
		Stage:   stage,
		Code:    ComponentCodeCanceled,
		Message: "component run canceled",
		Err:     cause,
	}
}
