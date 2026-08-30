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
  it('wires the query-owned AbortSignal through to fetch', async () => {
    const captured: AbortSignal[] = []
    const fetchMock = mockFetch((_input, init) => {
      captured.push(init?.signal as AbortSignal)
      // Stay pending until the internal signal aborts, so the caller-side
      // abort reaches the fetch signal while the request is in flight.
      return new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () =>
          reject(new DOMException('Aborted', 'AbortError')),
          { once: true },
        )
      })
    })

    const client = createClient()
    const controller = new AbortController()

    const promise = client.listLibraries(controller.signal)
    controller.abort()

    await expect(promise).rejects.toMatchObject({ name: 'AbortError' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(captured[0]).toBeInstanceOf(AbortSignal)
    // The abort propagated to the fetch signal that was actually handed out:
    expect(captured[0].aborted).toBe(true)
  })

  it('passes the signal through listFolders and getFolderTree', async () => {
    const captured: (AbortSignal | undefined)[] = []
    const fetchMock = mockFetch((_input, init) => {
      captured.push(init?.signal ?? undefined)
      return new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () =>
          reject(new DOMException('Aborted', 'AbortError')),
          { once: true },
        )
      })
    })

    const client = createClient()
    const controller = new AbortController()

    const calls = [
      client.listFolders('lib-1', controller.signal),
      client.getFolderTree('lib-1', 'folder-1', controller.signal),
    ]
    controller.abort()

    for (const call of calls) {
      await expect(call).rejects.toMatchObject({ name: 'AbortError' })
    }
    expect(fetchMock).toHaveBeenCalledTimes(2)
    for (const signal of captured) {
      expect(signal).toBeInstanceOf(AbortSignal)
      expect((signal as AbortSignal).aborted).toBe(true)
    }
  })

  it('sends If-Match and Idempotency-Key headers on workset writes', async () => {
    const fetchMock = mockFetch(async (input, init) => {
      const path = String(input)
      const headers = new Headers(init?.headers)
      if (path.endsWith('/draft')) {
        expect(headers.get('If-Match')).toBe('3')
        return okJson({ workset_id: 'ws-1', version: 4, planning_state: 'planned', members: [] })
      }
      if (path.endsWith('/revisions')) {
        expect(headers.get('Idempotency-Key')).toBe('key-1')
        return okJson({ created: true, generation: { generation_id: 'gen-1', status: 'queued' } })
      }
      if (path.endsWith('/worksets') && init?.method === 'POST') {
        expect(headers.get('Idempotency-Key')).toBe('create-key')
        return okJson({ workset: { workset_id: 'ws-1', members: [] }, created: true })
      }
      return okJson({})
    })

    const client = createClient()
    await client.saveWorksetDraft('ws-1', { schema_version: 1, steps: [] }, 3)
    await client.startGeneration('ws-1', { expected_draft_version: 3 }, 'key-1')
    await client.createWorkset({ library_id: 'lib-1', title: 't', folder_ids: ['f-1'] }, 'create-key')
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('always applies a management signal even when none is provided', async () => {
    const fetchMock = mockFetch(async () => okJson({ libraries: [] }))

    await createClient().listLibraries()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    // The request gets an internal timeout signal even without a caller one.
    expect(fetchMock.mock.calls[0][1]?.signal).toBeInstanceOf(AbortSignal)
  })

  it('times out a request that never settles', async () => {
    vi.useFakeTimers()
    try {
      const fetchMock = mockFetch(async (_input, init) => {
        await new Promise((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () =>
            reject(new DOMException('请求超时', 'TimeoutError')),
          )
        })
        return okJson({ libraries: [] })
      })

      const promise = createClient().listLibraries()
      // Attach the rejection handler before the timer fires so the timed-out
      // request is never flagged as an unhandled rejection.
      const expectation = expect(promise).rejects.toMatchObject({ name: 'TimeoutError' })
      await vi.advanceTimersByTimeAsync(15_000)

      await expectation
      expect(fetchMock).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
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