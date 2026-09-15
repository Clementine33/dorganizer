package workset

import (
	"context"
	"errors"
	"time"

	appconfig "github.com/onsei/organizer/backend/internal/config"
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
	"github.com/onsei/organizer/backend/internal/services/reconcile"
)

// seedDraft builds the initial sparse draft of a new conversion operation:
// no member records at all (everyone participates and inherits the common
// settings), mode available_sources stored explicitly, tag literals copied
// from config.json's prune.literal_tags, and wav + mp3@320 outputs so a new
// workset is immediately usable.
func (s *serviceImpl) seedDraft(worksetID, operationType string, now time.Time) sqlite.OperationDraft {
	doc := &DraftDoc{
		SchemaVersion:  DraftSchemaVersion,
		Mode:           reconcile.ModeAvailableSources,
		ClassifierTags: appconfig.LoadPruneLiteralTags(s.configDir),
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
	return sqlite.OperationDraft{
		WorksetID:     worksetID,
		OperationType: operationType,
		SchemaVersion: DraftSchemaVersion,
		DraftJSON:     raw,
		DraftHash:     hash,
		UpdatedAt:     now,
	}
}

func defaultProfile() reconcile.DesiredProfile {
	return reconcile.DesiredProfile{
		Lossless: &reconcile.AudioOutputSpec{Codec: reconcile.CodecWav},
		Encoded: &reconcile.AudioOutputSpec{
			Codec:   reconcile.CodecMp3,
			Quality: &reconcile.Quality{Kind: reconcile.QualityBitrate, Bitrate: 320},
		},
	}
}

// GetDraft returns the operation's sparse draft document with the operation
// version as its concurrency token.
func (s *serviceImpl) GetDraft(ctx context.Context, worksetID, operationType string) (*Draft, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	op, err := s.loadOperation(worksetID, operationType)
	if err != nil {
		return nil, err
	}
	d, err := s.repo.GetOperationDraft(worksetID, operationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load draft", err)
	}
	if d == nil {
		return nil, NewError(ErrKindNotFound, "DRAFT_NOT_FOUND", "operation has no draft", nil)
	}
	doc, err := ParseDraft(d.DraftJSON)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "stored draft is invalid", err)
	}
	return &Draft{
		WorksetID:     worksetID,
		OperationType: operationType,
		Version:       op.Version,
		SchemaVersion: d.SchemaVersion,
		Document:      doc,
		UpdatedAt:     d.UpdatedAt,
	}, nil
}

// SaveDraft replaces the operation's full sparse draft document under the
// operation version guard. The document is normalized to its sparse canonical
// form before hashing and storage: unmodified units are never materialized
// into member overrides. Structurally valid but business-incomplete drafts are
// accepted (an editing state); generation is where completeness is required.
func (s *serviceImpl) SaveDraft(
	ctx context.Context,
	worksetID, operationType string,
	req SaveDraftRequest,
) (*OperationView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Document == nil {
		return nil, NewError(ErrKindInvalidArgument, "INVALID_DRAFT", "draft document is required", nil)
	}
	if _, err := s.loadOperation(worksetID, operationType); err != nil {
		return nil, err
	}
	if err := s.rejectOrphaned(worksetID); err != nil {
		return nil, err
	}
	members, err := s.repo.ListWorksetMembers(worksetID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load members", err)
	}
	if validateErr := validateDraftDoc(req.Document, members); validateErr != nil {
		return nil, validateErr
	}
	// Reject while a generation is queued/running: the session freezes the
	// draft at enqueue time and must not race a replace (ADR 0004 §2, D05).
	active, err := s.repo.GetActiveGenerationForOperation(worksetID, operationType)
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
	// An active execution runs the confirmation of the revision this draft
	// belongs to; editing the draft under it would move the operation version
	// and desynchronize the session's frozen input.
	activeExec, err := s.repo.GetActiveExecutionForOperation(worksetID, operationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to check active execution", err)
	}
	if activeExec != nil {
		return nil, NewError(
			ErrKindConflict,
			"EXECUTION_IN_PROGRESS",
			"cancel or wait for the active execution before editing the draft",
			nil,
		)
	}
	doc := normalizeDraft(req.Document, members)
	raw, hash, err := MarshalDraft(doc)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to encode draft", err)
	}
	if err := s.repo.SaveOperationDraft(
		worksetID,
		operationType,
		DraftSchemaVersion,
		raw,
		hash,
		req.IfMatchVersion,
		time.Now(),
	); err != nil {
		if errors.Is(err, sqlite.ErrVersionConflict) {
			return nil, NewError(ErrKindConflict, "VERSION_CONFLICT", "operation version conflict", nil)
		}
		if errors.Is(err, sqlite.ErrOperationNotFound) {
			return nil, NewError(ErrKindNotFound, "OPERATION_NOT_FOUND", "operation not found", nil)
		}
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to save draft", err)
	}
	return s.GetOperation(ctx, worksetID, operationType)
}
