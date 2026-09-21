import { flushPromises, mount, enableAutoUnmount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { apiClientKey } from '@/lib/api/client'
import type {
  ApiClientContract,
  ExecutionComponent,
  ExecutionView,
  Operation,
  RevisionDetailResponse,
  Workset,
} from '@/lib/api/types'
import { apiStub as sharedApiStub } from '@/test/api-stub'
import { installTestQueryPlugin } from '@/test/query-client'
import { useWorksetExecutionStore } from '@/stores/workset-execution'
import ExecutionDetailPage from './ExecutionDetailPage.vue'

enableAutoUnmount(afterEach)

const workset: Workset = {
  workset_id: 'ws-1',
  title: '夏季整理',
  version: 1,
  library: { library_id: 'lib-a', name: 'Lib', root_path: '/music' },
  members: [],
  operations: [],
  updated_at: '2026-09-16T00:00:00Z',
  created_at: '2026-09-16T00:00:00Z',
}

const executionView: ExecutionView = {
  execution_id: 'exec-1',
  workset_id: 'ws-1',
  operation_type: 'conversion',
  plan_id: 'plan-1',
  status: 'running',
  options: { delete_mode: 'soft' },
  total_components: 2,
  completed_components: 1,
  total_operations: 3,
  completed_operations: 1,
  current_root: '/music/albumB',
  current_component_id: 'comp-b',
  current_phase: 'component',
  components: [],
  error_code: '',
  error_message: '',
  started_at: '',
  finished_at: '',
  created_at: '',
}

function operation(active: boolean): Operation {
  return {
    workset_id: 'ws-1',
    operation_type: 'conversion',
    version: 5,
    planning_state: 'planned',
    current_revision: {
      plan_id: 'plan-1',
      revision_index: 1,
      created_at: '2026-09-16T00:00:00Z',
      status: 'ready',
      summary_reason: '',
      counts: { members: 1, changed: 1, unmet_targets: 0, blocked: 0, unchanged: 0 },
      validation_state: 'valid',
      stale: false,
    },
    active_generation: null,
    latest_generation: null,
    active_execution: active
      ? {
          execution_id: 'exec-1',
          plan_id: 'plan-1',
          status: 'running',
          options: { delete_mode: 'soft' },
          total_components: 2,
          completed_components: 1,
          total_operations: 3,
          completed_operations: 1,
          current_root: '/music/albumB',
          current_component_id: 'comp-b',
          current_phase: 'component',
        }
      : null,
    latest_execution: null,
  }
}

function revision(execution: RevisionDetailResponse['execution']): RevisionDetailResponse {
  return {
    plan_id: 'plan-1',
    revision_index: 1,
    created_at: '2026-09-16T00:00:00Z',
    root_path: '/music',
    snapshot_token: 'tok',
    status: 'ready',
    summary: { operation_count: 3, error_count: 0, total_count: 2, actionable_count: 2, summary_reason: '' },
    task: {
      kind: 'conversion',
      schema_version: 1,
      payload: {
        policy: {},
        policy_hash: '',
        classifier: {},
        summary: { component_count: 2, blocked_count: 0, operation_count: 3, error_count: 0, summary_reason: '' },
        components: [],
      },
    },
    counts: { members: 1, changed: 1, unmet_targets: 0, blocked: 0, unchanged: 0 },
    members: [],
    roots: [],
    component_roots: [],
    execution,
  }
}

function pageApi(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  return sharedApiStub({
    getCurrentRecord: vi.fn().mockResolvedValue({ workset }),
    getWorkset: vi.fn().mockResolvedValue(workset),
    getOperation: vi.fn().mockResolvedValue(operation(true)),
    getOperationDraft: vi.fn().mockResolvedValue(undefined),
    getRevision: vi.fn().mockResolvedValue(revision(null)),
    getExecution: vi.fn().mockResolvedValue(executionView),
    ...overrides,
  })
}

async function mountPage(api: ApiClientContract): Promise<VueWrapper> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/worksets/:libraryId/conversion',
        name: 'conversion',
        component: { template: '<div />' },
      },
      {
        path: '/worksets/:libraryId/conversion/execution',
        name: 'conversion-execution',
        component: ExecutionDetailPage,
      },
    ],
  })
  await router.push('/worksets/lib-a/conversion/execution')
  await router.isReady()
  const wrapper = mount(ExecutionDetailPage, {
    global: {
      plugins: [createPinia(), router, installTestQueryPlugin()],
      provide: { [apiClientKey as symbol]: api },
    },
    attachTo: document.body,
  })
  await flushPromises()
  return wrapper
}

describe('ExecutionDetailPage', () => {
  it('renders the live report and offers the cancel action while the run is active', async () => {
    const cancelExecution = vi.fn().mockResolvedValue({ ...executionView, status: 'canceled' })
    const api = pageApi({ cancelExecution })
    const wrapper = await mountPage(api)

    expect(wrapper.get('[data-testid="execution-panel"]').text()).toContain('执行中')
    expect(wrapper.get('[data-testid="execution-progress"]').text()).toContain('组件 1/2')

    await wrapper.get('[data-testid="execution-cancel"]').trigger('click')
    await flushPromises()

    expect(cancelExecution).toHaveBeenCalledWith('ws-1', 'conversion', 'exec-1')
  })

  it('shows the finished report of the revision that ran, without a cancel action', async () => {
    const api = pageApi({
      getCurrentRecord: vi.fn().mockResolvedValue({ workset }),
      getOperation: vi.fn().mockResolvedValue(operation(false)),
      getRevision: vi
        .fn()
        .mockResolvedValue(revision({ execution_id: 'exec-1', plan_id: 'plan-1', status: 'succeeded' })),
      getExecution: vi.fn().mockResolvedValue({ ...executionView, status: 'succeeded', completed_components: 2, completed_operations: 3 }),
    })
    const wrapper = await mountPage(api)

    expect(wrapper.get('[data-testid="execution-status"]').text()).toBe('已完成')
    expect(wrapper.find('[data-testid="execution-cancel"]').exists()).toBe(false)
  })

  it("fills a run's per-component facts in as each component commits", async () => {
    const report: ExecutionView = {
      ...executionView,
      completed_components: 2,
      completed_operations: 2,
      current_root: '',
      current_component_id: '',
      components: [
        {
          component_index: 1,
          component_id: 'comp-a',
          root_path: '/music/albumA',
          partition: 'matched',
          status: 'succeeded',
          operations: 2,
          completed_operations: 2,
          committed: ['/music/albumA/00.mp3'],
          removed: [],
          remaining: [],
          recovery: [],
          inventory_synced: true,
        },
      ],
    }
    // The read the page opens with is the session as it stood then.
    const getExecution = vi
      .fn()
      .mockResolvedValue({ ...executionView, completed_components: 0, components: [] })
    const wrapper = await mountPage(pageApi({ getExecution }))

    // Attaching the stream: the snapshot reports the counts it had when it was
    // read, and no component has been read into the view yet.
    const store = useWorksetExecutionStore()
    store.status = 'streaming'
    store.view = { ...executionView, completed_components: 0, components: [] }
    await flushPromises()
    expect(wrapper.find('[data-testid="execution-folder"]').exists()).toBe(false)

    // The wire pushes the component's own result, then the progress that moved
    // the position onto it: the panel fills in from those events alone, without
    // asking for the session again.
    store.applyEvent({ type: 'component', data: report.components[0]! })
    store.applyEvent({
      type: 'progress',
      data: {
        execution_id: 'exec-1',
        status: 'running',
        total_components: 2,
        completed_components: 1,
        total_operations: 3,
        completed_operations: 2,
        current_root: '/music/albumA',
        current_component_id: 'comp-a',
        current_phase: 'component',
      },
    })
    await flushPromises()

    expect(getExecution).toHaveBeenCalledTimes(1)
    const card = wrapper.get('[data-testid="execution-folder"]')
    expect(card.get('[data-testid="execution-folder-name"]').text()).toContain('albumA')
    expect(card.get('[data-testid="execution-folder-status"]').text()).toBe('执行中')
    // The committed file of the component that just finished is on screen while
    // the run is still going.
    expect(card.text()).toContain('00.mp3')
  })

  it('states the absence of a session instead of pretending one exists', async () => {
    const api = pageApi({
      getCurrentRecord: vi.fn().mockResolvedValue({ workset }),
      getOperation: vi.fn().mockResolvedValue(operation(false)),
      getRevision: vi.fn().mockResolvedValue(revision(null)),
    })
    const wrapper = await mountPage(api)

    expect(wrapper.get('[data-testid="execution-empty"]').text()).toContain('没有执行记录')
    expect(wrapper.find('[data-testid="execution-panel"]').exists()).toBe(false)
  })
})

describe('ExecutionDetailPage component paging', () => {
  function component(index: number): ExecutionComponent {
    return {
      component_index: index,
      component_id: `comp-${index}`,
      root_path: '/music/albumA',
      partition: 'matched',
      status: 'pending',
      operations: 1,
      completed_operations: 0,
      committed: [],
      removed: [],
      remaining: [],
      recovery: [],
      inventory_synced: false,
    }
  }

  it('reads the next page of a run too large for one read, and merges it', async () => {
    const firstPage = [component(0), component(1)]
    const getExecution = vi
      .fn()
      .mockResolvedValueOnce({ ...executionView, total_components: 3, components: firstPage })
      .mockResolvedValueOnce({ ...executionView, total_components: 3, components: [component(2)] })
    const wrapper = await mountPage(pageApi({ getExecution }))
    await flushPromises()

    // The read carried two of the three components: the panel offers the rest
    // rather than pretending the run is smaller than it is.
    expect(wrapper.get('[data-testid="execution-progress"]').text()).toContain('组件 1/3')
    const more = wrapper.get('[data-testid="execution-load-more"]')
    expect(more.text()).toContain('加载更多组件')

    await more.trigger('click')
    await flushPromises()

    // The second read starts after the last component held and asks for a page.
    expect(getExecution).toHaveBeenLastCalledWith('ws-1', 'conversion', 'exec-1', undefined, {
      from: 2,
      limit: 200,
    })
    expect(wrapper.find('[data-testid="execution-load-more"]').exists()).toBe(false)
  })
})
