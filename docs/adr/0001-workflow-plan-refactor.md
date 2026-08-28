# 0001: Declarative workflow plan replaces slim/prune

## Status

Accepted (2026-08-28).

## Context

The backend exposed `slim` and `prune` as separate algorithm modes, forcing
callers to choose conversion/deletion mechanics rather than describing the
final audio set they want. The implementation also conflated four concerns
into one `Component` type: cross-directory association discovery, stem
grouping, component-wide encoded-quality consistency, and operation selection.

Cross-directory pairing is intentional domain behavior (see
`/mnt/d/TempDownload/unzip/RJ01567288`: `SEあり/{wav,mp3}/00..05` and
`SEなし/{wav,mp3}/00..05` must pair across `mp3/`/`wav/` lanes but never
merge between content classes).

## Decision

Users declare **Desired Audio Profiles** per classifier partition. The backend
infers classification, conversion, batch consistency, and non-target removal.

- **Pipeline**: Observed Inventory → classifier partition → Component
  discovery → Variant Groups → Desired Audio Profile → reconciliation →
  Decisions → Operations → Projected Inventory.
- **Classifier** partitions audio Entries by normalized planning-root-relative
  path **before** component discovery; `matched` (UI 无音效) and `unmatched`
  (UI 有音效) never share a Component. Classifiers are immutable and
  versioned; v1 freezes the installation's current `prune.regex_pattern`.
- **Component** keeps the intentional same-parent OR same-stem transitive
  discovery and is the encoded-lane consistency and fail-closed boundary. Any
  blocked Variant Group / lane blocks the whole Component (zero executable
  operations; decisions retained for review). Other Components may stay
  actionable.
- **Component identity** is structural: `digest(root + partition + sorted
  parent dirs)`. Inventory staleness uses a metadata fingerprint
  `digest(sorted(path, size, mtime))` + count — never audio content hashing
  (matching the scanner's existing size/mtime model, with the same accepted
  blind spots for same-second / mtime-preserving rewrites).
- **Desired outputs** are the exact managed audio set: at most one lossless
  (wav/flac) and one encoded (mp3/aac) output per profile, at least one. Users
  never declare `always`/`if_below`/prune flags/cleanup flags.
- **Encoded lane** is Component-consistent: all adequate → KEEP_ALL; any
  missing/below/unknown with qualified observed lossless sources →
  REBUILD_ALL; any group without a source → Component blocked.
- **Qualified sources** come from the Observed Inventory only: desired
  lossless codec → WAV → FLAC. lossless→lossless and lossless→encoded are
  the only conversions; never lossy→lossy or encoded→lossless.
- **Removal** is derived from the exact Desired Outputs and depends on all
  materialized targets committing. Non-audio files are never touched by this
  step.
- **Workflow schema v1** is a linear step list; only
  `reconcile_audio_outputs` exists. Each step emits a Projected Inventory for
  future linear steps and never consumes its own outputs.
- **Breaking migration**: legacy plans/execute sessions are purged (Plan is
  intermediate state, not history), the plans table is rebuilt with
  `plan_kind`/`workflow_schema_version`, and old `plan_type`/`target_format`
  fields return `LEGACY_FIELDS_NOT_SUPPORTED`. `single_delete`/`single_convert`
  remain as an independent `single_action` path. The gRPC `PlanOperations`
  surface is rejected (`WORKFLOW_REQUIRED`) because gRPC/Flutter is the
  abandoned client line.
- **Execute** for workflow plans is not implemented: the boundary returns
  `EXECUTE_NOT_SUPPORTED` before item loading. Execute (staging, journal,
  component commit) is a follow-up.

## Consequences

- Users reason about final audio sets, not conversion mechanics.
- Slim conversion and prune deletion unify into "audio not in Desired Outputs
  is removed after outputs commit".
- Cross-directory pairing is preserved while classifier partitions stay
  isolated.
- Breaking: existing plans and the old API contract are not retained. The web
  frontend adopts the new contract in a follow-up change.
- Future steps (rename, organize directories, sidecars/metadata) slot into the
  linear workflow with Projected Inventory as their input.

## Alternatives considered

- Reusing `Component`/`DetermineBranch` unchanged for the planner: preserves
  legacy behavior bit-for-bit but keeps branch pollution across stems/folders.
- Making `always`/`if_missing` user-facing generation policies: leaks planner
  mechanics the user does not need.
- File-content hashing for staleness: heavier than the existing
  size/mtime/content-rev model with no added detection for the accepted blind
  spots.
- A backend options-discovery endpoint: unnecessary; the supported set is a
  closed typed map on the frontend and an immutable preset/classifier registry
  in the backend.
