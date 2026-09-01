package workset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	appconfig "github.com/onsei/organizer/backend/internal/config"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
	planusecase "github.com/onsei/organizer/backend/internal/usecase/plan"
)

// seedDraft builds the initial editable draft: one reconcile step with a
// complete inline policy snapshot. Tag literals are copied from config.json's
// prune.literal_tags (empty when unconfigured); output profiles default to
// wav + mp3@320 so a new workset is immediately usable. The snapshot is
// self-contained: later config or slot changes never alter this draft.
func (s *serviceImpl) seedDraft(worksetID string, now time.Time) sqlite.WorksetDraft {
	policy := reconcile.Policy{
		SchemaVersion:  1,
		ClassifierTags: appconfig.LoadPruneLiteralTags(s.configDir),
		Matched: reconcile.DesiredProfile{
			Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
			Encoded: &reconcile.AudioOutputSpec{
				Codec:   reconcile.CodecMp3,
				Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320},
			},
		},
		Unmatched: reconcile.DesiredProfile{
			Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
			Encoded: &reconcile.AudioOutputSpec{
				Codec:   reconcile.CodecMp3,
				Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320},
			},
		},
	}
	wf := planusecase.Workflow{
		SchemaVersion: planusecase.WorkflowSchemaVersion,
		Steps: []planusecase.WorkflowStep{{
			StepType: planusecase.StepTypeReconcileAudio,
			Policy:   planusecase.PolicySource{Kind: "inline", InlinePolicy: &policy},
		}},
	}
	wfJSON := mustJSON(wf)
	return sqlite.WorksetDraft{
		WorksetID:             worksetID,
		WorkflowSchemaVersion: 1,
		StepsJSON:             wfJSON,
		DraftHash:             hashJSON([]byte(wfJSON)),
		UpdatedAt:             now,
	}
}

func (s *serviceImpl) GetDraft(ctx context.Context, id string) (*Draft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := s.repo.GetWorkset(id)
	if err != nil {
		if errors.Is(err, sqlite.ErrWorksetNotFound) {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	d, err := s.repo.GetWorksetDraft(id)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load draft", err)
	}
	if d == nil {
		return nil, NewError(ErrKindNotFound, "DRAFT_NOT_FOUND", "workset has no draft", nil)
	}
	wf, err := parseWorkflowJSON(d.StepsJSON)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to parse stored draft", err)
	}
	return &Draft{
		WorksetID:             d.WorksetID,
		Version:               w.Version,
		WorkflowSchemaVersion: d.WorkflowSchemaVersion,
		Workflow:              wf,
		WorkflowJSON:          d.StepsJSON,
		DraftHash:             d.DraftHash,
		UpdatedAt:             d.UpdatedAt,
	}, nil
}

func (s *serviceImpl) SaveDraft(ctx context.Context, id string, req SaveDraftRequest) (*WorksetView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := s.repo.GetWorkset(id)
	if err != nil {
		if errors.Is(err, sqlite.ErrWorksetNotFound) {
			return nil, NewError(ErrKindNotFound, "WORKSET_NOT_FOUND", "workset not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load workset", err)
	}
	if w.LibraryID == "" {
		return nil, NewError(ErrKindConflict, "ORPHANED_WORKSET", "orphaned worksets are read-only", nil)
	}
	if validateErr := validateWorkflow(req.Workflow); validateErr != nil {
		return nil, validateErr
	}
	// Reject while a generation is queued/running: the session freezes the
	// draft at enqueue time and must not race a replace.
	active, err := s.repo.GetActiveGenerationForWorkset(id)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active generation", err)
	}
	if active != nil {
		return nil, NewError(
			ErrKindConflict,
			"GENERATION_IN_PROGRESS",
			"cancel or wait for the active generation before editing the draft",
			nil,
		)
	}
	stepsJSON := mustJSON(req.Workflow)
	draftHash := hashJSON([]byte(stepsJSON))
	if err := s.repo.UpdateWorksetDraft(
		id,
		req.Workflow.SchemaVersion,
		stepsJSON,
		draftHash,
		req.IfMatchVersion,
		time.Now(),
	); err != nil {
		if errors.Is(err, sqlite.ErrVersionConflict) {
			return nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "workset version conflict", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to save draft", err)
	}
	return s.GetWorkset(ctx, id)
}

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

// parseWorkflowJSON decodes strict workflow JSON.
func parseWorkflowJSON(s string) (planusecase.Workflow, error) {
	var wf planusecase.Workflow
	if err := json.Unmarshal([]byte(s), &wf); err != nil {
		return wf, err
	}
	return wf, nil
}

// validateWorkflow accepts only schema v1 with exactly the supported step and
// an inline policy snapshot. This is the *structural* check shared by draft
// save: an incomplete policy (empty tags, empty profiles) is a normal,
// saveable editing state — the full reconcile.ValidatePolicy authority is
// applied only when the draft must run.
func validateWorkflow(wf planusecase.Workflow) error {
	if wf.SchemaVersion != planusecase.WorkflowSchemaVersion {
		return NewError(
			ErrKindInvalidArgument,
			"INVALID_WORKFLOW_SCHEMA",
			fmt.Sprintf("unsupported workflow schema version %d", wf.SchemaVersion),
			nil,
		)
	}
	if len(wf.Steps) != 1 || wf.Steps[0].StepType != planusecase.StepTypeReconcileAudio {
		return NewError(
			ErrKindInvalidArgument,
			"UNSUPPORTED_STEP",
			"schema v1 supports only the reconcile_audio_outputs step",
			nil,
		)
	}
	policy := wf.Steps[0].Policy
	if policy.Kind != "inline" || policy.InlinePolicy == nil {
		return NewError(
			ErrKindInvalidArgument,
			"INVALID_POLICY_SOURCE",
			"workflow policy must be a complete inline policy",
			nil,
		)
	}
	return nil
}

// validateExecutableWorkflow runs the structural check plus the full policy
// validation for the generation boundary: a draft that cannot produce a plan
// is rejected synchronously instead of failing the async worker.
func validateExecutableWorkflow(wf planusecase.Workflow) error {
	if err := validateWorkflow(wf); err != nil {
		return err
	}
	if err := reconcile.ValidatePolicy(*wf.Steps[0].Policy.InlinePolicy); err != nil {
		return NewError(ErrKindInvalidArgument, "INVALID_POLICY", err.Error(), nil)
	}
	return nil
}
