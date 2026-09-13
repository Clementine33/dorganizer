# Onsei Backend HTTP / SSE API


The backend runs a gRPC listener (Flutter client) and a net/http listener
(Vue web client) in one process over loopback. Both listeners bind
`127.0.0.1` only. They share the same SQLite repository and the same scan and
plan usecase instances, so browser and Flutter clients never contend for a
second writer.

HTTP endpoints live under `/api/v1`. Machine-checked contract coverage lives
in the Go tests (`backend/go/tests/e2e/http_library_scan_plan_test.go` and the
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
| POST | `/api/v1/plans` | yes | 200 | plan response (see below) |
| GET | `/api/v1/plans` | yes | 200 | `{"plans":[{...}]}` |

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
with `LIBRARY_HAS_WORKSETS` while linked Worksets exist. Deletion is blocked
by active operation generation (`GENERATION_IN_PROGRESS`); otherwise retained
Worksets become read-only orphans.

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

### Plan request

`POST /api/v1/plans` accepts exactly one branch:

- **workflow** — declarative desired audio outputs over folder planning roots
  (the `reconcile_audio_outputs` step). The user declares the final managed
  audio set per classifier partition; conversion/cleanup mechanics are derived
  by the backend.
- **single_action** — an explicit delete or convert of selected source files
  (retained independently of the workflow).

The legacy `plan_type` / `target_format` / `prune_matched_excluded` fields
were removed: sending them returns `400 LEGACY_FIELDS_NOT_SUPPORTED`. Nothing
is silently derived.

Workflow request:

```json
{
  "library_id": "uuid",
  "folder_ids": ["uuid", "..."],
  "workflow": {
    "schema_version": 1,
    "steps": [
      {
        "step_type": "reconcile_audio_outputs",
        "policy": {
          "kind": "inline",
          "policy": {
            "schema_version": 1,
            "mode": "available_sources",
            "classifier_tags": ["SEなし"],
            "matched": {"lossless": {"codec": "wav"}, "encoded": {"codec": "mp3", "quality": {"kind": "bitrate", "bitrate": 320}}},
            "unmatched": {"lossless": {"codec": "wav"}, "encoded": {"codec": "mp3", "quality": {"kind": "bitrate", "bitrate": 320}}}
          }
        }
      }
    ]
  }
}
```

`folder_ids` are required for workflow requests and resolve to the library's
own folder paths (a folder belonging to another library 404s). Each resolved
folder is an independent **planning root**: classifier partitioning, component
discovery and failure boundaries never cross roots.

The policy source must be `inline`, carrying a complete policy snapshot as
above. Preset references are no longer supported (`INVALID_POLICY_SOURCE`).
Each profile declares at most one lossless output (wav/flac) and one encoded
output (mp3/aac with a bitrate quality), at least one of the two. Literal
`classifier_tags` are trimmed, deduplicated case-insensitively and sorted;
at least one non-empty tag is required for planning. Tags match substrings of
the root-relative path, ignoring case. `matched` is the classifier match
(UI 无音效); `unmatched` is its complement (UI 有音效).

Structural policy errors are request failures (400, no Plan): `INVALID_POLICY`,
`INVALID_POLICY_SOURCE`, `INVALID_WORKFLOW_SCHEMA`,
`UNSUPPORTED_STEP`, `SCOPE_REQUIRED`. Media that cannot satisfy a *valid*
policy produces a reviewable Plan with blocked Components instead.

Workflow response (200):

```json
{
  "plan_id": "plan-...",
  "snapshot_token": "...",
  "root_path": "/home/me/music/albumA",
  "plan_kind": "workflow",
  "summary": {
    "operation_count": 4,
    "error_count": 0,
    "total_count": 4,
    "actionable_count": 4,
    "summary_reason": "ACTIONABLE"
  },
  "steps": [
    {
      "step_type": "reconcile_audio_outputs",
      "step_index": 0,
      "status": "ok",
      "policy": { "...": "..." },
      "policy_hash": "...",
      "classifier": { "tags": ["SEなし"], "hash": "..." },
      "summary": { "component_count": 1, "blocked_count": 0, "operation_count": 4, "error_count": 0, "summary_reason": "ACTIONABLE" },
      "components": [
        {
          "component_id": "...", "partition": "unmatched", "status": "ok",
          "lanes": [ { "lane": "lossless", "decision": "KEEP" }, { "lane": "encoded", "decision": "REBUILD_ALL" } ],
          "variant_decisions": [ { "stem": "00", "decisions": [...] } ],
          "operations": [ { "kind": "encode", "phase": "materialize_outputs", "component_id": "...", "variant_stem": "00", "source_path": ".../wav/00.wav", "target_path": ".../wav/00.mp3" } ],
          "projected_inventory": ["...00.mp3", "..."],
          "files": [ { "path": "...", "size": 1, "mtime": 1 } ]
        }
      ]
    }
  ]
}
```

`summary_reason` for workflows is `ACTIONABLE`, `NO_MATCH`, `BLOCKED`,
`PARTIAL`, or `UNMET_TARGETS` for relaxed planning. A **blocked Component**
contributes zero executable operations
(retaining its decisions for review) and other Components may remain
actionable. Non-audio files never receive decisions or operations.

Single-action request:

```json
{
  "library_id": "uuid",
  "single_action": {
    "action": "delete",
    "source_files": ["/abs/path.flac"]
  }
}
```

Every `source_files` path must lexically and physically resolve inside the
selected library root; outside paths, traversal escapes, and escapes through
symbolic links or Windows junctions return `SOURCE_FILE_OUTSIDE_LIBRARY`.

`GET /api/v1/plans?library_id=uuid&limit=100` lists plans for a library
(including standalone folder-scoped plans), newest first. Workset revisions
are discovered through their operation-scoped history, not this list. `GET /api/v1/plans/:id`
returns the same layered shape as the create response, rebuilt from persisted
snapshots (never from current templates or tags).

Workflow execution is not implemented: calling the gRPC `ExecutePlan` on a
workflow plan returns `EXECUTE_NOT_SUPPORTED` before any item loading. The
single-action path remains executable.

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
| GET | `/api/v1/worksets/{id}/operations/{type}` | Operation view: `version`, `planning_state`, `current_revision`, `active_generation`, `latest_generation`. Unknown workset, unsupported type and unestablished operation are all 404 (`UNKNOWN_OPERATION_TYPE` for a type this iteration does not implement). |
| GET | `/api/v1/worksets/{id}/operations/{type}/draft` | The sparse draft `document` plus `version` (the operation version). |
| PUT | `/api/v1/worksets/{id}/operations/{type}/draft` | Full replacement of the sparse document, `If-Match` = operation version. Structural validation only: an incomplete draft saves. `GENERATION_IN_PROGRESS` while a session is queued/running; `ORPHANED_WORKSET` when read-only. |
| POST | `/api/v1/worksets/{id}/operations/{type}/revisions` | Start generation. No request body; `If-Match` (operation version) and `Idempotency-Key` are both required. 202 `{"created":true,"generation":…}`; 200 with `revision` when nothing semantic changed, or with `generation` for a key replay. Conflicts: `GENERATION_IN_PROGRESS`, `SCAN_IN_PROGRESS`, `NO_ACTIVE_MEMBERS`, `INVALID_POLICY`, `VERSION_CONFLICT`. |
| GET | `/api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}` | Session detail. Statuses `queued`, `running`, `completed`, `failed`, `canceled`, `interrupted`. A session of another workset or operation is 404. |
| GET | `/api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}/events` | SSE progress (`session_snapshot`, `progress`, terminal event). |
| POST | `/api/v1/worksets/{id}/operations/{type}/planning-sessions/{genId}/cancel` | Cooperative cancel; idempotent on terminal sessions. |
| GET | `/api/v1/worksets/{id}/operations/{type}/revisions` | History, newest first, keyset `?before_index=&limit=` → `next_before_index`. |
| GET | `/api/v1/worksets/{id}/operations/{type}/revisions/{planId}` | Immutable snapshot: `counts`, frozen `members[]` (effective settings + per-unit `sources`), `roots[]`, `component_roots[]`, `confirmation`, `workflow`. |
| POST | `/api/v1/worksets/{id}/operations/{type}/revisions/{planId}/confirmation` | Formal whole-revision confirmation, `If-Match` = operation version. 201 first / 200 idempotent repeat. 409 `PLAN_NOT_CONFIRMABLE` with `details[]`: `NOT_CURRENT_REVISION`, `DRAFT_CHANGED`, `GENERATION_IN_PROGRESS`, `INPUT_CHANGED`, `BLOCKED_COMPONENTS`. Unmet targets and zero-operation revisions do not block. `ORPHANED_WORKSET` when the library is gone. |

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
