import type { InjectionKey } from 'vue'
import { inject } from 'vue'
import { parseSSEStream, type SSEEvent } from './sse'
import type {
  ApiClientContract,
  ApiConfig,
  ClassifierCustomTag,
  ClassifierTagLibraryResponse,
  CreateLibraryInput,
  CreateRecordInput,
  CreateRecordResponse,
  CurrentRecordResponse,
  FileOperation,
  FileOperationItem,
  FileOperationResult,
  LibraryDir,
  MemberTreeResponse,
  DraftEnvelopeResponse,
  DraftResponse,
  ExecutionEvent,
  ExecutionView,
  GenerationEvent,
  GenerationView,
  HealthResponse,
  Library,
  ListWorksetsParams,
  Operation,
  OperationDraftDocument,
  OperationType,
  PolicySlot,
  RevisionDetailResponse,
  SavePolicySlotInput,
  ScanEvent,
  StartExecutionResponse,
  StartGenerationResponse,
  UpdateLibraryInput,
  Workset,
  WorksetListResponse,
} from './types'

interface ErrorEnvelope {
  code?: string
  message?: string
  details?: string[]
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  /** Machine-readable reason codes (e.g. PLAN_NOT_CONFIRMABLE details). */
  readonly details: string[]

  constructor(status: number, code: string, message: string, details: string[] = []) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
  }
}

export class ApiClient implements ApiClientContract {
  private readonly baseUrl: string
  private readonly token: string | null

  constructor(config: ApiConfig) {
    this.baseUrl = config.baseUrl.replace(/\/+$/, '')
    this.token = config.token
  }

  getHealth(signal?: AbortSignal): Promise<HealthResponse> {
    return this.request('/health', { signal })
  }

  async listLibraries(signal?: AbortSignal): Promise<Library[]> {
    return (await this.request<{ libraries: Library[] }>('/libraries', { signal })).libraries
  }

  getLibrary(id: string, signal?: AbortSignal): Promise<Library> {
    return this.request(`/libraries/${encodeURIComponent(id)}`, { signal })
  }

  createLibrary(input: CreateLibraryInput): Promise<Library> {
    return this.request('/libraries', { method: 'POST', body: input })
  }

  updateLibrary(id: string, input: UpdateLibraryInput): Promise<Library> {
    return this.request(`/libraries/${encodeURIComponent(id)}`, { method: 'PATCH', body: input })
  }

  deleteLibrary(id: string): Promise<void> {
    return this.request(`/libraries/${encodeURIComponent(id)}`, { method: 'DELETE' })
  }

  async *scanLibrary(id: string, signal: AbortSignal, rootPath?: string): AsyncGenerator<ScanEvent> {
    const body = rootPath === undefined ? {} : { root_path: rootPath }
    const response = await fetch(this.url(`/libraries/${encodeURIComponent(id)}/scans`), {
      method: 'POST',
      headers: this.headers(true),
      body: JSON.stringify(body),
      signal,
    })
    if (!response.ok) throw await this.toApiError(response)
    if (!response.body) {
      throw new ApiError(response.status, 'STREAM_UNAVAILABLE', 'Scan response did not include a stream')
    }
    yield* parseSSEStream<ScanEvent>(response.body)
  }

  async listDirs(libraryId: string, signal?: AbortSignal): Promise<LibraryDir[]> {
    const result = await this.request<{ dirs: LibraryDir[] }>(
      `/libraries/${encodeURIComponent(libraryId)}/dirs`,
      { signal },
    )
    return result.dirs
  }

  /** The stored member tree: what the last scan recorded for this directory. */
  getMemberTree(libraryId: string, dirId: string, signal?: AbortSignal): Promise<MemberTreeResponse> {
    return this.request(
      `/libraries/${encodeURIComponent(libraryId)}/tree?dir=${encodeURIComponent(dirId)}`,
      { signal },
    )
  }

  /**
   * Re-scan one member directory and answer with the refreshed tree. A refresh
   * is a scan: it is refused while a file operation holds the admission slot,
   * and it refuses one in turn.
   */
  refreshMemberTree(libraryId: string, dirId: string): Promise<MemberTreeResponse> {
    return this.request(
      `/libraries/${encodeURIComponent(libraryId)}/tree/refresh?dir=${encodeURIComponent(dirId)}`,
      { method: 'POST', body: {}, timeoutMs: 60_000 },
    )
  }

  /**
   * Direct file management inside one member: single rename, single move, or a
   * batch soft delete. The whole request is refused while a scan, planning
   * session or execution is running.
   */
  applyFileOperation(
    libraryId: string,
    input: { member_path: string; operation: FileOperation; items: FileOperationItem[] },
  ): Promise<FileOperationResult> {
    return this.request(`/libraries/${encodeURIComponent(libraryId)}/file-operations`, {
      method: 'POST',
      body: input,
      timeoutMs: 60_000,
    })
  }

  // ==================== Policy slots ====================

  listPolicySlots(signal?: AbortSignal): Promise<PolicySlot[]> {
    return this.request<{ slots: PolicySlot[] }>('/policy-slots', { signal }).then((r) => r.slots)
  }

  savePolicySlot(slot: number, input: SavePolicySlotInput): Promise<PolicySlot> {
    return this.request(`/policy-slots/${slot}`, { method: 'PUT', body: input })
  }

  // ==================== Classifier tag library ====================

  listClassifierTags(signal?: AbortSignal): Promise<ClassifierTagLibraryResponse> {
    return this.request<ClassifierTagLibraryResponse>('/classifier-tags', { signal })
  }

  addClassifierTag(tag: string): Promise<ClassifierCustomTag> {
    return this.request<ClassifierCustomTag>('/classifier-tags', { method: 'POST', body: { tag } })
  }

  deleteClassifierTag(id: number): Promise<void> {
    return this.request(`/classifier-tags/${id}`, { method: 'DELETE' })
  }

  // ==================== Worksets ====================

  /** The library's current record for one operation, or null when it has none. */
  async getCurrentRecord(
    libraryId: string,
    operation: OperationType,
    signal?: AbortSignal,
  ): Promise<CurrentRecordResponse> {
    return this.request(
      `/libraries/${encodeURIComponent(libraryId)}/operations/${encodeURIComponent(operation)}/current`,
      { signal },
    )
  }

  /**
   * Create the current record of one (library, operation) pair, replacing the
   * record the caller saw as current. The idempotency key makes a retried
   * request answer with the record it already created.
   */
  createCurrentRecord(
    libraryId: string,
    operation: OperationType,
    input: CreateRecordInput,
    idempotencyKey: string,
  ): Promise<CreateRecordResponse> {
    return this.request(
      `/libraries/${encodeURIComponent(libraryId)}/operations/${encodeURIComponent(operation)}/current`,
      {
        method: 'PUT',
        body: input,
        headers: { 'Idempotency-Key': idempotencyKey },
        // The backend re-reads the scanned inventory for the whole selection
        // inside this request.
        timeoutMs: 60_000,
      },
    )
  }

  listWorksets(params: ListWorksetsParams = {}, signal?: AbortSignal): Promise<WorksetListResponse> {
    const query = new URLSearchParams()
    if (params.library_id) query.set('library_id', params.library_id)
    if (params.cursor) query.set('cursor', params.cursor)
    if (params.limit !== undefined) query.set('limit', String(params.limit))
    const qs = query.toString()
    return this.request(`/worksets${qs ? `?${qs}` : ''}`, { signal })
  }

  getWorkset(id: string, signal?: AbortSignal): Promise<Workset> {
    return this.request(`/worksets/${encodeURIComponent(id)}`, { signal })
  }

  getOperation(worksetId: string, operation: OperationType, signal?: AbortSignal): Promise<Operation> {
    return this.request(this.operationPath(worksetId, operation), { signal })
  }

  async getOperationDraft(
    worksetId: string,
    operation: OperationType,
    signal?: AbortSignal,
  ): Promise<DraftResponse> {
    const wire = await this.request<DraftEnvelopeResponse>(
      `${this.operationPath(worksetId, operation)}/draft`,
      { signal },
    )
    const payload = wire.task.payload
    return {
      workset_id: wire.workset_id,
      operation_type: wire.operation_type,
      version: wire.version,
      schema_version: wire.task.schema_version,
      // The stored document is sparse: absent keys mean "no records", and the
      // editors rely on the arrays being present.
      document: {
        ...payload,
        classifier_tags: payload.classifier_tags ?? [],
        members: payload.members ?? [],
      },
      updated_at: wire.updated_at,
    }
  }

  /** Full replacement of the sparse draft; If-Match is the operation version. */
  saveOperationDraft(
    worksetId: string,
    operation: OperationType,
    document: OperationDraftDocument,
    ifMatchVersion: number,
  ): Promise<Operation> {
    return this.request(`${this.operationPath(worksetId, operation)}/draft`, {
      method: 'PUT',
      body: document,
      headers: { 'If-Match': String(ifMatchVersion) },
    })
  }

  startGeneration(
    worksetId: string,
    operation: OperationType,
    ifMatchVersion: number,
    idempotencyKey: string,
  ): Promise<StartGenerationResponse> {
    // Not a pure enqueue: the backend's dedup fast path recomputes live
    // inventory fingerprints for every member root inside the request, which
    // can exceed the read-path timeout on large worksets. Aborting would
    // surface a false failure AND lose this idempotency key's protection
    // (a retry with a fresh key could start a duplicate session).
    return this.request(`${this.operationPath(worksetId, operation)}/revisions`, {
      method: 'POST',
      headers: { 'If-Match': String(ifMatchVersion), 'Idempotency-Key': idempotencyKey },
      timeoutMs: 60_000,
    })
  }

  getGeneration(
    worksetId: string,
    operation: OperationType,
    generationId: string,
    signal?: AbortSignal,
  ): Promise<GenerationView> {
    return this.request(
      `${this.operationPath(worksetId, operation)}/planning-sessions/${encodeURIComponent(generationId)}`,
      { signal },
    )
  }

  cancelGeneration(worksetId: string, operation: OperationType, generationId: string): Promise<GenerationView> {
    return this.request(
      `${this.operationPath(worksetId, operation)}/planning-sessions/${encodeURIComponent(generationId)}/cancel`,
      { method: 'POST', body: {} },
    )
  }

  getRevision(
    worksetId: string,
    operation: OperationType,
    planId: string,
    signal?: AbortSignal,
  ): Promise<RevisionDetailResponse> {
    return this.request(
      `${this.operationPath(worksetId, operation)}/revisions/${encodeURIComponent(planId)}`,
      { signal },
    )
  }

  /**
   * Enqueue the execution of the operation's current revision, optionally
   * scoped to some of its member folders. The file worklist and the session
   * options are the frozen revision's, never the client's; `folderPaths` says
   * how much of that revision this session covers, and an empty list runs all
   * of it. Disconnecting never cancels the session — only cancelExecution or
   * the backend process lifecycle does.
   */
  startExecution(
    worksetId: string,
    operation: OperationType,
    planId: string,
    input: { ifMatchVersion: number; idempotencyKey: string; folderPaths?: string[] },
  ): Promise<StartExecutionResponse> {
    return this.request(
      `${this.operationPath(worksetId, operation)}/revisions/${encodeURIComponent(planId)}/executions`,
      {
        method: 'POST',
        headers: { 'If-Match': String(input.ifMatchVersion), 'Idempotency-Key': input.idempotencyKey },
        body: { folder_paths: input.folderPaths ?? [] },
      },
    )
  }

  /** Session detail. `page` reads a slice of the component list, which is
   *  ordered by component index; without it the whole list comes back. */
  getExecution(
    worksetId: string,
    operation: OperationType,
    executionId: string,
    signal?: AbortSignal,
    page?: { from: number; limit: number },
  ): Promise<ExecutionView> {
    const query = page
      ? `?components_from=${page.from}&components_limit=${page.limit}`
      : ''
    return this.request(
      `${this.operationPath(worksetId, operation)}/executions/${encodeURIComponent(executionId)}${query}`,
      { signal },
    )
  }

  /** Cooperative cancel; idempotent on terminal sessions (200 either way). */
  cancelExecution(
    worksetId: string,
    operation: OperationType,
    executionId: string,
  ): Promise<ExecutionView> {
    return this.request(
      `${this.operationPath(worksetId, operation)}/executions/${encodeURIComponent(executionId)}/cancel`,
      { method: 'POST', body: {} },
    )
  }

  /** Every mutable planning resource is addressed through its operation. */
  private operationPath(worksetId: string, operation: OperationType): string {
    return `/worksets/${encodeURIComponent(worksetId)}/operations/${encodeURIComponent(operation)}`
  }

  streamGenerationEvents(
    worksetId: string,
    operation: OperationType,
    generationId: string,
    signal: AbortSignal,
  ): AsyncGenerator<GenerationEvent> {
    return this.streamEvents<GenerationEvent>(
      `${this.operationPath(worksetId, operation)}/planning-sessions/${encodeURIComponent(generationId)}/events`,
      signal,
      'Generation events',
    )
  }

  streamExecutionEvents(
    worksetId: string,
    operation: OperationType,
    executionId: string,
    signal: AbortSignal,
  ): AsyncGenerator<ExecutionEvent> {
    return this.streamEvents<ExecutionEvent>(
      `${this.operationPath(worksetId, operation)}/executions/${encodeURIComponent(executionId)}/events`,
      signal,
      'Execution events',
    )
  }

  /** Shared GET-SSE lifecycle: fetch, error envelope, then the parsed stream. */
  private async *streamEvents<T extends SSEEvent>(path: string, signal: AbortSignal, surface: string): AsyncGenerator<T> {
    const response = await fetch(this.url(path), { headers: this.headers(false), signal })
    if (!response.ok) throw await this.toApiError(response)
    if (!response.body) {
      throw new ApiError(response.status, 'STREAM_UNAVAILABLE', `${surface} response did not include a stream`)
    }
    yield* parseSSEStream<T>(response.body)
  }

  private url(path: string): string {
    return `${this.baseUrl}${path}`
  }

  private headers(json = false): Headers {
    const headers = new Headers({ Accept: 'application/json' })
    if (json) headers.set('Content-Type', 'application/json')
    if (this.token) headers.set('Authorization', `Bearer ${this.token}`)
    return headers
  }

  // Safety net for a hung connection (local backend): without it a request
  // that never resolves leaves query UI stuck on an infinite pending state
  // (e.g. the folder-tree page waiting on a never-settling libraries list).
  // The SSE scan stream is not routed through request() and is unaffected.
  // Mutations that trigger long server-side work override this via timeoutMs.
  private static readonly REQUEST_TIMEOUT_MS = 15_000

  private async request<T>(
    path: string,
    options: { method?: string; body?: unknown; signal?: AbortSignal; timeoutMs?: number; headers?: Record<string, string> } = {},
  ): Promise<T> {
    const controller = new AbortController()
    let removeAbort: (() => void) | null = null
    if (options.signal) {
      if (options.signal.aborted) {
        // Caller already cancelled: still issue the fetch so it rejects
        // immediately with the aborted reason rather than hanging.
        controller.abort(options.signal.reason)
      } else {
        const forwardAbort = () => controller.abort(options.signal?.reason)
        options.signal.addEventListener('abort', forwardAbort, { once: true })
        removeAbort = () => options.signal?.removeEventListener('abort', forwardAbort)
      }
    }
    const timer = setTimeout(
      () => controller.abort(new DOMException('请求超时', 'TimeoutError')),
      options.timeoutMs ?? ApiClient.REQUEST_TIMEOUT_MS,
    )
    try {
      return await this.doRequest<T>(path, options, controller.signal)
    } finally {
      clearTimeout(timer)
      removeAbort?.()
    }
  }

  private async doRequest<T>(
    path: string,
    options: { method?: string; body?: unknown; headers?: Record<string, string> },
    signal: AbortSignal,
  ): Promise<T> {
    const response = await fetch(this.url(path), {
      method: options.method ?? 'GET',
      headers: { ...this.headers(options.body !== undefined), ...options.headers },
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal,
    })
    if (!response.ok) throw await this.toApiError(response)
    if (response.status === 204) return undefined as T
    return (await response.json()) as T
  }

  private async toApiError(response: Response): Promise<ApiError> {
    let envelope: ErrorEnvelope = {}
    try {
      envelope = (await response.json()) as ErrorEnvelope
    } catch {
      // A proxy can replace the documented JSON envelope; retain useful status context.
    }
    return new ApiError(
      response.status,
      envelope.code ?? 'HTTP_ERROR',
      envelope.message ?? `Request failed with status ${response.status}`,
      envelope.details ?? [],
    )
  }
}

export const apiClientKey: InjectionKey<ApiClientContract> = Symbol('api-client')

export function useApiClient(): ApiClientContract {
  const client = inject(apiClientKey)
  if (!client) throw new Error('ApiClient was not provided')
  return client
}
