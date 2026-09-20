# Onsei Backend HTTP / SSE API


The backend runs a single net/http listener (the Vue web client) over
loopback. It binds `127.0.0.1` only and owns the one SQLite writer, the scan
service and the workset service.

HTTP endpoints live under `/api/v1`. Machine-checked contract coverage lives
in the Go tests (`backend/go/tests/e2e/*_test.go` and the
`backend/go/internal/httpapi` handler tests); this document is the human
reference.

## Startup handshake

On startup the backend prints exactly one line to stdout:

```
ONSEI_BACKEND_READY token=%s version=%s http_port=%d
```

- `http_port` — HTTP port
- `token` — the configured `ONSEI_TOKEN` (empty when auth is disabled)
- `version` — build version stamp (`dev` by default)

The Vue dev script and the Playwright launchers scan stdout for the
`ONSEI_BACKEND_READY` line and read the HTTP port from it.

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
| GET | `/api/v1/libraries/:id/dirs` | yes | 200 | `{"dirs":[{...}]}` |
| GET | `/api/v1/libraries/:id/tree?dir=…` | yes | 200 | `{"tree":{...},"dir_id":…,"member_path":…}` (see below) |
| POST | `/api/v1/libraries/:id/tree/refresh?dir=…` | yes | 200 | `{"tree":{...},"dir_id":…,"member_path":…,"refreshed":true}` |
| POST | `/api/v1/libraries/:id/file-operations` | yes | 200 | per-item result (see below) |
| GET | `/api/v1/libraries/:id/operations/:type/current` | yes | 200 | `{"workset":{...}\|null}` |
| PUT | `/api/v1/libraries/:id/operations/:type/current` | yes | 201/200 | record + `skipped[]` (see below) |

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
`root_path` clears the scanned inventory of the old root and the prior scan
state; the library must be scanned again before anything is planned from it.
A root change is rejected with `LIBRARY_HAS_WORKSETS` while the library still
has a processing record: rename the record's scope or delete the library first.
Both a root change and a deletion take the direct-file-management slot, so
neither interleaves with file management (spec C1).

`DELETE /api/v1/libraries/:id` removes the library **together with its
processing record**: the record's members, operation, draft, current plan and
execution results go in the same transaction, so no orphaned record survives
its library. Media files and the library-level `Delete/` recovery directory on
disk are never touched. Deletion is blocked by active operation generation
(`GENERATION_IN_PROGRESS`) and by an active execution session
(`EXECUTION_IN_PROGRESS`).

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
client-initiated cancellation with `cancelled`. The scan writes the library's
inventory — the only listing source: there is no separate derived folder table
to rebuild, and a rescan cannot renumber anything the workbench navigates by.
A `completed` scan state is recorded on the library.

A scan is refused with `EXECUTION_IN_PROGRESS` (409) while any record of the
library has a queued/running execution: an execution validates the inventory
the scan would rewrite. It is also refused with `BUSY` (409) while direct file
management holds the admission slot, and it takes the scanning side of that
slot itself, so a file operation refuses a running scan in turn (spec C1).

### Directories and member trees

`GET /api/v1/libraries/:id/dirs` lists **every** direct child directory of the
library root, whether or not it holds audio, with that directory's subtree
counts. The library-level recovery directory is not a member and is left out:

```json
{
  "dirs": [
    { "name": "albumA", "path": "/home/me/music/albumA", "rel_path": "albumA",
      "dir_id": "4b7fa32eebf5b001bcf43b1976c1b163",
      "audio_file_count": 4, "file_count": 7 }
  ]
}
```

`rel_path` is the data identity: it is stable across rescans, where a
scan-scoped folder id is not, and it is what a record's scope and file
management address. `dir_id` is the navigation identity a page address carries
instead of the name (ADR 0008): the backend derives it from a fixed version
marker, the library, the canonical identity of its root and the stored relative
path (SHA-256, first 128 bits, lowercase hex). The same root and path keep the
same value across rescans and restarts; renaming the directory, or changing the
library root, makes the old value unknown. It is an identity, **not** a
credential: the same auth and path checks apply to it as to any other route.

`GET /api/v1/libraries/:id/tree?dir=<dir_id>` returns the stored tree of one
member directory, and answers with the identity it resolved and the path that
identity stands for, so a caller that only holds the identity learns both. The
tree carries each node's `rel_path` (relative to the member root), which is what
file management addresses:

```json
{
  "dir_id": "4b7fa32eebf5b001bcf43b1976c1b163",
  "member_path": "albumA",
  "tree": {
    "name": "albumA", "path": "/home/me/music/albumA", "rel_path": "", "type": "dir",
    "children": [
      { "name": "track1.flac", "path": "/home/me/music/albumA/track1.flac",
        "rel_path": "track1.flac",
        "type": "file", "size": 12345, "bitrate": 920000, "format": "flac" }
    ]
  }
}
```

The identity is resolved against the library's scanned inventory — the direct
child directories its listing shows, recovery directory excluded — and only the
single match is then checked on disk. The refusals are distinct:

| Condition | Answer |
|---|---|
| No `dir` parameter | 400 `DIR_ID_REQUIRED` |
| Not 32 lowercase hex characters | 400 `DIR_ID_INVALID` |
| No directory of this library has that identity | 404 `DIRECTORY_NOT_FOUND` |
| More than one directory claims it | 409 `DIRECTORY_AMBIGUOUS` (never the first match) |
| A direct child in the inventory that is gone from disk | 404 `MEMBER_MISSING` |
| Names the recovery directory, or is a symlink | 400 `MEMBER_PATH_INVALID` / `MEMBER_IS_SYMLINK` |

The retired `?folder=<rel_path>` parameter is not read: a request that carries
it instead of `dir` is answered as a missing identity. A directory that no scan
has recorded yet has no identity to address.

`POST /api/v1/libraries/:id/tree/refresh?dir=<dir_id>` re-scans that one
member directory and answers `{"tree": …, "dir_id": …, "member_path": …,
"refreshed": true}`; the refreshed directory keeps its own row in the inventory,
so it stays listed and stays addressable. It takes the
scanning side of the admission slot (see above) and answers `BUSY` (409) while
a file operation holds it. A failed refresh is `502` with the scan's code: the
caller keeps the tree it already shows and marks it unrefreshed.

### Direct file management

`POST /api/v1/libraries/:id/file-operations` is the second, explicitly
non-plan write path (ADR 0007 §4). It holds the direct-file-management slot
for the whole request — including the inventory refresh that follows the
writes — and is refused with `BUSY` (409) while any scan is running or any
planning session or execution is queued or running.

Body:

```json
{
  "member_path": "albumA",
  "operation": "rename",
  "items": [ { "source": "track1.flac", "name": "track01.flac" } ]
}
```

| Operation | Items |
| --- | --- |
| `rename` | one item; `name` is a plain name, never a path (`INVALID_NAME`) |
| `move` | one item; `target_dir` is an existing directory of the same member (`INVALID_TARGET_DIR`, `MOVE_INTO_SELF`) |
| `soft_delete` | one or more items, applied in order |

Every path is validated independently of the request: absolute paths,
traversal, a path that leaves the member, a path through a symlink and a
symlink itself are refused (`PATH_INVALID`, `OUTSIDE_MEMBER`, `SYMLINK`), and
the member root is never an item. A destination that exists is refused
(`TARGET_EXISTS`) — a rename or move never overwrites, and the no-replace
rename of the target platform is what enforces it. A batch stops at its first
failure: the rest are reported as `not_attempted`, and nothing already done is
undone. Selecting a directory and something inside it is one operation: the
child is reported `skipped` with `COVERED_BY_PARENT`.

Response (200):

```json
{
  "operation": "soft_delete",
  "member_path": "albumA",
  "items": [
    { "source": "track1.flac", "status": "ok", "recovered_path": "Delete/albumA/track1.flac" },
    { "source": "cover.jpg", "status": "failed", "code": "TARGET_EXISTS", "message": "…" }
  ],
  "succeeded": 1, "failed": 1, "untouched": 0,
  "refresh": { "ok": false, "code": "REFRESH_FAILED", "message": "files were modified, but refreshing the inventory failed: …" }
}
```

`status` is `ok`, `failed`, `skipped` or `not_attempted`. A soft delete keeps
the item's path relative to the library root under `<library root>/Delete/` and
never overwrites media recycled earlier (a collision becomes `<stem>.N<ext>`);
`recovered_path` is that location, for the user to restore from with their own
file manager. A refresh failure is reported beside the item results and never
replaces them: the files were modified, and both facts are stated.

## Processing records and operations

A **record** is the current processing record of one `(library, operation)`
pair: the fixed, ordered set of 1–500 member folders a user selected, plus the
operation that works on them. `conversion` is the only operation type in this
iteration, and no unimplemented operation has an addressable route. At most one
record exists per pair — that is a storage-level unique index, not an
application check. Decisions: [ADR 0007](adr/0007-library-workbench-and-current-operation-record.md),
which supersedes the multi-workset model of [ADR 0004](adr/0004-independent-workset-operations.md).

**Status: implemented and machine-checked.** The Go handler, repository and
e2e tests are the reference for the shapes below. There is no revision-history
route: a record keeps exactly one plan, the current one, and publishing a new
plan retires the one it replaced — with its payload, roots, units and
execution sessions — in the same transaction. The pre-operation workset routes
(`/worksets/{id}/draft`, `/worksets/{id}/revisions`,
`/worksets/{id}/planning-sessions/*`) and the old record-creation route
(`POST /worksets` with `folder_ids`) were removed in the same delivery. An
incompatible database is refused at startup, never migrated or cleared (spec
D2).

### Creating the current record

`PUT /api/v1/libraries/{id}/operations/{type}/current` is the only way a record
comes into existence. It validates the requested scope against the **scanned
inventory** — the same listing the caller selected from — and reports every
directory that did not become a member instead of dropping it silently:

| `skipped[].reason` | Meaning |
| --- | --- |
| `no_audio` | the directory holds no audio anywhere beneath it; conversion skips it (R1) |
| `missing` | the inventory does not know it as a directory of this root |
| `not_direct_child` | only direct children of the library root are members |
| `recovery_dir` | the library-level `Delete/` is not a member |
| `invalid_path` | not a plain library-relative path |
| `duplicate` | selected more than once |

A selection whose every entry is unusable creates nothing and answers
`NO_AUDIO_MEMBERS` (400) with the skipped paths in `details`. More than 500
selected paths is `INVALID_FOLDER_COUNT` (400) — never a silent truncation.
`expected_current_id` is the record the caller saw as current (absent when it
saw none): a mismatch is `RECORD_REPLACED` (409) instead of an overwrite, and a
record with a queued or running session is `RECORD_BUSY` (409) instead of being
stranded. A replayed `Idempotency-Key` with the same request answers with the
record it already created; the same key with a different request is
`IDEMPOTENCY_KEY_REUSED` (409).

### Versions

Two independent counters, never interchangeable:

| Version | Advances on | Guarded by |
| --- | --- | --- |
| `workset.version` (metadata) | rename | `If-Match` on `PATCH /worksets/{id}` |
| `operation.version` | draft save, revision publication | `If-Match` on draft save, generation start |

A rename therefore never dirties an operation, and
another operation's change never advances this operation's version. There is no
separate draft counter: `GET .../draft` returns the operation version to echo
back as `If-Match`.

### Routes

All routes require auth and use the standard error envelope.

| Method | Path | Behavior |
| --- | --- | --- |
| GET | `/api/v1/libraries/{id}/operations/{type}/current` | The library's current record for one operation, or `{"workset": null}` with 200 when it has none. A library that does not exist is 404. |
| PUT | `/api/v1/libraries/{id}/operations/{type}/current` | Create or replace. Body `{"folder_paths":[...],"expected_current_id":"…","title":"…"}` (title optional; the library's name is the default), `Idempotency-Key` required. 201 `{"workset":…,"created":true,"recorded":N,"skipped":[{"path","reason"}]}`; 200 for an idempotent replay. The new record and the removal of the one it replaces — members, operation, draft, plan and sessions — are one transaction. |
| GET | `/api/v1/worksets` | Keyset list (`limit`, `cursor` → `next_cursor`, `library_id`, `status=active` excludes orphaned). |
| GET | `/api/v1/worksets/{id}` | Metadata view: `workset_id`, `title`, `version`, `library`, `members[]`, `operations[]`. Member coverage and planning state belong to an operation, never to the member or the workset. |
| PATCH | `/api/v1/worksets/{id}` | Rename only. `If-Match` = workset metadata version (`VERSION_REQUIRED` without it, `VERSION_CONFLICT` when stale, `ORPHANED_WORKSET` when the library is gone). |
| GET | `/api/v1/worksets/{id}/operations/{type}` | Operation view: `version`, `planning_state`, `current_revision`, `active_generation`, `latest_generation`, `active_execution`, `latest_execution`. Unknown workset, unsupported type and unestablished operation are all 404 (`UNKNOWN_OPERATION_TYPE` for a type this iteration does not implement). |
| GET | `/api/v1/worksets/{id}/operations/{type}/draft` | The operation version plus the sparse draft wrapped in its task envelope: `task: {kind, schema_version, payload}`. The payload is task-owned (conversion: the sparse document). |
| PUT | `/api/v1/worksets/{id}/operations/{type}/draft` | Full replacement of the sparse document, `If-Match` = operation version. Structural validation only: an incomplete draft saves. `GENERATION_IN_PROGRESS` while a session is queued/running; `EXECUTION_IN_PROGRESS` while an execution session is; `ORPHANED_WORKSET` when read-only. |
| POST | `/api/v1/worksets/{id}/operations/{type}/revisions` | Start generation. No request body; `If-Match` (operation version) and `Idempotency-Key` are both required. 202 `{"created":true,"generation":…}`; 200 with `revision` when nothing semantic changed, or with `generation` for a key replay. Conflicts: `GENERATION_IN_PROGRESS`, `EXECUTION_IN_PROGRESS`, `SCAN_IN_PROGRESS`, `NO_ACTIVE_MEMBERS`, `INVALID_POLICY`, `VERSION_CONFLICT`. |
| GET | `/api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}` | Session detail. Statuses `queued`, `running`, `completed`, `failed`, `canceled`, `interrupted`. A session of another workset or operation is 404. |
| GET | `/api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}/events` | SSE progress (`session_snapshot`, `progress`, terminal event). |
| POST | `/api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}/cancel` | Cooperative cancel; idempotent on terminal sessions. |
| GET | `/api/v1/worksets/{id}/operations/{type}/revisions/{planId}` | The immutable snapshot of the record's **current** plan (a replaced plan no longer resolves — 404): `root_path`, `snapshot_token`, `status`, `summary`, the plan payload in its task envelope (`task: {kind, schema_version, payload}` — conversion: policy, classifier, summary, components), `counts`, frozen `members[]` (effective settings + per-unit `sources`), `roots[]`, `component_roots[]`, and `execution` (the session that ran this revision, if any). |
| POST | `/api/v1/worksets/{id}/operations/{type}/revisions/{planId}/executions` | Execute the operation's current revision (see below), optionally scoped to some of its folders. Body optional: `{"folder_paths":["albumA",…]}` — record-relative member paths; absent or empty runs the whole revision. The worklist and the session options (the obsolete-audio handling the draft declares) are always the frozen revision's. `If-Match` (operation version) and `Idempotency-Key` are both required. 202 `{"created":true,"execution":…}`; 200 with the same session for a key replay. |
| GET | `/api/v1/worksets/{id}/operations/{type}/executions/{executionId}` | Session detail, including the per-component report. A session of another workset or operation is 404. |
| GET | `/api/v1/worksets/{id}/operations/{type}/executions/{executionId}/events` | SSE execution stream (`execution_snapshot`, `progress`, terminal event). |
| POST | `/api/v1/worksets/{id}/operations/{type}/executions/{executionId}/cancel` | Cooperative cancel; idempotent on terminal sessions (200 with the session either way). |

### Execution sessions

An execution session is the only write path to the disk. It runs exactly one
operation revision, as frozen: the revision's component outcomes, roots and
effective targets are consumed as persisted — no reconcile, no live draft, no
client-supplied file list.

**Authorization.** The gates below all run before a session exists; they are
the plan's own facts, re-read at execution time:

| Rejection (409 unless noted) | Meaning |
| --- | --- |
| `PLAN_NOT_EXECUTABLE` + `NOT_CURRENT_REVISION` | the plan is not the operation's current revision |
| `PLAN_NOT_EXECUTABLE` + `DRAFT_CHANGED` | the live draft no longer matches the revision's frozen draft hash |
| `PLAN_NOT_EXECUTABLE` + `INPUT_CHANGED` | a root is missing or its live inventory fingerprint no longer matches the frozen one, or the revision's members no longer resolve |
| `PLAN_NOT_EXECUTABLE` + `BLOCKED_COMPONENTS` | the frozen snapshot has a blocked component |
| `PLAN_NOT_EXECUTABLE` + `ALREADY_EXECUTED` | this revision already ran (any terminal status) |
| `PLAN_NOT_EXECUTABLE` (no details) | a guard predicate refused the start under a concurrent change; re-read the operation |
| `EXECUTION_IN_PROGRESS` | another session of this operation is queued/running |
| `SCAN_IN_PROGRESS` | the library root is being scanned |
| `VERSION_CONFLICT` | stale `If-Match`; re-read the operation |
| `IDEMPOTENCY_KEY_REUSED` | the key was used for another revision |
| `ORPHANED_WORKSET` | the library is gone; orphaned worksets are read-only |
| `FOLDER_NOT_IN_RECORD` (400) | `folder_paths` names something that is not a member of this record |

**Scope.** A session covers the whole revision unless `folder_paths` names
some of its members. A path the record does not hold is refused with
`FOLDER_NOT_IN_RECORD` (400) rather than skipped — running fewer folders than
were asked for would be a scope change nobody approved; a selected folder the
plan found nothing to do in simply contributes no components, which is the
plan's conclusion about that folder, not a refusal. The session's own
`total_components`, report and SSE progress cover the scope alone, and
`selected_folders` (the record-relative paths) says what that scope was, so a
client that did not start the run can tell it was partial.

**One session per revision.** The storage enforces it (a unique index on the
revision): a retry with the same `Idempotency-Key` returns its own session
(200), a new key on an executed revision is `ALREADY_EXECUTED`, and concurrent
starts have exactly one winner. A scoped run spends the revision like any
other: the folders it left out are the next plan's work — regenerate a plan and
execute that — never a second run of this one. The idempotency key covers the
scope, so the same key with a different `folder_paths` is
`IDEMPOTENCY_KEY_REUSED`. Failures, partial completions and interrupted
sessions do not auto-retry: refresh the inputs (rescan), then generate a new
revision — a revision never runs twice.

**Fresh facts before writing.** A session refreshes the scanned inventory of
its roots through the scanner before its first write and re-judges the
revision's recorded inputs against what is on disk now. A drifted folder stops
the session as `failed` with `INPUT_CHANGED` and every file untouched — the
drift is caught at the start, never component by component halfway through. A
refresh that cannot complete (`SCAN_FAILED`) also leaves the disk alone.
Generation works the same way: its session scans the participating member
folders before planning, so a plan is never made from a stale inventory.

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
`operation_type`, `plan_id`, `status`, `options` (the task-owned frozen
session options — conversion: `{"delete_mode":"soft"|"hard"}`),
`total_components`,
`completed_components` (components that are no longer pending),
`total_operations`, `completed_operations`, `current_root`,
`current_component_id`, `current_phase` (`component` while a component runs),
`components[]`, `selected_folders` (the scope this session ran; absent when it
ran the whole revision), `error_code`, `error_message`, `started_at`,
`finished_at`, `created_at`.

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
re-reads the detail route. The persisted report is written at every component
boundary, so re-reading the detail after a `progress` event that moved
`completed_components` yields that component's own facts while the run
continues. Disconnecting never cancels the session — only
`POST …/cancel` or the process lifecycle does.

**Cancellation.** Canceling a queued session ends it immediately; canceling a
running session sets a cooperative flag, and the worker stops at the
component's next safe stage boundary (a started commit finishes, so a recovery
copy is never destroyed halfway). Cancellation is idempotent.

**Inventory sync.** After each component the observed disk changes are applied
to the entries inventory for the affected paths only (removed sources lose
their row, committed outputs and `Delete/…` recovery files are refreshed with
the scan merge's `content_rev` semantics). A soft removal lands under the
library root (`<library root>/Delete/<member path>/…`), beside the member
folder rather than inside it, so recovered media never re-enters a member's own
inventory. A sync failure is disclosed per
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
a member record overrides only the units it explicitly replaces. The
operation-wide `delete_mode` (obsolete-audio handling: `soft` — the default —
or `hard`) is a common-level field, never a member override, and freezes into
every revision the draft produces; the execution uses the frozen value.

```json
{
  "schema_version": 1,
  "mode": "available_sources",
  "delete_mode": "soft",
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
  wrong types, unknown/duplicate `member_id`, unknown `mode` or `delete_mode`
  values and unknown codec or quality literals. It accepts structurally valid
  but incomplete settings (empty tags, undeclared outputs, missing quality).
  Generation requires every participating member's effective settings to pass
  the full reconcile policy validation (`INVALID_POLICY`).
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
Variant Group). A partition's declared profile is its final audio set in either
mode: missing declared outputs are materialized from the stem's qualified
lossless source, and files the profile does not declare — including the source
a declared output was encoded from — are removed once those replacements
commit. A profile that declares no output means the partition holds no managed
audio: every observed file in it is removed. `available_sources` differs in
`strict`'s failure behavior: a stem whose declared shape cannot be reached is
left exactly as it is and recorded as `UNMET_TARGET` (no generation, no
cleanup), where strict blocks the whole Component with zero operations.
Ambiguity and path conflicts block in both.
An encoded output is satisfied by the target codec at or above the target
bitrate (1 kbps tolerance for probe under-reporting); MP3 and AAC are both
compared on the probed bitrate, and an unprobed one never counts as satisfying.
`StepSummary.unmet_targets` counts kept-but-unsatisfied stems; `summary_reason`
may be `UNMET_TARGETS`. New operation drafts seed `mode: "available_sources"`.

A record's scope is fixed once created: it is replaced, never re-scanned in
place. `folder_paths` come from `GET /api/v1/libraries/:id/dirs` of the owning
library, as library-relative paths.

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

**The frontend echoes paths verbatim; the backend normalizes them, and only
the backend resolves them.**

The Vue frontend displays and sends the original user strings — it never
touches path separators, never joins or resolves paths, and never uses any
`path` module. Absolute paths (`root_path`) are normalized to POSIX form in Go
(`backend/go/internal/pathnorm`) before they reach SQLite or the plan usecase.

Paths *inside* a library are relative, and the backend resolves them: a
directory listing returns `rel_path`, a member tree is read by `?dir=<dir_id>`
(the identity derived from that path — see *Directories and member trees*), and
file management addresses items relative to their member. A relative path is validated as a plain descendant chain before it is
joined to a root — absolute paths, drive or device prefixes, backslashes,
`..`, `.` and empty segments are refused — and every write re-checks that the
resolved path is still inside the member it was resolved against.

## Shutdown

On `SIGINT`/`SIGTERM` (or stdin EOF), the backend drains the HTTP server
(in-flight SSE scans included) under a single 5-second forced-exit guard.
