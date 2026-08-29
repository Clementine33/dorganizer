# 0002: Workset aggregates own immutable Plan Revisions

## Status

Accepted (2026-08-30).

## Context

The desktop workbench moves the primary Feed identity from one large
immutable Plan snapshot to a long-lived **Workset** (工作集): a set of album
folders a user keeps working on across scans, regenerations, and (future)
executions. The web UI needs server persistence for:

- the Workset aggregate itself (title, album-folder membership, ownership);
- a mutable server-persisted Workflow Draft;
- asynchronous Planning Sessions with progress and cancellation;
- immutable Plan Revisions under each Workset;
- authoritative stale validation and orphan semantics.

The existing `plans` / `plan_workflow_steps` / `plan_roots` /
`plan_components` tables already persist immutable workflow snapshots and are
reused as Revision payloads. Worksets create workflow plans only;
`single_action` remains the independent standalone path. The gRPC/Flutter
client line is abandoned and receives no new compatibility work.

## Decision

### Aggregate boundaries

- **Workset**: long-lived aggregate owned by one Library while that Library
  exists. Duplicate-permitted title; fixed ordered set of 1–500 Album Folder
  members; one mutable Workflow Draft; revision history; one aggregate
  concurrency `version`.
- **Member identity**: normalized library-relative path. Folder ID, display
  name, absolute path, and library root are persisted as snapshots only;
  transient `library_folders.id` is never durable identity.
- **Workflow Draft**: server-persisted full replacement; new Worksets start
  with schema v1 and the immutable `balanced@1` preset. Policy source remains
  the tagged union (preset reference or complete inline policy), overrides
  never merged.
- **Plan Revision**: immutable workflow snapshot backed by the existing plan
  tables. `workset_revisions` is the sole Workset↔Plan association and
  revision ordering source; no separate `plans.workset_id` authority.
- **Planning Session**: persisted async generation that freezes the canonical
  Draft and ordered member input at enqueue time. One queued/running session
  per Workset; global FIFO queue; configurable worker concurrency (default 2).

### Concurrency

Only `worksets.version` is a mutation authority. Rename, Draft replacement,
and successful Revision promotion each increment it; mutations use `If-Match`.
Generation freezes the Draft/member snapshots after validating the version,
but rename remains allowed while it runs. Planning dirtiness is derived by
comparing the current Draft's canonical hash with the current Revision's
frozen Draft hash, never by comparing counters.

### Orthogonal states

Three independent axes are returned, not one overloaded status:

1. Workset `planning_state`: `orphaned | planning | unplanned | planned |
   needs_planning` (derived; hash-compare for planned/needs_planning).
2. Revision `validation_state`: `valid | stale | unavailable` (derived on
   authoritative detail/revision reads only; `unavailable` = orphaned).
3. Immutable Revision `summary_reason`: `ACTIONABLE | PARTIAL | BLOCKED |
   NO_MATCH`.

A failed, canceled, or restart-interrupted Planning Session never clears or
mutates the current Revision; it only appears as `latest_generation` and the
planning state falls back to the derivation above.

### Generation semantics

- `POST revisions` validates Workset state and version, freezes the Draft,
  and computes current per-root inventory fingerprints. Same Draft hash +
  member identity/order + unchanged fingerprints → returns the current
  Revision with HTTP 200 `created:false`; no enqueue, no version bump.
- Otherwise a queued session is inserted (HTTP 202). A singleton dispatcher
  claims sessions in `julianday(created_at), session_id` order with a
  conditional update; a zero-row claim means a queued cancel won and is
  skipped. Canceled-queued sessions never run.
- Queued cancel writes `canceled` synchronously; running cancel sets a
  cooperative flag the worker observes at root boundaries.
- Startup marks leftover queued/running sessions `interrupted`, releasing
  their idempotency keys; the dispatcher starts from an empty queue.
- Completion is one SQLite transaction: plan snapshot inserts, revision
  association (index = MAX+1), current-revision promotion with version bump,
  session `completed`. No partial Revision is ever visible.
- System failures expose stable codes/safe messages; internal causes are
  logged only. Failed/canceled/interrupted sessions never consume their
  idempotency key; completed sessions retain theirs for 30 days.

### Stale validation

Authoritative detail/revision reads recompute each persisted root's inventory
fingerprint/count with the same normalized entry collection and fingerprint
function used at planning time, and derive `validation_state`. Feed rows carry
persisted summaries only — no unbounded N×root scan across a global Feed page.
Orphaned Worksets return `validation_state=unavailable` and `stale: null`.

### Orphan semantics and Library lifecycle

- Library deletion is a guarded transaction: any owned Workset with a
  queued/running session → HTTP 409 `GENERATION_IN_PROGRESS` (cancel first);
  otherwise set `library_id=NULL` while retaining member/Draft/Revision
  snapshots, then delete the Library (folders cascade).
- Orphaned Worksets remain reviewable (list/detail/revisions/generation
  detail/events/cancel allowed) but read-only: rename, Draft save, and
  generation start → 409 `ORPHANED_WORKSET`.
- A Library root-path change is rejected (409 `LIBRARY_HAS_WORKSETS`) while
  any Workset is linked; deletion-then-recreation is the supported way to move
  roots. Name edits remain allowed.

### Idempotency and retention

- `Idempotency-Key` header is required for Workset creation and Revision
  generation; same key + same request replays, different request → 409
  `IDEMPOTENCY_KEY_REUSED`. Creation replay is guaranteed 30 days (expired
  keys are cleared, never the row); completed generation keys 30 days;
  failed/canceled/interrupted keys are reusable immediately.
- Plan retention deletes only standalone plans; all Workset Revisions
  (including orphaned) are exempt. Terminal Planning Session rows are purged
  at 30 days without touching their Revisions.

### Error/route surface

`/api/v1/worksets` routes (create/list/detail/rename, draft GET/PUT,
revisions POST/GET/GET{planId}, planning-sessions GET/events/cancel) follow
the existing strict-JSON, bearer-auth, `{"code","message"}` conventions.
Workset revisions are excluded from the generic `/api/v1/plans` list; nested
endpoints are the discovery path. `/api/v1/plans` create/detail behavior is
unchanged.

## Consequences

- The Vue workbench can render the Workset Feed, Draft editor, generation
  progress, and immutable Revision review without inventing server state.
- `plan.RunWorkflow` (extracted in the same change) is the single
  reconciliation implementation shared by `/plans` and the Workset worker;
  `/plans` request/response behavior is byte-identical.
- Missing member folders are persisted as root `missing` outcomes with
  `SOURCE_MISSING` (never a fabricated Component); other roots still plan, so
  a missing member yields a partial Revision.
- Workflow Execute, member-set editing, Workset archive/delete, future
  task-slot persistence, and standalone-Plan migration are explicit
  non-goals of this phase.
