import { createPinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { VueQueryPlugin, type QueryClient } from '@tanstack/vue-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { ApiError, apiClientKey } from '@/lib/api/client'
import type { ApiClientContract, PlanInfo, PlanResponse } from '@/lib/api/types'
import { queryKeys } from '@/queries/query-keys'
import { createTestQueryClient } from '@/test/query-client'
import PlanReviewPage from './PlanReviewPage.vue'

const plan: PlanResponse = {
  plan_id: 'plan-1',
  snapshot_token: 'snapshot-1',
  root_path: 'D:\\Music',
  summary: {
    operation_count: 2,
    error_count: 1,
    total_count: 3,
    actionable_count: 2,
    summary_reason: 'ACTIONABLE',
  },
  operations: [
    {
      type: 'delete',
      source_path: 'D:\\Music\\Blue Train\\01 - Blue Train.flac',
      target_path: '',
    },
    {
      type: 'delete',
      source_path: 'D:\\Music\\Blue Train\\Bonus\\bonus track.flac',
      target_path: '',
    },
    {
      type: 'convert',
      source_path: 'D:\\Music\\Kind of Blue\\So What.flac',
      target_path: 'D:\\Music\\Kind of Blue\\So What.m4a',
    },
  ],
  errors: [
    {
      folder_path: 'D:\\Music\\Incomplete',
      code: 'TOOL_NOT_FOUND',
      message: 'ffmpeg is not available',
      retryable: true,
    },
  ],
  successful_folders: ['D:\\Music\\Blue Train', 'D:\\Music\\Kind of Blue'],
}

const planInfo: PlanInfo = {
  plan_id: 'plan-1',
  root_path: 'D:\\Music',
  plan_type: 'slim',
  status: 'planned',
  created_at: '2026-08-22T00:00:00Z',
}

function apiStub(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  return {
    getHealth: vi.fn(),
    listLibraries: vi.fn().mockResolvedValue([]),
    getLibrary: vi.fn(),
    createLibrary: vi.fn(),
    updateLibrary: vi.fn(),
    deleteLibrary: vi.fn(),
    scanLibrary: vi.fn(),
    listFolders: vi.fn(),
    getFolderTree: vi.fn(),
    getPlan: vi.fn().mockResolvedValue(plan),
    createPlan: vi.fn(),
    listPlans: vi.fn().mockResolvedValue([]),
    ...overrides,
  }
}

async function mountPage(
  api: ApiClientContract,
  queryClient: QueryClient = createTestQueryClient(),
): Promise<{ wrapper: VueWrapper; router: Router; queryClient: QueryClient }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/plans', component: { template: '<div>plans</div>' } },
      { path: '/plans/:id', component: PlanReviewPage },
    ],
  })
  await router.push('/plans/plan-1')
  await router.isReady()
  const wrapper = mount(PlanReviewPage, {
    global: {
      plugins: [createPinia(), router, [VueQueryPlugin, { queryClient }]],
      provide: { [apiClientKey as symbol]: api },
    },
  })
  await flushPromises()
  return { wrapper, router, queryClient }
}

describe('PlanReviewPage', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('renders a seeded detail with summary cards and a status pill from list metadata', async () => {
    const queryClient = createTestQueryClient()
    queryClient.setQueryData(queryKeys.plans.detail(plan.plan_id), plan)
    queryClient.setQueryData(queryKeys.plans.list('lib-a', 100), [{ ...planInfo, status: 'running' }])
    const api = apiStub()
    const { wrapper } = await mountPage(api, queryClient)

    expect(api.getPlan).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="summary-actionable"]').text()).toContain('2')
    expect(wrapper.get('[data-testid="summary-errors"]').text()).toContain('1')
    expect(wrapper.get('[data-testid="summary-operations"]').text()).toContain('2')
    expect(wrapper.get('[data-testid="summary-reason"]').text()).toContain('ACTIONABLE')
    expect(wrapper.get('[data-testid="review-status-pill"]').text()).toContain('进行中')
  })

  it('groups operations by source folder with delete/convert badges', async () => {
    const queryClient = createTestQueryClient()
    queryClient.setQueryData(queryKeys.plans.detail(plan.plan_id), plan)
    const { wrapper } = await mountPage(apiStub(), queryClient)

    const groups = wrapper.findAll('[data-testid="operation-group"]')
    expect(groups).toHaveLength(2)
    expect(groups[0].text()).toContain('D:\\Music\\Blue Train')
    expect(groups[1].text()).toContain('D:\\Music\\Kind of Blue')
    expect(groups[0].text()).toContain('删除')
    expect(groups[1].text()).toContain('转换')
    expect(wrapper.text()).toContain('01 - Blue Train.flac')
    expect(wrapper.text()).toContain('So What.m4a')
  })

  it('keeps the folder errors panel collapsed by default and expands on toggle', async () => {
    const queryClient = createTestQueryClient()
    queryClient.setQueryData(queryKeys.plans.detail(plan.plan_id), plan)
    const { wrapper } = await mountPage(apiStub(), queryClient)

    expect(wrapper.find('[data-testid="folder-errors-content"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="folder-errors-panel"]').text()).toContain('1')

    await wrapper.get('[data-testid="toggle-folder-errors"]').trigger('click')
    expect(wrapper.find('[data-testid="folder-errors-content"]').exists()).toBe(true)
    const content = wrapper.get('[data-testid="folder-errors-content"]')
    expect(content.text()).toContain('D:\\Music\\Incomplete')
    expect(content.text()).toContain('TOOL_NOT_FOUND')
    expect(content.text()).toContain('ffmpeg is not available')
    expect(content.text()).toContain('可重试')
    expect(content.text()).not.toContain('没有文件夹错误')
  })

  it('never renders an Execute control', async () => {
    const queryClient = createTestQueryClient()
    queryClient.setQueryData(queryKeys.plans.detail(plan.plan_id), plan)
    const { wrapper } = await mountPage(apiStub(), queryClient)

    expect(wrapper.findAll('[data-testid^="execute"], [data-testid*="execute"]')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('执行')
  })

  it('loads durable detail for a cold deep link and uses list metadata for the status pill', async () => {
    const api = apiStub({ getPlan: vi.fn().mockResolvedValue(plan) })
    const queryClient = createTestQueryClient()
    queryClient.setQueryData(queryKeys.plans.list('lib-a', 100), [planInfo])
    const { wrapper, router } = await mountPage(api, queryClient)

    expect(api.getPlan).toHaveBeenCalledWith('plan-1', expect.any(AbortSignal))
    expect(wrapper.get('[data-testid="summary-actionable"]').text()).toContain('2')
    expect(wrapper.get('[data-testid="review-status-pill"]').text()).toContain('已规划')
    await wrapper.get('[data-testid="back-to-plans"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/plans')
  })

  it('shows a not-found state when the plan detail 404s', async () => {
    const api = apiStub({
      getPlan: vi.fn().mockRejectedValue(new ApiError(404, 'PLAN_NOT_FOUND', 'plan not found')),
    })
    const { wrapper } = await mountPage(api)

    expect(wrapper.get('[data-testid="plan-detail-error"]').text()).toContain('计划不存在')
    expect(wrapper.text()).toContain('PLAN_NOT_FOUND')
  })

  it('shows a retry state when the plan detail fetch fails', async () => {
    const getPlan = vi
      .fn()
      .mockRejectedValueOnce(new Error('backend unavailable'))
      .mockResolvedValueOnce(plan)
    const api = apiStub({ getPlan })
    const { wrapper } = await mountPage(api)

    expect(wrapper.get('[data-testid="plan-detail-error"]').text()).toContain('计划详情读取失败')
    await wrapper.get('[data-testid="retry-plan-detail"]').trigger('click')
    await flushPromises()
    expect(getPlan).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="summary-actionable"]').text()).toContain('2')
  })

  it('keeps cached detail and shows a non-blocking warning on background refetch failure', async () => {
    const getPlan = vi.fn().mockResolvedValue(plan)
    const api = apiStub({ getPlan })
    const queryClient = createTestQueryClient()
    queryClient.setQueryData(queryKeys.plans.detail(plan.plan_id), plan)
    const { wrapper } = await mountPage(api, queryClient)
    expect(api.getPlan).not.toHaveBeenCalled()

    getPlan.mockRejectedValueOnce(new ApiError(500, 'INTERNAL', 'backend blew up'))
    await queryClient.invalidateQueries({ queryKey: queryKeys.plans.detail(plan.plan_id) })
    await flushPromises()

    const warning = wrapper.get('[data-testid="plan-detail-refresh-warning"]')
    expect(warning.text()).toContain('INTERNAL')
    expect(warning.text()).toContain('backend blew up')
    expect(wrapper.get('[data-testid="summary-actionable"]').text()).toContain('2')
  })
})