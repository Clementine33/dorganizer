import { createPinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { ApiError, apiClientKey } from '@/lib/api/client'
import type { ApiClientContract, Library, PlanInfo } from '@/lib/api/types'
import PlansPage from './PlansPage.vue'

const library: Library = {
  id: 'lib-a',
  name: 'Lossless archive',
  root_path: 'D:\\Music',
  created_at: '2026-08-22T00:00:00Z',
  updated_at: '2026-08-22T00:00:00Z',
  last_scan_at: '2026-08-22T01:00:00Z',
  last_scan_status: 'completed',
  last_scan_error: '',
}

const plans: PlanInfo[] = [
  {
    plan_id: 'plan-b',
    root_path: 'D:\\Music\\Blue Train',
    plan_type: 'slim',
    status: 'planned',
    created_at: '2026-08-22T00:00:00Z',
  },
  {
    plan_id: 'plan-a',
    root_path: 'D:\\Music',
    plan_type: 'slim',
    status: 'finished',
    created_at: '2026-08-21T10:30:00Z',
  },
]

function apiStub(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  return {
    getHealth: vi.fn(),
    listLibraries: vi.fn().mockResolvedValue([library]),
    getLibrary: vi.fn(),
    createLibrary: vi.fn(),
    updateLibrary: vi.fn(),
    deleteLibrary: vi.fn(),
    scanLibrary: vi.fn(),
    listFolders: vi.fn(),
    getFolderTree: vi.fn(),
    createPlan: vi.fn(),
    listPlans: vi.fn().mockResolvedValue(plans),
    ...overrides,
  }
}

async function mountPage(api: ApiClientContract): Promise<{ wrapper: VueWrapper; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/plans', component: PlansPage },
      { path: '/plans/:id', component: { template: '<div>plan</div>' } },
    ],
  })
  await router.push('/plans')
  await router.isReady()
  const wrapper = mount(PlansPage, {
    global: {
      plugins: [createPinia(), router],
      provide: { [apiClientKey as symbol]: api },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('PlansPage', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('lists library plans with status badges and created time, opening review on click', async () => {
    const api = apiStub()
    const { wrapper, router } = await mountPage(api)

    expect(api.listPlans).toHaveBeenCalledWith('lib-a')
    expect(wrapper.text()).toContain('Blue Train')
    expect(wrapper.text()).toContain('2026-08-22 00:00')
    expect(wrapper.text()).toContain('2026-08-21 10:30')
    expect(wrapper.text()).toContain('已规划')
    expect(wrapper.text()).toContain('已完成')

    await wrapper.get('[data-testid="plan-link-plan-b"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/plans/plan-b')
  })

  it('shows a loading state while plans are fetched', async () => {
    const { wrapper } = await mountPage(
      apiStub({ listPlans: vi.fn(() => new Promise<PlanInfo[]>(() => {})) }),
    )
    expect(wrapper.text()).toContain('正在读取计划…')
  })

  it('shows the envelope code when the plan list fails and retries', async () => {
    const failure = new ApiError(404, 'LIBRARY_NOT_FOUND', 'library not found')
    const api = apiStub({
      listPlans: vi.fn().mockRejectedValueOnce(failure).mockResolvedValueOnce(plans),
    })
    const { wrapper } = await mountPage(api)

    const banner = wrapper.get('[data-testid="plans-error"]')
    expect(banner.text()).toContain('LIBRARY_NOT_FOUND')
    expect(banner.text()).toContain('library not found')

    await wrapper.get('[data-testid="retry-plans"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Blue Train')
  })

  it('shows the recovery banner instead of the empty state when loading fails', async () => {
    const failure = new ApiError(500, 'INTERNAL', 'failed to list plans')
    const api = apiStub({
      listPlans: vi.fn().mockRejectedValueOnce(failure).mockResolvedValueOnce(plans),
    })
    const { wrapper } = await mountPage(api)

    const banner = wrapper.get('[data-testid="plans-error"]')
    expect(banner.text()).toContain('INTERNAL')
    expect(banner.text()).toContain('failed to list plans')
    expect(banner.text()).toContain('重试')
    // The failure path must not be presented as a legitimately-empty list.
    expect(wrapper.find('[data-testid="empty-to-libraries"]').exists()).toBe(false)

    await wrapper.get('[data-testid="retry-plans"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Blue Train')
  })

  it('shows and recovers from a prerequisite library-list failure', async () => {
    const failure = new ApiError(500, 'INTERNAL', 'failed to list libraries')
    const api = apiStub({
      listLibraries: vi.fn().mockRejectedValueOnce(failure).mockResolvedValueOnce([library]),
    })
    const { wrapper } = await mountPage(api)

    const banner = wrapper.get('[data-testid="plans-error"]')
    expect(banner.text()).toContain('INTERNAL')
    expect(banner.text()).toContain('failed to list libraries')
    expect(wrapper.find('[data-testid="empty-to-libraries"]').exists()).toBe(false)

    await wrapper.get('[data-testid="retry-plans"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Blue Train')
  })

  it('invites generating the first plan when none exist', async () => {
    const { wrapper } = await mountPage(apiStub({ listPlans: vi.fn().mockResolvedValue([]) }))
    expect(wrapper.text()).toContain('还没有计划')
  })
})