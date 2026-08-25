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

export interface CreatePlanInput {
  library_id: string
  folder_ids?: string[]
  source_files?: string[]
  plan_type?: 'slim' | 'prune' | 'single_delete' | 'single_convert'
  target_format?: string
  prune_matched_excluded?: boolean
}

export interface PlanOperation {
  type: string
  source_path: string
  target_path: string
}

export interface PlanFolderError {
  folder_path: string
  code: string
  message: string
  retryable: boolean
}

export interface PlanSummary {
  operation_count: number
  error_count: number
  total_count: number
  actionable_count: number
  summary_reason: 'ACTIONABLE' | 'NO_MATCH' | 'GLOBAL_SHORT_CIRCUIT'
}

export interface PlanResponse {
  plan_id: string
  snapshot_token: string
  root_path: string
  summary: PlanSummary
  operations: PlanOperation[]
  errors: PlanFolderError[]
  successful_folders: string[]
}

export interface PlanInfo {
  plan_id: string
  root_path: string
  plan_type: string
  status: string
  created_at: string
}

export interface ApiClientContract {
  getHealth(): Promise<HealthResponse>
  listLibraries(): Promise<Library[]>
  getLibrary(id: string): Promise<Library>
  createLibrary(input: CreateLibraryInput): Promise<Library>
  updateLibrary(id: string, input: UpdateLibraryInput): Promise<Library>
  deleteLibrary(id: string): Promise<void>
  scanLibrary(id: string, signal: AbortSignal, rootPath?: string): AsyncIterable<ScanEvent>
  listFolders(libraryId: string): Promise<Folder[]>
  getFolderTree(libraryId: string, folderId: string): Promise<TreeNode>
  createPlan(input: CreatePlanInput): Promise<PlanResponse>
  listPlans(libraryId?: string, limit?: number): Promise<PlanInfo[]>
}
