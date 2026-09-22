package workset

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onsei/organizer/backend/internal/adapters/sqlite"
)

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
	doc, err := s.repo.GetOperationDraft(worksetID, operationType)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load draft", err)
	}
	if doc == nil {
		return nil, NewError(ErrKindNotFound, "DRAFT_NOT_FOUND", "operation has no draft", nil)
	}
	return &Draft{
		WorksetID:     worksetID,
		OperationType: operationType,
		Version:       op.Version,
		SchemaVersion: doc.SchemaVersion,
		Document:      json.RawMessage(doc.DraftJSON),
		UpdatedAt:     doc.UpdatedAt,
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
	docBytes := []byte(req.Document)
	if len(docBytes) == 0 {
		return nil, NewError(ErrKindInvalidArgument, "INVALID_DRAFT", "draft document is required", nil)
	}
	task, err := s.requireTask(operationType)
	if err != nil {
		return nil, err
	}
	if _, err = s.loadOperation(worksetID, operationType); err != nil {
		return nil, err
	}
	if err = s.rejectOrphaned(worksetID); err != nil {
		return nil, err
	}
	members, err := s.repo.ListWorksetMembers(worksetID)
	if err != nil {
		return nil, NewError(ErrKindInternal, "INTERNAL", "failed to load members", err)
	}
	if validateErr := task.ValidateDraft(docBytes, members); validateErr != nil {
		return nil, validateErr
	}
	// Reject while a generation is queued/running: the session freezes the
	// draft at enqueue time and must not race a replace (ADR 0001 §2).
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
	// An active execution runs the revision this draft
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
	canonical, hash, schemaVersion, normErr := task.NormalizeDraft(docBytes, members)
	if normErr != nil {
		return nil, normErr
	}
	if err := s.repo.SaveOperationDraft(
		worksetID,
		operationType,
		schemaVersion,
		string(canonical),
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
