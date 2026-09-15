# Onsei Backend HTTP / SSE API


The backend runs a gRPC listener (Flutter client) and a net/http listener
(Vue web client) in one process over loopback. Both listeners bind
`127.0.0.1` only. They share the same SQLite repository and the same scan and
plan usecase instances, so browser and Flutter clients never contend for a
second writer.

HTTP endpoints live under `/api/v1`. Machine-checked contract coverage lives
in the Go tests (`backend/go/tests/e2e/*_test.go` and the
`backend/go/internal/httpapi` handler tests); this document is the human
reference.

## Startup handshake

On startup the backend prints exactly one line to stdout:

```
ONSEI_BACKEND_READY port=%d token=%s version=%s http_port=%d
```

- `port` — gRPC port
- `http_port` — HTTP port
- `token` — the configured `ONSEI_TOKEN` (empty when auth is disabled)
- `version` — build version stamp (`dev` by default)

Hosts (Flutter, the Vue dev script) scan stdout for the `ONSEI_BACKEND_READY`
line and read both ports from it. Existing Flutter key/value parsing is
preserved — `http_port` is purely additive.

## Auth

Protected routes require:

```
Authorization: Bearer <token>
```

The token comes from the `ONSEI_TOKEN` env var. An empty token disables auth
entirely (local-developer mode). Missing or invalid tokens return
`401` with `{"code":"UNAUTHORIZED","message":"..."}`.

## CORS

`ONSEI_CORS_ORIGINS` is a comma-separated allowlist of browser origins. The
default is `http://localhost:5173,http://127.0.0.1:5173` (Vite dev servers).
Origins outside the allowlist receive no CORS headers and are blocked by the
browser. Preflight (`OPTIONS`) is answered by the CORS middleware before the
auth middleware runs, because browsers do not send the `Authorization` header
on preflight.

## Error envelope

Non-2xx JSON responses always use the same shape:

```json
{ "code": "LIBRARY_NOT_FOUND", "message": "library not found" }
```

Common codes: `INVALID_ARGUMENT` (400), `UNAUTHORIZED` (401),
`LIBRARY_NOT_FOUND` / `FOLDER_NOT_FOUND` / `LIBRARY_FOLDER_NOT_FOUND` (404),
`LIBRARY_EXISTS` (409), `SCOPE_REQUIRED` (400), `INTERNAL` (500).

## Endpoints

| Method | Path | Auth | Status | Response |
| --- | --- | --- | --- | --- |
| GET | `/api/v1/health` | no | 200 | `{"ok":true,"version":"dev"}` |
| GET | `/api/v1/libraries` | yes | 200 | `{"libraries":[{...}]}` |
| POST | `/api/v1/libraries` | yes | 201 | library object |
| GET | `/api/v1/libraries/:id` | yes | 200 | library object |
| PATCH | `/api/v1/libraries/:id` | yes | 200 | library object |
| DELETE | `/api/v1/libraries/:id` | yes | 204 | — |
| POST | `/api/v1/libraries/:id/scans` | yes | 200 | SSE stream (see below) |
| GET | `/api/v1/libraries/:id/folders` | yes | 200 | `{"folders":[{...}]}` |
| GET | `/api/v1/libraries/:id/folders/:folderId/tree` | yes | 200 | `{"tree":{...}}` |

### Library object

```json
{
  "id": "uuid",
  "name": "Music",
  "root_path": "/home/me/music",
  "created_at": "2026-08-22T00:00:00Z",
  "updated_at": "2026-08-22T00:00:00Z",
  "last_scan_at": null,
  "last_scan_status": "",
  "last_scan_error": ""
}
```

`POST /api/v1/libraries` body: `{"name":"...","root_path":"/abs/path"}`.
`PATCH` applies only the fields present (`name`, `root_path`). Changing
`root_path` clears the derived folder index and prior scan state; the library
must be scanned again before folder-scoped planning. Root changes are rejected
with `LIBRARY_HAS_WORKSETS` while linked Worksets exist. Deletion is blocked by
active operation generation (`GENERATION_IN_PROGRESS`) and by an active
execution session (`EXECUTION_IN_PROGRESS`); otherwise retained Worksets become
read-only orphans.

### Scan: POST + SSE

`POST /api/v1/libraries/:id/scans` requires a JSON object body; send `{}`
when no override is needed. A zero-byte request body is invalid. An optional
`root_path` is retained for client compatibility but must resolve to the
selected library's configured root; cross-library scan overrides are rejected
with `ROOT_PATH_OUTSIDE_LIBRARY`. The response is
`Content-Type: text/event-stream` and is cancelled automatically if the client
disconnects.

| Event | Data fields |
| --- | --- |
| `started` | `stage`, `message` |
| `progress` | `stage`, `files_scanned`, `dirs_scanned` |
| `completed` | `stage`, `scan_id`, `root_path`, `files_scanned` |
| `error` | `stage`, `code`, `message` |
| `cancelled` | `stage`, `message` |

A successful scan always emits `started` then one or more `progress` then
`completed` (`scan_id` is set). Failures end with `error`;
client-initiated cancellation with `cancelled`. After a successful scan the
backend rebuilds the library's direct-child folder index and records a
`completed` scan state on the library.

A scan is refused with `EXECUTION_IN_PROGRESS` (409) while any Workset of the
library has a queued/running execution: an execution validates the inventory
the scan would rewrite.

### Folders and tree

`GET /api/v1/libraries/:id/folders` returns the direct-child audio folders of
the library root:

```json
{
  "folders": [
    { "id": "uuid", "name": "albumA", "path": "/home/me/music/albumA",
      "relative_path": "albumA", "audio_file_count": 4 }
  ]
}
```

`GET /api/v1/libraries/:id/folders/:folderId/tree` returns a recursive tree of
the folder (folders scoped to the owning library):

```json
{
  "tree": {
    "name": "albumA", "path": "/home/me/music/albumA", "type": "dir",
    "children": [
      { "name": "track1.flac", "path": "/home/me/music/albumA/track1.flac",
        "type": "file", "size": 12345, "bitrate": 920000, "format": "flac" }
    ]
  }
}
```

## Worksets 与操作（implemented）

A Workset is a fixed, ordered set of album folders of one library (1–500
members). Every workset owns independent **Workset Operations**; `conversion`
is the only operation type in this iteration, and no unimplemented operation
has an addressable route. Decisions: [ADR 0004](adr/0004-independent-workset-operations.md).

**Status: implemented and machine-checked.** The Go handler, repository and
e2e tests are the reference for the shapes below. The pre-operation workset
routes (`/worksets/{id}/draft`, `/worksets/{id}/revisions`,
`/worksets/{id}/planning-sessions/*`) were removed in the same delivery: there
is no workset-level draft, session or revision. Legacy aggregate tables are
never migrated, read or deleted.

### Versions

Two independent counters, never interchangeable:

| Version | Advances on | Guarded by |
| --- | --- | --- |
| `workset.version` (metadata) | rename | `If-Match` on `PATCH /worksets/{id}` |
| `operation.version` | draft save, revision publication | `If-Match` on draft save, generation start, confirmation |

A rename therefore never dirties an operation or revokes a confirmation, and
another operation's change never advances this operation's version. There is no
separate draft counter: `GET .../draft` returns the operation version to echo
back as `If-Match`.

### Routes

All routes require auth and use the standard error envelope.

| Method | Path | Behavior |
| --- | --- | --- |
| POST | `/api/v1/worksets` | Create. Body `{"library_id","title","folder_ids":[...]}`, `Idempotency-Key` required. 201 `{"workset":…,"created":true}`; the fixed members, the `conversion` operation and its seeded sparse draft are written in one transaction. Replay returns 200 with the same workset. |
| GET | `/api/v1/worksets` | Keyset list (`limit`, `cursor` → `next_cursor`, `library_id`, `status=active` excludes orphaned). |
| GET | `/api/v1/worksets/{id}` | Metadata view: `workset_id`, `title`, `version`, `library`, `members[]`, `operations[]`. Member coverage and planning state belong to an operation, never to the member or the workset. |
| PATCH | `/api/v1/worksets/{id}` | Rename only. `If-Match` = workset metadata version (`VERSION_REQUIRED` without it, `VERSION_CONFLICT` when stale, `ORPHANED_WORKSET` when the library is gone). |
| GET | `/api/v1/worksets/{id}/operations/{type}` | Operation view: `version`, `planning_state`, `current_revision`, `active_generation`, `latest_generation`, `active_execution`, `latest_execution`. Unknown workset, unsupported type and unestablished operation are all 404 (`UNKNOWN_OPERATION_TYPE` for a type this iteration does not implement). |
| GET | `/api/v1/worksets/{id}/operations/{type}/draft` | The sparse draft `document` plus `version` (the operation version). |
| PUT | `/api/v1/worksets/{id}/operations/{type}/draft` | Full replacement of the sparse document, `If-Match` = operation version. Structural validation only: an incomplete draft saves. `GENERATION_IN_PROGRESS` while a session is queued/running; `EXECUTION_IN_PROGRESS` while an execution session is; `ORPHANED_WORKSET` when read-only. |
| POST | `/api/v1/worksets/{id}/operations/{type}/revisions` | Start generation. No request body; `If-Match` (operation version) and `Idempotency-Key` are both required. 202 `{"created":true,"generation":…}`; 200 with `revision` when nothing semantic changed, or with `generation` for a key replay. Conflicts: `GENERATION_IN_PROGRESS`, `EXECUTION_IN_PROGRESS`, `SCAN_IN_PROGRESS`, `NO_ACTIVE_MEMBERS`, `INVALID_POLICY`, `VERSION_CONFLICT`. |
| GET | `/api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}` | Session detail. Statuses `queued`, `running`, `completed`, `failed`, `canceled`, `interrupted`. A session of another workset or operation is 404. |
| GET | `/api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}/events` | SSE progress (`session_snapshot`, `progress`, terminal event). |
| POST | `/api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}/cancel` | Cooperative cancel; idempotent on terminal sessions. |
| GET | `/api/v1/worksets/{id}/operations/{type}/revisions` | History, newest first, keyset `?before_index=&limit=` → `next_before_index`. |
| GET | `/api/v1/worksets/{id}/operations/{type}/revisions/{planId}` | Immutable snapshot: `counts`, frozen `members[]` (effective settings + per-unit `sources`), `roots[]`, `component_roots[]`, `confirmation`, `workflow`, and `execution` (the session that ran this revision, if any). |
| POST | `/api/v1/worksets/{id}/operations/{type}/revisions/{planId}/confirmation` | Formal whole-revision confirmation, `If-Match` = operation version. 201 first / 200 idempotent repeat. 409 `PLAN_NOT_CONFIRMABLE` with `details[]`: `NOT_CURRENT_REVISION`, `DRAFT_CHANGED`, `GENERATION_IN_PROGRESS`, `INPUT_CHANGED`, `BLOCKED_COMPONENTS`. Unmet targets and zero-operation revisions do not block. `ORPHANED_WORKSET` when the library is gone. |
| POST | `/api/v1/worksets/{id}/operations/{type}/revisions/{planId}/executions` | Execute a confirmed revision (see below). Body `{"delete_mode":"soft"\|"hard"}`, optional, default `soft`. `If-Match` (operation version) and `Idempotency-Key` are both required. 202 `{"created":true,"execution":…}`; 200 with the same session for a key replay. |
| GET | `/api/v1/worksets/{id}/operations/{type}/executions/{executionId}` | Session detail, including the per-component report. A session of another workset or operation is 404. |
| GET | `/api/v1/worksets/{id}/operations/{type}/executions/{executionId}/events` | SSE execution stream (`execution_snapshot`, `progress`, terminal event). |
| POST | `/api/v1/worksets/{id}/operations/{type}/executions/{executionId}/cancel` | Cooperative cancel; idempotent on terminal sessions (200 with the session either way). |

### Execution sessions

An execution session is the only write path to the disk. It runs exactly one
operation revision, as frozen: the revision's component outcomes, roots and
effective targets are consumed as persisted — no reconcile, no live draft, no
client-supplied file list.

**Authorization.** The gates below all run before a session exists; they are
the same facts confirmation certified, re-read at execution time:

| Rejection (409 unless noted) | Meaning |
| --- | --- |
| `PLAN_NOT_EXECUTABLE` + `NOT_CURRENT_REVISION` | the plan is not the operation's current revision |
| `PLAN_NOT_EXECUTABLE` + `NOT_CONFIRMED` | the revision has no confirmation |
| `PLAN_NOT_EXECUTABLE` + `DRAFT_CHANGED` | the live draft no longer matches the revision's frozen draft hash |
| `PLAN_NOT_EXECUTABLE` + `INPUT_CHANGED` | a root is missing or its live inventory fingerprint no longer matches the frozen one, or the revision's members no longer resolve |
| `PLAN_NOT_EXECUTABLE` + `BLOCKED_COMPONENTS` | the frozen snapshot has a blocked component |
| `PLAN_NOT_EXECUTABLE` + `ALREADY_EXECUTED` | this revision already ran (any terminal status) |
| `PLAN_NOT_EXECUTABLE` (no details) | a guard predicate refused the start under a concurrent change; re-read the operation |
| `EXECUTION_IN_PROGRESS` | another session of this operation is queued/running |
| `SCAN_IN_PROGRESS` | the library root is being scanned |
| `VERSION_CONFLICT` | stale `If-Match`; re-read the operation |
| `IDEMPOTENCY_KEY_REUSED` | the key was used for another revision or delete mode |
| `ORPHANED_WORKSET` | the library is gone; orphaned worksets are read-only |
| `INVALID_DELETE_MODE` (400) | `delete_mode` is neither `soft` nor `hard` |

**One session per revision.** The storage enforces it (a unique index on the
revision): a retry with the same `Idempotency-Key` returns its own session
(200), a new key on an executed revision is `ALREADY_EXECUTED`, and concurrent
starts have exactly one winner. Failures, partial completions and interrupted
sessions do not auto-retry: refresh the inputs (rescan), then generate and
confirm a new revision — an old confirmation never authorizes a second run.

**Lifecycle.** Statuses `queued` → `running` → `succeeded` | `failed` |
`canceled` | `interrupted`; a terminal status never regresses. Sessions run
serially (one worker) in frozen root/component order; the first component
failure stops admission, and every later component stays `pending` in the
report. `report_json` is rewritten at every component boundary, so a crash
keeps the facts of everything that already happened on disk. On startup any
leftover queued/running session is marked `interrupted`: partial results stay
visible, nothing is resumed, re-encoded or re-deleted, and the in-flight
component's disk state must be treated as unverified (a temporary
`<target>.tmp.<token>` output may remain).

**Detail payload** (also the SSE snapshot): `execution_id`, `workset_id`,
`operation_type`, `plan_id`, `status`, `delete_mode`, `total_components`,
`completed_components` (components that are no longer pending),
`total_operations`, `completed_operations`, `current_root`,
`current_component_id`, `current_phase` (`component` while a component runs),
`components[]`, `error_code`, `error_message`, `started_at`, `finished_at`,
`created_at`.

Each `components[]` entry: `component_index`, `component_id`, `root_path`,
`partition`, `status` (`pending` | `succeeded` | `failed` | `canceled`),
`stage` (the failing stage: `precheck`, `materialize`, `validate`, `commit`,
`remove`), `operations`, `completed_operations`, `committed[]`, `removed[]`,
`remaining[]` (operations not completed, in frozen order), `recovery[]`
(preserved files needing operator attention: soft-delete destinations,
replaced-old copies, leftover temporaries), `error_code`, `error_message`,
`inventory_synced`, `inventory_sync_error`.

**Events.** Every connection first receives `execution_snapshot` (the full
detail above), then `progress` (counts + current component; never a fabricated
percentage) and exactly one terminal event: `succeeded`, `failed`, `canceled`
or `interrupted`. There is no event-log replay; a client that missed events
re-reads the detail route. Disconnecting never cancels the session — only
`POST …/cancel` or the process lifecycle does.

**Cancellation.** Canceling a queued session ends it immediately; canceling a
running session sets a cooperative flag, and the worker stops at the
component's next safe stage boundary (a started commit finishes, so a recovery
copy is never destroyed halfway). Cancellation is idempotent.

**Inventory sync.** After each component the observed disk changes are applied
to the entries inventory for the affected paths only (removed sources lose
their row, committed outputs and `Delete/…` recovery files are refreshed with
the scan merge's `content_rev` semantics). A sync failure is disclosed per
component (`inventory_synced:false`, `inventory_sync_error`) instead of being
reported as "unchanged"; the library folder counts still only change on the
next scan. The executed revision keeps its frozen fingerprints, so it reports
`validation_state:"stale"` once the inventory moved — a follow-up run needs a
new generation.

**Mutual exclusion.** While a session is queued/running for an operation: its
drafts cannot be edited and it cannot be re-generated (`EXECUTION_IN_PROGRESS`
on both routes), the owning library cannot be deleted, and its library root
cannot be scanned.

### Operation draft document

The draft is one sparse document. The four common setting groups are the base;
a member record overrides only the units it explicitly replaces.

```json
{
  "schema_version": 1,
  "mode": "available_sources",
  "classifier_tags": ["SEなし"],
  "matched":   {"lossless": {"codec": "wav"}, "encoded": {"codec": "mp3", "quality": {"kind": "bitrate", "bitrate": 320}}},
  "unmatched": {"lossless": {"codec": "wav"}, "encoded": {"codec": "mp3", "quality": {"kind": "bitrate", "bitrate": 320}}},
  "members": [
    {"member_id": "m-…", "overrides": {"matched": {"lossless": {"codec": "flac"}}}},
    {"member_id": "m-…", "excluded": true}
  ]
}
```

- **Absence means inheritance.** A unit missing from `overrides` follows the
  common value, whatever that value is now. Null is never the way to express
  "clear": `"classifier_tags": []` is an explicit empty set, and removing the
  key restores inheritance. The two behave differently after later common
  changes.
- **Sparse storage.** The server normalizes on save: a member record with
  neither an exclusion nor any override is dropped, records are stored in
  member order, and a missing `classifier_tags` on the common level is stored
  as `[]`. `GET` returns that canonical document, so a save/read round trip
  never materializes unmodified units.
- **Structural vs business validation.** Save rejects unknown JSON fields,
  wrong types, unknown/duplicate `member_id`, unknown `mode` values and unknown
  codec or quality literals. It accepts structurally valid but incomplete
  settings (empty tags, undeclared outputs, missing quality). Generation
  requires every participating member's effective settings to pass the full
  reconcile policy validation (`INVALID_POLICY`).
- **Exclusion** changes participation only: overrides and inheritance survive
  it, and restoring participation restores the previous relationships.
  Excluding every member saves but cannot generate (`NO_ACTIVE_MEMBERS`).

### Frozen revisions

`GET .../revisions/{planId}` resolves the revision's frozen draft snapshot into
per-member effective settings and per-unit sources (`common` | `member`), so a
historical revision always reports the configuration it was planned with —
equal values with different provenance stay distinguishable, and later common
changes never rewrite history. `counts` reports independent facts
(`members`, `changed`, `unmet_targets`, `blocked`, `unchanged`) that may
overlap; no exclusive status label is derived from them.

Conversion modes (policy `mode`, applies to both partitions): `strict` (the
historical rules; missing mode = strict) and `available_sources` (relaxed per
Variant Group: satisfied outputs kept, unique qualified lossless source
generates missing/below targets, sourceless stems kept with `UNMET_TARGET`
records, ambiguity/path conflicts still block). `StepSummary.unmet_targets`
counts kept-but-unsatisfied stems; `summary_reason` may be `UNMET_TARGETS`.
New operation drafts seed `mode: "available_sources"`.

Workset creation is not re-scannable here: `folder_ids` must come from
`GET /api/v1/libraries/:id/folders` of the owning library.

## Reusable policy slots and classifier tags

These authenticated routes supply reusable values; edits never rewrite saved
operation drafts or revisions.

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/v1/policy-slots` | 200 `{"slots":[{"slot":1,"name":"…","policy":null},…]}`; exactly three slots, initially unconfigured. |
| PUT | `/api/v1/policy-slots/{slot}` | Body `{"name":"…","policy":{…}}`; 200 slot object. Slot must be 1–3, trimmed name 1–120 characters, policy complete and valid. Errors: `INVALID_SLOT`, `INVALID_SLOT_NAME`, `INVALID_POLICY` (400). |
| GET | `/api/v1/classifier-tags` | 200 `{"default_tags":[…],"custom_tags":[{"id":1,"tag":"…","created_at":"…"}]}`. Defaults come from `prune.literal_tags` and are read-only here. |
| POST | `/api/v1/classifier-tags` | Body `{"tag":"…"}`; 201 custom tag object. Empty trimmed tags return `INVALID_TAG` (400). |
| DELETE | `/api/v1/classifier-tags/{id}` | 204; invalid IDs return `INVALID_ID` (400), missing tags `NOT_FOUND` (404). |

Applying a slot is a client edit that copies policy values into the operation
draft and uses the normal version-guarded save. There is no server-side live
slot reference, slot creation or slot deletion endpoint.

## Paths: the one rule

**The frontend echoes paths verbatim; the backend normalizes.**

The Vue frontend displays and sends the original user strings — it never
touches path separators, never joins or resolves paths, and never uses any
`path` module. All normalization to POSIX form happens in Go
(`backend/go/internal/pathnorm`). `root_path` and `source_files` values
received over HTTP are normalized by the backend before they reach SQLite or
the plan usecase.

## Shutdown

On `SIGINT`/`SIGTERM` (or stdin EOF), the backend drains the HTTP server
(in-flight SSE scans included) and gRPC server concurrently,
under a single 5-second forced-exit guard shared by both listeners.
