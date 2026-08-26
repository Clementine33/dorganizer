import type { InjectionKey } from 'vue'
import { inject } from 'vue'
import { parseSSEStream } from './sse'
import type {
  ApiClientContract,
  ApiConfig,
  CreateLibraryInput,
  CreatePlanInput,
  Folder,
  HealthResponse,
  Library,
  PlanInfo,
  PlanResponse,
  ScanEvent,
  TreeNode,
  UpdateLibraryInput,
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

  createPlan(input: CreatePlanInput): Promise<PlanResponse> {
    return this.request('/plans', { method: 'POST', body: input })
  }

  async listPlans(libraryId?: string, limit = 100, signal?: AbortSignal): Promise<PlanInfo[]> {
    const query = new URLSearchParams({ limit: String(limit) })
    if (libraryId) query.set('library_id', libraryId)
    return (await this.request<{ plans: PlanInfo[] }>(`/plans?${query.toString()}`, { signal })).plans
  }

  getPlan(id: string, signal?: AbortSignal): Promise<PlanResponse> {
    return this.request(`/plans/${encodeURIComponent(id)}`, { signal })
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
  private static readonly REQUEST_TIMEOUT_MS = 15_000

  private async request<T>(
    path: string,
    options: { method?: string; body?: unknown; signal?: AbortSignal } = {},
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
      ApiClient.REQUEST_TIMEOUT_MS,
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
    options: { method?: string; body?: unknown },
    signal: AbortSignal,
  ): Promise<T> {
    const response = await fetch(this.url(path), {
      method: options.method ?? 'GET',
      headers: this.headers(options.body !== undefined),
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
