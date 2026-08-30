import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ApiClientContract, GenerationEvent } from '@/lib/api/types'
import { apiStub } from '@/test/api-stub'
import { useWorksetGenerationStore } from './workset-generation'

async function* events(items: GenerationEvent[]): AsyncGenerator<GenerationEvent> {
  for (const item of items) yield item
}

function makeEventStream(terminal: GenerationEvent['type'], extra: Record<string, unknown> = {}): ApiClientContract['streamGenerationEvents'] {
  return vi.fn((_worksetId: string, _generationId: string, _signal: AbortSignal) =>
    events([
      {
        type: 'session_snapshot',
        data: {
          generation_id: 'gen-1', workset_id: 'ws-1', status: 'running', total_roots: 3, completed_roots: 1,
          current_root: 'albumB', error_count: 0, revision_id: '', error_code: '', error_message: '',
          started_at: '', finished_at: '', created_at: '',
        },
      } as GenerationEvent,
      { type: 'progress', data: { generation_id: 'gen-1', total_roots: 3, completed_roots: 2, current_root: 'albumC', error_count: 0 } } as GenerationEvent,
      terminal === 'completed'
        ? ({ type: 'completed', data: { generation_id: 'gen-1', revision_id: 'plan-x', ...extra } } as GenerationEvent)
        : terminal === 'failed'
          ? ({ type: 'failed', data: { generation_id: 'gen-1', error_code: 'GENERATION_FAILED', error_message: 'planning failed', ...extra } } as GenerationEvent)
          : ({ type: terminal, data: { generation_id: 'gen-1', ...extra } } as GenerationEvent),
    ]),
  ) as ApiClientContract['streamGenerationEvents']
}

describe('workset generation store', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('applies snapshot and progress events then confirms the completed terminal', async () => {
    const store = useWorksetGenerationStore()
    const api = apiStub({ streamGenerationEvents: makeEventStream('completed') })

    await store.attach('ws-1', 'gen-1', api)

    expect(store.status).toBe('completed')
    expect(store.terminal).toBe('event')
    expect(store.revisionId).toBe('plan-x')
    expect(store.completedRoots).toBe(2)
    expect(store.totalRoots).toBe(3)
    expect(store.receivedEvent).toBe(true)
  })

  it('tracks failed and canceled terminals distinctly', async () => {
    const failed = useWorksetGenerationStore()
    await failed.attach('ws-1', 'gen-1', apiStub({ streamGenerationEvents: makeEventStream('failed') }))
    expect(failed.status).toBe('failed')
    expect(failed.errorCode).toBe('GENERATION_FAILED')

    const canceled = useWorksetGenerationStore()
    await canceled.attach('ws-1', 'gen-2', apiStub({ streamGenerationEvents: makeEventStream('canceled') }))
    expect(canceled.status).toBe('canceled')
    expect(canceled.terminal).toBe('event')
  })

  it('refuses a second attach while streaming', async () => {
    const streamGenerationEvents = vi.fn((_w: string, _g: string, signal: AbortSignal) =>
      (async function* (): AsyncGenerator<GenerationEvent> {
        yield {
          type: 'session_snapshot',
          data: {
            generation_id: 'gen-1', workset_id: 'ws-1', status: 'running', total_roots: 1, completed_roots: 0,
            current_root: '', error_count: 0, revision_id: '', error_code: '', error_message: '',
            started_at: '', finished_at: '', created_at: '',
          },
        } as GenerationEvent
        await new Promise<void>((resolve) => {
          signal.addEventListener('abort', () => resolve(), { once: true })
        })
      })(),
    )
    const store = useWorksetGenerationStore()
    const api = apiStub({ streamGenerationEvents })

    const first = store.attach('ws-1', 'gen-1', api)
    // Second attach while streaming must be a no-op: the stream is only
    // opened once.
    store.attach('ws-1', 'gen-2', api)
    // Let the generator reach its await (registering the abort listener)
    // before cancelling.
    await Promise.resolve()
    await Promise.resolve()
    store.cancel()
    await first
    expect(streamGenerationEvents).toHaveBeenCalledTimes(1)
  })

  it('marks a transport terminal when the stream ends without a terminal event', async () => {
    const streamGenerationEvents = vi.fn(() =>
      events([{ type: 'progress', data: { generation_id: 'gen-1', total_roots: 2, completed_roots: 1, current_root: '', error_count: 0 } } as GenerationEvent]),
    )
    const store = useWorksetGenerationStore()
    await store.attach('ws-1', 'gen-1', apiStub({ streamGenerationEvents }))

    expect(store.status).toBe('error')
    expect(store.terminal).toBe('transport')
    expect(store.errorCode).toBe('STREAM_ENDED')
  })

  it('marks a pre-event transport terminal when the stream never started', async () => {
    const streamGenerationEvents = vi.fn(() => {
      throw new Error('boom')
    }) as unknown as ApiClientContract['streamGenerationEvents']
    const store = useWorksetGenerationStore()
    await store.attach('ws-1', 'gen-1', apiStub({ streamGenerationEvents }))

    expect(store.status).toBe('error')
    expect(store.terminal).toBe('transport')
    expect(store.receivedEvent).toBe(false)
  })
})
