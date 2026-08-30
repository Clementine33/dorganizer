import { createPinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { VueQueryPlugin } from '@tanstack/vue-query'
import { describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { ApiError, apiClientKey } from '@/lib/api/client'
import type { ApiClientContract, Workset } from '@/lib/api/types'
import { apiStub } from '@/test/api-stub'
import { createTestQueryClient } from '@/test/query-client'
import WorksetsPage from './WorksetsPage.vue'

const worksetA: Workset = {
  workset_id: 'ws-a',
  title: '夏季整理',
  version: 3,
  library: { library_id: 'lib-a', name: 'Onsei', root_path: 'D:\\Music' },
  planning_state: 'planned',
  current_revision: {
    plan_id: 'plan-1', revision_index: 0, created_at: '2026-08-30T00:00:00Z', status: 'ready',
    summary_reason: 'ACTIONABLE', blocked_count: 0, validation_state: 'valid', stale: false,
  },
  active_generation: null,
  latest_generation: null,
  members: [
    { folder_id: 'f-1', folder_path: 'D:\\Music\\albumA', folder_name: 'albumA', rel_path: 'albumA', state: 'planned' },
  ],
  updated_at: '2026-08-30T00:00:00Z',
  created_at: '2026-08-30T00:00:00Z',
}

const worksetB: Workset = {
  ...worksetA,
  workset_id: 'ws-b',
  title: '秋季整理',
  planning_state: 'unplanned',
  current_revision: null,
  version: 1,
}

function worksetsPage(worksets: Workset[], nextCursor?: string) {
  return { worksets, next_cursor: nextCursor }
}

function apiStubFor(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  return apiStub({
    // Mimics the backend's server-side feed filter (pending excludes planned).
    listWorksets: vi.fn((params: { feed?: string; cursor?: string } = {}) => {
      const list = params.feed === 'pending' ? [worksetB] : [worksetA, worksetB]
      return Promise.resolve(worksetsPage(list))
    }),
    getWorkset: vi.fn().mockResolvedValue(worksetA),
    getWorksetDraft: vi.fn().mockResolvedValue({
      workset_id: 'ws-a', version: 3, workflow_schema_version: 1,
      workflow: { schema_version: 1, steps: [{ step_type: 'reconcile_audio_outputs', policy: { kind: 'preset', name: 'balanced', version: 1 } }] },
      updated_at: '',
    }),
    listRevisions: vi.fn().mockResolvedValue([]),
    listWorkflowPresets: vi.fn().mockResolvedValue([]),
    ...overrides,
  })
}

async function mountPage(
  api: ApiClientContract,
  initialPath = '/worksets',
): Promise<{ wrapper: VueWrapper; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/worksets', component: WorksetsPage },
      { path: '/worksets/:worksetId', component: WorksetsPage },
      { path: '/libraries', component: { template: '<div>libraries</div>' } },
    ],
  })
  await router.push(initialPath)
  await router.isReady()
  const wrapper = mount(WorksetsPage, {
    global: {
      plugins: [createPinia(), router, [VueQueryPlugin, { queryClient: createTestQueryClient() }]],
      provide: { [apiClientKey as symbol]: api },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('WorksetsPage', () => {
  it('renders the feed and auto-selects the first workset via deep-link URL', async () => {
    const api = apiStubFor()
    const { wrapper, router } = await mountPage(api)

    expect(wrapper.get('[data-testid="workset-feed"]').text()).toContain('夏季整理')
    expect(wrapper.get('[data-testid="workset-feed"]').text()).toContain('秋季整理')
    // Auto-selection replaces /worksets with the first entry's URL.
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/worksets/ws-a')
    expect(wrapper.find('[data-testid="workset-workbench"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('夏季整理')
  })

  it('shows the empty state when no worksets exist', async () => {
    const api = apiStubFor({ listWorksets: vi.fn().mockResolvedValue(worksetsPage([])) })
    const { wrapper } = await mountPage(api)

    expect(wrapper.find('[data-testid="worksets-empty"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('还没有工作集')
  })

  it('shows the feed error with retry', async () => {
    const failure = new ApiError(500, 'INTERNAL', 'failed to list worksets')
    const api = apiStubFor({ listWorksets: vi.fn().mockRejectedValue(failure) })
    const { wrapper } = await mountPage(api)

    const error = wrapper.get('[data-testid="worksets-error"]')
    expect(error.text()).toContain('INTERNAL')
    expect(wrapper.find('[data-testid="retry-worksets"]').exists()).toBe(true)
  })

  it('loads more pages explicitly via the cursor', async () => {
    const listWorksets = vi
      .fn()
      .mockResolvedValueOnce(worksetsPage([worksetA], 'cursor-1'))
      .mockResolvedValueOnce(worksetsPage([worksetB]))
    const api = apiStubFor({ listWorksets })
    const { wrapper } = await mountPage(api)

    const loadMore = wrapper.get('[data-testid="load-more-worksets"]')
    await loadMore.trigger('click')
    await flushPromises()

    expect(listWorksets).toHaveBeenCalledTimes(2)
    const secondCall = listWorksets.mock.calls[1]?.[0] as { cursor?: string }
    expect(secondCall.cursor).toBe('cursor-1')
    expect(wrapper.text()).toContain('秋季整理')
    expect(wrapper.find('[data-testid="load-more-worksets"]').exists()).toBe(false)
  })

  it('keeps the selected workset when the filter excludes it, with a notice', async () => {
    const api = apiStubFor()
    const { wrapper, router } = await mountPage(api, '/worksets/ws-a')

    // Filter to pending: ws-a (planned) drops out of the feed.
    await wrapper.get('[data-testid="feed-filter-pending"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.query.feed).toBe('pending')
    // The right pane keeps the selected workset.
    expect(wrapper.find('[data-testid="workset-workbench"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="feed-selected-excluded"]').exists()).toBe(true)
  })

  it('shows an orphaned workset in the feed with the read-only state', async () => {
    const orphaned: Workset = {
      ...worksetA,
      workset_id: 'ws-orphan',
      title: '孤立工作集',
      planning_state: 'orphaned',
      library: null,
    }
    const api = apiStubFor({
      listWorksets: vi.fn().mockResolvedValue(worksetsPage([orphaned])),
      getWorkset: vi.fn().mockResolvedValue(orphaned),
    })
    const { wrapper } = await mountPage(api, '/worksets/ws-orphan')

    expect(wrapper.text()).toContain('孤立工作集')
    expect(wrapper.text()).toContain('已孤立')
  })
})
