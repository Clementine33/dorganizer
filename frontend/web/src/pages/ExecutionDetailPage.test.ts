import { flushPromises, mount, enableAutoUnmount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { apiClientKey } from '@/lib/api/client'
import type {
  ApiClientContract,
  ExecutionView,
  Operation,
  RevisionDetailResponse,
  Workset,
} from '@/lib/api/types'
import { apiStub as sharedApiStub } from '@/test/api-stub'
import { installTestQueryPlugin } from '@/test/query-client'
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
    getWorkset: vi.fn().mockResolvedValue(workset),
    getOperation: vi.fn().mockResolvedValue(operation(true)),
    getOperationDraft: vi.fn().mockResolvedValue(undefined),
    getRevision: vi.fn().mockResolvedValue(revision(null)),
    listRevisions: vi.fn().mockResolvedValue({ revisions: [] }),
    getExecution: vi.fn().mockResolvedValue(executionView),
    ...overrides,
  })
}

async function mountPage(api: ApiClientContract): Promise<VueWrapper> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/worksets/:worksetId/conversion', name: 'conversion', component: { template: '<div />' } },
      {
        path: '/worksets/:worksetId/conversion/execution',
        name: 'conversion-execution',
        component: ExecutionDetailPage,
      },
    ],
  })
  await router.push('/worksets/ws-1/conversion/execution')
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

  it('states the absence of a session instead of pretending one exists', async () => {
    const api = pageApi({
      getOperation: vi.fn().mockResolvedValue(operation(false)),
      getRevision: vi.fn().mockResolvedValue(revision(null)),
    })
    const wrapper = await mountPage(api)

    expect(wrapper.get('[data-testid="execution-empty"]').text()).toContain('没有执行记录')
    expect(wrapper.find('[data-testid="execution-panel"]').exists()).toBe(false)
  })
})
