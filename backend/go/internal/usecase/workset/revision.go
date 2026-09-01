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

// loadCurrentRevision builds the compact immutable conclusion and returns the
// already-loaded roots for member coverage.
func (s *serviceImpl) loadCurrentRevision(w *sqlite.Workset) (*RevisionSummary, []sqlite.WorkflowRootRecord, error) {
	rev, err := s.repo.GetWorksetRevision(w.ID, w.CurrentRevisionID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load current revision", err)
	}
	detail, err := s.repo.GetWorkflowPlanDetail(w.CurrentRevisionID)
	if err != nil {
		return nil, nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision plan", err)
	}
	stale, validation := s.validateRevision(w, detail)
	return &RevisionSummary{
		PlanID:          detail.Plan.PlanID,
		RevisionIndex:   rev.RevisionIndex,
		CreatedAt:       detail.Plan.CreatedAt,
		Status:          detail.Plan.Status,
		SummaryReason:   revisionSummaryReason(detail),
		BlockedCount:    revisionBlockedCount(detail),
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

func revisionBlockedCount(detail *sqlite.WorkflowPlanDetail) int {
	count := 0
	for _, c := range detail.Components {
		if c.Status == "blocked" {
			count++
		}
	}
	for _, r := range detail.Roots {
		if r.RootStatus == "missing" {
			count++
		}
	}
	return count
}

// ListRevisions returns one page of revision summaries newest-first with a
// keyset on revision_index plus the next-page cursor.
func (s *serviceImpl) ListRevisions(
	ctx context.Context,
	worksetID string,
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
	w, err := s.repo.GetWorkset(worksetID)
	if err != nil {
		if errors.Is(err, sqlite.ErrWorksetNotFound) {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	revs, err := s.repo.ListWorksetRevisions(worksetID, beforeIndex, limit)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to list revisions", err)
	}
	out := make([]*RevisionSummary, 0, len(revs))
	for _, r := range revs {
		detail, derr := s.repo.GetWorkflowPlanDetail(r.PlanID)
		if derr != nil {
			continue // detached plan rows are skipped rather than failing the page
		}
		stale, validation := s.validateRevision(w, detail)
		out = append(out, &RevisionSummary{
			PlanID:          r.PlanID,
			RevisionIndex:   r.RevisionIndex,
			CreatedAt:       r.CreatedAt,
			Status:          detail.Plan.Status,
			SummaryReason:   revisionSummaryReason(detail),
			BlockedCount:    revisionBlockedCount(detail),
			ValidationState: validation,
			Stale:           stale,
		})
	}
	next := 0
	if len(revs) > 0 && len(revs) == limit {
		next = revs[len(revs)-1].RevisionIndex
	}
	return &RevisionListResult{Revisions: out, NextBeforeIndex: next}, nil
}

// GetRevision returns the immutable nested review detail.
func (s *serviceImpl) GetRevision(ctx context.Context, worksetID, planID string) (*RevisionView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rev, err := s.repo.GetWorksetRevision(worksetID, planID)
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
	stale, _ := s.validateRevision(w, detail)

	out := &RevisionView{
		PlanID:        planID,
		RevisionIndex: rev.RevisionIndex,
		CreatedAt:     rev.CreatedAt,
	}
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

// toPlanResponse converts a persisted workflow detail into the usecase
// response shape using the same reconstruction as the plan usecase
// GetWorkflowPlanDetail.
func toPlanResponse(detail *sqlite.WorkflowPlanDetail) planusecase.Response {
	out := planusecase.Response{
		PlanID:            detail.Plan.PlanID,
		SnapshotToken:     detail.Plan.SnapshotToken,
		RootPath:          detail.Plan.RootPath,
		PlanKind:          detail.Plan.PlanKind,
		Operations:        []planusecase.Operation{},
		Errors:            []planusecase.FolderError{},
		SuccessfulFolders: []string{},
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
