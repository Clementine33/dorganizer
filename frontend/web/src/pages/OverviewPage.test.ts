import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { createPinia } from 'pinia'
import { apiClientKey } from '@/lib/api/client'
import { apiStub } from '@/test/api-stub'
import { installTestQueryPlugin } from '@/test/query-client'
import OverviewPage from './OverviewPage.vue'
import { useLibraryUiStore } from '@/stores/library-ui'
import type { Library, LibraryDir, Workset } from '@/lib/api/types'

/** The page's selection store, as a row click would drive it. */
function ui() {
  return useLibraryUiStore()
}

/**
 * The overview is where a library's conversion scope is chosen (spec N3, R1).
 * These tests drive the real page against a stubbed API: what they pin is the
 * behavior the spec names — the create carries the selected library-relative
 * paths and the record it saw, a creation reports the folders it skipped, and
 * replacing an existing record is confirmed first.
 *
 * The directory rows themselves are virtualized and covered by the DirList
 * test; here the selection is driven through the same store action a row click
 * calls, so these tests are about the page contract, not about scrolling.
 */

const library: Library = {
  id: 'lib-1',
  name: 'Archive',
  root_path: '/music',
  created_at: '',
  updated_at: '',
  last_scan_at: null,
  last_scan_status: 'completed',
  last_scan_error: '',
}

const dirs: LibraryDir[] = [
  { name: 'albumA', path: '/music/albumA', rel_path: 'albumA', audio_file_count: 3, file_count: 4 },
  { name: 'docs', path: '/music/docs', rel_path: 'docs', audio_file_count: 0, file_count: 1 },
]

const record: Workset = {
  workset_id: 'ws-1',
  title: 'Archive',
  version: 1,
  library: { library_id: 'lib-1', name: 'Archive', root_path: '/music' },
  members: [
    { member_id: 'm-1', folder_path: '/music/albumA', folder_name: 'albumA', rel_path: 'albumA' },
  ],
  operations: [],
  updated_at: '',
  created_at: '',
}

function routerFor(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/worksets/libraries/:libraryId', name: 'workbench-overview', component: OverviewPage },
      { path: '/worksets/libraries/:libraryId/files', name: 'overview-files', component: { template: '<div />' } },
      { path: '/worksets/libraries/:libraryId/conversion', name: 'conversion', component: { template: '<div />' } },
      { path: '/worksets', name: 'worksets', component: { template: '<div />' } },
    ],
  })
}

async function mountOverview(overrides: Partial<Record<string, unknown>> = {}): Promise<{
  wrapper: VueWrapper
  api: ReturnType<typeof apiStub>
  router: Router
}> {
  const api = apiStub({
    listLibraries: vi.fn().mockResolvedValue([library]),
    listDirs: vi.fn().mockResolvedValue(dirs),
    getCurrentRecord: vi.fn().mockResolvedValue({ workset: null }),
    ...overrides,
  } as never)
  const router = routerFor()
  await router.push('/worksets/libraries/lib-1')
  await router.isReady()
  const wrapper = mount(OverviewPage, {
    global: { plugins: [createPinia(), router, installTestQueryPlugin()], provide: { [apiClientKey as symbol]: api } },
  })
  await flushPromises()
  return { wrapper, api, router }
}

beforeEach(() => {
  localStorage.clear()
})

describe('workbench overview', () => {
  it('lists what the scan saw, and offers no action until something is selected', async () => {
    const { wrapper, api } = await mountOverview()

    expect(api.listDirs).toHaveBeenCalled()
    expect(wrapper.get('[data-testid="dir-list"]').text()).toContain('共 2 个文件夹')
    // Browsing alone creates nothing, and there is no empty action strip (N15).
    expect(wrapper.find('[data-testid="enter-conversion"]').exists()).toBe(false)

    ui().toggleDir('albumA', dirs)
    await flushPromises()
    expect(wrapper.get('[data-testid="enter-conversion"]').text()).toContain('进入转换')
  })

  it('creates the record from the selected library-relative paths', async () => {
    const createCurrentRecord = vi.fn().mockResolvedValue({ workset: record, created: true, recorded: 1, skipped: [] })
    const { wrapper, router } = await mountOverview({ createCurrentRecord })
    ui().toggleDir('albumA', dirs)
    await flushPromises()
    await wrapper.get('[data-testid="enter-conversion"]').trigger('click')
    await flushPromises()

    const request = createCurrentRecord.mock.calls[0] as unknown as [string, string, { folder_paths: string[] }]
    expect(request[0]).toBe('lib-1')
    expect(request[1]).toBe('conversion')
    expect(request[2].folder_paths).toEqual(['albumA'])
    expect(router.currentRoute.value.name).toBe('conversion')
  })

  it('reports the folders a creation skipped instead of hiding them', async () => {
    const createCurrentRecord = vi.fn().mockResolvedValue({
      workset: record,
      created: true,
      recorded: 1,
      skipped: [{ path: 'docs', reason: 'no_audio' }],
    })
    const { wrapper } = await mountOverview({ createCurrentRecord })
    ui().toggleDir('albumA', dirs)
    await flushPromises()
    await wrapper.get('[data-testid="enter-conversion"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="skipped-report"]').text()).toContain('docs')
    expect(wrapper.get('[data-testid="skipped-report"]').text()).toContain('没有音频')
  })

  it('asks before replacing an existing record', async () => {
    const createCurrentRecord = vi.fn().mockResolvedValue({ workset: record, created: true, recorded: 1, skipped: [] })
    const { wrapper } = await mountOverview({
      getCurrentRecord: vi.fn().mockResolvedValue({ workset: record }),
      createCurrentRecord,
    })

    expect(wrapper.get('[data-testid="current-record"]').text()).toContain('进入转换')
    ui().toggleDir('albumA', dirs)
    await flushPromises()

    await wrapper.get('[data-testid="enter-conversion"]').trigger('click')
    await flushPromises()

    // Nothing was sent until the confirmation is accepted, and the dialog
    // says what replacing costs (spec R1.3). The modal is portalled, so it is
    // the document that holds it.
    expect(createCurrentRecord).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('删除它的设置、当前计划与执行结果')
    const confirm = document.body.querySelector('[data-testid="confirm-replace"]') as HTMLElement
    confirm.click()
    await flushPromises()

    const request = createCurrentRecord.mock.calls[0] as unknown as [string, string, { expected_current_id?: string }]
    expect(request[2].expected_current_id).toBe('ws-1')
  })

  it('surfaces a refused creation with the folders it could not use', async () => {
    const { ApiError } = await import('@/lib/api/client')
    const createCurrentRecord = vi
      .fn()
      .mockRejectedValue(
        new ApiError(400, 'NO_AUDIO_MEMBERS', 'none of the selected folders can join', ['docs: no_audio']),
      )
    const { wrapper } = await mountOverview({ createCurrentRecord })
    ui().toggleDir('albumA', dirs)
    await flushPromises()
    await wrapper.get('[data-testid="enter-conversion"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="create-error"]').text()).toContain('none of the selected folders')
    expect(wrapper.get('[data-testid="skipped-report"]').text()).toContain('docs')
  })
})
