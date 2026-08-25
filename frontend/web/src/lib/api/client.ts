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

  getHealth(): Promise<HealthResponse> {
    return this.request('/health')
  }

  async listLibraries(): Promise<Library[]> {
    return (await this.request<{ libraries: Library[] }>('/libraries')).libraries
  }

  getLibrary(id: string): Promise<Library> {
    return this.request(`/libraries/${encodeURIComponent(id)}`)
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

  async listFolders(libraryId: string): Promise<Folder[]> {
    const result = await this.request<{ folders: Folder[] }>(
      `/libraries/${encodeURIComponent(libraryId)}/folders`,
    )
    return result.folders
  }

  async getFolderTree(libraryId: string, folderId: string): Promise<TreeNode> {
    const result = await this.request<{ tree: TreeNode }>(
      `/libraries/${encodeURIComponent(libraryId)}/folders/${encodeURIComponent(folderId)}/tree`,
    )
    return result.tree
  }

  createPlan(input: CreatePlanInput): Promise<PlanResponse> {
    return this.request('/plans', { method: 'POST', body: input })
  }

  async listPlans(libraryId?: string, limit = 100): Promise<PlanInfo[]> {
    const query = new URLSearchParams({ limit: String(limit) })
    if (libraryId) query.set('library_id', libraryId)
    return (await this.request<{ plans: PlanInfo[] }>(`/plans?${query.toString()}`)).plans
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

  private async request<T>(
    path: string,
    options: { method?: string; body?: unknown } = {},
  ): Promise<T> {
    const response = await fetch(this.url(path), {
      method: options.method ?? 'GET',
      headers: this.headers(options.body !== undefined),
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
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
