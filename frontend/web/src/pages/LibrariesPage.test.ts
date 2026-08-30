import { createPinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { VueQueryPlugin, type QueryClient } from '@tanstack/vue-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { ApiError, apiClientKey } from '@/lib/api/client'
import { apiStub as sharedApiStub } from '@/test/api-stub'
import { desktopAdapterKey, type DesktopAdapter } from '@/lib/desktop/desktop-adapter'
import type {
  ApiClientContract,
  Folder,
  Library,
  ScanEvent,
} from '@/lib/api/types'
import { createTestQueryClient } from '@/test/query-client'
import LibrariesPage from './LibrariesPage.vue'

// jsdom has no layout, so the folder-list virtualizer's scroller measures a
// 0-height viewport and renders an empty window. Give every element a
// viewport-sized box in this file only (the 0-layout semantics itself is
// covered explicitly in FolderFlatList.test.ts 'renders an empty window…').
// Set AFTER restoreAllMocks so the describe-level reset cannot strip them.
function stubViewportMetrics(): void {
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(600)
  vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(800)
}

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

function apiStub(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  return sharedApiStub({
    listLibraries: vi.fn().mockResolvedValue([library]),
    scanLibrary: vi.fn(() =>
      (async function* (): AsyncGenerator<ScanEvent> {
        yield { type: 'completed', data: { stage: 'scan', scan_id: 'scan-1', root_path: library.root_path, files_scanned: 11 } }
      })(),
    ),
    listFolders: vi.fn().mockResolvedValue(folders),
    ...overrides,
  })
}

async function mountPage(api: ApiClientContract): Promise<{ wrapper: VueWrapper; router: Router }> {
  return mountPageWithClient(api, createTestQueryClient())
}

async function mountPageWithClient(
  api: ApiClientContract,
  queryClient: QueryClient,
): Promise<{ wrapper: VueWrapper; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/libraries', component: LibrariesPage },
      { path: '/libraries/:libraryId/folders/:folderId', component: { template: '<div>folder</div>' } },
    ],
  })
  await router.push('/libraries')
  await router.isReady()
  const wrapper = mount(LibrariesPage, {
    global: {
      plugins: [createPinia(), router, [VueQueryPlugin, { queryClient }]],
      provide: {
        [apiClientKey as symbol]: api,
        [desktopAdapterKey as symbol]: {
          pickFolder: vi.fn().mockResolvedValue(null),
        } as unknown as DesktopAdapter,
      },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('LibrariesPage', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    stubViewportMetrics()
  })

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

  it('selects folders, opens the create dialog, and creates one workset before navigating to it', async () => {
    const api = apiStub({
      createWorkset: vi.fn().mockResolvedValue({
        workset: {
          workset_id: 'ws-1', title: 't', version: 1, library: null, planning_state: 'unplanned',
          current_revision: null, active_generation: null, latest_generation: null, members: [],
          updated_at: '', created_at: '',
        },
        created: true,
      }),
    })
    const { wrapper, router } = await mountPage(api)
    const createButton = wrapper.get('[data-testid="create-workset"]')
    expect(createButton.attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="folder-checkbox-folder-a"]').setValue(true)
    expect(createButton.attributes('disabled')).toBeUndefined()

    await createButton.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="create-workset-dialog"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="workset-folder-review"]').text()).toContain('Blue Train')

    await wrapper.get('[data-testid="confirm-create-workset"]').trigger('click')
    await flushPromises()

    expect(api.createWorkset).toHaveBeenCalledTimes(1)
    const [input, key] = vi.mocked(api.createWorkset).mock.calls[0]
    expect(input).toEqual({ library_id: 'lib-a', title: 'Lossless archive 工作集', folder_ids: ['folder-a'] })
    expect(String(key)).not.toBe('')
    expect(router.currentRoute.value.fullPath).toBe('/worksets/ws-1')
  })

  it('rejects an empty workset title in the create dialog', async () => {
    const api = apiStub()
    const { wrapper } = await mountPage(api)
    await wrapper.get('[data-testid="folder-checkbox-folder-a"]').setValue(true)
    await wrapper.get('[data-testid="create-workset"]').trigger('click')
    await flushPromises()

    const input = wrapper.get('[data-testid="workset-title-input"]')
    await input.setValue('   ')
    expect(wrapper.get('[data-testid="confirm-create-workset"]').attributes('disabled')).toBeDefined()
    expect(api.createWorkset).not.toHaveBeenCalled()
  })

  it('selects every folder from the table header and opens folder detail from its name', async () => {
    const { wrapper, router } = await mountPage(apiStub())

    await wrapper.get('[data-testid="select-all-folders"]').setValue(true)
    expect(wrapper.get('[data-testid="create-workset"]').text()).toContain('2')

    await wrapper.get('[data-testid="folder-link-folder-b"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/libraries/lib-a/folders/folder-b')
  })

  it('fetches folders exactly once on a cold mount', async () => {
    const api = apiStub()
    await mountPage(api)

    expect(api.listLibraries).toHaveBeenCalledTimes(1)
    expect(api.listFolders).toHaveBeenCalledTimes(1)
  })

  it('hides scan progress on other libraries without cancelling the running scan', async () => {
    let release!: () => void
    const scanLibrary = vi.fn((_id: string, signal: AbortSignal) => ({
      async *[Symbol.asyncIterator]() {
        yield { type: 'started', data: { stage: 'scan', message: 'Scanning' } } as ScanEvent
        await new Promise<void>((resolve) => {
          release = resolve
          signal.addEventListener('abort', () => resolve(), { once: true })
        })
        if (signal.aborted) yield { type: 'cancelled', data: { stage: 'scan', message: 'scan canceled' } } as ScanEvent
      },
    }))
    const secondLibrary: Library = { ...library, id: 'lib-b', name: 'Other library' }
    const { wrapper } = await mountPage(apiStub({ scanLibrary, listLibraries: vi.fn().mockResolvedValue([library, secondLibrary]) }))

    await wrapper.get('[data-testid="scan-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="cancel-scan"]').exists()).toBe(true)

    // Switch to another library: progress + cancel hide, the backend scan
    // keeps running, and the disabled scan button still signals the
    // background scan.
    await wrapper.get('[aria-label="切换媒体库"]').setValue('lib-b')
    await flushPromises()
    expect(wrapper.find('[data-testid="cancel-scan"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="scan-button"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('扫描中…')
    expect(scanLibrary.mock.calls[0]?.[1].aborted).toBe(false)

    // Switching back reveals the still-running scan.
    await wrapper.get('[aria-label="切换媒体库"]').setValue('lib-a')
    await flushPromises()
    expect(wrapper.find('[data-testid="cancel-scan"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('扫描中…')
    await wrapper.get('[data-testid="cancel-scan"]').trigger('click')
    await flushPromises()
    release()
  })

  it('clears folder selection when the active library root genuinely changes', async () => {
    const api = apiStub({
      updateLibrary: vi.fn().mockResolvedValue({ ...library, root_path: 'E:\\NewRoot' }),
    })
    const { wrapper } = await mountPage(api)

    await wrapper.get('[data-testid="folder-checkbox-folder-a"]').setValue(true)
    const planButton = wrapper.get('[data-testid="create-workset"]')
    expect(planButton.attributes('disabled')).toBeUndefined()

    await wrapper.get('[aria-label="编辑媒体库"]').trigger('click')
    await wrapper.get('#library-root').setValue('E:\\NewRoot')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(api.updateLibrary).toHaveBeenCalledWith('lib-a', { name: 'Lossless archive', root_path: 'E:\\NewRoot' })
    expect(wrapper.get('[data-testid="create-workset"]').attributes('disabled')).toBeDefined()
  })

  it('remounts from the cached folder list without issuing new GETs', async () => {
    const api = apiStub()
    const queryClient = createTestQueryClient()

    const first = await mountPageWithClient(api, queryClient)
    expect(first.wrapper.text()).toContain('Blue Train')
    first.wrapper.unmount()
    await flushPromises()

    const second = await mountPageWithClient(api, queryClient)
    expect(second.wrapper.text()).toContain('Blue Train')
    expect(api.listLibraries).toHaveBeenCalledTimes(1)
    expect(api.listFolders).toHaveBeenCalledTimes(1)
  })
})
