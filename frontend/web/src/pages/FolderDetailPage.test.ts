import { createPinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { ApiError, apiClientKey } from '@/lib/api/client'
import { apiStub as sharedApiStub } from '@/test/api-stub'
import type { ApiClientContract, Library, TreeNode } from '@/lib/api/types'
import { installTestQueryPlugin } from '@/test/query-client'
import FolderDetailPage from './FolderDetailPage.vue'

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

const treeRoot: TreeNode = {
  name: 'Blue Train',
  path: 'D:\\Music\\Blue Train',
  type: 'dir',
  format: '',
  bitrate: null,
  children: [
    {
      name: '01 - Blue Train.flac',
      path: 'D:\\Music\\Blue Train\\01 - Blue Train.flac',
      type: 'file',
      format: 'flac',
      bitrate: 920000,
      size: 12345678,
    },
    {
      name: 'Bonus',
      path: 'D:\\Music\\Blue Train\\Bonus',
      type: 'dir',
      format: '',
      bitrate: null,
      children: [
        {
          name: 'bonus track.flac',
          path: 'D:\\Music\\Blue Train\\Bonus\\bonus track.flac',
          type: 'file',
          format: 'm4a',
          bitrate: 640000,
          size: 2048,
        },
      ],
    },
  ],
}

function apiStub(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  return sharedApiStub({
    listLibraries: vi.fn().mockResolvedValue([library]),
    getFolderTree: vi.fn().mockResolvedValue(treeRoot),
    ...overrides,
  })
}

async function mountPage(api: ApiClientContract): Promise<{ wrapper: VueWrapper; router: Router }> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/libraries', component: { template: '<div>libraries</div>' } },
      {
        path: '/libraries/:libraryId/folders/:folderId',
        component: FolderDetailPage,
      },
    ],
  })
  await router.push('/libraries/lib-a/folders/folder-a')
  await router.isReady()
  const wrapper = mount(FolderDetailPage, {
    global: {
      plugins: [createPinia(), router, installTestQueryPlugin()],
      provide: { [apiClientKey as symbol]: api },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('FolderDetailPage', () => {
  beforeEach(() => vi.restoreAllMocks())

  it('loads the folder tree and renders one card with indented rows and file metadata', async () => {
    const api = apiStub()
    const { wrapper } = await mountPage(api)

    expect(api.getFolderTree).toHaveBeenCalledWith('lib-a', 'folder-a', expect.any(AbortSignal))
    expect(wrapper.findAll('[data-testid="folder-tree-card"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('Lossless archive')
    expect(wrapper.text()).toContain('Blue Train')
    expect(wrapper.text()).toContain('01 - Blue Train.flac')
    expect(wrapper.text()).toContain('FLAC')
    expect(wrapper.text()).toContain('920 kbps')
    expect(wrapper.text()).toContain('11.8 MB')

    const rootRow = wrapper.get('[data-testid="tree-row-0"]')
    const firstFileRow = wrapper.get('[data-testid="tree-row-2"]')
    expect(rootRow.attributes('data-indent')).toBe('0')
    expect(firstFileRow.attributes('data-indent')).toBe('1')

    // Nested rows appear once their directory is expanded.
    await wrapper.get('[aria-label="展开 Bonus"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('bonus track.flac')
    expect(wrapper.get('[data-testid="tree-row-2"]').attributes('data-indent')).toBe('2')
  })

  it('shows a loading state while the tree is being fetched', async () => {
    const { wrapper } = await mountPage(
      apiStub({ getFolderTree: vi.fn(() => new Promise<TreeNode>(() => {})) }),
    )
    expect(wrapper.text()).toContain('正在读取文件夹树…')
  })

  it('shows the envelope code when the tree request fails and retries', async () => {
    const failure = new ApiError(404, 'FOLDER_NOT_FOUND', 'folder not found')
    const api = apiStub({
      getFolderTree: vi.fn().mockRejectedValueOnce(failure).mockResolvedValueOnce(treeRoot),
    })
    const { wrapper } = await mountPage(api)

    const banner = wrapper.get('[data-testid="tree-error"]')
    expect(banner.text()).toContain('FOLDER_NOT_FOUND')
    expect(banner.text()).toContain('folder not found')

    await wrapper.get('[data-testid="retry-tree"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('01 - Blue Train.flac')
  })

  it('invites action when the folder has no audio files', async () => {
    const empty: TreeNode = { ...treeRoot, children: [] }
    const { wrapper } = await mountPage(apiStub({ getFolderTree: vi.fn().mockResolvedValue(empty) }))
    expect(wrapper.text()).toContain('还没有音频文件')
  })

  it('updates the selected-file summary as files and directories are selected', async () => {
    const { wrapper } = await mountPage(apiStub())

    expect(wrapper.text()).toContain('在树中选择文件或文件夹')

    await wrapper.get('[data-testid="file-checkbox-2"]').setValue(true)
    expect(wrapper.text()).toContain('1 个文件已选择')

    await wrapper.get('[data-testid="dir-checkbox-1"]').setValue(true)
    expect(wrapper.text()).toContain('2 个文件已选择')
  })

  it('loads the tree even when the library list fails (direct-link fallback)', async () => {
    const api = apiStub({
      listLibraries: vi.fn().mockRejectedValue(new ApiError(500, 'INTERNAL', 'failed to list libraries')),
    })
    const { wrapper } = await mountPage(api)

    expect(api.getFolderTree).toHaveBeenCalledWith('lib-a', 'folder-a', expect.any(AbortSignal))
    expect(wrapper.find('[data-testid="folder-tree-card"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('01 - Blue Train.flac')
  })

})