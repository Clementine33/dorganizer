package workset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

type generationInput struct {
	workset    *sqlite.Workset
	members    []*sqlite.WorksetMember
	draftHash  string
	memberHash string
}

func (s *serviceImpl) StartGeneration(ctx context.Context, id string, req StartGenerationRequest) (*StartGenerationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.ExpectedDraftVersion <= 0 && req.IfMatchVersion > 0 {
		req.ExpectedDraftVersion = req.IfMatchVersion
	}
	if err := validateIdemKey(req.IdempotencyKey); err != nil {
		return nil, err
	}
	input, err := s.prepareGeneration(id)
	if err != nil {
		return nil, err
	}
	if result, replayed, err := s.replayCurrentRevision(ctx, input); err != nil || replayed {
		return result, err
	}
	if req.ExpectedDraftVersion > 0 && req.ExpectedDraftVersion != input.workset.Version {
		return nil, NewError(ErrKindConflict, "DRAFT_VERSION_CONFLICT", "workset changed since the draft was read", nil)
	}
	requestHash := hashJSON([]byte(input.draftHash + "|" + input.memberHash))
	if result, replayed, err := s.replayGeneration(id, req.IdempotencyKey, requestHash); err != nil || replayed {
		return result, err
	}
	return s.persistGeneration(id, req.IdempotencyKey, requestHash, input)
}

func (s *serviceImpl) prepareGeneration(id string) (*generationInput, error) {
	w, err := s.repo.GetWorkset(id)
	if err != nil {
		if err == sqlite.ErrWorksetNotFound {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	if w.LibraryID == "" {
		return nil, NewError(ErrKindConflict, "ORPHANED_WORKSET", "orphaned worksets are read-only", nil)
	}
	draft, err := s.repo.GetWorksetDraft(id)
	if err != nil || draft == nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load draft", err)
	}
	wf, err := parseWorkflowJSON(draft.StepsJSON)
	if err != nil {
		return nil, NewError(ErrKindInvalidArgument, "INVALID_WORKFLOW", "stored draft is invalid", err)
	}
	if err := validateExecutableWorkflow(wf); err != nil {
		return nil, err
	}
	active, err := s.repo.GetActiveGenerationForWorkset(id)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active generation", err)
	}
	if active != nil {
		return nil, NewError(ErrKindConflict, "GENERATION_IN_PROGRESS", "a generation is already queued or running for this workset", nil)
	}
	scanning, err := s.repo.HasActiveScanForRoot(w.RootPath)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check library scan", err)
	}
	if scanning {
		return nil, NewError(ErrKindConflict, "SCAN_IN_PROGRESS", "wait for the library scan to finish before generating", nil)
	}
	members, err := s.repo.ListWorksetMembers(id)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load members", err)
	}
	if len(members) == 0 {
		return nil, NewError(ErrKindInvalidArgument, "INVALID_FOLDER_COUNT", "workset has no album folders", nil)
	}
	return &generationInput{
		workset: w, members: members,
		draftHash: draft.DraftHash, memberHash: hashMembers(members),
	}, nil
}

func (s *serviceImpl) replayCurrentRevision(ctx context.Context, input *generationInput) (*StartGenerationResult, bool, error) {
	w := input.workset
	if w.CurrentRevisionID == "" {
		return nil, false, nil
	}
	fingerprints, err := s.rootFingerprints(ctx, input.members)
	if err != nil {
		return nil, false, err
	}
	rev, err := s.repo.GetWorksetRevision(w.ID, w.CurrentRevisionID)
	if err != nil || rev.DraftHash != input.draftHash || rev.MemberHash != input.memberHash || !rootsMatch(w, fingerprints, s.repo) {
		return nil, false, nil
	}
	summary, _, err := s.loadCurrentRevision(w)
	if err != nil {
		return nil, false, nil
	}
	return &StartGenerationResult{Revision: summary, Created: false}, true, nil
}

func (s *serviceImpl) replayGeneration(worksetID, key, requestHash string) (*StartGenerationResult, bool, error) {
	if key == "" {
		return nil, false, nil
	}
	existing, err := s.repo.GetGenerationByWorksetKey(worksetID, key)
	if err != nil {
		return nil, false, NewError(ErrKindInternal, "INTERNAL", "failed to check generation idempotency", err)
	}
	if existing == nil || (existing.Status != sqlite.GenStatusCompleted && existing.Status != sqlite.GenStatusQueued && existing.Status != sqlite.GenStatusRunning) {
		return nil, false, nil
	}
	if existing.RequestHash != requestHash {
		return nil, false, NewError(ErrKindConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used with a different request", nil)
	}
	return &StartGenerationResult{Generation: toGenerationView(existing), Created: false}, true, nil
}

func (s *serviceImpl) persistGeneration(worksetID, key, requestHash string, input *generationInput) (*StartGenerationResult, error) {
	now := time.Now()
	gen := &sqlite.PlanGeneration{
		GenerationID:         "gen-" + newToken(),
		WorksetID:            worksetID,
		IdempotencyKey:       key,
		RequestHash:          requestHash,
		ExpectedDraftVersion: input.workset.Version,
		RequestJSON:          mustJSON(map[string]any{"draft_hash": input.draftHash, "member_hash": input.memberHash, "roots": len(input.members)}),
		TotalRoots:           len(input.members),
		CreatedAt:            now,
	}
	if err := s.repo.CreateGeneration(gen); err != nil {
		if err == sqlite.ErrGenerationIdemConflict {
			existing, loadErr := s.repo.GetGenerationByWorksetKey(worksetID, key)
			if loadErr == nil && existing != nil {
				if existing.RequestHash == requestHash {
					return &StartGenerationResult{Generation: toGenerationView(existing), Created: false}, nil
				}
				return nil, NewError(ErrKindConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used with a different request", nil)
			}
			return nil, NewError(ErrKindConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key conflict", err)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to start generation", err)
	}
	s.dispatcher.wake()
	return &StartGenerationResult{Generation: toGenerationView(gen), Created: true}, nil
}

func (s *serviceImpl) loadGeneration(worksetID, generationID string) (*sqlite.PlanGeneration, error) {
	g, err := s.repo.GetGeneration(generationID)
	if err != nil {
		if err == sqlite.ErrGenerationNotFound {
			return nil, NewError(ErrKindNotFound, "GENERATION_NOT_FOUND", "generation not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load generation", err)
	}
	if g.WorksetID != worksetID {
		return nil, NewError(ErrKindNotFound, "GENERATION_NOT_FOUND", "generation not found", nil)
	}
	return g, nil
}

// GetGeneration returns the session view scoped to a workset.
func (s *serviceImpl) GetGeneration(ctx context.Context, worksetID, generationID string) (*GenerationView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, err := s.loadGeneration(worksetID, generationID)
	if err != nil {
		return nil, err
	}
	return toGenerationView(g), nil
}

// CancelGeneration cancels a session. It is idempotent: terminal rows keep
// their status. Orphaned worksets may still cancel a leftover generation for
// cleanup.
func (s *serviceImpl) CancelGeneration(ctx context.Context, worksetID, generationID string) (*GenerationView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, err := s.loadGeneration(worksetID, generationID)
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
	return s.GetGeneration(ctx, worksetID, generationID)
}

// rootFingerprints recomputes the LIVE per-root inventory fingerprints for the
// given member folder paths (same entry collection and fingerprint function as
// the workflow runner). This is the dedup/stale authority: after a scan, the
// values reflect the current entries table.
func (s *serviceImpl) rootFingerprints(ctx context.Context, members []*sqlite.WorksetMember) (map[string]reconcile.ReconcileResult, error) {
	out := make(map[string]reconcile.ReconcileResult, len(members))
	for _, m := range members {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, err := collectWorkflowEntries(s.repo, m.FolderPath)
		if err != nil {
			return nil, NewError(ErrKindInternal, "INTERNAL", fmt.Sprintf("failed to fingerprint %s: %v", m.FolderPath, err), err)
		}
		audio := reconcile.AudioEntries(entries)
		digest, count := reconcile.InventoryFingerprint(audio)
		out[m.FolderPath] = reconcile.ReconcileResult{Digest: digest, Count: count}
	}
	return out, nil
}

// rootsMatch compares the live fingerprints against the current revision's
// persisted fingerprints by root path.
func rootsMatch(w *sqlite.Workset, current map[string]reconcile.ReconcileResult, repo *sqlite.Repository) bool {
	if w.CurrentRevisionID == "" {
		return false
	}
	persisted, err := repo.GetWorkflowPlanRoots(w.CurrentRevisionID)
	if err != nil {
		return false
	}
	if len(persisted) != len(current) {
		return false
	}
	for _, r := range persisted {
		live, ok := current[r.RootPath]
		if !ok {
			return false
		}
		if live.Digest != r.InventoryFingerprint || live.Count != r.EntryCount {
			return false
		}
	}
	return true
}

func hashMembers(members []*sqlite.WorksetMember) string {
	h := sha256.New()
	for _, m := range members {
		h.Write([]byte(m.RelPath))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
