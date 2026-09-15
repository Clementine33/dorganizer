package workset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// loadCurrentRevision builds the compact immutable conclusion of an operation's
// current revision and returns the already-loaded roots for coverage.
func (s *serviceImpl) loadCurrentRevision(
	op *sqlite.Operation,
) (*RevisionSummary, []sqlite.WorkflowRootRecord, error) {
	rev, err := s.repo.GetOperationRevision(op.WorksetID, op.OperationType, op.CurrentRevisionID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load current revision", err)
	}
	detail, err := s.repo.GetWorkflowPlanDetail(op.CurrentRevisionID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision plan", err)
	}
	w, err := s.repo.GetWorkset(op.WorksetID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	stale, validation := s.validateRevision(w.LibraryID == "", detail)
	return &RevisionSummary{
		PlanID:          detail.Plan.PlanID,
		RevisionIndex:   rev.RevisionIndex,
		CreatedAt:       detail.Plan.CreatedAt,
		Status:          detail.Plan.Status,
		SummaryReason:   revisionSummaryReason(detail),
		Counts:          revisionCounts(detail),
		ValidationState: validation,
		Stale:           stale,
	}, detail.Roots, nil
}

func revisionSummaryReason(detail *sqlite.WorkflowPlanDetail) string {
	if len(detail.Steps) == 0 {
		return ""
	}
	return reconcileStepSummary(detail.Steps[0].StepSummaryJSON).SummaryReason
}

// revisionCounts derives the four independent plan facts from the frozen
// snapshot. Changed, unmet, blocked and unchanged are separate facts that may
// overlap; no exclusive status label is used to derive them (ADR 0004 §4).
func revisionCounts(detail *sqlite.WorkflowPlanDetail) RevisionCounts {
	summary := reconcile.StepSummary{}
	if len(detail.Steps) > 0 {
		summary = reconcileStepSummary(detail.Steps[0].StepSummaryJSON)
	}
	withOperations := map[string]bool{}
	blockedComponents := 0
	for _, c := range detail.Components {
		if c.Status == "blocked" {
			blockedComponents++
			continue
		}
		var outcome reconcile.ComponentOutcome
		if err := json.Unmarshal([]byte(c.OutcomeJSON), &outcome); err == nil && len(outcome.Operations) > 0 {
			withOperations[c.ComponentID] = true
		}
	}
	missingRoots := 0
	for _, r := range detail.Roots {
		if r.RootStatus == "missing" {
			missingRoots++
		}
	}
	componentCount := summary.ComponentCount
	if componentCount == 0 {
		componentCount = len(detail.Components)
	}
	changed := len(withOperations)
	return RevisionCounts{
		Members:      len(detail.Roots),
		Changed:      changed,
		UnmetTargets: summary.UnmetTargets,
		Blocked:      blockedComponents + missingRoots,
		Unchanged:    componentCount - changed - blockedComponents,
	}
}

// ListRevisions returns one page of revision summaries newest-first with a
// keyset on revision_index plus the next-page cursor.
func (s *serviceImpl) ListRevisions(
	ctx context.Context,
	worksetID, operationType string,
	beforeIndex, limit int,
) (*RevisionListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	op, err := s.loadOperation(worksetID, operationType)
	if err != nil {
		return nil, err
	}
	w, err := s.repo.GetWorkset(worksetID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	revs, err := s.repo.ListOperationRevisions(worksetID, operationType, beforeIndex, limit)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to list revisions", err)
	}
	out := make([]*RevisionSummary, 0, len(revs))
	for _, r := range revs {
		detail, derr := s.repo.GetWorkflowPlanDetail(r.PlanID)
		if derr != nil {
			continue // detached plan rows are skipped rather than failing the page
		}
		stale, validation := s.validateRevision(w.LibraryID == "", detail)
		out = append(out, &RevisionSummary{
			PlanID:          r.PlanID,
			RevisionIndex:   r.RevisionIndex,
			CreatedAt:       r.CreatedAt,
			Status:          detail.Plan.Status,
			SummaryReason:   revisionSummaryReason(detail),
			Counts:          revisionCounts(detail),
			ValidationState: validation,
			Stale:           stale,
		})
	}
	next := 0
	if len(revs) > 0 && len(revs) == limit {
		next = revs[len(revs)-1].RevisionIndex
	}
	_ = op
	return &RevisionListResult{Revisions: out, NextBeforeIndex: next}, nil
}

// GetRevision returns the immutable nested review detail. Members are resolved
// from the revision's frozen sparse draft, so a historical revision shows the
// configuration and inheritance sources it was planned with — never the
// current common values.
func (s *serviceImpl) GetRevision(
	ctx context.Context,
	worksetID, operationType, planID string,
) (*RevisionView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := s.loadOperation(worksetID, operationType); err != nil {
		return nil, err
	}
	rev, err := s.repo.GetOperationRevision(worksetID, operationType, planID)
	if err != nil {
		if errors.Is(err, sqlite.ErrRevisionNotFound) {
			return nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision", err)
	}
	detail, err := s.repo.GetWorkflowPlanDetail(planID)
	if err != nil {
		return nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
	}
	w, err := s.repo.GetWorkset(worksetID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	stale, _ := s.validateRevision(w.LibraryID == "", detail)

	out := &RevisionView{
		PlanID:        planID,
		RevisionIndex: rev.RevisionIndex,
		CreatedAt:     rev.CreatedAt,
		Counts:        revisionCounts(detail),
	}
	members, mErr := s.repo.ListWorksetMembers(worksetID)
	if mErr != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load members", mErr)
	}
	out.Members = revisionMembers(rev, members)

	roots := make([]RootValidation, 0, len(detail.Roots))
	for _, r := range detail.Roots {
		rootStale := false
		if stale != nil && *stale {
			rootStale = rootIsStale(s.repo, r)
		}
		roots = append(roots, RootValidation{
			RootIndex:            r.RootIndex,
			RootPath:             r.RootPath,
			RootStatus:           r.RootStatus,
			RootErrorCode:        r.RootErrorCode,
			RootErrorMessage:     r.RootErrorMessage,
			Stale:                rootStale,
			InventoryFingerprint: r.InventoryFingerprint,
			EntryCount:           r.EntryCount,
		})
	}
	out.Roots = roots
	for _, c := range detail.Components {
		out.ComponentRoots = append(out.ComponentRoots, ComponentRootRef{
			StepIndex:      c.StepIndex,
			ComponentIndex: c.ComponentIndex,
			ComponentID:    c.ComponentID,
			RootIndex:      c.RootIndex,
		})
	}
	out.Workflow = toPlanResponse(detail)
	return out, nil
}

// revisionMembers resolves the frozen draft snapshot into per-member effective
// configs with inheritance sources. An unreadable snapshot yields no members
// rather than failing the whole historical revision read.
func revisionMembers(rev *sqlite.OperationRevision, members []*sqlite.WorksetMember) []RevisionMember {
	if rev.DraftSnapshot == "" {
		return nil
	}
	doc, err := ParseDraft(rev.DraftSnapshot)
	if err != nil {
		return nil
	}
	effective, err := ResolveEffective(doc, members)
	if err != nil {
		return nil
	}
	names := map[string]string{}
	for _, m := range members {
		names[m.MemberID] = m.FolderName
	}
	out := make([]RevisionMember, 0, len(effective))
	excluded := excludedSet(rev.ExcludedScope)
	for _, e := range effective {
		// Exclusion is authoritative in the frozen scope; the snapshot's own
		// flags are the same fact recorded twice.
		e.Excluded = e.Excluded || excluded[e.MemberID]
		out = append(out, RevisionMember{
			MemberID:   e.MemberID,
			FolderPath: e.FolderPath,
			MemberName: names[e.MemberID],
			Excluded:   e.Excluded,
			Policy:     e.Policy,
			Sources:    e.Sources,
		})
	}
	return out
}

func excludedSet(scope string) map[string]bool {
	out := map[string]bool{}
	if scope == "" {
		return out
	}
	for id := range strings.SplitSeq(scope, "\x00") {
		if id != "" {
			out[id] = true
		}
	}
	return out
}

// toPlanResponse converts a persisted workflow detail into the usecase
// response shape, rebuilt from the persisted workflow snapshots.
func toPlanResponse(detail *sqlite.WorkflowPlanDetail) planusecase.Response {
	out := planusecase.Response{
		PlanID:        detail.Plan.PlanID,
		SnapshotToken: detail.Plan.SnapshotToken,
		RootPath:      detail.Plan.RootPath,
		PlanKind:      detail.Plan.PlanKind,
	}
	for i, st := range detail.Steps {
		sum := reconcileStepSummary(st.StepSummaryJSON)
		if i == 0 {
			out.Summary.SummaryReason = sum.SummaryReason
		}
		out.Summary.OperationCount += sum.OperationCount
		out.Summary.ErrorCount += sum.ErrorCount
		step := planusecase.StepResponse{
			StepType:   st.StepType,
			StepIndex:  st.StepIndex,
			Status:     st.Status,
			PolicyHash: st.PolicyHash,
			Summary:    sum,
		}
		_ = json.Unmarshal([]byte(st.PolicyJSON), &step.Policy)
		step.Classifier.Tags = strings.Split(st.ClassifierTags, "\x00")
		if step.Classifier.Tags[0] == "" && len(step.Classifier.Tags) == 1 {
			step.Classifier.Tags = []string{}
		}
		step.Classifier.Hash = st.ClassifierHash
		for _, c := range detail.Components {
			if c.StepIndex != st.StepIndex {
				continue
			}
			var comp reconcile.ComponentOutcome
			_ = json.Unmarshal([]byte(c.OutcomeJSON), &comp)
			step.Components = append(step.Components, comp)
		}
		out.Steps = append(out.Steps, step)
	}
	return out
}

func reconcileStepSummary(jsonStr string) reconcile.StepSummary {
	var sum reconcile.StepSummary
	_ = json.Unmarshal([]byte(jsonStr), &sum)
	return sum
}
