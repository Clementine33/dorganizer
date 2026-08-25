import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { apiClientKey } from '@/lib/api/client'
import type { ApiClientContract, PlanInfo, PlanResponse } from '@/lib/api/types'
import { usePlansStore } from '@/stores/plans'
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
    createPlan: vi.fn(),
    listPlans: vi.fn().mockResolvedValue([]),
    ...overrides,
  }
}

async function mountPage(
  api: ApiClientContract,
  storeState?: { currentPlan: PlanResponse | null; plans: PlanInfo[] },
): Promise<{ wrapper: VueWrapper; router: Router }> {
  const pinia = createPinia()
  setActivePinia(pinia)
  const plans = usePlansStore()
  if (storeState) {
    plans.currentPlan = storeState.currentPlan
    plans.plans = storeState.plans
  }
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
      plugins: [pinia, router],
      provide: { [apiClientKey as symbol]: api },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('PlanReviewPage', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('renders summary cards for actionable, error and reason and a status pill', async () => {
    const { wrapper } = await mountPage(apiStub(), {
      currentPlan: plan,
      plans: [{ ...plan, plan_id: 'plan-1', status: 'running' } as unknown as PlanInfo],
    })

    expect(wrapper.get('[data-testid="summary-actionable"]').text()).toContain('2')
    expect(wrapper.get('[data-testid="summary-errors"]').text()).toContain('1')
    expect(wrapper.get('[data-testid="summary-operations"]').text()).toContain('2')
    expect(wrapper.get('[data-testid="summary-reason"]').text()).toContain('ACTIONABLE')
    expect(wrapper.get('[data-testid="review-status-pill"]').text()).toContain('进行中')
  })

  it('groups operations by source folder with delete/convert badges', async () => {
    const { wrapper } = await mountPage(apiStub(), { currentPlan: plan, plans: [] })

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
    const { wrapper } = await mountPage(apiStub(), { currentPlan: plan, plans: [] })

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
    const { wrapper } = await mountPage(apiStub(), { currentPlan: plan, plans: [] })

    expect(wrapper.findAll('[data-testid^="execute"], [data-testid*="execute"]')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('执行')
  })

  it('explains a deep link without a loaded plan response', async () => {
    const { wrapper, router } = await mountPage(apiStub(), {
      currentPlan: null,
      plans: [
        {
          plan_id: 'plan-1',
          root_path: 'D:\\Music',
          plan_type: 'slim',
          status: 'planned',
          created_at: '2026-08-22T00:00:00Z',
        },
      ],
    })

    expect(wrapper.text()).toContain('plan-1')
    expect(wrapper.text()).toContain('已规划')
    await wrapper.get('[data-testid="back-to-plans"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/plans')
  })
})