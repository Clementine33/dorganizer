import { createPinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { ApiError, apiClientKey } from '@/lib/api/client'
import type {
  ApiClientContract,
  Folder,
  Library,
  PlanResponse,
  ScanEvent,
} from '@/lib/api/types'
import LibrariesPage from './LibrariesPage.vue'

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

const folders: Folder[] = [
  { id: 'folder-a', name: 'Blue Train', path: 'D:\\Music\\Blue Train', relative_path: 'Blue Train', audio_file_count: 5 },
  { id: 'folder-b', name: 'Kind of Blue', path: 'D:\\Music\\Kind of Blue', relative_path: 'Kind of Blue', audio_file_count: 6 },
]

const plan: PlanResponse = {
  plan_id: 'plan-1',
  snapshot_token: 'snap-1',
  root_path: 'D:\\Music',
  summary: {
    operation_count: 1,
    error_count: 0,
    total_count: 1,
    actionable_count: 1,
    summary_reason: 'ACTIONABLE',
  },
  operations: [],
  errors: [],
  successful_folders: ['D:\\Music\\Blue Train'],
}

function apiStub(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  return {
    getHealth: vi.fn(),
    listLibraries: vi.fn().mockResolvedValue([library]),
    getLibrary: vi.fn(),
    createLibrary: vi.fn(),
    updateLibrary: vi.fn(),
    deleteLibrary: vi.fn(),
    scanLibrary: vi.fn(() =>
      (async function* (): AsyncGenerator<ScanEvent> {
        yield { type: 'completed', data: { stage: 'scan', scan_id: 'scan-1', root_path: library.root_path, files_scanned: 11 } }
      })(),
    ),
    listFolders: vi.fn().mockResolvedValue(folders),
    getFolderTree: vi.fn(),
    createPlan: vi.fn().mockResolvedValue(plan),
    listPlans: vi.fn(),
    ...overrides,
  }
}

async function mountPage(api: ApiClientContract): Promise<{ wrapper: VueWrapper; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/libraries', component: LibrariesPage },
      { path: '/libraries/:libraryId/folders/:folderId', component: { template: '<div>folder</div>' } },
      { path: '/plans/:id', component: { template: '<div>plan</div>' } },
    ],
  })
  await router.push('/libraries')
  await router.isReady()
  const wrapper = mount(LibrariesPage, {
    global: {
      plugins: [createPinia(), router],
      provide: { [apiClientKey as symbol]: api },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('LibrariesPage', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('renders a library switcher and the flat folder list', async () => {
    const { wrapper } = await mountPage(apiStub())

    expect(wrapper.get('[aria-label="切换媒体库"]').text()).toContain('Lossless archive')
    expect(wrapper.text()).toContain('Blue Train')
    expect(wrapper.text()).toContain('5 个音频文件')
    expect(wrapper.text()).toContain('D:\\Music')
  })

  it('shows the API envelope code in the recovery banner', async () => {
    const failure = new ApiError(409, 'LIBRARY_EXISTS', 'library already exists')
    const { wrapper } = await mountPage(
      apiStub({ listFolders: vi.fn().mockRejectedValue(failure) }),
    )

    const banner = wrapper.get('[data-testid="page-error"]')
    expect(banner.text()).toContain('LIBRARY_EXISTS')
    expect(banner.text()).toContain('library already exists')
  })

  it('shows and recovers from an initial library-list failure without a false empty state', async () => {
    const failure = new ApiError(500, 'INTERNAL', 'failed to list libraries')
    const api = apiStub({
      listLibraries: vi.fn().mockRejectedValueOnce(failure).mockResolvedValueOnce([library]),
    })
    const { wrapper } = await mountPage(api)

    const banner = wrapper.get('[data-testid="page-error"]')
    expect(banner.text()).toContain('INTERNAL')
    expect(banner.text()).toContain('failed to list libraries')
    expect(wrapper.find('[data-testid="empty-add-library"]').exists()).toBe(false)

    await wrapper.get('[data-testid="retry-page"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Blue Train')
  })

  it('invites adding a library when none exist', async () => {
    const { wrapper } = await mountPage(apiStub({ listLibraries: vi.fn().mockResolvedValue([]) }))

    expect(wrapper.text()).toContain('还没有媒体库')
    expect(wrapper.get('[data-testid="empty-add-library"]').text()).toContain('添加媒体库')
  })

  it('disables scan while streaming and allows cancellation', async () => {
    let release!: () => void
    const scanLibrary = vi.fn((_id: string, signal: AbortSignal) => ({
      async *[Symbol.asyncIterator]() {
        yield { type: 'started', data: { stage: 'scan' } } as ScanEvent
        await new Promise<void>((resolve) => {
          release = resolve
          signal.addEventListener('abort', () => resolve(), { once: true })
        })
        if (signal.aborted) yield { type: 'cancelled', data: { stage: 'scan', message: 'scan canceled' } } as ScanEvent
      },
    }))
    const { wrapper } = await mountPage(apiStub({ scanLibrary }))

    await wrapper.get('[data-testid="scan-button"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="scan-button"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="cancel-scan"]').attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-testid="cancel-scan"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('已取消')
    release()
  })

  it('selects folders and creates one batch plan before navigating to review', async () => {
    const api = apiStub()
    const { wrapper, router } = await mountPage(api)
    const planButton = wrapper.get('[data-testid="generate-plan"]')
    expect(planButton.attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="folder-checkbox-folder-a"]').setValue(true)
    expect(planButton.attributes('disabled')).toBeUndefined()

    await planButton.trigger('click')
    await flushPromises()

    expect(api.createPlan).toHaveBeenCalledWith({
      library_id: 'lib-a',
      folder_ids: ['folder-a'],
      plan_type: 'slim',
      target_format: 'slim:mode1',
      prune_matched_excluded: false,
    })
    expect(router.currentRoute.value.fullPath).toBe('/plans/plan-1')
  })

  it('retries the failed plan request from the page error banner', async () => {
    const failure = new ApiError(500, 'INTERNAL', 'failed to create plan')
    const api = apiStub({
      createPlan: vi.fn().mockRejectedValueOnce(failure).mockResolvedValueOnce(plan),
    })
    const { wrapper, router } = await mountPage(api)

    await wrapper.get('[data-testid="folder-checkbox-folder-a"]').setValue(true)
    await wrapper.get('[data-testid="generate-plan"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="page-error"]').text()).toContain('failed to create plan')

    await wrapper.get('[data-testid="retry-page"]').trigger('click')
    await flushPromises()
    expect(api.createPlan).toHaveBeenCalledTimes(2)
    expect(router.currentRoute.value.fullPath).toBe('/plans/plan-1')
  })

  it('selects every folder from the table header and opens folder detail from its name', async () => {
    const { wrapper, router } = await mountPage(apiStub())

    await wrapper.get('[data-testid="select-all-folders"]').setValue(true)
    expect(wrapper.get('[data-testid="generate-plan"]').text()).toContain('2')

    await wrapper.get('[data-testid="folder-link-folder-b"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/libraries/lib-a/folders/folder-b')
  })
})
