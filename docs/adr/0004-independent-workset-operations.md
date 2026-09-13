# 0004: Declarative audio planning in independent Workset Operations

Accepted; consolidated on 2026-09-14 against the squash feature commits through
`7b46b0f`. This record merges the former ADRs 0001–0004 from documentation
commit `172c1c5404e8ce7ea5170088d339137c96e91e7e`. Number 0004 is retained
for existing references; earlier numbered decisions remain in Git history.
The backend operation model, Vue workbench and responsive navigation are
implemented. Workflow filesystem execution remains unsupported.

Domain language lives in [CONTEXT.md](../../CONTEXT.md); request and response
contracts live in [api.md](../api.md).

## 1. Declarative conversion and media safety

Users declare the exact desired audio set per classifier partition instead of
choosing separate slim/prune algorithms or generation/cleanup flags. The
planner derives conversion and removal from observed inputs:

Observed Inventory → classifier partition → Component → Variant Groups →
Desired Audio Profile → Decisions → Operations → Projected Inventory.

- Classification uses normalized literal tags matched case-insensitively
  against Planning Root-relative paths. Each root and each matched/unmatched
  partition is isolated before association discovery; tags are not regex input.
- Component discovery retains transitive same-parent OR same-stem association
  so tracks can pair across wav/mp3 directories without crossing content classes.
- Each profile specifies at most one lossless output (WAV/FLAC) and one encoded
  output (MP3/AAC with bitrate quality), with at least one output required.
- `strict` keeps an adequate encoded lane or rebuilds the entire lane from
  qualified observed lossless sources; an unsatisfied or unsafe Component is
  blocked. A missing policy mode retains strict semantics.
- `available_sources` works per Variant Group: keep satisfied outputs, generate
  targets from qualified sources, and preserve source-less tracks with unmet
  targets. Ambiguity and path conflicts still block; relaxed planning is not
  permission for unsafe conversion or deletion.
- Only observed lossless sources qualify for conversion. No lossy upgrades,
  encoded-to-lossless conversion or reuse of the current plan's projected
  outputs as observed sources is allowed.
- Blocked Components produce no executable operations but retain decisions
  for review. Other Components can remain actionable. Non-audio files are
  outside reconciliation; removal depends on replacement outputs committing.
- Structural Component identity is distinct from input validity. Inventory
  fingerprints use path, size and modification time plus entry count, accepting
  the scanner's blind spots for metadata-preserving changes rather than adding
  audio-content hashing.

The conversion engine and standalone `/plans` workflow representation are
reused behind operation planning. They do not imply a user-composed sequence
of Workset Operations. Policies are complete inline snapshots; the earlier
preset/classifier-registry contract has been replaced by literal tags and
reusable policy slots.

## 2. Aggregate ownership and concurrency

A Workset owns its name, Library reference and fixed ordered set of 1–500
members. Each member has a stable server-generated `member_id`; scan folder
IDs, member indexes and paths are not editing identities.

Each `(workset, operation type)` owns its sparse draft, current revision,
revision ordering, generation sessions and confirmations. `conversion` is the
only public type. Workset metadata and each operation have separate versions:
rename guards the metadata version; draft save, generation and confirmation
guard the operation version. Draft save and successful publication advance
that operation's version. There is no additional draft counter.

At most one queued/running generation exists per operation. Its frozen inputs
must not race draft replacement, so that operation's draft is locked during
generation. Renaming the Workset does not dirty its operation or revoke a
confirmation. Idempotency, session ownership and query cache keys include the
operation identity.

Library deletion is blocked while any owned operation has active generation.
Otherwise it retains Workset snapshots as read-only orphans, including review
history; orphaned operations cannot save, generate or confirm. A linked
Library's root cannot be changed while it has Worksets.

## 3. Sparse inheritance and explicit editing

The four atomic override groups are `mode`, `classifier_tags`, `matched` and
`unmatched`. A missing override inherits the current common value; a present
override replaces that entire group, without recursive target merging. Equal
values with different sources remain different configuration relationships.
Removing a key restores inheritance; an empty tag array is an explicit value.
Exclusion changes only participation in that operation and preserves overrides.

Draft persistence atomically replaces the full sparse document under a version
guard. Structurally valid but incomplete drafts can be saved; generation fully
validates participating members' effective settings. All-excluded drafts save
but cannot generate. New drafts explicitly use `available_sources`, default
WAV + MP3 at 320 kbps profiles and a snapshot of configured default tags.

Three global policy slots are reusable templates, not live references in a
draft. Applying one copies its policy. Custom classifier tags likewise supply
reusable values without rewriting saved drafts or historical revisions.

Common, batch and member editing are explicit entry points. Selection alone
does not choose an edit target. A batch session freezes its member list and
updates only intentionally changed groups; other groups retain their values
and inheritance. Restoring inheritance is distinct from setting a value.

## 4. Immutable revisions, generation and confirmation

Generation freezes the sparse draft, ordered members and participation,
effective settings and sources, paths and input fingerprints. Historical
results are reconstructed from those snapshots, never today's common values.
Dirtiness compares semantic draft hashes, including inheritance relationships,
not metadata counters or effective values alone.

The existing asynchronous queue produces complete proposals. Publication of
the plan, operation revision, current-revision pointer/version and completed
session is atomic. Failure, cancellation or restart interruption leaves the
previous current revision intact. An unchanged draft, member scope and input
inventory can reuse the current revision; an idempotency key cannot be reused
for a different request. Queued cancellation prevents work; running cancellation
is cooperative. Creation/completed-generation replay windows and terminal
session retention are 30 days; unsuccessful sessions release their keys.
Workset revisions are exempt from standalone plan retention.

Planning state, input validity and result facts are separate axes. Input
validation compares frozen fingerprints with current scanned inventory on
authoritative reads, rather than scanning every root for every list row.
Missing member roots remain explicit missing outcomes. Changed, unmet-target,
blocked and unchanged counts are independent facts, not an exclusive status.

Confirmation accepts the current operation's complete revision, not selected
members. The server checks version, current-revision identity, matching draft,
no active generation, valid inputs and no blocked Components. Unmet targets
and zero-operation proposals do not themselves block confirmation; their
counts and excluded scope remain visible. The confirmation record is separate
from immutable results and repeat confirmation is idempotent while eligible.
New revisions never inherit it. Saving does not generate; generating does not
confirm; confirming does not execute.

Version guards protect persistence, not the filesystem. A future executor must
revalidate inputs immediately before changing files.

## 5. Workbench navigation and state ownership

The Vue workbench exposes Workset → conversion → settings/member/batch routes.
Overview and conversion share one workspace; nested detail/editor routes retain
its list and editing session. Search, filters and reviewed revision belong in
the URL; selections, frozen batch lists and unapplied edits remain transient.
Historical results are read-only. A stale save retains edits and requires an
explicit reload/discard decision, never an automatic overwrite retry.

Vue Query owns server/cache state, with operation-scoped keys and explicit
refresh after mutations and generation events. UI selection and editing state
remain local to the workbench; scan SSE lifecycle remains transient process
state in the scan store. Background refresh must not overwrite dirty edits.

Global navigation contains Libraries and Worksets, rendered as a desktop rail
or mobile bottom bar. The Workset's local navigation groups conversion and its
common settings; collapsing it hides the whole sidebar. Detail/editor carriers
adapt to actual workspace width: full-page drill-down at ≤640px, modal side
sheet at 641–1100px, and an adjacent panel above 1100px. Layout changes preserve
URL, selection and edit contents; only one interactive form is mounted.
Semantic theme tokens and shared accessible controls keep focus, keyboard and
modal behavior consistent across layouts.

## 6. Consequences and rejected alternatives

- Independent operations replace Workset-wide drafts and step-based editing.
  The old full-policy member override, workflow-wide exclusion and future-step
  placeholder contracts are retired; sparse inheritance avoids copying common
  settings into every member.
- This branch introduced a new Workset storage contract without a legacy-data
  migration or dual-schema reader. Existing development databases and media
  are not cleared by that choice; isolated data is used for fresh testing.
- The standalone workflow and executable `single_action` paths remain separate.
  Workflow execution returns `EXECUTE_NOT_SUPPORTED`; confirmation is only
  review acceptance. Member-set editing, future operation types and concurrent
  cross-operation filesystem scheduling remain outside the delivered scope.
- Flutter/gRPC is the legacy client line and receives no new Workbench UI.
  The Vue HTTP client is the active product surface.
