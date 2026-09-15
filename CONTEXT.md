# Onsei Media Organization

Onsei organizes audio collections by comparing observed media with desired audio outputs. Worksets keep the folders under review together while each business operation owns its settings and planning history.

## Language

### Collections and workbench

**Library**:
A named media collection rooted at one directory, whose scanned inventory supplies album folders and media facts.

**Album Folder / Planning Root**:
A direct-child audio folder of a Library, including its descendants. Each selected folder is an independent boundary for classification, association and planning.

**Workset**:
A fixed, ordered set of 1–500 album folders from one Library, with a shared name and membership for its independent Workset Operations.

**Member / Member ID**:
An album folder's stable identity within a Workset. Its path and position describe the member but do not define its identity.
_Avoid_: Scan folder ID, list index or path as member identity.

**Workset Operation**:
An independent business activity on Workset members, owning its draft, participation scope, planning sessions, revisions and confirmations. Conversion is the currently available activity.
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

**Plan Revision**:
An immutable proposal owned by one Workset Operation, freezing its draft, member participation, effective settings and sources, input facts and results.

**Planning Session**:
An asynchronous attempt to produce a complete Plan Revision from frozen settings and member inputs. Only successful completion replaces the operation's current revision.

**Planning State**:
The operation's relationship to planning: unplanned, planning, planned, needs planning or orphaned. It is distinct from input validity and the proposal's results.

**Input Facts**:
What a plan was made from: per Planning Root, the identity, inventory fingerprint and entry count observed at planning time. The Task says how to recompute them and what counts as moved.
_Avoid_: Snapshot token, ETag.

**Execution Session**:
The asynchronous, durable run of one confirmed Plan Revision: globally serialized, cooperatively cancelable at unit boundaries, with a per-unit report that survives a crash and no automatic retry.
_Avoid_: Run, job.

**Input Validity**:
Whether a revision's recorded inputs still agree with the current observed inventory: valid, stale or unavailable.

**Confirmation**:
The user's acceptance of one complete current Plan Revision after eligibility checks. It neither executes files nor transfers to a new revision.

**Orphaned Workset**:
A Workset whose Library has been deleted, retaining its members and historical proposals for read-only review.

### Audio reconciliation

**Plan Policy**:
A declaration of literal classifier tags, desired audio outputs for each partition and the conversion mode used to reconcile them.
_Avoid_: Slim/prune mode, user-composed conversion and cleanup steps.

**Filter Match / Classifier Partition**:
Whether an audio file's Planning Root-relative path contains a classifier tag, ignoring case. The partitions are `matched` (UI “无音效”) and `unmatched` (UI “有音效”); these labels describe rule matching, not proof of audio effects.

**Desired Audio Profile**:
The exact managed audio outputs wanted for one classifier partition: at most one lossless output and one encoded output, with at least one present.
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
Observed lossless audio from which a requested output can be generated. Lossy audio is not a source for lossless reconstruction or a claimed quality upgrade.

**Conversion Mode**:
`strict` enforces Component-wide encoded consistency; `available_sources` preserves satisfied outputs and generates unsatisfied targets where qualified sources exist. Source ambiguity and safety conflicts remain blocking in either mode.

**Unknown Bitrate**:
An observed encoded file whose bitrate cannot be established, so it cannot be assumed to satisfy the target quality.

**Unmet Target**:
A desired output that cannot be supplied from available qualified sources in `available_sources` mode, with existing media preserved. It is disclosed separately from blocking conflicts and does not by itself prevent confirmation.

**Decision**:
A reviewable conclusion such as keep, delete or encode, with its reason. A keep decision requires no filesystem change.

**Operation**:
An executable filesystem change derived from a Plan's Decisions, such as encoding or deletion.
_Avoid_: Workset Operation when referring to a file change.

**Reconciliation**:
Comparison of observed variants with their Desired Audio Profile to produce Decisions and required Operations under the chosen Conversion Mode.
