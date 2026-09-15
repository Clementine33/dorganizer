// Package conversion is the conversion task: reconciliation of a workset's
// audio inventory against its draft's desired outputs, plus execution of the
// confirmed revisions that reconciliation produces. It is the first adapter
// behind the workset Task seam.
package conversion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	appconfig "github.com/onsei/organizer/backend/internal/config"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/execute"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	worksetusecase "github.com/onsei/organizer/backend/internal/usecase/workset"
)

// Task implements worksetusecase.Task for the conversion operation.
type Task struct {
	configDir string
}

// New creates the conversion task rooted at one config directory.
func New(configDir string) *Task {
	return &Task{configDir: configDir}
}

func (*Task) Kind() string { return worksetusecase.OperationTypeConversion }

// SeedDraft builds the initial sparse draft of a new conversion operation: no
// member records at all (everyone participates and inherits the common
// settings), mode available_sources stored explicitly, tag literals copied
// from config.json's prune.literal_tags, and wav + mp3@320 outputs so a new
// workset is immediately usable.
func (t *Task) SeedDraft() ([]byte, string, int) {
	doc := &DraftDoc{
		SchemaVersion:  DraftSchemaVersion,
		Mode:           reconcile.ModeAvailableSources,
		ClassifierTags: appconfig.LoadPruneLiteralTags(t.configDir),
		Matched:        defaultProfile(),
		Unmatched:      defaultProfile(),
	}
	if doc.ClassifierTags == nil {
		doc.ClassifierTags = []string{}
	}
	raw, hash, err := MarshalDraft(doc)
	if err != nil {
		raw, hash = "{}", ""
	}
	return []byte(raw), hash, DraftSchemaVersion
}

func (*Task) ValidateDraft(raw []byte, members []*sqlite.WorksetMember) error {
	doc, err := ParseDraft(string(raw))
	if err != nil {
		return worksetusecase.NewError(
			worksetusecase.ErrKindInvalidArgument, "INVALID_DRAFT", "draft document is not valid", err,
		)
	}
	return validateDraftDoc(doc, members)
}

func (*Task) NormalizeDraft(raw []byte, members []*sqlite.WorksetMember) ([]byte, string, int, error) {
	doc, err := ParseDraft(string(raw))
	if err != nil {
		return nil, "", 0, worksetusecase.NewError(
			worksetusecase.ErrKindInvalidArgument, "INVALID_DRAFT", "draft document is not valid", err,
		)
	}
	normalized := normalizeDraft(doc, members)
	out, hash, err := MarshalDraft(normalized)
	if err != nil {
		return nil, "", 0, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "INTERNAL", "failed to encode draft", err,
		)
	}
	return []byte(out), hash, DraftSchemaVersion, nil
}

// ValidateSessionInput is the executable business validation at the generation
// boundary: the frozen draft must resolve into a complete, participating
// policy set.
func (*Task) ValidateSessionInput(rawDraft []byte, members []*sqlite.WorksetMember) error {
	doc, err := ParseDraft(string(rawDraft))
	if err != nil {
		return worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "INTERNAL", "stored draft is invalid", err,
		)
	}
	if _, err := ResolveExecutable(doc, members); err != nil {
		return err
	}
	return nil
}

// PlanSession resolves the frozen draft into the executable per-member input
// and runs one planning pass, mapping the planner snapshot onto the seam's
// plan snapshot.
func (t *Task) PlanSession(
	ctx context.Context,
	repo *sqlite.Repository,
	in worksetusecase.PlanSessionInput,
) (*worksetusecase.PlanSnapshot, error) {
	doc, err := ParseDraft(string(in.RawDraft))
	if err != nil {
		return nil, worksetusecase.NewError(
			worksetusecase.ErrKindInvalidArgument, "DRAFT_LOAD_FAILED", "failed to load the frozen draft", err,
		)
	}
	effective, err := ResolveExecutable(doc, in.Members)
	if err != nil {
		return nil, err
	}
	policies := ParticipatingPolicyMap(effective)
	roots := make([]RootInput, 0, len(policies))
	for _, m := range in.Members {
		if policy, ok := policies[m.FolderPath]; ok {
			roots = append(roots, RootInput{Path: m.FolderPath, Policy: policy})
		}
	}
	var progress func(Progress)
	if in.Progress != nil {
		progress = func(p Progress) {
			in.Progress(worksetusecase.PlanProgress(p))
		}
	}
	snap, err := Plan(ctx, repo, t.configDir, Input{
		Policy:           CommonPolicy(doc),
		Roots:            roots,
		MarkMissingRoots: true,
		Progress:         progress,
	})
	if err != nil {
		return nil, err
	}
	return planSnapshotOf(snap, effective), nil
}

// planSnapshotOf maps the planner's snapshot onto the seam's plan snapshot:
// every fact the generic side persists, with the conversion payloads opaque.
func planSnapshotOf(snap *Snapshot, effective []MemberEffective) *worksetusecase.PlanSnapshot {
	out := &worksetusecase.PlanSnapshot{
		RootPath:             snap.RootPath,
		Payload:              json.RawMessage(snap.PolicyJSON),
		PayloadHash:          snap.PolicyHash,
		PayloadSchemaVersion: snap.Policy.SchemaVersion,
		Tags:                 snap.ClassifierTags,
		TagsHash:             snap.Classifier.Hash,
		Summary:              json.RawMessage(mustJSON(snap.Summary)),
		Status:               snap.Status,
		ExcludedScope:        strings.Join(ExcludedMemberIDs(effective), "\x00"),
	}
	for _, r := range snap.Roots {
		out.Roots = append(out.Roots, worksetusecase.PlanRootFacts{
			Index:                r.Index,
			Path:                 r.Path,
			Identity:             r.Identity,
			InventoryFingerprint: r.InventoryFingerprint,
			Count:                r.Count,
			Status:               r.Status,
			ErrorCode:            r.ErrorCode,
			ErrorMessage:         r.ErrorMessage,
		})
	}
	for _, c := range snap.Components {
		out.Units = append(out.Units, worksetusecase.PlannedUnit{
			Index:      c.Index,
			RootIndex:  c.RootIndex,
			ID:         c.Outcome.ComponentID,
			Partition:  string(c.Outcome.Partition),
			Status:     c.Outcome.Status,
			ReasonCode: c.Outcome.ReasonCode,
			Payload:    json.RawMessage(mustJSON(c.Outcome)),
		})
	}
	return out
}

// EvaluateRevision reports whether the live input facts still match the frozen
// ones and whether any stored unit is blocked.
func (*Task) EvaluateRevision(
	repo *sqlite.Repository,
	in worksetusecase.RevisionFacts,
) (worksetusecase.RevisionHealth, error) {
	if in.Detail == nil {
		return worksetusecase.RevisionHealth{}, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "INTERNAL", "revision facts are missing", nil,
		)
	}
	out := worksetusecase.RevisionHealth{}
	for _, c := range in.Detail.Components {
		if c.Status == "blocked" {
			out.UnitsBlocked = true
			break
		}
	}
	for _, r := range in.Detail.Roots {
		stale := rootIsStale(repo, r)
		if stale {
			out.StaleRoots = append(out.StaleRoots, r.RootIndex)
		}
		// A missing root and a stale fingerprint are both "the disk no longer
		// matches what was frozen".
		if r.RootStatus == "missing" || stale {
			out.InputMoved = true
		}
	}
	return out, nil
}

// FreezeExecution validates that the revision can run and freezes its ordered
// units: each component's root path plus the effective target profile its
// partition resolves to in the revision's own draft snapshot.
func (t *Task) FreezeExecution(
	repo *sqlite.Repository,
	in worksetusecase.RevisionFacts,
	_ string,
) (worksetusecase.FrozenExecution, []string, error) {
	frozen := worksetusecase.FrozenExecution{}
	if in.Detail == nil {
		return frozen, nil, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "INTERNAL", "revision facts are missing", nil,
		)
	}
	doc, err := ParseDraft(string(in.DraftSnapshot))
	if err != nil {
		return frozen, nil, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "INTERNAL", "stored revision snapshot is invalid", err,
		)
	}
	effective, err := ResolveEffective(doc, in.Members)
	if err != nil {
		return frozen, nil, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "INTERNAL", "stored revision snapshot does not resolve", err,
		)
	}
	policyByRoot := map[string]reconcile.Policy{}
	for _, e := range effective {
		if !e.Excluded {
			policyByRoot[e.FolderPath] = e.Policy
		}
	}
	rootByIndex := map[int]sqlite.WorkflowRootRecord{}
	for _, r := range in.Detail.Roots {
		rootByIndex[r.RootIndex] = r
	}
	for _, c := range in.Detail.Components {
		root, ok := rootByIndex[c.RootIndex]
		if !ok {
			return frozen, []string{worksetusecase.ExecBlockedInput}, nil
		}
		policy, ok := policyByRoot[root.RootPath]
		if !ok {
			return frozen, []string{worksetusecase.ExecBlockedInput}, nil
		}
		profile := reconcile.ProfileFor(policy, reconcile.Partition(c.Partition))
		operations := componentOperationCount(c.OutcomeJSON)
		frozen.Units = append(frozen.Units, worksetusecase.ExecutionUnit{
			Index:      c.ComponentIndex,
			RootIndex:  c.RootIndex,
			ID:         c.ComponentID,
			RootPath:   root.RootPath,
			Partition:  c.Partition,
			Operations: operations,
			Payload:    json.RawMessage(mustJSON(profile)),
		})
		frozen.TotalOperations += operations
	}
	return frozen, nil, nil
}

// RunUnit executes one frozen unit through the Component pipeline and reports
// its observed facts plus the inventory refresh the generic side applies.
func (t *Task) RunUnit(
	ctx context.Context,
	repo *sqlite.Repository,
	in worksetusecase.UnitRunInput,
) (worksetusecase.UnitResult, error) {
	result := worksetusecase.UnitResult{}
	var outcome reconcile.ComponentOutcome
	if err := json.Unmarshal(in.Outcome, &outcome); err != nil {
		return result, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "REVISION_LOAD_FAILED", "frozen component is unreadable", err,
		)
	}
	var profile reconcile.DesiredProfile
	if err := json.Unmarshal(in.Unit.Payload, &profile); err != nil {
		return result, worksetusecase.NewError(
			worksetusecase.ErrKindInternal, "REQUEST_LOAD_FAILED", "frozen unit is unreadable", err,
		)
	}
	res, runErr := execute.RunComponent(ctx, execute.ComponentRunRequest{
		Root:       in.Unit.RootPath,
		Component:  outcome,
		Specs:      profile,
		DeleteMode: execute.DeleteMode(in.DeleteMode),
		Tools:      t.tools(),
	})
	result.Committed = nonNil(res.Committed)
	result.Removed = nonNil(res.Removed)
	result.Remaining = nonNil(res.Remaining)
	result.Recovery = nonNil(res.Recovery)
	if runErr != nil {
		result.Stage, result.ErrorCode, result.ErrorMessage = componentErrorOf(runErr)
		if ctx.Err() != nil || result.ErrorCode == execute.ComponentCodeCanceled {
			result.Canceled = true
			result.ErrorMessage = "component run canceled"
		}
	}
	t.fillInventoryFacts(&result, in)
	return result, nil
}

// fillInventoryFacts gathers the observed disk facts of one unit: the removed
// sources plus the committed outputs and soft-delete destinations that are
// real media in their scanned place. A stat failure is disclosed instead of
// being reported as "unchanged".
func (*Task) fillInventoryFacts(result *worksetusecase.UnitResult, in worksetusecase.UnitRunInput) {
	paths := make([]string, 0, len(result.Committed)+len(result.Recovery))
	paths = append(paths, result.Committed...)
	for _, p := range result.Recovery {
		if underRecoveryDir(in.Unit.RootPath, p) {
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

// RevisionMembers resolves the frozen draft snapshot into per-member effective
// configs with inheritance sources. An unreadable snapshot yields no members
// rather than failing the whole historical revision read.
func (*Task) RevisionMembers(in worksetusecase.RevisionFacts) ([]worksetusecase.RevisionMemberFacts, error) {
	if len(in.DraftSnapshot) == 0 {
		return nil, nil
	}
	doc, err := ParseDraft(string(in.DraftSnapshot))
	if err != nil {
		return nil, nil
	}
	effective, err := ResolveEffective(doc, in.Members)
	if err != nil {
		return nil, nil
	}
	out := make([]worksetusecase.RevisionMemberFacts, 0, len(effective))
	for _, e := range effective {
		out = append(out, worksetusecase.RevisionMemberFacts{
			MemberID:   e.MemberID,
			FolderPath: e.FolderPath,
			Excluded:   e.Excluded,
			Payload:    json.RawMessage(mustJSON(e.Policy)),
			Sources:    e.Sources,
		})
	}
	return out, nil
}

// ReviewRevision rebuilds the reviewable payload of one persisted revision
// from its stored rows: the plan payload, the tag snapshot and the re-encoded
// unit outcomes.
func (*Task) ReviewRevision(in worksetusecase.RevisionFacts) (worksetusecase.PlanReview, error) {
	out := worksetusecase.PlanReview{}
	if in.Detail == nil {
		return out, nil
	}
	if len(in.Detail.Steps) > 0 {
		st := in.Detail.Steps[0]
		out.Payload = json.RawMessage(st.PolicyJSON)
		out.Tags = strings.Split(st.ClassifierTags, "\x00")
		if len(out.Tags) == 1 && out.Tags[0] == "" {
			out.Tags = []string{}
		}
		out.TagHash = st.ClassifierHash
	}
	for _, c := range in.Detail.Components {
		var comp reconcile.ComponentOutcome
		if err := json.Unmarshal([]byte(c.OutcomeJSON), &comp); err != nil {
			return out, nil
		}
		out.Units = append(out.Units, worksetusecase.UnitReview{
			Payload:    json.RawMessage(mustJSON(comp)),
			Operations: len(comp.Operations),
		})
	}
	return out, nil
}

// tools resolves the encoder tools from config.json, matching the planner's
// own resolution; empty paths fall back to PATH.
func (t *Task) tools() execute.ToolsConfig {
	tools := execute.ToolsConfig{}
	data, err := os.ReadFile(filepath.Join(t.configDir, "config.json"))
	if err != nil {
		return tools
	}
	cfg := appconfig.DefaultAppConfig()
	if json.Unmarshal(data, &cfg) != nil {
		return tools
	}
	tools.FFmpegPath = cfg.Tools.FFmpegPath
	tools.FFprobePath = cfg.Tools.FFprobePath
	return tools
}

// componentErrorOf extracts the stable failure facts of a component run.
func componentErrorOf(err error) (stage, code, message string) {
	cerr, ok := errors.AsType[*execute.ComponentError](err)
	if !ok {
		return "", "COMPONENT_FAILED", err.Error()
	}
	msg := cerr.Message
	if cerr.Path != "" {
		msg = fmt.Sprintf("%s (%s)", msg, cerr.Path)
	}
	return cerr.Stage, cerr.Code, msg
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
	return len(parts) > 1 && parts[0] == softDeleteDirName
}

// softDeleteDirName mirrors the soft-delete convention owned by the execute
// service: removed media is preserved at <member root>/Delete/<relative path>.
const softDeleteDirName = "Delete"

// nonNil turns a nil path slice into an empty one so the persisted report never
// marshals a planned-empty list as JSON null.
func nonNil(paths []string) []string {
	if paths == nil {
		return []string{}
	}
	return paths
}

// mustJSON marshals a snapshot fragment for storage.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
