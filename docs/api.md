# Onsei Backend HTTP / SSE API

The backend runs a gRPC listener (Flutter client) and a net/http listener
(Vue prototype) in one process over loopback. Both listeners bind
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
must be scanned again before folder-scoped planning.

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
        "policy": { "kind": "preset", "name": "balanced", "version": 1 }
      }
    ]
  }
}
```

`folder_ids` are required for workflow requests and resolve to the library's
own folder paths (a folder belonging to another library 404s). Each resolved
folder is an independent **planning root**: classifier partitioning, component
discovery and failure boundaries never cross roots.

The policy source is a tagged union:

- `preset` — an immutable compiled-in preset (`balanced@1`, `compact@1`,
  `archive@1`).
- `inline` — a full policy object:
  ```json
  "policy": {
    "kind": "inline",
    "policy": {
      "schema_version": 1,
      "classifier": { "name": "effect-direction", "version": 1 },
      "matched":   { "lossless": { "codec": "wav" }, "encoded": { "codec": "mp3", "quality": { "kind": "bitrate", "bitrate": 320 } } },
      "unmatched": { "lossless": { "codec": "wav" }, "encoded": { "codec": "mp3", "quality": { "kind": "bitrate", "bitrate": 320 } } }
    }
  }
  ```
  Each profile declares at most one lossless output (wav/flac) and one encoded
  output (mp3/aac with a bitrate quality), at least one of the two. `matched`
  is the classifier match (UI 无音效); `unmatched` is its complement (UI 有音效).

Structural policy errors are request failures (400, no Plan): `INVALID_POLICY`,
`UNKNOWN_PRESET`, `UNKNOWN_CLASSIFIER`, `INVALID_WORKFLOW_SCHEMA`,
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
      "classifier": { "name": "effect-direction", "version": 1, "pattern": "...", "hash": "..." },
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

`summary_reason` for workflows is `ACTIONABLE`, `NO_MATCH`, `BLOCKED`, or
`PARTIAL`. A **blocked Component** contributes zero executable operations
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
(including folder-scoped plans), newest first. `GET /api/v1/plans/:id`
returns the same layered shape as the create response, rebuilt from persisted
snapshots (never from live preset/classifier state).

Workflow execution is not implemented: calling the gRPC `ExecutePlan` on a
workflow plan returns `EXECUTE_NOT_SUPPORTED` before any item loading. The
single-action path remains executable.

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
(in-flight SSE scans included) and then stops the gRPC server gracefully,
under a single 5-second forced-exit guard shared by both listeners.