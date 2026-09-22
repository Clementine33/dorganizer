package execute

import (
	"context"
	"errors"
	"fmt"
)

// PreparedComponent is one frozen component that passed the full precheck and
// has written nothing yet: its staged outputs are materialized one index at a
// time, possibly concurrently, before a single Commit lands them in frozen
// order. A caller that will not commit them must Discard it.
type PreparedComponent struct {
	tk      *componentToolkit
	encoder FFmpeg
	plan    *plannedComponent
	soft    bool
}

// PrepareComponent prechecks a frozen component and writes nothing. It is the
// whole gate the serial run has always applied — operations, conflicts,
// dependencies, paths, disk facts and tool availability — split so a session
// can prepare a component before it decides to encode it.
func PrepareComponent(ctx context.Context, req ComponentRunRequest) (*PreparedComponent, error) {
	return prepareComponent(ctx, req, defaultComponentToolkit(req.Tools))
}

func prepareComponent(ctx context.Context, req ComponentRunRequest, tk *componentToolkit) (*PreparedComponent, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, canceledComponentError("", ctxErr)
	}
	plan, precheckErr := precheckComponentRequest(req)
	if precheckErr != nil {
		return nil, precheckErr
	}
	return &PreparedComponent{
		tk:      tk,
		encoder: newFFmpeg(req.Tools),
		plan:    plan,
		soft:    req.DeleteMode == DeleteModeSoft,
	}, nil
}

// Encodes returns how many staged outputs Commit expects.
func (p *PreparedComponent) Encodes() int { return len(p.plan.encodes) }

// EncodeOne materializes one staged output. Indices may run concurrently and
// must each be called exactly once: every call touches only its own staged
// path and its own encode slot. The staged temp is assigned before encoding
// starts, so a partial file is always covered by Discard.
func (p *PreparedComponent) EncodeOne(ctx context.Context, index int) error {
	if index < 0 || index >= len(p.plan.encodes) {
		return componentError(ComponentStageMaterialize, ComponentCodeInvalidRequest, "",
			fmt.Sprintf("encode index %d outside [0,%d)", index, len(p.plan.encodes)), nil)
	}
	enc := &p.plan.encodes[index]
	if ctxErr := ctx.Err(); ctxErr != nil {
		return canceledComponentError(ComponentStageMaterialize, ctxErr)
	}
	enc.temp = tempOutputPath(enc.target)
	if err := p.tk.encode(ctx, enc.source, enc.temp, enc.spec); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return canceledComponentError(ComponentStageMaterialize, ctxErr)
		}
		return &ComponentError{
			Stage:   ComponentStageMaterialize,
			Code:    ComponentCodeEncodeFailed,
			Path:    posixForm(enc.target),
			Message: "encoding the frozen target failed",
			Err:     err,
		}
	}
	return nil
}

// Commit re-probes every staged output, commits them in frozen order and then
// removes the obsolete audio. It is terminal and single-threaded: the caller
// must have every EncodeOne returned. A stop — cancellation as much as a
// failed sibling — is honoured between operations, never mid-operation, so a
// started rename or removal finishes and the returned facts describe exactly
// what landed.
func (p *PreparedComponent) Commit(ctx context.Context) (ComponentRunResult, error) {
	result := ComponentRunResult{Status: ComponentStatusSucceeded}

	// Validate: every staged output is re-probed on disk before any commit can
	// touch existing media.
	for i := range p.plan.encodes {
		enc := &p.plan.encodes[i]
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stopCanceled(p.plan, p.tk, result, ComponentStageValidate, ctxErr)
		}
		if err := validateStagedOutput(ctx, p.encoder, enc.temp, enc.spec); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return stopCanceled(p.plan, p.tk, result, ComponentStageValidate, ctxErr)
			}
			return stopRun(p.plan, p.tk, result, ComponentStatusFailed, ComponentStageValidate, &ComponentError{
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
	for i := range p.plan.encodes {
		enc := &p.plan.encodes[i]
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stopCanceled(p.plan, p.tk, result, ComponentStageCommit, ctxErr)
		}
		recovery, committed, err := commitOutput(p.tk, p.plan.recoveryRoot, enc, p.soft)
		result.Recovery = append(result.Recovery, recovery...)
		if committed {
			result.Committed = append(result.Committed, posixForm(enc.target))
		}
		if err != nil {
			return stopRun(p.plan, p.tk, result, ComponentStatusFailed, ComponentStageCommit, &ComponentError{
				Stage:   ComponentStageCommit,
				Code:    ComponentCodeCommitFailed,
				Path:    posixForm(enc.target),
				Message: "committing the output failed",
				Err:     err,
			})
		}
	}

	// Remove: obsolete audio is cleaned only after every commit succeeded.
	for i := range p.plan.removes {
		rem := &p.plan.removes[i]
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stopCanceled(p.plan, p.tk, result, ComponentStageRemove, ctxErr)
		}
		recoveryPath, err := removeObsolete(p.tk, p.plan.recoveryRoot, rem.source, p.soft)
		if err != nil {
			return stopRun(p.plan, p.tk, result, ComponentStatusFailed, ComponentStageRemove, &ComponentError{
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

// Discard cleans the staged outputs of a component that will not commit and
// reports the stopped run's facts: every frozen target is still to do, nothing
// landed, and a temp that could not be removed is reported instead of dropped.
// A nil cause reports a canceled stop; the cause otherwise supplies the
// reported status and stage.
func (p *PreparedComponent) Discard(cause error) ComponentRunResult {
	result := stoppedResult(cause)
	result.Remaining = remainingOps(p.plan, 0, 0)
	result.Recovery = cleanupTemps(p.tk, p.plan)
	return result
}

// stoppedResult is the facts of a component that never committed: a nil cause
// or a canceled ComponentError is a cancellation, any other cause a failure
// carrying the stage it stopped in. Both a failed preparation and a Discard go
// through it, so the serial wrapper and a session report the same shape.
func stoppedResult(cause error) ComponentRunResult {
	if cause == nil {
		return ComponentRunResult{Status: ComponentStatusCanceled}
	}
	cerr, ok := errors.AsType[*ComponentError](cause)
	if !ok {
		return ComponentRunResult{Status: ComponentStatusFailed}
	}
	status := ComponentStatusFailed
	if cerr.Code == ComponentCodeCanceled {
		status = ComponentStatusCanceled
	}
	return ComponentRunResult{Status: status, Stage: cerr.Stage}
}
