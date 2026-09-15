package workset

import (
	"context"
	"errors"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Confirmability reason codes returned with PLAN_NOT_CONFIRMABLE. They are
// stable contract values the UI can branch on.
const (
	ConfirmBlockedNotCurrent = "NOT_CURRENT_REVISION"
	ConfirmBlockedDraft      = "DRAFT_CHANGED"
	ConfirmBlockedGeneration = "GENERATION_IN_PROGRESS"
	ConfirmBlockedInput      = "INPUT_CHANGED"
	ConfirmBlockedComponents = "BLOCKED_COMPONENTS"
)

// confirmationJSONTime is the response timestamp layout (mirrors the HTTP
// layer's JSON time format).
const confirmationJSONTime = "2006-01-02T15:04:05.999999999Z07:00"

// ConfirmRevision formally accepts one operation revision (whole-version, not
// per-member). Authoritative checks, in order:
//
//  1. the revision is the operation's current revision;
//  2. the operation version still matches the caller's If-Match;
//  3. the current draft still matches the revision's frozen draft hash;
//  4. no queued/running generation exists for this operation;
//  5. every member's input still validates (not stale, not unavailable);
//  6. no unresolved blocked components.
//
// Unmet targets and zero-operation plans do NOT block confirmation; their
// counts and the exclusion scope are disclosed through the revision detail.
// The insert itself re-checks the version inside one transaction, so a
// concurrent draft save or promotion loses the race.
func (s *serviceImpl) ConfirmRevision(
	ctx context.Context,
	worksetID, operationType, planID string,
	req ConfirmRequest,
) (*ConfirmResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.rejectOrphaned(worksetID); err != nil {
		return nil, err
	}
	op, err := s.loadOperation(worksetID, operationType)
	if err != nil {
		return nil, err
	}
	if op.CurrentRevisionID == "" || op.CurrentRevisionID != planID {
		return nil, notConfirmable(ConfirmBlockedNotCurrent)
	}
	rev, err := s.repo.GetOperationRevision(worksetID, operationType, planID)
	if err != nil {
		if errors.Is(err, sqlite.ErrRevisionNotFound) {
			return nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision", err)
	}
	if req.IfMatchVersion != op.Version {
		// A stale If-Match means the caller's view is outdated; the draft in
		// their hands may differ. Same conflict shape, retry after re-read.
		return nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "operation version conflict", nil)
	}

	reasons := make([]string, 0, 4)
	// (3) Draft drift: the canonical draft hash must equal the revision's.
	draft, err := s.repo.GetOperationDraft(worksetID, operationType)
	if err != nil || draft == nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load draft", err)
	}
	if rev.DraftHash != draft.DraftHash {
		reasons = append(reasons, ConfirmBlockedDraft)
	}

	// (4) Active generation, scoped to this operation only.
	active, err := s.repo.GetActiveGenerationForOperation(worksetID, operationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active generation", err)
	}
	if active != nil {
		reasons = append(reasons, ConfirmBlockedGeneration)
	}

	// (5) Input freshness and (6) unresolved blocked units, answered by the
	// operation's task over the frozen revision.
	detail, err := s.repo.GetWorkflowPlanDetail(planID)
	if err != nil {
		return nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
	}
	health, healthErr := s.revisionHealth(operationType, detail)
	if healthErr != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to evaluate revision health", healthErr)
	}
	if health.InputMoved {
		reasons = append(reasons, ConfirmBlockedInput)
	}
	if health.UnitsBlocked {
		reasons = append(reasons, ConfirmBlockedComponents)
	}
	if len(reasons) > 0 {
		return nil, NewError(ErrKindConflict, "PLAN_NOT_CONFIRMABLE", "revision cannot be confirmed", nil).
			WithDetails(reasons)
	}

	// Idempotency: detect an existing record before inserting so the HTTP
	// layer can answer 201 (first) vs 200 (repeat).
	_, confErr := s.repo.GetOperationConfirmation(planID)
	created := errors.Is(confErr, sqlite.ErrConfirmationNotFound)
	if confErr != nil && !created {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load confirmation", confErr)
	}

	persistErr := s.repo.ConfirmOperationRevision(worksetID, operationType, planID, req.IfMatchVersion, time.Now())
	if persistErr != nil {
		switch {
		case errors.Is(persistErr, sqlite.ErrVersionConflict):
			return nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "operation version conflict", nil)
		case errors.Is(persistErr, sqlite.ErrRevisionNotFound):
			return nil, notConfirmable(ConfirmBlockedNotCurrent)
		case errors.Is(persistErr, sqlite.ErrOperationNotFound):
			return nil, NewError(ErrKindNotFound, "OPERATION_NOT_FOUND", "operation not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to persist confirmation", persistErr)
	}

	view, err := s.GetConfirmation(ctx, worksetID, operationType, planID)
	if err != nil {
		return nil, err
	}
	return &ConfirmResult{Confirmation: *view, Created: created}, nil
}

// GetConfirmation returns the confirmation state of one revision. The revision
// must belong to the operation; a plan from another operation is not found.
func (s *serviceImpl) GetConfirmation(
	_ context.Context,
	worksetID, operationType, planID string,
) (*ConfirmationView, error) {
	if _, err := s.repo.GetOperationRevision(worksetID, operationType, planID); err != nil {
		if errors.Is(err, sqlite.ErrRevisionNotFound) {
			return nil, NewError(ErrKindNotFound, "REVISION_NOT_FOUND", "revision not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load revision", err)
	}
	c, err := s.repo.GetOperationConfirmation(planID)
	if errors.Is(err, sqlite.ErrConfirmationNotFound) {
		return &ConfirmationView{Confirmed: false}, nil
	}
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load confirmation", err)
	}
	view := toConfirmationView(c)
	return &view, nil
}

func toConfirmationView(c *sqlite.OperationConfirmation) ConfirmationView {
	return ConfirmationView{
		Confirmed:        true,
		ConfirmedVersion: c.ConfirmedVersion,
		ConfirmedAt:      c.ConfirmedAt.UTC().Format(confirmationJSONTime),
	}
}

// notConfirmable builds the canonical conflict error for one reason.
func notConfirmable(reason string) error {
	return NewError(ErrKindConflict, "PLAN_NOT_CONFIRMABLE", "revision cannot be confirmed", nil).
		WithDetails([]string{reason})
}
