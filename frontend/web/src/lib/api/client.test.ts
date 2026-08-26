import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiClient, ApiError } from './client'
import type { ApiConfig } from './types'

function createClient(config: Partial<ApiConfig> = {}): ApiClient {
  return new ApiClient({ baseUrl: config.baseUrl ?? 'http://example.test/api/v1', token: config.token ?? null })
}

function okJson(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

// Typed fetch mock factory so mock.calls elements keep their RequestInit type.
function mockFetch(
  handler: (input: RequestInfo | URL, init?: RequestInit) => Response | Promise<Response>,
) {
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    return await handler(input, init)
  })
  vi.stubGlobal('fetch', fn)
  return fn
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ApiClient', () => {
  it('forwards the exact AbortSignal to fetch for query-owned GETs', async () => {
    const fetchMock = mockFetch(async () => okJson({ libraries: [] }))

    const client = createClient()
    const signal = new AbortController().signal

    await client.listLibraries(signal)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][1]?.signal).toBe(signal)
  })

  it('passes the signal through listFolders, getFolderTree, listPlans and getPlan', async () => {
    const fetchMock = mockFetch(async () =>
      okJson({
        folders: [],
        tree: { name: 'root', path: '/', type: 'dir', bitrate: null, format: '' },
        plans: [],
      }),
    )

    const client = createClient()
    const signal = new AbortController().signal

    await client.listFolders('lib-1', signal)
    await client.getFolderTree('lib-1', 'folder-1', signal)
    await client.listPlans('lib-1', 100, signal)
    await client.getPlan('plan-1', signal)

    expect(fetchMock).toHaveBeenCalledTimes(4)
    for (const call of fetchMock.mock.calls) {
      expect(call[1]?.signal).toBe(signal)
    }
  })

  it('does not pass a signal when none is provided', async () => {
    const fetchMock = mockFetch(async () => okJson({ libraries: [] }))

    await createClient().listLibraries()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][1]?.signal).toBeUndefined()
  })

  it('surfaces fetch aborts without wrapping them as ApiError', async () => {
    const abortError = new DOMException('The user aborted a request.', 'AbortError')
    const fetchMock = mockFetch(async () => {
      throw abortError
    })

    const client = createClient()
    const controller = new AbortController()
    const promise = client.listLibraries(controller.signal)
    controller.abort()

    await expect(promise).rejects.toBe(abortError)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('parses the JSON envelope and wraps failures as ApiError', async () => {
    const fetchMock = mockFetch(async () => {
      return new Response(JSON.stringify({ code: 'LIBRARY_NOT_FOUND', message: 'no such library' }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    const error = await createClient().getLibrary('missing').catch((e: unknown) => e)

    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ code: 'LIBRARY_NOT_FOUND', status: 404 })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('treats a 204 response as undefined', async () => {
    const fetchMock = mockFetch(async () => new Response(null, { status: 204 }))

    await expect(createClient().deleteLibrary('lib-1')).resolves.toBeUndefined()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})