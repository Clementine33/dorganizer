export interface ApiConfig {
  baseUrl: string
  token: string | null
}

export interface HealthResponse {
  ok: boolean
  version: string
}

export interface Library {
  id: string
  name: string
  root_path: string
  created_at: string
  updated_at: string
  last_scan_at: string | null
  last_scan_status: string
  last_scan_error: string
}

export interface CreateLibraryInput {
  name: string
  root_path: string
}

export interface UpdateLibraryInput {
  name?: string
  root_path?: string
}

/**
 * One direct child directory of a library root. Its identity is the
 * library-relative path — a rescan cannot renumber what the workbench
 * navigates by — and the audio count is a status fact: a directory without
 * audio is still listed and still browsable.
 */
export interface LibraryDir {
  name: string
  path: string
  rel_path: string
  /** The directory's navigation identity: what a page address carries. */
  dir_id: string
  audio_file_count: number
  file_count: number
}

export interface TreeNode {
  name: string
  path: string
  /** Path relative to the member root: what file management addresses. */
  rel_path: string
  type: 'dir' | 'file'
  size?: number
  bitrate: number | null
  format: string
  children?: TreeNode[]
}

/** The member tree read: cached from the inventory, or just re-scanned. */
export interface MemberTreeResponse {
  tree: TreeNode
  /** The identity the route resolved, and the member path it names. */
  dir_id: string
  member_path: string
  refreshed?: boolean
}

/** One item of a direct file-management request. */
export interface FileOperationItem {
  source: string
  name?: string
  target_dir?: string
}

export type FileOperation = 'rename' | 'move' | 'soft_delete'

export type FileItemStatus = 'ok' | 'failed' | 'skipped' | 'not_attempted'

export interface FileOperationItemResult {
  source: string
  status: FileItemStatus
  code?: string
  message?: string
  /** Where a rename or move put the item, member-relative. */
  target?: string
  /** Where a soft delete put the item, relative to the library root. */
  recovered_path?: string
}

/** The inventory refresh that follows a write; a failure here is reported, never hidden. */
export interface RefreshState {
  ok: boolean
  code?: string
  message?: string
}

export interface FileOperationResult {
  operation: FileOperation
  member_path: string
  items: FileOperationItemResult[]
  succeeded: number
  failed: number
  untouched: number
  refresh: RefreshState
}

export interface ScanEventData {
  stage: string
  message?: string
  scan_id?: string
  root_path?: string
  files_scanned?: number
  dirs_scanned?: number
  code?: string
}

export type ScanEventType = 'started' | 'progress' | 'completed' | 'cancelled' | 'error'

export interface ScanEvent {
  type: ScanEventType
  data: ScanEventData
}

// ==================== Policy slots ====================

export interface QualitySpec {
  kind: string
  bitrate?: number
}

export interface AudioOutputSpec {
  codec: string
  quality?: QualitySpec
}

export interface PolicyProfile {
  lossless?: AudioOutputSpec | null
  encoded?: AudioOutputSpec | null
}

/**
 * Wire shape of a resolved reconcile policy. classifier_tags are literal
 * content tags matched case-insensitively as substrings of each Album
 * Root-relative path (a hit → matched / 无音效).
 */
export type ConversionMode = 'strict' | 'available_sources'

export interface ResolvedPolicy {
  schema_version: number
  /** Conversion decision rule; missing = strict (server-side default). */
  mode?: ConversionMode
  classifier_tags: string[]
  matched: PolicyProfile
  unmatched: PolicyProfile
}

/** One of the three fixed global policy slots. policy is null while unconfigured. */
export interface PolicySlot {
  slot: number
  name: string
  policy: ResolvedPolicy | null
  updated_at?: string
}

export interface PolicySlotListResponse {
  slots: PolicySlot[]
}

export interface SavePolicySlotInput {
  name: string
  policy: ResolvedPolicy
}

export interface ClassifierCustomTag {
  id: number
  tag: string
  created_at?: string
}

export interface ClassifierTagLibraryResponse {
  default_tags: string[]
  custom_tags: ClassifierCustomTag[]
}

// ==================== Worksets and operations ====================

export type PlanningState = 'unplanned' | 'planned' | 'needs_planning' | 'planning' | 'orphaned'
export type GenerationStatus = 'queued' | 'running' | 'completed' | 'failed' | 'canceled' | 'interrupted'
export type OperationType = 'conversion'
/** The only publicly addressable operation type in this iteration. */
export const CONVERSION: OperationType = 'conversion'

export interface LibraryRef {
  library_id: string
  name: string
  root_path: string
}

export interface WorksetMember {
  member_id: string
  folder_path: string
  folder_name: string
  /** The durable library-relative path of the member directory. */
  rel_path: string
  /** The navigation identity of that path: what the file page addresses. */
  dir_id: string
}

/** Independent plan facts of one revision; they may overlap by design. */
export interface RevisionCounts {
  members: number
  changed: number
  unmet_targets: number
  blocked: number
  unchanged: number
}

export interface CurrentRevisionSummary {
  plan_id: string
  revision_index: number
  created_at: string
  status: string
  summary_reason: string
  counts: RevisionCounts
  validation_state: string
  stale: boolean | null
}

export interface GenerationProgress {
  generation_id: string
  status: GenerationStatus
  total_roots: number
  completed_roots: number
  current_root: string
  error_count: number
}

export interface GenerationSummary {
  generation_id: string
  status: GenerationStatus
  error_code: string
  error_message: string
  finished_at: string
}

/** Operation-scoped aggregate: the only carrier of planning state. */
export interface Operation {
  workset_id: string
  operation_type: OperationType
  version: number
  planning_state: PlanningState
  current_revision: CurrentRevisionSummary | null
  active_generation: GenerationProgress | null
  latest_generation: GenerationSummary | null
  active_execution: ExecutionProgress | null
  latest_execution: ExecutionRef | null
}

export interface Workset {
  workset_id: string
  title: string
  version: number
  library: LibraryRef | null
  members: WorksetMember[]
  operations: Operation[]
  updated_at: string
  created_at: string
}

export interface WorksetListResponse {
  worksets: Workset[]
  next_cursor?: string
}

/** Why a selected directory did not become a member of the record. */
export type SkippedReason =
  | 'no_audio'
  | 'missing'
  | 'recovery_dir'
  | 'duplicate'
  | 'invalid_path'
  | 'not_direct_child'

export interface SkippedFolder {
  path: string
  reason: SkippedReason
}

/** The create-or-replace payload of a library's current record. */
export interface CreateRecordInput {
  folder_paths: string[]
  /** The record the caller saw as current; absent when it saw none. */
  expected_current_id?: string
  title?: string
}

export interface CreateRecordResponse {
  workset: Workset
  created: boolean
  recorded: number
  skipped: SkippedFolder[]
}

/** Zero or one record: a library without one is an ordinary state, not an error. */
export interface CurrentRecordResponse {
  workset: Workset | null
}

export interface ListWorksetsParams {
  library_id?: string
  cursor?: string
  limit?: number
}

// ==================== Operation draft (sparse document) ====================

/** One shared output spec of a desired profile. */
export interface AudioOutputSpec {
  codec: string
  quality?: { kind: string; bitrate?: number }
}

export interface DesiredProfile {
  lossless?: AudioOutputSpec
  encoded?: AudioOutputSpec
}

/** The four override units. What an exception replaces, one group at a time. */
export type OverrideUnit = 'mode' | 'classifier_tags' | 'matched' | 'unmatched'

/**
 * A member's explicit values. A unit that is absent inherits the common value;
 * a unit that is present — including an explicitly empty tag array — is a real
 * override that later common changes never touch.
 */
export interface OverrideSet {
  mode?: 'strict' | 'available_sources'
  classifier_tags?: string[]
  matched?: DesiredProfile
  unmatched?: DesiredProfile
}

export interface DraftMember {
  member_id: string
  excluded?: boolean
  overrides?: OverrideSet
}

/** The persisted sparse draft document. */
export interface OperationDraftDocument {
  schema_version: number
  mode?: 'strict' | 'available_sources'
  /**
   * Obsolete-audio handling of the whole operation; absent means soft. It is
   * one global choice (never a per-member override) and is frozen into every
   * revision the draft produces.
   */
  delete_mode?: DeleteMode
  classifier_tags: string[]
  matched: DesiredProfile
  unmatched: DesiredProfile
  members: DraftMember[]
}

/** The wire shape of a persisted draft: document wrapped in its task envelope. */
export interface DraftEnvelopeResponse {
  workset_id: string
  operation_type: OperationType
  /** The OPERATION version: echo it back as If-Match on the next save. */
  version: number
  task: TaskEnvelope<OperationDraftDocument>
  updated_at: string
}

/** The unwrapped draft editors consume; the API client maps the envelope to it. */
export interface DraftResponse {
  workset_id: string
  operation_type: OperationType
  /** The OPERATION version: echo it back as If-Match on the next save. */
  version: number
  schema_version: number
  document: OperationDraftDocument
  updated_at: string
}

// ==================== Planning sessions ====================

export interface GenerationView {
  generation_id: string
  workset_id: string
  operation_type: OperationType
  status: GenerationStatus
  total_roots: number
  completed_roots: number
  current_root: string
  error_count: number
  revision_id: string
  error_code: string
  error_message: string
  started_at: string
  finished_at: string
  created_at: string
}

export interface RevisionListResponse {
  revisions: CurrentRevisionSummary[]
  /** Keyset cursor for the next (older) page; 0 when the oldest is included. */
  next_before_index?: number
}

export type StartGenerationResponse =
  | { created: true; generation: GenerationView }
  // Unchanged-input replay: current revision stands (200).
  | { created: false; revision: CurrentRevisionSummary }
  // Idempotency-key replay of an already-accepted request: the existing
  // session is returned (202).
  | { created: false; generation: GenerationView }

// Generation SSE events (session_snapshot payload is a GenerationView).
export type GenerationEvent =
  | { type: 'session_snapshot'; data: GenerationView }
  | { type: 'progress'; data: { generation_id: string; total_roots: number; completed_roots: number; current_root: string; error_count: number } }
  | { type: 'completed'; data: { generation_id: string; revision_id: string } }
  | { type: 'failed'; data: { generation_id: string; error_code: string; error_message: string } }
  | { type: 'canceled'; data: { generation_id: string } }
  | { type: 'interrupted'; data: { generation_id: string } }
  | { type: 'error'; data: { code: string; message: string } }

// ==================== Execution sessions ====================

export type DeleteMode = 'soft' | 'hard'
export type ExecutionStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'canceled' | 'interrupted'

/**
 * Task-owned frozen session options. The generic side stores and forwards
 * this payload untouched; the conversion task freezes the delete mode its
 * draft declared.
 */
export interface ExecutionOptions {
  delete_mode?: DeleteMode
}

/** Where a failing component stopped (only set on failure). */
export type ExecutionStage = 'precheck' | 'materialize' | 'validate' | 'commit' | 'remove'

/** Compact session reference attached to the operation and revision views. */
export interface ExecutionRef {
  execution_id: string
  plan_id: string
  status: ExecutionStatus
  error_code?: string
  error_message?: string
  finished_at?: string
}

/** Progress of the session currently queued/running for an operation. */
export interface ExecutionProgress {
  execution_id: string
  plan_id: string
  status: ExecutionStatus
  options?: ExecutionOptions
  total_components: number
  completed_components: number
  total_operations: number
  completed_operations: number
  current_root: string
  current_component_id: string
  current_phase: string
}

/**
 * One component's frozen work and observed outcome. `remaining` lists the
 * operations that did not complete (frozen order) and `recovery` the preserved
 * files an operator may need to look at.
 */
export interface ExecutionComponent {
  component_index: number
  component_id: string
  root_path: string
  partition: 'matched' | 'unmatched'
  status: 'pending' | 'succeeded' | 'failed' | 'canceled'
  stage?: ExecutionStage
  operations: number
  completed_operations: number
  committed: string[]
  removed: string[]
  remaining: string[]
  recovery: string[]
  error_code?: string
  error_message?: string
  inventory_synced: boolean
  inventory_sync_error?: string
}

/** The session detail payload; also the SSE snapshot. */
export interface ExecutionView {
  execution_id: string
  workset_id: string
  operation_type: OperationType
  plan_id: string
  status: ExecutionStatus
  options?: ExecutionOptions
  total_components: number
  completed_components: number
  total_operations: number
  completed_operations: number
  current_root: string
  current_component_id: string
  current_phase: string
  components: ExecutionComponent[]
  /** The scope this session ran: record-relative paths, absent for a whole
   *  revision. The folders it left out are the next plan's work. */
  selected_folders?: string[]
  error_code: string
  error_message: string
  started_at: string
  finished_at: string
  created_at: string
}

/** The progress event carries counts and the current position, never the
 *  components themselves: those arrive as `component` events, and a client that
 *  missed them reads the detail route. */
export interface ExecutionProgressEvent {
  execution_id: string
  status: ExecutionStatus
  total_components: number
  completed_components: number
  total_operations: number
  completed_operations: number
  current_root: string
  current_component_id: string
  current_phase: string
}

// Execution SSE events (execution_snapshot payload is an ExecutionView).
export type ExecutionEvent =
  | { type: 'execution_snapshot'; data: ExecutionView }
  /** One component's entry, as it appears in `components[]`, sent as its
   *  result lands; the next terminal event always follows the results. */
  | { type: 'component'; data: ExecutionComponent }
  | { type: 'progress'; data: ExecutionProgressEvent }
  | { type: 'succeeded'; data: { execution_id: string; plan_id: string } }
  | { type: 'failed'; data: { execution_id: string; error_code: string; error_message: string } }
  | { type: 'canceled'; data: { execution_id: string } }
  | { type: 'interrupted'; data: { execution_id: string } }
  | { type: 'error'; data: { code: string; message: string } }

export interface StartExecutionResponse {
  /** true only for a fresh 202; false when the idempotency key replayed (200). */
  created: boolean
  execution: ExecutionView
}

// ==================== Revision detail (immutable review) ====================

export interface RootValidation {
  root_index: number
  root_path: string
  root_status: string
  root_error_code: string
  root_error_message: string
  stale: boolean
  inventory_fingerprint: string
  entry_count: number
}

export interface ComponentRootRef {
  step_index: number
  component_index: number
  component_id: string
  root_index: number
}

export interface FileDecision {
  path: string
  resolution: string
  reason_code?: string
  message?: string
  target_path?: string
}

export interface VariantDecision {
  stem: string
  decisions: FileDecision[]
}

export interface LaneDecision {
  lane: string
  decision: string
  reason_code?: string
  message?: string
}

export interface PlanOperation {
  kind: string
  phase: string
  component_id: string
  variant_stem: string
  source_path: string
  target_path?: string
  depends_on?: string[]
}

export interface ComponentOutcome {
  component_id: string
  partition: 'matched' | 'unmatched'
  status: 'ok' | 'blocked'
  reason_code?: string
  message?: string
  lanes: LaneDecision[]
  variant_decisions: VariantDecision[]
  operations: PlanOperation[]
  projected_inventory: string[]
  files: { path: string; size: number; mtime: number }[]
}

export interface StepSummary {
  component_count: number
  blocked_count: number
  operation_count: number
  error_count: number
  /** Stems kept with an unsatisfied target (available_sources mode). */
  unmet_targets?: number
  summary_reason: string
}

export interface ClassifierSnapshot {
  tags?: string[]
  hash?: string
}

/**
 * Opaque task envelope: the generic side names the task kind and its payload
 * schema version; everything inside `payload` belongs to the task.
 */
export interface TaskEnvelope<P> {
  kind: string
  schema_version: number
  payload: P
}

/** The conversion task's reviewable plan payload. */
export interface ConversionPlanPayload {
  policy: unknown
  policy_hash: string
  classifier: ClassifierSnapshot
  summary: StepSummary
  components: ComponentOutcome[]
}

/** One member of a frozen revision: effective settings plus unit provenance. */
export interface RevisionMember {
  member_id: string
  member_name: string
  folder_path: string
  excluded: boolean
  effective: {
    schema_version: number
    mode?: string
    classifier_tags?: string[]
    matched?: DesiredProfile
    unmatched?: DesiredProfile
  }
  /** unit -> 'common' | 'member'. Equal values with different sources stay apart. */
  sources: Record<string, string>
}

export interface RevisionDetailResponse {
  plan_id: string
  revision_index: number
  created_at: string
  root_path: string
  snapshot_token: string
  status: string
  summary: {
    operation_count: number
    error_count: number
    total_count: number
    actionable_count: number
    summary_reason: string
  }
  /** The plan snapshot, wrapped in its task envelope. */
  task: TaskEnvelope<ConversionPlanPayload>
  counts: RevisionCounts
  /** Frozen per-member effective settings and inheritance sources. */
  members: RevisionMember[]
  roots: RootValidation[]
  component_roots: ComponentRootRef[]
  /** The session that ran this revision, if any (a revision runs at most once). */
  execution: ExecutionRef | null
}

export interface ApiClientContract {
  getHealth(signal?: AbortSignal): Promise<HealthResponse>
  listLibraries(signal?: AbortSignal): Promise<Library[]>
  getLibrary(id: string, signal?: AbortSignal): Promise<Library>
  createLibrary(input: CreateLibraryInput): Promise<Library>
  updateLibrary(id: string, input: UpdateLibraryInput): Promise<Library>
  deleteLibrary(id: string): Promise<void>
  scanLibrary(id: string, signal: AbortSignal, rootPath?: string): AsyncIterable<ScanEvent>
  listDirs(libraryId: string, signal?: AbortSignal): Promise<LibraryDir[]>
  getMemberTree(libraryId: string, folderRelPath: string, signal?: AbortSignal): Promise<MemberTreeResponse>
  refreshMemberTree(libraryId: string, folderRelPath: string): Promise<MemberTreeResponse>
  applyFileOperation(
    libraryId: string,
    input: { member_path: string; operation: FileOperation; items: FileOperationItem[] },
  ): Promise<FileOperationResult>
  listPolicySlots(signal?: AbortSignal): Promise<PolicySlot[]>
  savePolicySlot(slot: number, input: SavePolicySlotInput): Promise<PolicySlot>
  listClassifierTags(signal?: AbortSignal): Promise<ClassifierTagLibraryResponse>
  addClassifierTag(tag: string): Promise<ClassifierCustomTag>
  deleteClassifierTag(id: number): Promise<void>
  getCurrentRecord(
    libraryId: string,
    operation: OperationType,
    signal?: AbortSignal,
  ): Promise<CurrentRecordResponse>
  createCurrentRecord(
    libraryId: string,
    operation: OperationType,
    input: CreateRecordInput,
    idempotencyKey: string,
  ): Promise<CreateRecordResponse>
  listWorksets(params?: ListWorksetsParams, signal?: AbortSignal): Promise<WorksetListResponse>
  getWorkset(id: string, signal?: AbortSignal): Promise<Workset>
  getOperation(worksetId: string, operation: OperationType, signal?: AbortSignal): Promise<Operation>
  getOperationDraft(worksetId: string, operation: OperationType, signal?: AbortSignal): Promise<DraftResponse>
  saveOperationDraft(
    worksetId: string,
    operation: OperationType,
    document: OperationDraftDocument,
    ifMatchVersion: number,
  ): Promise<Operation>
  startGeneration(
    worksetId: string,
    operation: OperationType,
    ifMatchVersion: number,
    idempotencyKey: string,
  ): Promise<StartGenerationResponse>
  getGeneration(
    worksetId: string,
    operation: OperationType,
    generationId: string,
    signal?: AbortSignal,
  ): Promise<GenerationView>
  cancelGeneration(worksetId: string, operation: OperationType, generationId: string): Promise<GenerationView>
  streamGenerationEvents(
    worksetId: string,
    operation: OperationType,
    generationId: string,
    signal: AbortSignal,
  ): AsyncIterable<GenerationEvent>
  getRevision(
    worksetId: string,
    operation: OperationType,
    planId: string,
    signal?: AbortSignal,
  ): Promise<RevisionDetailResponse>
  startExecution(
    worksetId: string,
    operation: OperationType,
    planId: string,
    input: { ifMatchVersion: number; idempotencyKey: string; folderPaths?: string[] },
  ): Promise<StartExecutionResponse>
  getExecution(
    worksetId: string,
    operation: OperationType,
    executionId: string,
    signal?: AbortSignal,
    page?: { from: number; limit: number },
  ): Promise<ExecutionView>
  cancelExecution(
    worksetId: string,
    operation: OperationType,
    executionId: string,
  ): Promise<ExecutionView>
  streamExecutionEvents(
    worksetId: string,
    operation: OperationType,
    executionId: string,
    signal: AbortSignal,
  ): AsyncIterable<ExecutionEvent>
}
