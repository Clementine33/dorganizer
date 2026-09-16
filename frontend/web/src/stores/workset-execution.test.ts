import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ApiClientContract, ExecutionEvent, ExecutionView } from '@/lib/api/types'
import { apiStub } from '@/test/api-stub'
import { useWorksetExecutionStore } from './workset-execution'

function view(overrides: Partial<ExecutionView> = {}): ExecutionView {
  return {
    execution_id: 'exec-1',
    workset_id: 'ws-1',
    operation_type: 'conversion',
    plan_id: 'plan-1',
    status: 'running',
    options: { delete_mode: 'soft' },
    total_components: 2,
    completed_components: 0,
    total_operations: 3,
    completed_operations: 0,
    current_root: '',
    current_component_id: '',
    current_phase: 'component',
    components: [],
    error_code: '',
    error_message: '',
    started_at: '',
    finished_at: '',
    created_at: '',
    ...overrides,
  }
}

async function* events(items: ExecutionEvent[]): AsyncGenerator<ExecutionEvent> {
  for (const item of items) yield item
}

function makeEventStream(
  terminal: ExecutionEvent['type'],
  extra: Record<string, unknown> = {},
): ApiClientContract['streamExecutionEvents'] {
  return vi.fn(() =>
    events([
      { type: 'execution_snapshot', data: view() } as ExecutionEvent,
      {
        type: 'progress',
        data: {
          execution_id: 'exec-1',
          status: 'running',
          total_components: 2,
          completed_components: 1,
          total_operations: 3,
          completed_operations: 1,
          current_root: 'albumB',
          current_component_id: 'c2',
          current_phase: 'component',
        },
      } as ExecutionEvent,
      terminal === 'succeeded'
        ? ({ type: 'succeeded', data: { execution_id: 'exec-1', plan_id: 'plan-1', ...extra } } as ExecutionEvent)
        : terminal === 'failed'
          ? ({
              type: 'failed',
              data: { execution_id: 'exec-1', error_code: 'COMPONENT_FAILED', error_message: 'encode failed', ...extra },
            } as ExecutionEvent)
          : ({ type: terminal, data: { execution_id: 'exec-1', ...extra } } as ExecutionEvent),
    ]),
  ) as ApiClientContract['streamExecutionEvents']
}

describe('workset execution store', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('merges snapshot and progress into the view, then confirms the succeeded terminal', async () => {
    const store = useWorksetExecutionStore()
    const api = apiStub({ streamExecutionEvents: makeEventStream('succeeded') })

    await store.attach('ws-1', 'conversion', 'exec-1', api)

    expect(store.status).toBe('succeeded')
    expect(store.terminal).toBe('event')
    expect(store.view?.status).toBe('succeeded')
    expect(store.view?.completed_components).toBe(1)
    expect(store.view?.total_operations).toBe(3)
    expect(store.view?.current_component_id).toBe('c2')
    expect(store.receivedEvent).toBe(true)
  })

  it('tracks failed and canceled terminals distinctly', async () => {
    const failed = useWorksetExecutionStore()
    await failed.attach('ws-1', 'conversion', 'exec-1', apiStub({ streamExecutionEvents: makeEventStream('failed') }))
    expect(failed.status).toBe('failed')
    expect(failed.errorCode).toBe('COMPONENT_FAILED')
    expect(failed.view?.status).toBe('failed')

    const canceled = useWorksetExecutionStore()
    await canceled.attach('ws-1', 'conversion', 'exec-2', apiStub({ streamExecutionEvents: makeEventStream('canceled') }))
    expect(canceled.status).toBe('canceled')
    expect(canceled.terminal).toBe('event')
  })

  it('marks a transport terminal when the stream ends without a terminal event', async () => {
    const streamExecutionEvents = vi.fn(() =>
      events([{ type: 'execution_snapshot', data: view() } as ExecutionEvent]),
    )
    const store = useWorksetExecutionStore()
    await store.attach('ws-1', 'conversion', 'exec-1', apiStub({ streamExecutionEvents }))

    expect(store.status).toBe('error')
    expect(store.terminal).toBe('transport')
    expect(store.errorCode).toBe('STREAM_ENDED')
  })

  it('keeps reporting 取消中 until the cooperative cancel reaches its terminal event', async () => {
    let pushCanceled: () => void = () => {}
    const streamExecutionEvents = vi.fn(() =>
      (async function* (): AsyncGenerator<ExecutionEvent> {
        yield { type: 'execution_snapshot', data: view() } as ExecutionEvent
        // The running worker stops at its next safe boundary; only then does
        // the terminal event arrive.
        await new Promise<void>((resolve) => {
          pushCanceled = resolve
        })
        yield { type: 'canceled', data: { execution_id: 'exec-1' } } as ExecutionEvent
      })(),
    )
    const store = useWorksetExecutionStore()
    const attached = store.attach('ws-1', 'conversion', 'exec-1', apiStub({ streamExecutionEvents }))
    await Promise.resolve()
    await Promise.resolve()

    store.requestCancel('ws-1', 'exec-1')
    expect(store.canceling).toBe(true)

    pushCanceled()
    await attached

    expect(store.status).toBe('canceled')
    expect(store.canceling).toBe(false)
  })

  it('refuses a second attach while streaming', async () => {
    const streamExecutionEvents = vi.fn((_w: string, _o: string, _e: string, signal: AbortSignal) =>
      (async function* (): AsyncGenerator<ExecutionEvent> {
        yield { type: 'execution_snapshot', data: view() } as ExecutionEvent
        await new Promise<void>((resolve) => {
          signal.addEventListener('abort', () => resolve(), { once: true })
        })
      })(),
    )
    const store = useWorksetExecutionStore()
    const api = apiStub({ streamExecutionEvents })

    const first = store.attach('ws-1', 'conversion', 'exec-1', api)
    const second = await store.attach('ws-1', 'conversion', 'exec-2', api)
    expect(second).toBe(false)

    await Promise.resolve()
    await Promise.resolve()
    store.reset()
    await first
    expect(streamExecutionEvents).toHaveBeenCalledTimes(1)
  })
})
