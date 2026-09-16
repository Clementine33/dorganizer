import { flushPromises, mount, enableAutoUnmount, type VueWrapper } from '@vue/test-utils'
import { VueQueryPlugin, type QueryClient } from '@tanstack/vue-query'
import { createPinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { ApiError, apiClientKey } from '@/lib/api/client'
import type {
  ApiClientContract,
  DraftResponse,
  ExecutionEvent,
  ExecutionView,
  Operation,
  RevisionDetailResponse,
  Workset,
} from '@/lib/api/types'
import { queryKeys } from '@/queries/query-keys'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import { apiStub as sharedApiStub } from '@/test/api-stub'
import { createTestQueryClient } from '@/test/query-client'
import WorksetWorkspacePage from './WorksetWorkspacePage.vue'

enableAutoUnmount(afterEach)

const operation: Operation = {
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
  active_execution: null,
  latest_execution: null,
}

const workset: Workset = {
  workset_id: 'ws-1',
  title: '夏季整理',
  version: 1,
  library: { library_id: 'lib-a', name: 'Lib', root_path: '/music' },
  members: [
    { member_id: 'm1', folder_id: 'f1', folder_path: '/music/albumA', folder_name: 'albumA', rel_path: 'albumA' },
  ],
  operations: [operation],
  updated_at: '2026-09-16T00:00:00Z',
  created_at: '2026-09-16T00:00:00Z',
}

const draft: DraftResponse = {
  workset_id: 'ws-1',
  operation_type: 'conversion',
  version: 5,
  schema_version: 1,
  document: { schema_version: 1, classifier_tags: [], matched: {}, unmatched: {}, members: [] },
  updated_at: '2026-09-16T00:00:00Z',
}

const revision: RevisionDetailResponse = {
  plan_id: 'plan-1',
  revision_index: 1,
  created_at: '2026-09-16T00:00:00Z',
  root_path: '/music/albumA',
  snapshot_token: 'tok',
  status: 'ready',
  summary: { operation_count: 2, error_count: 0, total_count: 1, actionable_count: 1, summary_reason: '' },
  task: {
    kind: 'conversion',
    schema_version: 1,
    payload: {
      policy: {},
      policy_hash: '',
      classifier: {},
      summary: { component_count: 1, blocked_count: 0, operation_count: 2, error_count: 0, summary_reason: '' },
      components: [],
    },
  },
  counts: { members: 1, changed: 1, unmet_targets: 0, blocked: 0, unchanged: 0 },
  members: [
    { member_id: 'm1', member_name: 'albumA', folder_path: '/music/albumA', excluded: false, effective: { schema_version: 1 }, sources: {} },
  ],
  roots: [],
  component_roots: [],
  execution: null,
}

const executionView: ExecutionView = {
  execution_id: 'exec-1',
  workset_id: 'ws-1',
  operation_type: 'conversion',
  plan_id: 'plan-1',
  status: 'queued',
  options: { delete_mode: 'soft' },
  total_components: 1,
  completed_components: 0,
  total_operations: 2,
  completed_operations: 0,
  current_root: '',
  current_component_id: '',
  current_phase: '',
  components: [],
  error_code: '',
  error_message: '',
  started_at: '',
  finished_at: '',
  created_at: '2026-09-16T00:00:00Z',
}

/** A stream that stays open until the consumer aborts (or the page unmounts). */
function pendingStream(): ApiClientContract['streamExecutionEvents'] {
  return vi.fn((_w: string, _o: string, _e: string, signal: AbortSignal) =>
    (async function* (): AsyncGenerator<ExecutionEvent> {
      yield { type: 'execution_snapshot', data: executionView } as ExecutionEvent
      await new Promise<void>((resolve) => {
        signal.addEventListener('abort', () => resolve(), { once: true })
      })
    })(),
  ) as ApiClientContract['streamExecutionEvents']
}

function pageApi(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  return sharedApiStub({
    getWorkset: vi.fn().mockResolvedValue(workset),
    getOperation: vi.fn().mockResolvedValue(operation),
    getOperationDraft: vi.fn().mockResolvedValue(draft),
    getRevision: vi.fn().mockResolvedValue(revision),
    listRevisions: vi.fn().mockResolvedValue({ revisions: [] }),
    getExecution: vi.fn().mockResolvedValue(executionView),
    streamExecutionEvents: pendingStream(),
    ...overrides,
  })
}

async function mountPage(
  api: ApiClientContract,
  queryClient: QueryClient = createTestQueryClient(),
): Promise<{
  wrapper: VueWrapper
  router: Router
  pinia: ReturnType<typeof createPinia>
  queryClient: QueryClient
}> {
  const pinia = createPinia()
  const queryPlugin: [typeof VueQueryPlugin, { queryClient: QueryClient }] = [VueQueryPlugin, { queryClient }]
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/worksets', component: { template: '<div>list</div>' } },
      { path: '/worksets/:worksetId', name: 'workset-overview', component: { template: '<div>overview</div>' } },
      { path: '/libraries/:libraryId/folders/:folderId', name: 'folder-detail', component: { template: '<div />' } },
      {
        path: '/worksets/:worksetId/conversion',
        name: 'conversion',
        component: WorksetWorkspacePage,
        // The workspace links into its carriers by name; the registry must
        // know them even when the carriers themselves are stubbed out.
        children: [
          { path: 'members/:memberId', name: 'conversion-member', component: { template: '<div />' }, meta: { carrier: true, title: '文件夹详情' } },
          { path: 'members/:memberId/edit', name: 'conversion-member-edit', component: { template: '<div />' }, meta: { carrier: true, title: '修改此文件夹' } },
          { path: 'settings', name: 'conversion-settings', component: { template: '<div />' }, meta: { carrier: true, title: '转换全局设置' } },
          { path: 'execution', name: 'conversion-execution', component: { template: '<div />' }, meta: { carrier: true, title: '执行结果' } },
          { path: 'batch-edit', name: 'conversion-batch-edit', component: { template: '<div />' }, meta: { carrier: true, title: '批量修改' } },
        ],
      },
    ],
  })
  await router.push('/worksets/ws-1/conversion')
  await router.isReady()
  const wrapper = mount(WorksetWorkspacePage, {
    global: {
      plugins: [pinia, router, queryPlugin],
      provide: { [apiClientKey as symbol]: api },
      stubs: { RouterView: true },
    },
    attachTo: document.body,
  })
  await flushPromises()
  return { wrapper, router, pinia, queryClient }
}

describe('WorksetWorkspacePage execution entry', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('offers no execution entry before a plan exists, and generating one never starts a run', async () => {
    const startGeneration = vi.fn().mockResolvedValue({ created: false, revision: operation.current_revision })
    const api = pageApi({
      getOperation: vi.fn().mockResolvedValue({ ...operation, current_revision: null }),
      startGeneration,
    })
    const { wrapper } = await mountPage(api)

    expect(wrapper.find('[data-testid="start-execution"]').exists()).toBe(false)

    await wrapper.get('[data-testid="start-generation"]').trigger('click')
    await flushPromises()

    expect(startGeneration).toHaveBeenCalledTimes(1)
    expect(api.startExecution).not.toHaveBeenCalled()
  })

  it('offers the execution entry directly on a planned revision', async () => {
    const api = pageApi()
    const { wrapper } = await mountPage(api)

    const execute = wrapper.get('[data-testid="start-execution"]')
    expect(execute.attributes('disabled')).toBeUndefined()
  })

  it('starts the run straight from the header — no second confirmation step', async () => {
    const startExecution = vi.fn().mockResolvedValue({ created: true, execution: executionView })
    const api = pageApi({ startExecution })
    const { wrapper, router } = await mountPage(api)

    await wrapper.get('[data-testid="start-execution"]').trigger('click')
    await flushPromises()

    expect(startExecution).toHaveBeenCalledTimes(1)
    expect(startExecution).toHaveBeenCalledWith('ws-1', 'conversion', 'plan-1', {
      ifMatchVersion: 5,
      idempotencyKey: expect.any(String),
    })
    // A live run opens its own detail carrier instead of crowding the list.
    expect(router.currentRoute.value.path).toMatch(/\/conversion\/execution$/)
  })

  it('carries a fresh idempotency key per attempt and never fires twice from double clicks', async () => {
    let release: (value: { created: boolean; execution: ExecutionView }) => void = () => {}
    const startExecution = vi.fn(() => new Promise<{ created: boolean; execution: ExecutionView }>((resolve) => { release = resolve }))
    const api = pageApi({ startExecution })
    const { wrapper } = await mountPage(api)

    const executeButton = wrapper.get('[data-testid="start-execution"]')
    await executeButton.trigger('click')
    await flushPromises()
    // In flight: the button is disabled, so a second click cannot start a run.
    expect(executeButton.attributes('disabled')).toBeDefined()
    await executeButton.trigger('click')
    await flushPromises()

    expect(startExecution).toHaveBeenCalledTimes(1)
    release({ created: true, execution: executionView })
    await flushPromises()
  })

  it('explains a refused start against refreshed state and never retries on its own', async () => {
    const startExecution = vi
      .fn()
      .mockRejectedValue(new ApiError(409, 'PLAN_NOT_EXECUTABLE', 'revision cannot be executed', ['ALREADY_EXECUTED']))
    const getOperation = vi.fn().mockResolvedValue(operation)
    const api = pageApi({ startExecution, getOperation })
    const { wrapper } = await mountPage(api)
    const readsBefore = getOperation.mock.calls.length

    await wrapper.get('[data-testid="start-execution"]').trigger('click')
    await flushPromises()

    expect(startExecution).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="execute-error"]').text()).toContain('已执行过')
    // The refusal re-read the operation so the stale view is replaced.
    expect(getOperation.mock.calls.length).toBeGreaterThan(readsBefore)
  })

  it('shows a live run in the header and cancels it through the session route', async () => {
    const activeExecution = {
      execution_id: 'exec-1',
      plan_id: 'plan-1',
      status: 'running' as const,
      options: { delete_mode: 'soft' as const },
      total_components: 1,
      completed_components: 0,
      total_operations: 2,
      completed_operations: 0,
      current_root: '',
      current_component_id: '',
      current_phase: 'component',
    }
    const cancelExecution = vi.fn().mockResolvedValue({ ...executionView, status: 'canceled' })
    const api = pageApi({
      getOperation: vi.fn().mockResolvedValue({ ...operation, active_execution: activeExecution }),
      cancelExecution,
    })
    const { wrapper, router } = await mountPage(api)

    // The reload path re-attached the live session and the header offers both
    // the cancel action and the way back into the report carrier.
    expect(api.streamExecutionEvents).toHaveBeenCalledWith('ws-1', 'conversion', 'exec-1', expect.any(AbortSignal))
    await wrapper.get('[data-testid="cancel-execution"]').trigger('click')
    await flushPromises()

    expect(cancelExecution).toHaveBeenCalledWith('ws-1', 'conversion', 'exec-1')
    expect(wrapper.get('[data-testid="cancel-execution"]').text()).toBe('取消中…')

    // The carrier holds the report, so the entry navigates into it (in this
    // narrow container that hands the main area to the carrier).
    await wrapper.get('[data-testid="open-execution"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toMatch(/\/conversion\/execution$/)
  })
  it('states a refused generation start in place instead of leaving it in the console', async () => {
    const startGeneration = vi
      .fn()
      .mockRejectedValue(
        new ApiError(400, 'INVALID_POLICY', 'effective settings for member m-1: policy requires at least one non-empty classifier tag'),
      )
    const api = pageApi({ startGeneration })
    const { wrapper } = await mountPage(api)

    await wrapper.get('[data-testid="start-generation"]').trigger('click')
    await flushPromises()

    expect(startGeneration).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="generate-error"]').text()).toContain('分类标签')
  })
})

describe('WorksetWorkspacePage draft reconciliation', () => {  beforeEach(() => vi.restoreAllMocks())

  it('re-bases the edit session when the draft refetch only advanced the version', async () => {
    const getOperationDraft = vi.fn().mockResolvedValueOnce(draft).mockResolvedValue({ ...draft, version: 6 })
    const { pinia, queryClient } = await mountPage(pageApi({ getOperationDraft }))
    const editor = useWorksetEditorStore(pinia)
    editor.open({
      worksetId: 'ws-1',
      operation: 'conversion',
      target: { kind: 'common' },
      baseVersion: 5,
      baseDocument: draft.document,
    })

    await queryClient.invalidateQueries({ queryKey: queryKeys.worksets.draft('ws-1', 'conversion') })
    await flushPromises()

    expect(getOperationDraft).toHaveBeenCalledTimes(2)
    expect(editor.session?.baseVersion).toBe(6)
    expect(editor.session?.baseDocument).toEqual(draft.document)
  })
})
