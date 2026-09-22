package conversion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
	"github.com/onsei/organizer/backend/internal/inventory"
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
	component, profile, err := t.prepareComponent(ctx, in)
	if err != nil {
		return nil, err
	}
	return &preparedUnit{in: in, component: component, profile: profile, tools: t.tools()}, nil
}

// prepareComponent decodes the frozen unit payloads and prechecks the
// component; a component failure is mapped onto the seam error, keeping the
// stage and code its report entry carries. The decoded profile comes back with
// it: it is what the credentials of the committed outputs are written from.
func (t *Task) prepareComponent(
	ctx context.Context,
	in worksetusecase.UnitRunInput,
) (*execute.PreparedComponent, reconcile.DesiredProfile, error) {
	var outcome reconcile.ComponentOutcome
	if err := json.Unmarshal(in.Outcome, &outcome); err != nil {
		return nil, reconcile.DesiredProfile{}, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "REVISION_LOAD_FAILED", "frozen component is unreadable", err,
		)
	}
	var profile reconcile.DesiredProfile
	if err := json.Unmarshal(in.Unit.Payload, &profile); err != nil {
		return nil, reconcile.DesiredProfile{}, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "REQUEST_LOAD_FAILED", "frozen unit is unreadable", err,
		)
	}
	mode, modeErr := deleteModeFromOptions(in.Options)
	if modeErr != nil {
		return nil, reconcile.DesiredProfile{}, modeErr
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
		return nil, reconcile.DesiredProfile{}, unitFailureOf(prepErr)
	}
	return component, profile, nil
}

// preparedUnit is one prepared conversion unit: the frozen inputs, the profile
// its outputs are written for, and the component whose staged outputs the
// session encodes and commits.
type preparedUnit struct {
	in        worksetusecase.UnitRunInput
	component *execute.PreparedComponent
	profile   reconcile.DesiredProfile
	tools     execute.ToolsConfig
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
	result := p.resultOf(res, err)
	p.recordGenerations(ctx, &result)
	return result, nil
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

// recordGenerations gathers the generation credential of every encoded output
// this unit committed: the target it was written for, the encoder that wrote
// it, and the hash of the bytes now sitting at its path. These are the facts the
// next plan judges such a file by where its measured rate cannot — a VBR average,
// or an encoder that saturates — so they are read back from the committed file
// itself, never from the plan that asked for it.
//
// A failure here discloses itself on the unit and drops the whole batch with
// the inventory refresh: a generation is then committed but unrecorded, which
// costs one redundant rebuild and never a wrong acceptance.
func (p *preparedUnit) recordGenerations(ctx context.Context, result *worksetusecase.UnitResult) {
	version, probed := "", false
	for _, path := range result.Committed {
		spec, ok := p.encodedSpecFor(path)
		if !ok {
			continue
		}
		if !probed {
			version, probed = execute.ToolVersion(ctx, p.tools), true
		}
		encoder, mode, factsErr := execute.TargetFacts(spec)
		if factsErr != nil {
			result.InventoryError = fmt.Sprintf("generation facts for %s: %v", path, factsErr)
			return
		}
		info, statErr := os.Stat(filepath.FromSlash(path))
		if statErr != nil {
			result.InventoryError = fmt.Sprintf("stat committed output %s: %v", path, statErr)
			return
		}
		digest, hashErr := fileSHA256(filepath.FromSlash(path))
		if hashErr != nil {
			result.InventoryError = fmt.Sprintf("hash committed output %s: %v", path, hashErr)
			return
		}
		result.Generated = append(result.Generated, inventory.GenerationRecord{
			Path:           path,
			Codec:          string(spec.Codec),
			Encoder:        encoder,
			EncoderVersion: version,
			BitrateKbps:    spec.Quality.Bitrate,
			Mode:           mode,
			Size:           info.Size(),
			Mtime:          info.ModTime().Unix(),
			ContentSHA256:  digest,
			CreatedAt:      time.Now(),
		})
	}
}

// encodedSpecFor returns the encoded target one committed path was written for,
// or false when the unit's profile declares none for that extension. The
// lossless lane carries no credential: a lossless target is accepted on its
// codec, where there is no rate to mis-read.
func (p *preparedUnit) encodedSpecFor(path string) (reconcile.AudioOutputSpec, bool) {
	spec := p.profile.Encoded
	if spec == nil || spec.Quality == nil {
		return reconcile.AudioOutputSpec{}, false
	}
	if reconcile.ExtForCodec(spec.Codec) != strings.ToLower(filepath.Ext(path)) {
		return reconcile.AudioOutputSpec{}, false
	}
	return *spec, true
}

// fileSHA256 hashes one committed output: the credential's binding to the exact
// bytes, kept so a deep re-check stays possible even though a plan compares the
// cheaper size and mtime.
func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
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
	facts := make([]inventory.InventoryFile, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(filepath.FromSlash(p))
		if err != nil {
			result.InventoryError = fmt.Sprintf("stat %s: %v", p, err)
			return
		}
		facts = append(facts, inventory.InventoryFile{Path: p, Size: info.Size(), Mtime: info.ModTime().Unix()})
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

// nonNil turns a nil path slice into an empty one so a persisted component
// result never marshals a planned-empty list as JSON null.
func nonNil(paths []string) []string {
	if paths == nil {
		return []string{}
	}
	return paths
}
