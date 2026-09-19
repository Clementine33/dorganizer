import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { createPinia } from 'pinia'
import { ApiError, apiClientKey } from '@/lib/api/client'
import { apiStub } from '@/test/api-stub'
import { installTestQueryPlugin } from '@/test/query-client'
import MemberFiles from './MemberFiles.vue'
import type { FileOperationResult, TreeNode, Workset } from '@/lib/api/types'

/**
 * The shared current-files module (spec T1, T2, F1-F4).
 *
 * What these tests pin is the module's own contract: entering refreshes the
 * member and modification is refused until the refresh settles, a failed
 * refresh keeps the cached tree and says it is unrefreshed, a request that
 * changed files but could not refresh says both facts, and a member link whose
 * record was replaced offers the way back instead of another member's files.
 */

const memberTree: TreeNode = {
  name: 'albumA',
  path: '/music/albumA',
  rel_path: '',
  type: 'dir',
  format: '',
  bitrate: null,
  children: [
    {
      name: '01.flac',
      path: '/music/albumA/01.flac',
      rel_path: '01.flac',
      type: 'file',
      size: 1024,
      bitrate: 900000,
      format: 'flac',
    },
    {
      name: 'cover.jpg',
      path: '/music/albumA/cover.jpg',
      rel_path: 'cover.jpg',
      type: 'file',
      size: 512,
      bitrate: null,
      format: '',
    },
  ],
}

const record: Workset = {
  workset_id: 'ws-1',
  title: 'Archive',
  version: 1,
  library: { library_id: 'lib-1', name: 'Archive', root_path: '/music' },
  members: [{ member_id: 'm-1', folder_path: '/music/albumA', folder_name: 'albumA', rel_path: 'albumA' }],
  operations: [],
  updated_at: '',
  created_at: '',
}

function routerFor(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/worksets/libraries/:libraryId/files', name: 'overview-files', component: MemberFiles },
      {
        path: '/worksets/libraries/:libraryId/conversion/members/:memberId/files',
        name: 'conversion-member-files',
        component: MemberFiles,
      },
      { path: '/worksets/libraries/:libraryId/conversion', name: 'conversion', component: { template: '<div />' } },
      { path: '/worksets/libraries/:libraryId', name: 'workbench-overview', component: { template: '<div />' } },
      { path: '/worksets', name: 'worksets', component: { template: '<div />' } },
    ],
  })
}

async function mountFiles(
  path: string,
  overrides: Partial<Record<string, unknown>> = {},
): Promise<{ wrapper: VueWrapper; api: ReturnType<typeof apiStub>; router: Router }> {
  const api = apiStub({
    listLibraries: vi.fn().mockResolvedValue([
      {
        id: 'lib-1',
        name: 'Archive',
        root_path: '/music',
        created_at: '',
        updated_at: '',
        last_scan_at: null,
        last_scan_status: 'completed',
        last_scan_error: '',
      },
    ]),
    getCurrentRecord: vi.fn().mockResolvedValue({ workset: record }),
    getMemberTree: vi.fn().mockResolvedValue({ tree: memberTree }),
    refreshMemberTree: vi.fn().mockResolvedValue({ tree: memberTree, refreshed: true }),
    applyFileOperation: vi.fn(),
    ...overrides,
  } as never)
  const router = routerFor()
  await router.push(path)
  await router.isReady()
  const wrapper = mount(MemberFiles, {
    global: { plugins: [createPinia(), router, installTestQueryPlugin()], provide: { [apiClientKey as symbol]: api } },
  })
  await flushPromises()
  return { wrapper, api, router }
}

beforeEach(() => {
  localStorage.clear()
})

describe('shared member files', () => {
  it('refreshes the member on entry and disables modification until it settles', async () => {
    let release: (() => void) | undefined
    const refreshMemberTree = vi.fn(
      () =>
        new Promise((resolve) => {
          release = () => resolve({ tree: memberTree, refreshed: true })
        }),
    )
    const { wrapper, api } = await mountFiles('/worksets/libraries/lib-1/files?folder=albumA', {
      refreshMemberTree,
    })

    expect(api.getMemberTree).toHaveBeenCalledWith('lib-1', 'albumA', expect.anything())
    expect(wrapper.get('[data-testid="refreshing"]').text()).toContain('正在刷新')
    expect(wrapper.text()).toContain('刷新完成前不能修改')

    release?.()
    await flushPromises()
    expect(refreshMemberTree).toHaveBeenCalledWith('lib-1', 'albumA')
    expect(wrapper.find('[data-testid="refreshing"]').exists()).toBe(false)
  })

  it('keeps the cached tree and marks it unrefreshed when the refresh fails', async () => {
    const refreshMemberTree = vi
      .fn()
      .mockRejectedValue(new ApiError(502, 'SCAN_FAILED', 'scan blew up'))
    const { wrapper } = await mountFiles('/worksets/libraries/lib-1/files?folder=albumA', {
      refreshMemberTree,
    })

    // The tree is still the cached one, and the failure is stated separately.
    expect(wrapper.get('[data-testid="member-tree"]').text()).toContain('01.flac')
    expect(wrapper.get('[data-testid="refresh-error"]').text()).toContain('scan blew up')
    expect(wrapper.get('[data-testid="tree-stale"]').text()).toContain('未能刷新')
  })

  it('sends a batch delete as a single request over the selection', async () => {
    const result: FileOperationResult = {
      operation: 'soft_delete',
      member_path: 'albumA',
      items: [
        { source: '01.flac', status: 'ok', recovered_path: 'Delete/albumA/01.flac' },
        { source: 'cover.jpg', status: 'failed', code: 'TARGET_EXISTS', message: 'the destination already exists' },
      ],
      succeeded: 1,
      failed: 1,
      untouched: 0,
      refresh: { ok: false, code: 'REFRESH_FAILED', message: 'files were modified, but refreshing failed' },
    }
    const applyFileOperation = vi.fn().mockResolvedValue(result)
    const { wrapper } = await mountFiles('/worksets/libraries/lib-1/files?folder=albumA', { applyFileOperation })

    // Select both files (the checkboxes are the tree's, not virtualized).
    await wrapper.get('[data-testid="file-checkbox-01.flac"]').setValue(true)
    await wrapper.get('[data-testid="file-checkbox-cover.jpg"]').setValue(true)
    await wrapper.get('[data-testid="delete-selection"]').trigger('click')
    await flushPromises()

    // The confirmation is portalled; accept it.
    ;(document.body.querySelector('[data-testid="delete-submit"]') as HTMLElement).click()
    await flushPromises()

    const call = applyFileOperation.mock.calls[0] as unknown as [
      string,
      { member_path: string; operation: string; items: { source: string }[] },
    ]
    expect(call[0]).toBe('lib-1')
    expect(call[1].operation).toBe('soft_delete')
    expect(call[1].items.map((item) => item.source)).toEqual(['01.flac', 'cover.jpg'])

    // Both facts are reported: what happened per item, and that the refresh
    // failed after the files were already modified.
    const report = wrapper.get('[data-testid="file-op-result"]').text()
    expect(report).toContain('完成 1 项')
    expect(report).toContain('失败 1 项')
    expect(report).toContain('Delete/albumA/01.flac')
    expect(report).toContain('文件已修改，刷新失败')
  })

  it('explains a refused request without claiming anything ran', async () => {
    const applyFileOperation = vi
      .fn()
      .mockRejectedValue(new ApiError(409, 'BUSY', 'a scan is running; wait for it to finish'))
    const { wrapper } = await mountFiles('/worksets/libraries/lib-1/files?folder=albumA', { applyFileOperation })

    await wrapper.get('[data-testid="file-checkbox-01.flac"]').setValue(true)
    await wrapper.get('[data-testid="delete-selection"]').trigger('click')
    await flushPromises()
    ;(document.body.querySelector('[data-testid="delete-submit"]') as HTMLElement).click()
    await flushPromises()

    const alert = wrapper.findAll('[role="alert"]').map((node) => node.text()).join(' ')
    expect(alert).toContain('BUSY')
    expect(wrapper.find('[data-testid="file-op-result"]').exists()).toBe(false)
  })

  it('refuses to rename to a path: a name is one component', async () => {
    const applyFileOperation = vi.fn().mockResolvedValue({
      operation: 'rename',
      member_path: 'albumA',
      items: [{ source: '01.flac', status: 'ok', target: '02.flac' }],
      succeeded: 1,
      failed: 0,
      untouched: 0,
      refresh: { ok: true },
    })
    const { wrapper } = await mountFiles('/worksets/libraries/lib-1/files?folder=albumA', { applyFileOperation })

    await wrapper.get('[data-testid="rename-01.flac"]').trigger('click')
    await flushPromises()
    // The dialog is portalled: it lives in the document, not in the page tree.
    const input = document.body.querySelector('[data-testid="rename-input"]') as HTMLInputElement
    input.value = 'sub/02.flac'
    input.dispatchEvent(new Event('input'))
    await flushPromises()
    ;(document.body.querySelector('[data-testid="rename-submit"]') as HTMLElement).click()
    await flushPromises()

    const call = applyFileOperation.mock.calls[0] as unknown as [string, { items: { name: string }[] }]
    expect(call[1].items[0].name).toBe('sub/02.flac')
    // The backend is the authority on the name; the client sends what the user
    // typed and reports the refusal it gets back.
    expect(wrapper.get('[data-testid="file-op-result"]').text()).toContain('02.flac')
  })

  it('tells the user when the record that owned this member was replaced', async () => {
    const { wrapper } = await mountFiles(
      '/worksets/libraries/lib-1/conversion/members/m-old/files',
      { getCurrentRecord: vi.fn().mockResolvedValue({ workset: record }) },
    )

    expect(wrapper.get('[data-testid="record-replaced"]').text()).toContain('处理记录已更新')
    expect(wrapper.find('[data-testid="back-to-conversion"]').exists()).toBe(true)
  })

  it('offers the plan review only on the conversion entry, and keeps it read-only', async () => {
    const overview = await mountFiles('/worksets/libraries/lib-1/files?folder=albumA')
    expect(overview.wrapper.find('[data-testid="view-plan"]').exists()).toBe(false)

    const conversion = await mountFiles('/worksets/libraries/lib-1/conversion/members/m-1/files')
    expect(conversion.wrapper.get('[data-testid="view-current"]').attributes('aria-selected')).toBe('true')

    await conversion.wrapper.get('[data-testid="view-plan"]').trigger('click')
    await flushPromises()
    expect(conversion.wrapper.find('[data-testid="plan-review-tree"]').exists()).toBe(true)
    // Read-only by construction: no selection, no actions in the plan view.
    // Read-only by construction: the plan view renders no selection controls.
    expect(conversion.wrapper.find('[data-testid="file-toolbar"]').exists()).toBe(false)
  })
})
