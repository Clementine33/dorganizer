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
	"strings"

	"github.com/onsei/organizer/backend/internal/conversion/execute"
	"github.com/onsei/organizer/backend/internal/conversion/reconcile"
	"github.com/onsei/organizer/backend/internal/workset"
)

// Task implements workset.Task for the conversion operation.
type Task struct {
	inventory    Inventory
	settings     Settings
	buildEncoder execute.EncoderFactory
}

// New creates the conversion task: its input facts come from the injected
// inventory, its configuration from the injected settings, and the media tool
// it prepares with from the injected factory — built when a unit is prepared,
// so the tool paths in force are the ones configured at that moment.
func New(inv Inventory, settings Settings, buildEncoder execute.EncoderFactory) *Task {
	return &Task{inventory: inv, settings: settings, buildEncoder: buildEncoder}
}

// encoder builds the media tool for the tool paths the configuration names now.
func (t *Task) encoder() execute.Encoder {
	return t.buildEncoder(t.settings.Tools())
}

func (*Task) Kind() string { return workset.OperationTypeConversion }

// SeedDraft builds the initial sparse draft of a new conversion operation: no
// member records at all (everyone participates and inherits the common
// settings), mode available_sources stored explicitly, tag literals copied
// from config.json's prune.literal_tags, and wav + mp3@320 outputs so a new
// workset is immediately usable.
func (t *Task) SeedDraft() ([]byte, string, int) {
	doc := &DraftDoc{
		SchemaVersion:  DraftSchemaVersion,
		Mode:           reconcile.ModeAvailableSources,
		ClassifierTags: t.settings.PruneLiteralTags(),
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

func (*Task) ValidateDraft(raw []byte, members []*workset.WorksetMember) error {
	doc, err := ParseDraft(string(raw))
	if err != nil {
		return workset.NewError(
			workset.ErrKindInvalidArgument, "INVALID_DRAFT", "draft document is not valid", err,
		)
	}
	return validateDraftDoc(doc, members)
}

func (*Task) NormalizeDraft(raw []byte, members []*workset.WorksetMember) ([]byte, string, int, error) {
	doc, err := ParseDraft(string(raw))
	if err != nil {
		return nil, "", 0, workset.NewError(
			workset.ErrKindInvalidArgument, "INVALID_DRAFT", "draft document is not valid", err,
		)
	}
	normalized := normalizeDraft(doc, members)
	out, hash, err := MarshalDraft(normalized)
	if err != nil {
		return nil, "", 0, workset.NewError(
			workset.ErrKindInternal, "INTERNAL", "failed to encode draft", err,
		)
	}
	return []byte(out), hash, DraftSchemaVersion, nil
}

// ValidateSessionInput is the executable business validation at the generation
// boundary: the frozen draft must resolve into a complete, participating
// policy set.
func (*Task) ValidateSessionInput(rawDraft []byte, members []*workset.WorksetMember) error {
	doc, err := ParseDraft(string(rawDraft))
	if err != nil {
		return workset.NewError(
			workset.ErrKindInternal, "INTERNAL", "stored draft is invalid", err,
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
	in workset.PlanSessionInput,
) (*workset.PlanSnapshot, error) {
	doc, err := ParseDraft(string(in.RawDraft))
	if err != nil {
		return nil, workset.NewError(
			workset.ErrKindInvalidArgument, "DRAFT_LOAD_FAILED", "failed to load the frozen draft", err,
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
			in.Progress(workset.PlanProgress(p))
		}
	}
	snap, err := Plan(ctx, t.inventory, t.settings, Input{
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
func planSnapshotOf(snap *Snapshot, effective []MemberEffective) *workset.PlanSnapshot {
	out := &workset.PlanSnapshot{
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
		out.Roots = append(out.Roots, workset.PlanRootFacts{
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
		out.Units = append(out.Units, workset.PlannedUnit{
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
func (t *Task) EvaluateRevision(
	in workset.RevisionFacts,
) (workset.RevisionHealth, error) {
	if in.Detail == nil {
		return workset.RevisionHealth{}, workset.NewError(
			workset.ErrKindInternal, "INTERNAL", "revision facts are missing", nil,
		)
	}
	out := workset.RevisionHealth{}
	for _, c := range in.Detail.Components {
		if c.Status == "blocked" {
			out.UnitsBlocked = true
			break
		}
	}
	for _, r := range in.Detail.Roots {
		stale := rootIsStale(t.inventory, r)
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
// partition resolves to in the revision's own draft snapshot. The session
// options — the obsolete-audio handling the draft declared — are frozen here
// too, so a run always uses what the plan was made with.
func (*Task) FreezeExecution(
	in workset.RevisionFacts,
) (workset.FrozenExecution, []string, error) {
	frozen := workset.FrozenExecution{}
	if in.Detail == nil {
		return frozen, nil, workset.NewError(
			workset.ErrKindInternal, "INTERNAL", "revision facts are missing", nil,
		)
	}
	doc, err := ParseDraft(string(in.DraftSnapshot))
	if err != nil {
		return frozen, nil, workset.NewError(
			workset.ErrKindInternal, "INTERNAL", "stored revision snapshot is invalid", err,
		)
	}
	options, optionsErr := executionOptionsOf(doc)
	if optionsErr != nil {
		return frozen, nil, optionsErr
	}
	frozen.Options = options
	effective, err := ResolveEffective(doc, in.Members)
	if err != nil {
		return frozen, nil, workset.NewError(
			workset.ErrKindInternal, "INTERNAL", "stored revision snapshot does not resolve", err,
		)
	}
	policyByRoot := map[string]reconcile.Policy{}
	for _, e := range effective {
		if !e.Excluded {
			policyByRoot[e.FolderPath] = e.Policy
		}
	}
	rootByIndex := map[int]workset.PlanRootRecord{}
	for _, r := range in.Detail.Roots {
		rootByIndex[r.RootIndex] = r
	}
	for _, c := range in.Detail.Components {
		root, ok := rootByIndex[c.RootIndex]
		if !ok {
			return frozen, []string{workset.ExecBlockedInput}, nil
		}
		policy, ok := policyByRoot[root.RootPath]
		if !ok {
			return frozen, []string{workset.ExecBlockedInput}, nil
		}
		profile := reconcile.ProfileFor(policy, reconcile.Partition(c.Partition))
		operations := componentOperationCount(c.OutcomeJSON)
		frozen.Units = append(frozen.Units, workset.ExecutionUnit{
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

// RevisionMembers resolves the frozen draft snapshot into per-member effective
// configs with inheritance sources. An unreadable snapshot yields no members
// rather than failing the whole historical revision read.
func (*Task) RevisionMembers(in workset.RevisionFacts) ([]workset.RevisionMemberFacts, error) {
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
	out := make([]workset.RevisionMemberFacts, 0, len(effective))
	for _, e := range effective {
		out = append(out, workset.RevisionMemberFacts{
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
func (*Task) ReviewRevision(in workset.RevisionFacts) (workset.PlanReview, error) {
	out := workset.PlanReview{}
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
		out.Units = append(out.Units, workset.UnitReview{
			Payload:    json.RawMessage(mustJSON(comp)),
			Operations: len(comp.Operations),
		})
	}
	return out, nil
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

// deleteModeOf maps the draft's declared obsolete-audio handling onto the
// execute service's mode. The draft value names the choice the user made in
// the global settings; an absent value is the soft default.
func deleteModeOf(declared string) (execute.DeleteMode, error) {
	switch declared {
	case "", string(execute.DeleteModeSoft):
		return execute.DeleteModeSoft, nil
	case string(execute.DeleteModeHard):
		return execute.DeleteModeHard, nil
	}
	return "", workset.NewError(
		workset.ErrKindInvalidArgument,
		"INVALID_DELETE_MODE",
		"delete_mode must be soft or hard",
		nil,
	)
}

// executionOptionsOf freezes the session options the draft declares: the
// delete mode every unit run must use.
func executionOptionsOf(doc *DraftDoc) (json.RawMessage, error) {
	mode, err := deleteModeOf(doc.DeleteMode)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(mustJSON(struct {
		DeleteMode string `json:"delete_mode"`
	}{DeleteMode: string(mode)})), nil
}

// deleteModeFromOptions reads the frozen session options back.
func deleteModeFromOptions(options json.RawMessage) (execute.DeleteMode, error) {
	var opts struct {
		DeleteMode string `json:"delete_mode"`
	}
	if len(options) > 0 {
		if err := json.Unmarshal(options, &opts); err != nil {
			return "", workset.NewError(
				workset.ErrKindInternal,
				"REQUEST_LOAD_FAILED",
				"frozen session options are unreadable",
				err,
			)
		}
	}
	return deleteModeOf(opts.DeleteMode)
}

// mustJSON marshals a snapshot fragment for storage.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
