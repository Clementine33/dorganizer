package workset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// validateRevision computes the revision-level validation state from the
// operation's task health:
//   - orphaned worksets: (nil, "unavailable") — there is no live library to
//     validate against;
//   - any root's live input facts moved: (true, "stale");
//   - otherwise: (false, "valid").
func (s *serviceImpl) validateRevision(
	operationType string, orphaned bool, detail *PlanDetail,
) (*bool, string) {
	if orphaned {
		return nil, ValidationUnavailable
	}
	health, err := s.revisionHealth(operationType, detail)
	if err != nil || len(health.StaleRoots) > 0 {
		t := true
		return &t, ValidationStale
	}
	f := false
	return &f, ValidationValid
}

// loadCurrentRevision builds the compact immutable conclusion of an operation's
// current revision and returns the already-loaded roots for coverage.
func (s *serviceImpl) loadCurrentRevision(
	op *Operation,
) (*RevisionSummary, []PlanRootRecord, error) {
	rev, err := s.repo.GetOperationRevision(op.WorksetID, op.OperationType, op.CurrentRevisionID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load current revision", err)
	}
	detail, err := s.repo.GetPlanDetail(op.CurrentRevisionID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision plan", err)
	}
	w, err := s.repo.GetWorkset(op.WorksetID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	stale, validation := s.validateRevision(op.OperationType, w.LibraryID == "", detail)
	review, reviewErr := s.revisionReview(op.OperationType, detail)
	if reviewErr != nil {
		return nil, nil, reviewErr
	}
	return &RevisionSummary{
		PlanID:          detail.Plan.PlanID,
		RevisionIndex:   rev.RevisionIndex,
		CreatedAt:       detail.Plan.CreatedAt,
		Status:          detail.Plan.Status,
		SummaryReason:   revisionSummaryReason(detail),
		Counts:          revisionCounts(detail, review),
		ValidationState: validation,
		Stale:           stale,
	}, detail.Roots, nil
}

// revisionReview asks the operation's task for the reviewable view of one
// persisted revision.
func (s *serviceImpl) revisionReview(
	operationType string, detail *PlanDetail,
) (PlanReview, error) {
	task, err := s.requireTask(operationType)
	if err != nil {
		return PlanReview{}, err
	}
	return task.ReviewRevision(RevisionFacts{Detail: detail})
}

// planSummaryOf parses the generic plan-summary facts out of a persisted
// revision.
func planSummaryOf(detail *PlanDetail) PlanSummary {
	if len(detail.Steps) == 0 {
		return PlanSummary{}
	}
	var summary PlanSummary
	_ = json.Unmarshal([]byte(detail.Steps[0].StepSummaryJSON), &summary)
	return summary
}

func revisionSummaryReason(detail *PlanDetail) string {
	return planSummaryOf(detail).SummaryReason
}

// revisionCounts derives the four independent plan facts from the frozen
// snapshot. Changed, unmet, blocked and unchanged are separate facts that may
// overlap; no exclusive status label is used to derive them (ADR 0001 §2).
func revisionCounts(detail *PlanDetail, review PlanReview) RevisionCounts {
	summary := planSummaryOf(detail)
	withOperations := map[string]bool{}
	blockedComponents := 0
	for i, c := range detail.Components {
		if c.Status == "blocked" {
			blockedComponents++
			continue
		}
		if i < len(review.Units) && review.Units[i].Operations > 0 {
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
		if errors.Is(err, ErrRevisionNotFound) {
			return nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision", err)
	}
	detail, err := s.repo.GetPlanDetail(planID)
	if err != nil {
		return nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
	}
	w, err := s.repo.GetWorkset(worksetID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	staleRoots := map[int]bool{}
	if w.LibraryID != "" {
		if health, healthErr := s.revisionHealth(operationType, detail); healthErr == nil {
			for _, idx := range health.StaleRoots {
				staleRoots[idx] = true
			}
		}
	}

	review, reviewErr := s.revisionReview(operationType, detail)
	if reviewErr != nil {
		return nil, reviewErr
	}
	out := &RevisionView{
		PlanID:        planID,
		RevisionIndex: rev.RevisionIndex,
		CreatedAt:     rev.CreatedAt,
		Counts:        revisionCounts(detail, review),
	}
	members, mErr := s.repo.ListWorksetMembers(worksetID)
	if mErr != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load members", mErr)
	}
	memberFacts, mfErr := s.revisionMemberFacts(operationType, rev.DraftSnapshot, members)
	if mfErr != nil {
		memberFacts = nil
	}
	out.Members = revisionMembers(memberFacts, members, rev.ExcludedScope)

	roots := make([]RootValidation, 0, len(detail.Roots))
	for _, r := range detail.Roots {
		roots = append(roots, RootValidation{
			RootIndex:            r.RootIndex,
			RootPath:             r.RootPath,
			RootStatus:           r.RootStatus,
			RootErrorCode:        r.RootErrorCode,
			RootErrorMessage:     r.RootErrorMessage,
			Stale:                staleRoots[r.RootIndex],
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
	out.Plan = s.planView(operationType, detail)
	executed, execErr := s.repo.GetExecutionForRevision(planID)
	if execErr != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision execution", execErr)
	}
	if executed != nil {
		out.Execution = executionRefOf(executed)
	}
	return out, nil
}

// revisionMemberFacts asks the operation's task to resolve the frozen draft
// snapshot into per-member effective configs.
func (s *serviceImpl) revisionMemberFacts(
	operationType string, draftSnapshot string, members []*WorksetMember,
) ([]RevisionMemberFacts, error) {
	task, err := s.requireTask(operationType)
	if err != nil {
		return nil, err
	}
	return task.RevisionMembers(RevisionFacts{DraftSnapshot: []byte(draftSnapshot), Members: members})
}

// revisionMembers joins the task's resolved member configs with the workset's
// member names and the revision's frozen exclusion scope. An unreadable
// snapshot yields no members rather than failing the whole historical revision
// read.
func revisionMembers(
	facts []RevisionMemberFacts, members []*WorksetMember, excludedScope string,
) []RevisionMember {
	if len(facts) == 0 {
		return nil
	}
	names := map[string]string{}
	for _, m := range members {
		names[m.MemberID] = m.FolderName
	}
	excluded := excludedSet(excludedScope)
	out := make([]RevisionMember, 0, len(facts))
	for _, f := range facts {
		// Exclusion is authoritative in the frozen scope; the snapshot's own
		// flags are the same fact recorded twice.
		out = append(out, RevisionMember{
			MemberID:   f.MemberID,
			FolderPath: f.FolderPath,
			MemberName: names[f.MemberID],
			Excluded:   f.Excluded || excluded[f.MemberID],
			Payload:    f.Payload,
			Sources:    f.Sources,
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

// planView rebuilds one revision's reviewable plan from its persisted records
// — never from live policy/classifier state. A revision carries exactly one
// plan, so the persisted single step flattens into it; the payload decode
// belongs to the operation's task.
func (s *serviceImpl) planView(operationType string, detail *PlanDetail) RevisionPlan {
	out := RevisionPlan{
		PlanID:            detail.Plan.PlanID,
		SnapshotToken:     detail.Plan.SnapshotToken,
		RootPath:          detail.Plan.RootPath,
		TaskKind:          detail.Plan.TaskKind,
		TaskSchemaVersion: detail.Plan.TaskSchemaVersion,
		Summary:           planSummaryOf(detail),
	}
	if len(detail.Steps) > 0 {
		st := detail.Steps[0]
		out.Status = st.Status
		out.PolicyHash = st.PolicyHash
		out.StepSummary = json.RawMessage(st.StepSummaryJSON)
	}
	if review, err := s.revisionReview(operationType, detail); err == nil {
		out.Payload = review.Payload
		out.ClassifierTags = review.Tags
		out.ClassifierHash = review.TagHash
		for _, u := range review.Units {
			out.Units = append(out.Units, u.Payload)
		}
	}
	return out
}
