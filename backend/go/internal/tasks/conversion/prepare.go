package conversion

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/execute"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// PrepareUnit prechecks one frozen unit through the Component pipeline and
// returns it ready to encode: the frozen outcome, profile and session options
// are decoded, and nothing is written yet.
func (t *Task) PrepareUnit(
	ctx context.Context,
	_ *sqlite.Repository,
	in worksetusecase.UnitRunInput,
) (worksetusecase.PreparedUnit, error) {
	component, err := t.prepareComponent(ctx, in)
	if err != nil {
		return nil, err
	}
	return &preparedUnit{in: in, component: component}, nil
}

// prepareComponent decodes the frozen unit payloads and prechecks the
// component; a component failure is mapped onto the seam error, keeping the
// stage and code its report entry carries.
func (t *Task) prepareComponent(
	ctx context.Context,
	in worksetusecase.UnitRunInput,
) (*execute.PreparedComponent, error) {
	var outcome reconcile.ComponentOutcome
	if err := json.Unmarshal(in.Outcome, &outcome); err != nil {
		return nil, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "REVISION_LOAD_FAILED", "frozen component is unreadable", err,
		)
	}
	var profile reconcile.DesiredProfile
	if err := json.Unmarshal(in.Unit.Payload, &profile); err != nil {
		return nil, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "REQUEST_LOAD_FAILED", "frozen unit is unreadable", err,
		)
	}
	mode, modeErr := deleteModeFromOptions(in.Options)
	if modeErr != nil {
		return nil, modeErr
	}
	component, prepErr := execute.PrepareComponent(ctx, execute.ComponentRunRequest{
		Root:       in.Unit.RootPath,
		Component:  outcome,
		Specs:      profile,
		DeleteMode: mode,
		Tools:      t.tools(),
		// Recovery copies land under <workset root>/Delete/..., beside the
		// member folders, so they never re-enter the member's own inventory.
		RecoveryRoot: in.WorksetRoot,
	})
	if prepErr != nil {
		return nil, unitFailureOf(prepErr)
	}
	return component, nil
}

// preparedUnit is one prepared conversion unit: the frozen inputs plus the
// component whose staged outputs the session encodes and commits.
type preparedUnit struct {
	in        worksetusecase.UnitRunInput
	component *execute.PreparedComponent
}

// EncodeTasks is how many staged outputs Commit expects.
func (p *preparedUnit) EncodeTasks() int { return p.component.Encodes() }

// EncodeTask materializes one staged output.
func (p *preparedUnit) EncodeTask(ctx context.Context, index int) error {
	if err := p.component.EncodeOne(ctx, index); err != nil {
		return unitFailureOf(err)
	}
	return nil
}

// Commit lands the encoded outputs in frozen order and reports the unit's
// observed facts plus the inventory refresh the generic side applies.
func (p *preparedUnit) Commit(ctx context.Context) (worksetusecase.UnitResult, error) {
	res, err := p.component.Commit(ctx)
	return p.resultOf(res, err), nil
}

// Discard cleans the staged outputs of a unit that will not commit.
func (p *preparedUnit) Discard(cause error) worksetusecase.UnitResult {
	return p.resultOf(p.component.Discard(cause), cause)
}

// resultOf maps one component outcome onto the seam facts. The observing run's
// cause carries the stage and code of a failed or canceled unit.
func (p *preparedUnit) resultOf(res execute.ComponentRunResult, cause error) worksetusecase.UnitResult {
	result := worksetusecase.UnitResult{
		Committed: nonNil(res.Committed),
		Removed:   nonNil(res.Removed),
		Remaining: nonNil(res.Remaining),
		Recovery:  nonNil(res.Recovery),
	}
	if cause != nil {
		result.Stage, result.ErrorCode, result.ErrorMessage = componentErrorOf(cause)
	}
	if res.Status == execute.ComponentStatusCanceled ||
		result.ErrorCode == execute.ComponentCodeCanceled {
		result.Canceled = true
		result.ErrorMessage = "component run canceled"
	}
	fillInventoryFacts(&result, p.in)
	return result
}

// unitFailureOf maps a component failure onto the seam error: the unit's stage
// and code cross the seam so its own report entry carries them.
func unitFailureOf(err error) error {
	stage, code, message := componentErrorOf(err)
	return worksetusecase.NewError(worksetusecase.ErrKindInternal, code, message, err).WithStage(stage)
}

// fillInventoryFacts gathers the observed disk facts of one unit: the removed
// sources plus the committed outputs and soft-delete destinations that are
// real media in their scanned place. A stat failure is disclosed instead of
// being reported as "unchanged".
func fillInventoryFacts(result *worksetusecase.UnitResult, in worksetusecase.UnitRunInput) {
	paths := make([]string, 0, len(result.Committed)+len(result.Recovery))
	paths = append(paths, result.Committed...)
	for _, p := range result.Recovery {
		if underRecoveryDir(in.WorksetRoot, p) {
			paths = append(paths, p)
		}
	}
	facts := make([]sqlite.InventoryFile, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(filepath.FromSlash(p))
		if err != nil {
			result.InventoryError = fmt.Sprintf("stat %s: %v", p, err)
			return
		}
		facts = append(facts, sqlite.InventoryFile{Path: p, Size: info.Size(), Mtime: info.ModTime().Unix()})
	}
	result.InventoryRemoved = result.Removed
	result.InventoryRefreshed = facts
}

// underRecoveryDir reports whether a persisted path is inside the member root's
// soft-delete recovery folder: the only recovery entries that are real media in
// their scanned place. A temp leftover is not an inventory fact.
func underRecoveryDir(componentRoot, p string) bool {
	rel, err := filepath.Rel(filepath.FromSlash(componentRoot), filepath.FromSlash(p))
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) > 1 && parts[0] == execute.RecoveryDirName
}

// nonNil turns a nil path slice into an empty one so the persisted report never
// marshals a planned-empty list as JSON null.
func nonNil(paths []string) []string {
	if paths == nil {
		return []string{}
	}
	return paths
}
