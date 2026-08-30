import type { InjectionKey } from 'vue'
import { inject } from 'vue'
import { parseSSEStream } from './sse'
import type {
  ApiClientContract,
  ApiConfig,
  CreateLibraryInput,
  CreateWorksetInput,
  DraftResponse,
  Folder,
  GenerationEvent,
  GenerationView,
  HealthResponse,
  Library,
  ListWorksetsParams,
  RevisionDetailResponse,
  RevisionListResponse,
  ScanEvent,
  StartGenerationInput,
  StartGenerationResponse,
  TreeNode,
  UpdateLibraryInput,
  WorkflowInput,
  Workset,
  WorksetListResponse,
  WorkflowPreset,
  CurrentRevisionSummary,
} from './types'

interface ErrorEnvelope {
  code?: string
  message?: string
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
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

  async listFolders(libraryId: string, signal?: AbortSignal): Promise<Folder[]> {
    const result = await this.request<{ folders: Folder[] }>(
      `/libraries/${encodeURIComponent(libraryId)}/folders`,
      { signal },
    )
    return result.folders
  }

  async getFolderTree(
    libraryId: string,
    folderId: string,
    signal?: AbortSignal,
  ): Promise<TreeNode> {
    const result = await this.request<{ tree: TreeNode }>(
      `/libraries/${encodeURIComponent(libraryId)}/folders/${encodeURIComponent(folderId)}/tree`,
      { signal },
    )
    return result.tree
  }

  // ==================== Workflow presets ====================

  listWorkflowPresets(signal?: AbortSignal): Promise<WorkflowPreset[]> {
    return this.request<{ presets: WorkflowPreset[] }>('/workflow-presets', { signal }).then((r) => r.presets)
  }

  // ==================== Worksets ====================

  createWorkset(input: CreateWorksetInput, idempotencyKey: string): Promise<{ workset: Workset; created: boolean }> {
    return this.request('/worksets', {
      method: 'POST',
      body: input,
      headers: { 'Idempotency-Key': idempotencyKey },
    })
  }

  listWorksets(params: ListWorksetsParams = {}, signal?: AbortSignal): Promise<WorksetListResponse> {
    const query = new URLSearchParams()
    if (params.library_id) query.set('library_id', params.library_id)
    if (params.feed) query.set('feed', params.feed)
    if (params.cursor) query.set('cursor', params.cursor)
    if (params.limit !== undefined) query.set('limit', String(params.limit))
    const qs = query.toString()
    return this.request(`/worksets${qs ? `?${qs}` : ''}`, { signal })
  }

  getWorkset(id: string, signal?: AbortSignal): Promise<Workset> {
    return this.request(`/worksets/${encodeURIComponent(id)}`, { signal })
  }

  getWorksetDraft(id: string, signal?: AbortSignal): Promise<DraftResponse> {
    return this.request(`/worksets/${encodeURIComponent(id)}/draft`, { signal })
  }

  saveWorksetDraft(id: string, workflow: WorkflowInput, ifMatchVersion: number): Promise<Workset> {
    return this.request(`/worksets/${encodeURIComponent(id)}/draft`, {
      method: 'PUT',
      body: { workflow },
      headers: { 'If-Match': String(ifMatchVersion) },
    })
  }

  startGeneration(id: string, input: StartGenerationInput, idempotencyKey: string): Promise<StartGenerationResponse> {
    // Not a pure enqueue: the backend's dedup fast path recomputes live
    // inventory fingerprints for every member root inside the request, which
    // can exceed the read-path timeout on large worksets. Aborting would
    // surface a false failure AND lose this idempotency key's protection
    // (a retry with a fresh key could start a duplicate session).
    return this.request(`/worksets/${encodeURIComponent(id)}/revisions`, {
      method: 'POST',
      body: input,
      headers: { 'Idempotency-Key': idempotencyKey },
      timeoutMs: 60_000,
    })
  }

  getGeneration(worksetId: string, generationId: string, signal?: AbortSignal): Promise<GenerationView> {
    return this.request(
      `/worksets/${encodeURIComponent(worksetId)}/planning-sessions/${encodeURIComponent(generationId)}`,
      { signal },
    )
  }

  cancelGeneration(worksetId: string, generationId: string): Promise<GenerationView> {
    return this.request(
      `/worksets/${encodeURIComponent(worksetId)}/planning-sessions/${encodeURIComponent(generationId)}/cancel`,
      { method: 'POST', body: {} },
    )
  }

  listRevisions(worksetId: string, limit = 50, signal?: AbortSignal): Promise<CurrentRevisionSummary[]> {
    return this.request<RevisionListResponse>(
      `/worksets/${encodeURIComponent(worksetId)}/revisions?limit=${limit}`,
      { signal },
    ).then((r) => r.revisions)
  }

  getRevision(worksetId: string, planId: string, signal?: AbortSignal): Promise<RevisionDetailResponse> {
    return this.request(
      `/worksets/${encodeURIComponent(worksetId)}/revisions/${encodeURIComponent(planId)}`,
      { signal },
    )
  }

  async *streamGenerationEvents(
    worksetId: string,
    generationId: string,
    signal: AbortSignal,
  ): AsyncGenerator<GenerationEvent> {
    const response = await fetch(
      this.url(`/worksets/${encodeURIComponent(worksetId)}/planning-sessions/${encodeURIComponent(generationId)}/events`),
      { headers: this.headers(false), signal },
    )
    if (!response.ok) throw await this.toApiError(response)
    if (!response.body) {
      throw new ApiError(response.status, 'STREAM_UNAVAILABLE', 'Generation events response did not include a stream')
    }
    yield* parseSSEStream<GenerationEvent>(response.body)
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
    )
  }
}

export const apiClientKey: InjectionKey<ApiClientContract> = Symbol('api-client')

export function useApiClient(): ApiClientContract {
  const client = inject(apiClientKey)
  if (!client) throw new Error('ApiClient was not provided')
  return client
}
