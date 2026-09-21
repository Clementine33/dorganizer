# Onsei Media Organization

Onsei organizes audio collections by comparing observed media with desired audio outputs. A media library IS the workbench: its folders are browsed, organized and converted there, and each operation keeps one current record of the folders it works on.

## Language

### Collections and workbench

**Library**:
A named media collection rooted at one directory, whose scanned inventory supplies the member directories and media facts. It owns the workbench: entering a library is entering its overview, its browsing, its conversion record and its file management. Its deletion removes its entry (and that entry's record) but never the media.

**Album Folder / Planning Root**:
A direct-child directory of a Library, including its descendants. It is listed whether or not it holds audio — the audio count is a status, not a filter — and each *selected* folder becomes one planning root, an independent boundary for classification, association and planning.

**Workset (processing record)**:
The current record of one (Library, operation) pair: a fixed, ordered set of 1–500 member directories the user selected, plus that operation's settings and its current plan. At most one exists per pair, and creating a new one replaces the record it supersedes — together with that record's plan and execution results. The product entry keeps the name 工作集.
_Avoid_: Multiple named collections per library; permanent per-record history.

**Member / Member ID**:
A directory of one processing record. Its library-relative path (`rel_path`) is the durable data identity — a rescan cannot renumber it, and file management addresses items relative to it — `member_id` is the record-scoped identity the conversion routes carry, and `dir_id` is the identity its *page address* carries.
_Avoid_: Scan folder ID, list index, or the absolute path as member identity.

**Directory Identity (`dir_id`)**:
The navigation identity of one member directory, derived by the backend from a fixed version marker, the library, the canonical identity of its root and the directory's stored relative path. Page addresses and the tree routes carry it instead of the path, so an address names no folder. It is stable across rescans and restarts, unknown again once the directory is renamed or the library root changes, and it is never an access credential.
_Avoid_: Treating it as a permission, or confusing it with the data identity `rel_path`.

**Member Files / Current Files**:
What the shared file module shows for one member: the directory's contents as the last scan recorded them, refreshed when the page is entered. It serves every caller — the overview's browsing and a conversion member's page alike — and it reads no draft and interprets no plan.
_Avoid_: Conversion file browser; a page-owned tree.

**Plan Review**:
The read-only view of one member's frozen plan: what will be kept, deleted or generated, and which planned outputs do not exist yet. It is built from the frozen plan, never from the latest scan, and it never manages files.
_Avoid_: Using the current-files tree as the plan's view.

**Direct File Management**:
Renaming one item, moving one item inside its member, and soft-deleting one or more items into the library-level `Delete/`, without a plan. It validates its own paths and conflicts, reports per-item results, and never overwrites a destination.
_Avoid_: Treating these writes as a conversion step; disguising them as a plan.

**File Management Admission**:
The process-wide rule that direct file management and the managed task paths (scan, planning, execution) never interleave: one side is refused while the other is in flight, checked and registered atomically. Library root changes and deletions take the same slot.
_Avoid_: Frontend-only disabling; a queue of waiting file operations.

**Workset Operation**:
An independent business activity on a record's members, owning its draft, participation scope, planning sessions and its current plan. Conversion is the currently available activity, and it is the operation a record is created for.
_Avoid_: Workflow step; filesystem Operation when referring to this activity.

**Operation Draft**:
The saved common settings, member field overrides and participation choices of one Workset Operation.
_Avoid_: Batch Draft, Workset-wide Workflow Draft.

**Common Settings**:
The current values inherited by members without an explicit override for the corresponding setting group.

**Field Override**:
An explicit member-specific replacement of conversion mode, classifier tags, matched targets or unmatched targets. Other groups keep inheriting; an explicit empty value or a value equal to the common value still counts as an override.

**Effective Settings**:
The settings resolved for a member from common values and its field overrides, together with each group's common or member source.

**Excluded Member**:
A member that does not participate in one Workset Operation but retains its membership and configuration relationships.

**Batch Edit**:
An explicit edit of selected setting groups for a fixed member list. Unedited groups and members outside that list retain their existing settings and inheritance.

**Policy Slot**:
One of three reusable, named policy templates, initially unconfigured. Applying a configured slot copies its policy; later template changes do not alter saved drafts or revisions.

**Classifier Tag Library**:
Reusable literal classification tags, comprising installation defaults and user-saved custom tags. Drafts carry their own chosen tag values.

### Planning and review

**Task**:
One kind of work a Workset Operation can carry: it owns that work's draft document shape, its planning pass, its input facts and its execution. The generic workset module names the kind and payload schema version but never reads a task's payload; one adapter (conversion) exists today.
_Avoid_: Workflow, job, plugin.

**Plan**:
A reviewable proposal of media decisions and required filesystem changes, produced before execution.

**Plan Snapshot**:
The frozen payload of one Plan Revision as its Task wrote it: opaque to the generic workset module, carries `kind` and `schema_version` so a reader knows what it is looking at.

**Plan Revision (current plan)**:
The immutable proposal a processing record currently holds: its draft, member participation, effective settings and sources, input facts and results are frozen. Publishing a new one retires the plan it replaced, with its payload and execution results, in the same transaction — a record keeps one plan.
_Avoid_: Revision history; browsing an older plan of a record.

**Planning Session**:
An asynchronous attempt to produce a complete Plan Revision from frozen settings and member inputs. Only successful completion replaces the operation's current plan; failure, cancellation or interruption leaves the previous plan exactly as it was.

**Planning State**:
The operation's relationship to planning: unplanned, planning, planned, needs planning or orphaned. It is distinct from input validity and the proposal's results.

**Input Facts**:
What a plan was made from: per Planning Root, the identity, inventory fingerprint and entry count observed at planning time. The Task says how to recompute them and what counts as moved.
_Avoid_: Snapshot token, ETag.

**Execution Session**:
The asynchronous, durable run of one Plan Revision: globally serialized, with one shared pool of encode workers running single-file encode tasks across every member folder and a single coordinator committing components in frozen order. Cooperatively cancelable — a stop is checked between commit operations and stops admission at once — with a per-component result that survives a crash and no automatic retry.
_Avoid_: Run, job.

**Obsolete Audio Handling**:
The whole-operation choice of what happens to replaced and obsolete audio at execution: soft (moved to the library-level `Delete/`, under the member's own path — beside the member folder, never inside it — and recoverable) or hard (removed). It is a draft setting, never a per-member override; the plan freezes it and the run uses the frozen value.
_Avoid_: Delete mode as a per-run option.

**Input Validity**:
Whether a revision's recorded inputs still agree with the current observed inventory: valid, stale or unavailable.

**Deleted Library**:
A Library entry the user removed: its processing record, plan and execution results are deleted with it, while the media files and the `Delete/` recovery directory on disk stay untouched. There are no orphaned records left behind to review.

### Audio reconciliation

**Plan Policy**:
A declaration of literal classifier tags, desired audio outputs for each partition and the conversion mode used to reconcile them.
_Avoid_: Slim/prune mode, user-composed conversion and cleanup steps.

**Filter Match / Classifier Partition**:
Whether an audio file's Planning Root-relative path contains a classifier tag, ignoring case. The partitions are `matched` (UI “无音效”) and `unmatched` (UI “有音效”); these labels describe rule matching, not proof of audio effects.

**Desired Audio Profile**:
The exact managed audio outputs wanted for one classifier partition: at most one lossless output and one encoded output. It is the partition's final shape — media of an output it does not declare is obsolete once the declared set is reached, and a profile that declares nothing means the partition holds no managed audio at all.
_Avoid_: User-facing generation policy such as always or if missing.

**Target Encode Specification**:
The codec and quality required for an encoded output, such as MP3 at 320 kbps, against which existing encoded media is assessed.

**Media Facts / Observed Inventory**:
The observed files and their properties, including paths, codec, size, modification time and bitrate. These facts describe available inputs rather than desired outputs.

**Component**:
A transitive association of audio files sharing a parent directory or track stem within one Planning Root and classifier partition. It is the safety boundary for blocking changes and, in strict mode, maintaining encoded-output consistency.

**Variant Group**:
The files representing the same logical track within a Component, grouped by stem.

**Qualified Source**:
Observed lossless audio from which a requested output can be generated. Lossy audio is never a conversion source: not for a lossless output, and not for a codec change either, so a 256 kbps MP3 is no source for a 256 kbps AAC target.

**Conversion Mode**:
`strict` enforces Component-wide encoded consistency; `available_sources` preserves satisfied outputs and generates unsatisfied targets where qualified sources exist. Source ambiguity and safety conflicts remain blocking in either mode.

**Unknown Bitrate**:
An observed encoded file whose bitrate cannot be established, so it cannot be assumed to satisfy the target quality.

**Unmet Target**:
A desired output that cannot be supplied from available qualified sources in `available_sources` mode. The Variant Group it belongs to is left exactly as it is — existing media preserved, nothing removed — and the fact is disclosed separately from blocking conflicts; it does not by itself prevent execution.

**Decision**:
A reviewable conclusion such as keep, delete or encode, with its reason. A keep decision requires no filesystem change.

**Operation**:
An executable filesystem change derived from a Plan's Decisions, such as encoding or deletion.
_Avoid_: Workset Operation when referring to a file change.

**Reconciliation**:
Comparison of observed variants with their Desired Audio Profile to produce Decisions and required Operations under the chosen Conversion Mode.
