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

export interface Folder {
  id: string
  name: string
  path: string
  relative_path: string
  audio_file_count: number
}

export interface TreeNode {
  name: string
  path: string
  type: 'dir' | 'file'
  size?: number
  bitrate: number | null
  format: string
  children?: TreeNode[]
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
export interface ResolvedPolicy {
  schema_version: number
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

// ==================== Worksets ====================

export type PlanningState = 'unplanned' | 'planned' | 'needs_planning' | 'planning' | 'orphaned'
export type MemberState = 'planned' | 'pending' | 'missing'
export type GenerationStatus = 'queued' | 'running' | 'completed' | 'failed' | 'canceled' | 'interrupted'

export interface LibraryRef {
  library_id: string
  name: string
  root_path: string
}

export interface WorksetMember {
  folder_id: string
  folder_path: string
  folder_name: string
  rel_path: string
  state: MemberState
}

export interface CurrentRevisionSummary {
  plan_id: string
  revision_index: number
  created_at: string
  status: string
  summary_reason: string
  blocked_count: number
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

export interface Workset {
  workset_id: string
  title: string
  version: number
  library: LibraryRef | null
  planning_state: PlanningState
  current_revision: CurrentRevisionSummary | null
  active_generation: GenerationProgress | null
  latest_generation: GenerationSummary | null
  members: WorksetMember[]
  updated_at: string
  created_at: string
}

export interface WorksetListResponse {
  worksets: Workset[]
  next_cursor?: string
}

export interface CreateWorksetInput {
  library_id: string
  title: string
  folder_ids: string[]
}

export type WorksetFeedFilter = 'all' | 'pending' | 'normal' | 'error'

export interface ListWorksetsParams {
  library_id?: string
  feed?: WorksetFeedFilter
  cursor?: string
  limit?: number
}

// ==================== Workflow draft ====================

export type PolicySourceWire = { kind: 'inline'; policy: ResolvedPolicy }

export interface WorkflowStepInput {
  step_type: string
  policy: PolicySourceWire
}

export interface WorkflowInput {
  schema_version: number
  steps: WorkflowStepInput[]
}

export interface DraftResponse {
  workset_id: string
  version: number
  workflow_schema_version: number
  workflow: WorkflowInput
  updated_at: string
}

// ==================== Planning sessions ====================

export interface GenerationView {
  generation_id: string
  workset_id: string
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

export interface StartGenerationInput {
  expected_draft_version?: number
}

// Generation SSE events (session_snapshot payload is a GenerationView).
export type GenerationEvent =
  | { type: 'session_snapshot'; data: GenerationView }
  | { type: 'progress'; data: { generation_id: string; total_roots: number; completed_roots: number; current_root: string; error_count: number } }
  | { type: 'completed'; data: { generation_id: string; revision_id: string } }
  | { type: 'failed'; data: { generation_id: string; error_code: string; error_message: string } }
  | { type: 'canceled'; data: { generation_id: string } }
  | { type: 'interrupted'; data: { generation_id: string } }
  | { type: 'error'; data: { code: string; message: string } }

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

export interface WorkflowOperation {
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
  operations: WorkflowOperation[]
  projected_inventory: string[]
  files: { path: string; size: number; mtime: number }[]
}

export interface StepSummary {
  component_count: number
  blocked_count: number
  operation_count: number
  error_count: number
  summary_reason: string
}

export interface ClassifierSnapshot {
  tags?: string[]
  hash?: string
}

export interface WorkflowStepDetail {
  step_type: string
  step_index: number
  status: string
  policy: unknown
  policy_hash: string
  classifier: ClassifierSnapshot
  summary: StepSummary
  components: ComponentOutcome[]
}

export interface WorkflowPlanDetail {
  plan_id: string
  snapshot_token: string
  root_path: string
  plan_kind: string
  summary: {
    operation_count: number
    error_count: number
    total_count: number
    actionable_count: number
    summary_reason: string
  }
  steps: WorkflowStepDetail[]
}

export interface RevisionDetailResponse {
  plan_id: string
  revision_index: number
  created_at: string
  roots: RootValidation[]
  component_roots: ComponentRootRef[]
  workflow: WorkflowPlanDetail
}

export interface ApiClientContract {
  getHealth(signal?: AbortSignal): Promise<HealthResponse>
  listLibraries(signal?: AbortSignal): Promise<Library[]>
  getLibrary(id: string, signal?: AbortSignal): Promise<Library>
  createLibrary(input: CreateLibraryInput): Promise<Library>
  updateLibrary(id: string, input: UpdateLibraryInput): Promise<Library>
  deleteLibrary(id: string): Promise<void>
  scanLibrary(id: string, signal: AbortSignal, rootPath?: string): AsyncIterable<ScanEvent>
  listFolders(libraryId: string, signal?: AbortSignal): Promise<Folder[]>
  getFolderTree(libraryId: string, folderId: string, signal?: AbortSignal): Promise<TreeNode>
  listPolicySlots(signal?: AbortSignal): Promise<PolicySlot[]>
  savePolicySlot(slot: number, input: SavePolicySlotInput): Promise<PolicySlot>
  listClassifierTags(signal?: AbortSignal): Promise<ClassifierTagLibraryResponse>
  addClassifierTag(tag: string): Promise<ClassifierCustomTag>
  deleteClassifierTag(id: number): Promise<void>
  createWorkset(input: CreateWorksetInput, idempotencyKey: string): Promise<{ workset: Workset; created: boolean }>
  listWorksets(params?: ListWorksetsParams, signal?: AbortSignal): Promise<WorksetListResponse>
  getWorkset(id: string, signal?: AbortSignal): Promise<Workset>
  getWorksetDraft(id: string, signal?: AbortSignal): Promise<DraftResponse>
  saveWorksetDraft(id: string, workflow: WorkflowInput, ifMatchVersion: number): Promise<Workset>
  startGeneration(id: string, input: StartGenerationInput, idempotencyKey: string): Promise<StartGenerationResponse>
  getGeneration(worksetId: string, generationId: string, signal?: AbortSignal): Promise<GenerationView>
  cancelGeneration(worksetId: string, generationId: string): Promise<GenerationView>
  streamGenerationEvents(worksetId: string, generationId: string, signal: AbortSignal): AsyncIterable<GenerationEvent>
  listRevisions(worksetId: string, limit?: number, beforeIndex?: number, signal?: AbortSignal): Promise<RevisionListResponse>
  getRevision(worksetId: string, planId: string, signal?: AbortSignal): Promise<RevisionDetailResponse>
}
