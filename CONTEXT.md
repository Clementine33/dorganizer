# Domain Glossary

## Workset

A long-lived aggregate referencing a single Library: the first-level Feed
identity of the workbench, holding an ordered set of Album Folder members
(1–500), one mutable Workflow Draft, planning history, and a single
concurrency `version`. Deleting the owning Library orphans the Workset:
membership, Draft, and Revisions remain reviewable but the Workset becomes
read-only and no longer plans. See `docs/adr/0002-workset-plan-revision.md`.

## Workflow Draft

The server-persisted mutable configuration of a Workset: a schema-v1 linear
workflow whose only persisted step is `reconcile_audio_outputs`, backed by a
preset reference or a complete inline policy. Draft replacement is
full-replacement and guarded by the Workset version.

## Plan Revision

An immutable workflow snapshot generated from a frozen Draft + ordered member
set, owned by exactly one Workset. Backed by the existing `plans`/
`plan_workflow_steps`/`plan_roots`/`plan_components` rows; `workset_revisions`
is the single source of revision association and ordering. Revision
`validation_state` (`valid | stale | unavailable`) is derived on read from
stored per-root inventory fingerprints; `summary_reason` is immutable.

## Planning Session

An asynchronous, persisted generation request: it freezes the Draft and
member inputs at enqueue time, runs on a global FIFO dispatcher (configurable
concurrency, default 2), and streams root-level progress over SSE. Only a
successful completion promotes the Workset's current Revision; failure,
cancellation, and restart interruption leave the current Revision untouched.

## Plan

A reviewable proposal for reconciling the currently observed media inventory with a chosen Plan Policy. A Plan records decisions before any filesystem changes are executed.

## Plan Policy

A versioned declaration of the media variants a user wants to retain and the conditions under which missing or inadequate variants should be generated. A policy describes desired outcomes, not an ordered list of filesystem operations.

## Media Facts

Observed properties of a media file, such as codec, container, bitrate state, and whether it matches the configured content filter. Media Facts do not contain user intent.

## Filter Match

Whether a file matches the configured content-classification rule.

- **matched**: displayed to users as “无音效”.
- **unmatched**: displayed to users as “有音效”.

The canonical terms are `matched` and `unmatched`: the system observes rule matching and does not independently prove whether audio effects are present.

## Source Quality State

A source file’s quality relative to a policy’s target encode specification. Canonical states include lossless, target-or-above, below-target, unknown-bitrate, and other-lossy.

## Target Encode Specification

The codec and quality target for a generated encoded variant, such as MP3 at 320 kbps. Whether an existing file is adequate is evaluated relative to this specification.

## Desired State

The set of media variants that a Plan Policy says should remain for a content class. It includes retention, generation, and cleanup intent.

## Generation Policy

The condition under which a target encoded variant should be generated:

- **never**: do not generate the target variant.
- **if missing**: generate only when no adequate target variant exists.
- **if missing or below**: generate when the target is absent or an existing variant is below the target specification.
- **always**: regenerate from a qualified source even when an adequate target variant already exists.

“Force converted” means the `always` policy. “Below-target auto conversion” means `if missing or below`.

## Qualified Source

A source from which the requested target can be generated without pretending to improve already-lossy media. Lossless media is a qualified source for generating MP3 320 kbps; a lower-bitrate MP3 is not.

When no qualified source exists, the default outcome is an error with no generated operation, while existing media remains untouched.

## Unknown Bitrate

A state in which bitrate probing cannot establish an MP3 file’s quality. The default outcome is an error rather than treating the file as below-target or replacing it automatically.

## Variant Group

The set of files considered alternative representations of the same logical track. Desired-state reconciliation is performed for a Variant Group rather than independently for each file.

## Decision

A reviewable conclusion about a file or desired variant, such as keep, delete, or encode, together with a stable reason. Decisions explain the Plan and may include outcomes that require no execution.

## Operation

An executable filesystem change derived from one or more Decisions. Keeping a file is a Decision, not an Operation.

## Reconciliation

The process of comparing a Variant Group’s current inventory with its Desired State, producing Decisions, and lowering only the required changes into executable Operations.
