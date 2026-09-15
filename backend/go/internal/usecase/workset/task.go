package workset

import (
	"github.com/onsei/organizer/backend/internal/repo/sqlite"
)

// Task is one kind of work a workset operation can carry. The workset module
// owns the operation lifecycle — identity, ownership, versioning, drafts,
// revisions, confirmations, planning and execution sessions — and a Task
// supplies the business content behind it.
//
// Contract (binding on every Task implementation):
//
//  1. Admission. The generic side keeps ownership, version, transaction,
//     session and idempotency constraints; the Task supplies the business
//     block reasons behind "cannot confirm" and "cannot execute" (e.g. input
//     facts that moved since planning).
//  2. Payload schema versions. A payload schema version the Task does not
//     know is rejected at every entry point (read, draft write, confirm,
//     execute) with a stable error code — never half-read.
//  3. Options. Session options are normalized and frozen by the Task when the
//     session is created, and take part in the idempotency request hash.
//  4. Progress and cancellation. A Task persists partial results at its own
//     unit boundaries, observes cooperative cancellation, and never moves a
//     terminal session status backwards.
//
// The catalog part (kind, seed draft, draft validation and normalization) is
// wired here; the planner, input-facts and executor parts join the same seam
// as their call sites move behind it.
type Task interface {
	// Kind is the stable operation-type identifier: the {type} path segment
	// and the value stored on operations and revisions.
	Kind() string

	// SeedDraft returns the raw draft document a newly created operation
	// starts from, its canonical content hash, and the document schema
	// version.
	SeedDraft() (raw []byte, hash string, schemaVersion int)

	// ValidateDraft checks one submitted draft document against the workset's
	// members. Only malformed documents are rejected here; business
	// completeness is enforced at planning time.
	ValidateDraft(raw []byte, members []*sqlite.WorksetMember) error

	// NormalizeDraft returns the canonical sparse form of a validated draft
	// document, its content hash, and the document schema version.
	NormalizeDraft(
		raw []byte,
		members []*sqlite.WorksetMember,
	) (canonical []byte, hash string, schemaVersion int, err error)
}
