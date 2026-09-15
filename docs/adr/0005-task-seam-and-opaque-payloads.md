# 5. Task seam and opaque payloads

Date: 2026-09-16

## Status

Accepted.

## Context

The workset module was built around conversion: its draft document, its planner
and its executor lived inside `usecase/workset`, the revision payload named
policy and component rows, and the storage schema named them too. Worksets are
meant to be folders that can run *more than one kind of task* (file renaming
driven by crawled metadata is the next one), so the conversion content has to
sit behind a seam instead of inside the lifecycle.

## Decision

1. **The generic side owns the operation lifecycle.** Identity, ownership,
   versioning, drafts, revisions, confirmations, planning sessions, execution
   sessions, their mutual exclusions and their storage stay in
   `usecase/workset` — task-agnostic.

2. **A Task owns the business content behind that lifecycle.** A Task declares
   its `kind`, its seed draft, draft validation and normalization, its planning
   pass (frozen input → plan snapshot), its input-fact health rule, its member
   resolution, its review payload and its execution of frozen units. The
   conversion Task is the first adapter (`internal/tasks/conversion`); the
   generic module never imports a task package.

3. **Payloads are opaque.** Draft documents, plan payloads, plan-level facts
   (policy-style blob, tag snapshot, summary) and per-unit outcomes cross the
   seam as bytes plus the kind and schema version that describe them. The
   generic side stores and forwards them; it decodes only the facts it needs to
   name (counts, statuses, root facts, progress).

4. **Errors cross the seam in one envelope.** Tasks return the workset error
   type, so codes and details reach the HTTP layer unchanged.

5. **Contract points binding every Task**: admission (the generic side keeps
   ownership, version, transaction and idempotency; the task reports the
   business reasons), schema versions (an unknown payload schema version is
   rejected at every entry point), frozen session options (normalized once and
   part of the idempotency request hash), and progress (persisted at the task's
   own unit boundaries, cooperative cancellation, terminal statuses never
   regress).

## Consequences

- The revision payload on the wire is `task: {kind, schema_version, payload}`;
  a reader that does not know the kind treats the payload as opaque.
- Storage keeps task-owned tables (`conversion_steps`, `conversion_components`)
  next to generic ones (`plans` with `task_kind`/`task_schema_version`,
  `plan_roots` as input facts).
- Pre-task-seam databases are reset, not migrated: the plan, revision and
  execution domain is intermediate state and the project is in a
  rapid-iteration phase (libraries, entries, scans, worksets, members,
  operations and drafts survive).
- Adding a task means registering an adapter at the composition root and
  shipping its tables; it does not change the lifecycle paths.
- The gRPC/Flutter client line and the protobuf schema were removed in the
  same refactor; `entries`' legacy columns remain a separate, later retirement
  (ADR 0004 §6).
