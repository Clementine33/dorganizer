import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { Folder } from '@/lib/api/types'
import FolderFlatList from './FolderFlatList.vue'

/**
 * jsdom has no layout, so the virtualizer's scroller reports offsetHeight 0
 * and renders an empty window (the library's real behavior for a 0-height
 * container). Tests that exercise rows stub the scroller's metrics on the
 * instance right after mount — before the pre-flush watcher measures it.
 */
function scaffold(folders: Folder[], props: Record<string, unknown> = {}) {
  const wrapper = mount(FolderFlatList, {
    props: { folders, selectedIds: [], allSelected: false, ...props },
  })
  const scroller = wrapper.find('[data-testid="folder-list-scroller"]').element as HTMLElement
  let top = 0
  Object.defineProperties(scroller, {
    offsetHeight: { configurable: true, get: () => 560 },
    offsetWidth: { configurable: true, get: () => 800 },
    scrollTop: {
      configurable: true,
      get: () => top,
      set: (v: number) => {
        top = v
      },
    },
  })
  return {
    wrapper,
    setScrollTop: (v: number): void => {
      top = v
      scroller.dispatchEvent(new Event('scroll'))
    },
  }
}

async function settle(): Promise<void> {
  await flushPromises()
  await nextTick()
}

function makeFolders(count: number): Folder[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `f-${i}`,
    name: `Folder ${i}`,
    path: `/music/f-${i}`,
    relative_path: `f-${i}`,
    audio_file_count: 3,
  }))
}

function rowOf(wrapper: VueWrapper, testId: string): HTMLElement | null {
  const cell = wrapper.find(`[data-testid="${testId}"]`).element
  return cell?.closest('li') ?? null
}

describe('FolderFlatList', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders only the windowed slice with the correct spacer', async () => {
    const { wrapper } = scaffold(makeFolders(100))
    await settle()

    // 20 windowed rows (ceil(560/56)=10 visible + 10 overscan).
    expect(wrapper.findAll('li')).toHaveLength(20)
    expect(wrapper.find('[data-testid="folder-checkbox-f-0"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="folder-checkbox-f-99"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="folder-list-spacer"]').attributes('style')).toContain('height: 5600px')
  })

  it('moves the window and the translateY offset on scroll', async () => {
    const { wrapper, setScrollTop } = scaffold(makeFolders(100))
    await settle()

    setScrollTop(2800) // row 50
    await settle()

    // scrollTop 2800 -> first rendered index 40 (rounded index 50 - overscan).
    expect(wrapper.find('[data-testid="folder-checkbox-f-40"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="folder-link-f-69"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="folder-checkbox-f-0"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="folder-checkbox-f-70"]').exists()).toBe(false)
    const row = rowOf(wrapper, 'folder-link-f-40')
    expect(row?.getAttribute('style')).toContain('translateY(2240px)')
    expect(wrapper.find('[data-testid="folder-list-spacer"]').attributes('style')).toContain('height: 5600px')
  })

  it('carries the same facts in both layouts, without a minimum width', async () => {
    const { wrapper } = scaffold(makeFolders(2))
    await settle()

    // The compact (phone) layout keeps the count under the name, so the merged
    // row still shows checkbox, name, audio count and view (L03); the desktop
    // layout shows the count in its own column. Both come from the one template
    // and the root never forces a horizontal minimum.
    const row = rowOf(wrapper, 'folder-link-f-0')
    expect(row?.textContent).toContain('Folder 0')
    expect(row?.textContent).toContain('3 个音频文件')
    expect(wrapper.get('[data-testid="folder-link-f-0"]').text()).toContain('3 个音频文件')
    expect(wrapper.get('[data-testid="folder-list"]').classes()).not.toContain('min-w-[680px]')
    expect(wrapper.get('[data-testid="folder-list"]').classes()).toContain('min-w-0')
  })

  it('keeps the view action on screen instead of revealing it on hover', async () => {
    const { wrapper } = scaffold(makeFolders(1))
    await settle()

    const view = wrapper.get('[aria-label="打开 Folder 0"]')
    expect(view.classes()).not.toContain('opacity-60')
    expect(view.classes()).not.toContain('group-hover:opacity-100')
  })

  it('shows the folder count next to select-all in the compact layout', async () => {
    const { wrapper } = scaffold(makeFolders(4))
    await settle()

    expect(wrapper.get('[data-testid="folder-count"]').text()).toBe('共 4 个文件夹')
  })

  it('preserves selection across windowing (selection is store-driven)', async () => {
    const { wrapper, setScrollTop } = scaffold(makeFolders(100), { selectedIds: ['f-5', 'f-40'] })
    await settle()

    expect((wrapper.find('[data-testid="folder-checkbox-f-5"]').element as HTMLInputElement).checked).toBe(true)

    setScrollTop(2800)
    await settle()
    expect(wrapper.find('[data-testid="folder-checkbox-f-5"]').exists()).toBe(false)
    expect((wrapper.find('[data-testid="folder-checkbox-f-40"]').element as HTMLInputElement).checked).toBe(true)

    setScrollTop(0)
    await settle()
    expect((wrapper.find('[data-testid="folder-checkbox-f-5"]').element as HTMLInputElement).checked).toBe(true)
  })

  it('emits selectAll, select and open, and never selects from the row body', async () => {
    const { wrapper } = scaffold(makeFolders(3))
    await settle()

    await wrapper.find('[data-testid="select-all-folders"]').setValue(true)
    expect(wrapper.emitted('selectAll')).toEqual([[true]])

    await wrapper.find('[data-testid="folder-checkbox-f-0"]').setValue(true)
    expect(wrapper.emitted('select')).toEqual([['f-0', true]])

    // The row body navigates; it never toggles the checkbox (L06).
    await wrapper.find('[data-testid="folder-link-f-1"]').trigger('click')
    expect(wrapper.emitted('open')).toEqual([['f-1']])
    expect(wrapper.emitted('select')).toEqual([['f-0', true]])

    await wrapper.find('[aria-label="打开 Folder 2"]').trigger('click')
    expect(wrapper.emitted('open')).toEqual([['f-1'], ['f-2']])
  })

  it('renders no rows for an empty folder set', async () => {
    const { wrapper } = scaffold([])
    await settle()

    expect(wrapper.findAll('li')).toHaveLength(0)
    expect(wrapper.find('[data-testid="folder-list-spacer"]').attributes('style')).toContain('height: 0px')
  })

  it('renders an empty window for a 0-height scroller (documented library behavior)', async () => {
    // No layout stub: jsdom offsetHeight is 0, exactly like a real
    // height-collapsed container. The virtualizer renders nothing in that
    // case — no crash, no ghost rows.
    const wrapper = mount(FolderFlatList, {
      props: { folders: makeFolders(2), selectedIds: [], allSelected: false },
    })
    await settle()

    expect(wrapper.findAll('li')).toHaveLength(0)
    expect(wrapper.find('[data-testid="folder-checkbox-f-0"]').exists()).toBe(false)
  })
})
