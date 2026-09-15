package workset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// generationInput is the frozen enqueue-time input of one session.
type generationInput struct {
	operation *sqlite.Operation
	rawDraft  []byte
	members   []*sqlite.WorksetMember
	draftHash string
	request   generationRequest
}

// generationRequest is the frozen session payload persisted on the row: the
// canonical hashes plus the exact draft document the session will plan with.
// Freezing the draft here means a session never reads a later draft, whatever
// happens to the operation while it waits in the queue.
type generationRequest struct {
	DraftHash  string          `json:"draft_hash"`
	MemberHash string          `json:"member_hash"`
	Roots      int             `json:"roots"`
	Draft      json.RawMessage `json:"draft"`
}

// StartGeneration enqueues one planning session for an operation. The operation
// version is the write authority (If-Match) and doubles as the draft-changed
// guard: a stale version means the caller's draft view is outdated.
func (s *serviceImpl) StartGeneration(
	ctx context.Context,
	worksetID, operationType string,
	req StartGenerationRequest,
) (*StartGenerationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateIdemKey(req.IdempotencyKey); err != nil {
		return nil, err
	}
	input, err := s.prepareGeneration(worksetID, operationType)
	if err != nil {
		return nil, err
	}
	// Replays answer before the version and single-session gates. A retried
	// request must observe what it already asked for — the current revision, or
	// its own queued session — rather than a conflict caused by a promotion
	// that same request already triggered.
	if result, replayed, err := s.replayCurrentRevision(ctx, input); err != nil || replayed {
		return result, err
	}
	requestHash := hashJSON([]byte(input.draftHash + "|" + input.request.MemberHash))
	if result, replayed, err := s.replayGeneration(
		input.operation,
		req.IdempotencyKey,
		requestHash,
	); err != nil ||
		replayed {
		return result, err
	}
	// Only genuinely new work is gated by the caller's view of the operation.
	if req.IfMatchVersion != input.operation.Version {
		return nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "operation version conflict", nil)
	}
	if result, err := s.rejectActiveSession(input.operation); err != nil || result != nil {
		return result, err
	}
	return s.persistGeneration(input.operation, req.IdempotencyKey, requestHash, input)
}

// prepareGeneration performs the synchronous checks the async worker must not
// have to fail on later: ownership, version, executable draft, single active
// session, library scan, and frozen input construction.
func (s *serviceImpl) prepareGeneration(
	worksetID, operationType string,
) (*generationInput, error) {
	op, err := s.loadOperation(worksetID, operationType)
	if err != nil {
		return nil, err
	}
	if orphanErr := s.rejectOrphaned(worksetID); orphanErr != nil {
		return nil, orphanErr
	}
	draft, err := s.repo.GetOperationDraft(worksetID, operationType)
	if err != nil || draft == nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load draft", err)
	}
	task, err := s.requireTask(operationType)
	if err != nil {
		return nil, err
	}
	members, err := s.repo.ListWorksetMembers(worksetID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load members", err)
	}
	if len(members) == 0 {
		return nil, NewError(ErrKindInvalidArgument, "INVALID_FOLDER_COUNT", "workset has no album folders", nil)
	}
	// Executable validation runs synchronously so an incomplete draft is
	// rejected here instead of failing the queued session (ADR 0004 §3, C13).
	if execErr := task.ValidateSessionInput([]byte(draft.DraftJSON), members); execErr != nil {
		return nil, execErr
	}
	w, err := s.repo.GetWorkset(worksetID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	scanning, err := s.repo.HasActiveScanForRoot(w.RootPath)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check library scan", err)
	}
	if scanning {
		return nil, NewError(
			ErrKindConflict,
			"SCAN_IN_PROGRESS",
			"wait for the library scan to finish before generating",
			nil,
		)
	}
	// An active execution owns the operation's disk state; planning must wait
	// for it to reach a terminal status.
	activeExec, err := s.repo.GetActiveExecutionForOperation(worksetID, operationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active execution", err)
	}
	if activeExec != nil {
		return nil, NewError(
			ErrKindConflict,
			"EXECUTION_IN_PROGRESS",
			"wait for the active execution to finish before generating",
			nil,
		)
	}
	return &generationInput{
		operation: op,
		rawDraft:  []byte(draft.DraftJSON),
		members:   members,
		draftHash: draft.DraftHash,
		request: generationRequest{
			DraftHash:  draft.DraftHash,
			MemberHash: hashMembers(members),
			Roots:      len(members),
			Draft:      json.RawMessage(draft.DraftJSON),
		},
	}, nil
}

// rejectActiveSession enforces one queued/running session per operation
// (ADR 0004 §2, D05). It runs after the replay checks so an idempotent retry
// observes its own session instead of conflicting with it.
func (s *serviceImpl) rejectActiveSession(op *sqlite.Operation) (*StartGenerationResult, error) {
	active, err := s.repo.GetActiveGenerationForOperation(op.WorksetID, op.OperationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active generation", err)
	}
	if active != nil {
		return nil, NewError(
			ErrKindConflict,
			"GENERATION_IN_PROGRESS",
			"a generation is already queued or running for this operation",
			nil,
		)
	}
	return nil, nil
}

// replayCurrentRevision answers a new enqueue with the existing current
// revision when nothing semantic changed: same operation, same draft, same
// members, same live input facts (ADR 0004 §4, P03).
func (s *serviceImpl) replayCurrentRevision(
	ctx context.Context,
	input *generationInput,
) (*StartGenerationResult, bool, error) {
	op := input.operation
	if op.CurrentRevisionID == "" {
		return nil, false, nil
	}
	rev, err := s.repo.GetOperationRevision(op.WorksetID, op.OperationType, op.CurrentRevisionID)
	if err != nil || rev.DraftHash != input.draftHash || rev.MemberHash != input.request.MemberHash {
		return nil, false, nil
	}
	detail, detailErr := s.repo.GetPlanDetail(op.CurrentRevisionID)
	if detailErr != nil {
		return nil, false, nil
	}
	health, healthErr := s.revisionHealth(op.OperationType, detail)
	if healthErr != nil || health.InputMoved {
		return nil, false, nil
	}
	summary, _, err := s.loadCurrentRevision(op)
	if err != nil {
		return nil, false, nil
	}
	return &StartGenerationResult{Revision: summary, Created: false}, true, nil
}

// revisionHealth asks the operation's task for the business health of one
// frozen revision.
func (s *serviceImpl) revisionHealth(
	operationType string, detail *sqlite.PlanDetail,
) (RevisionHealth, error) {
	task, err := s.requireTask(operationType)
	if err != nil {
		return RevisionHealth{}, err
	}
	return task.EvaluateRevision(s.repo, RevisionFacts{Detail: detail})
}

// participatingMembers filters the member list to those taking part, resolved
// through the operation's task from the frozen draft.
func (s *serviceImpl) participatingMembers(input *generationInput) []*sqlite.WorksetMember {
	task, err := s.requireTask(input.operation.OperationType)
	if err != nil {
		return nil
	}
	facts, err := task.RevisionMembers(RevisionFacts{
		DraftSnapshot: input.rawDraft,
		Members:       input.members,
	})
	if err != nil {
		return nil
	}
	participating := map[string]bool{}
	for _, f := range facts {
		if !f.Excluded {
			participating[f.MemberID] = true
		}
	}
	out := make([]*sqlite.WorksetMember, 0, len(participating))
	for _, m := range input.members {
		if participating[m.MemberID] {
			out = append(out, m)
		}
	}
	return out
}

func (s *serviceImpl) replayGeneration(
	op *sqlite.Operation,
	key, requestHash string,
) (*StartGenerationResult, bool, error) {
	if key == "" {
		return nil, false, nil
	}
	existing, err := s.repo.GetGenerationByOperationKey(op.WorksetID, op.OperationType, key)
	if err != nil {
		return nil, false, NewError(ErrKindInternal, "INTERNAL", "failed to check generation idempotency", err)
	}
	if existing == nil ||
		(existing.Status != sqlite.GenStatusCompleted && existing.Status != sqlite.GenStatusQueued && existing.Status != sqlite.GenStatusRunning) {
		return nil, false, nil
	}
	if existing.RequestHash != requestHash {
		return nil, false, NewError(
			ErrKindConflict,
			"IDEMPOTENCY_KEY_REUSED",
			"idempotency key was used with a different request",
			nil,
		)
	}
	return &StartGenerationResult{Generation: toGenerationView(existing), Created: false}, true, nil
}

func (s *serviceImpl) persistGeneration(
	op *sqlite.Operation,
	key, requestHash string,
	input *generationInput,
) (*StartGenerationResult, error) {
	now := time.Now()
	gen := &sqlite.PlanGeneration{
		GenerationID:         "gen-" + newToken(),
		WorksetID:            op.WorksetID,
		OperationType:        op.OperationType,
		IdempotencyKey:       key,
		RequestHash:          requestHash,
		ExpectedDraftVersion: op.Version,
		RequestJSON:          mustJSON(input.request),
		TotalRoots:           len(s.participatingMembers(input)),
		CreatedAt:            now,
	}
	if err := s.repo.CreateGeneration(gen); err != nil {
		if errors.Is(err, sqlite.ErrGenerationIdemConflict) {
			existing, loadErr := s.repo.GetGenerationByOperationKey(op.WorksetID, op.OperationType, key)
			if loadErr == nil && existing != nil {
				if existing.RequestHash == requestHash {
					return &StartGenerationResult{Generation: toGenerationView(existing), Created: false}, nil
				}
				return nil, NewError(
					ErrKindConflict,
					"IDEMPOTENCY_KEY_REUSED",
					"idempotency key was used with a different request",
					nil,
				)
			}
			return nil, NewError(ErrKindConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key conflict", err)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to start generation", err)
	}
	s.dispatcher.wake()
	return &StartGenerationResult{Generation: toGenerationView(gen), Created: true}, nil
}

// loadGeneration loads a session and checks its operation ownership: a session
// of another workset or another operation of the same workset is not found.
func (s *serviceImpl) loadGeneration(
	worksetID, operationType, generationID string,
) (*sqlite.PlanGeneration, error) {
	g, err := s.repo.GetGeneration(generationID)
	if err != nil {
		if errors.Is(err, sqlite.ErrGenerationNotFound) {
			return nil, NewError(ErrKindNotFound, "GENERATION_NOT_FOUND", "generation not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load generation", err)
	}
	if g.WorksetID != worksetID || g.OperationType != operationType {
		return nil, NewError(ErrKindNotFound, "GENERATION_NOT_FOUND", "generation not found", nil)
	}
	return g, nil
}

// GetGeneration returns the session view scoped to a workset operation.
func (s *serviceImpl) GetGeneration(
	ctx context.Context,
	worksetID, operationType, generationID string,
) (*GenerationView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, err := s.loadGeneration(worksetID, operationType, generationID)
	if err != nil {
		return nil, err
	}
	return toGenerationView(g), nil
}

// CancelGeneration cancels a session. It is idempotent: terminal rows keep
// their status. Orphaned worksets may still cancel a leftover generation for
// cleanup.
func (s *serviceImpl) CancelGeneration(
	ctx context.Context,
	worksetID, operationType, generationID string,
) (*GenerationView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, err := s.loadGeneration(worksetID, operationType, generationID)
	if err != nil {
		return nil, err
	}
	if g.Status == sqlite.GenStatusRunning || g.Status == sqlite.GenStatusQueued {
		if err := s.repo.CancelGeneration(generationID); err != nil {
			return nil, NewError(ErrKindInternal, "INTERNAL", "failed to cancel generation", err)
		}
		if g.Status == sqlite.GenStatusQueued {
			s.dispatcher.wake()
		}
	}
	return s.GetGeneration(ctx, worksetID, operationType, generationID)
}

// hashJSON hashes a request-scope string pair.
func hashJSON(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// parseGenerationRequest decodes the frozen session payload.
func parseGenerationRequest(raw string) (*generationRequest, error) {
	var req generationRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return nil, err
	}
	if req.Draft == nil {
		return nil, errors.New("generation request carries no frozen draft")
	}
	return &req, nil
}

// runGeneration executes the frozen session input through the operation's
// task. It is the single place the generation worker turns a session row into
// a revision snapshot.
func (s *serviceImpl) runGeneration(
	ctx context.Context,
	operationType string,
	req *generationRequest,
	members []*sqlite.WorksetMember,
	progress func(PlanProgress),
) (*PlanSnapshot, error) {
	task, err := s.requireTask(operationType)
	if err != nil {
		return nil, err
	}
	return task.PlanSession(ctx, s.repo, PlanSessionInput{
		RawDraft: req.Draft,
		Members:  members,
		Progress: progress,
	})
}

func hashMembers(members []*sqlite.WorksetMember) string {
	h := sha256.New()
	for _, m := range members {
		h.Write([]byte(m.RelPath))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
