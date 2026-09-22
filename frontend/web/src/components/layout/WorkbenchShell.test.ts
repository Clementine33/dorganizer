import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { createRouter, createWebHistory, type Router } from 'vue-router'
import WorkbenchShell from './WorkbenchShell.vue'

/**
 * The shell is the framework seam for the container tiers: the carrier that shows a
 * detail is chosen by the workbench container's own width, only one carrier
 * mounts, and the narrow drawer is a modal layer over the whole application.
 * jsdom has no layout, so the width is driven through a stub ResizeObserver —
 * the same hook the production code uses, not a test-only shortcut.
 */
class StubResizeObserver {
  static instances: StubResizeObserver[] = []
  readonly observe = vi.fn()
  readonly disconnect = vi.fn()
  private readonly callback: () => void

  constructor(callback: () => void) {
    this.callback = callback
    StubResizeObserver.instances.push(this)
  }

  trigger() {
    this.callback()
  }
}

const router: Router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: { template: '<div />' } },
    { path: '/other', name: 'other', component: { template: '<div />' } },
    { path: '/worksets', name: 'worksets', component: { template: '<div />' } },
  ],
})

const shell = defineComponent({
  components: { WorkbenchShell },
  setup: () => () =>
    h(WorkbenchShell, { contextTitle: '工作集名', contextPage: '转换', contextBackTo: '/worksets', contextBackLabel: '工作集列表' }, {
      nav: () =>
        h('div', { 'data-testid': 'nav-content' }, [
          h('a', { href: '/' }, 'current page'),
          h('button', { type: 'button', 'data-testid': 'fold-action' }, 'fold'),
        ]),
      main: () => h('div', { 'data-testid': 'main-content' }, 'list'),
      detail: () => h('div', { 'data-testid': 'inline-detail' }, 'detail'),
      'detail-modal': () => h('div', { 'data-testid': 'modal-detail' }, 'detail'),
    }),
})

let container: HTMLElement | null = null

function mountShell(width: number, attached = false): { wrapper: VueWrapper; elementWidth: ReturnType<typeof vi.spyOn> } {
  const elementWidth = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(width)
  const options = { global: { plugins: [router] } }
  const wrapper = attached
    ? mount(shell, { ...options, attachTo: (container = document.body.appendChild(document.createElement('div'))) })
    : mount(shell, options)
  return { wrapper, elementWidth }
}

afterEach(() => {
  vi.restoreAllMocks()
  StubResizeObserver.instances = []
  container?.remove()
  container = null
})

async function mountAt(width: number, attached = false) {
  vi.stubGlobal('ResizeObserver', StubResizeObserver)
  const mounted = mountShell(width, attached)
  await nextTick()
  // The observer fires once after mount, mirroring a real layout settle.
  if (StubResizeObserver.instances.length > 0) {
    StubResizeObserver.instances[0].trigger()
    await nextTick()
  }
  return mounted
}

async function openDrawer(wrapper: VueWrapper) {
  await wrapper.get('[data-testid="workbench-nav-toggle"]').trigger('click')
  await nextTick()
}

describe('workbench container tiers', () => {
  it('shows the inline detail and the full sidebar on a wide container', async () => {
    const { wrapper, elementWidth } = await mountAt(1280)
    expect(wrapper.find('[data-testid="inline-detail"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="modal-detail"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="workbench-nav"]').classes()).toContain('w-[200px]')
    elementWidth.mockRestore()
  })

  it('switches to the modal sheet and hides the inline sidebar on a mid container', async () => {
    const { wrapper, elementWidth } = await mountAt(800)
    expect(wrapper.find('[data-testid="inline-detail"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="modal-detail"]').exists()).toBe(true)
    expect((wrapper.find('[data-testid="workbench-nav"]').element as HTMLElement).style.display).toBe('none')
    elementWidth.mockRestore()
  })

  it('collapses the whole sidebar on demand and brings the labelled list back', async () => {
    const { wrapper, elementWidth } = await mountAt(1280)
    // v-show is the mechanism, so the assertion reads the inline display: after
    // a dynamic change jsdom's getComputedStyle keeps the pre-toggle value and
    // would report the hidden sidebar as visible.
    const navDisplay = () =>
      (wrapper.find('[data-testid="workbench-nav"]').element as HTMLElement).style.display
    const toggle = () => wrapper.get('[data-testid="workbench-nav-toggle"]')
    expect(navDisplay()).not.toBe('none')

    await toggle().trigger('click')

    // No icon-only rail survives a collapse: the whole sidebar is
    // hidden and the toggle is the way back.
    expect(navDisplay()).toBe('none')
    expect(toggle().attributes('aria-expanded')).toBe('false')
    expect(toggle().attributes('aria-label')).toBe('展开导航')

    await toggle().trigger('click')

    expect(navDisplay()).not.toBe('none')
    expect(wrapper.get('[data-testid="workbench-nav"] [data-testid="nav-content"]').isVisible()).toBe(true)
    elementWidth.mockRestore()
  })

  it('keeps the desktop breadcrumb on the wide and mid tiers', async () => {
    const wide = await mountAt(1280)
    expect(wide.wrapper.find('[data-testid="workbench-context"]').exists()).toBe(false)
    wide.elementWidth.mockRestore()

    const mid = await mountAt(800)
    expect(mid.wrapper.find('[data-testid="workbench-context"]').exists()).toBe(false)
    mid.elementWidth.mockRestore()
  })

  it('never mounts the inline detail and the sheet together', async () => {
    const { wrapper, elementWidth } = await mountAt(1280)
    const inline = wrapper.findAll('[data-testid="inline-detail"]').length
    const modal = wrapper.findAll('[data-testid="modal-detail"]').length
    expect(inline + modal).toBe(1)
    elementWidth.mockRestore()
  })
})

describe('narrow workbench drawer', () => {
  it('replaces the sidebar with a closed drawer, one level at a time', async () => {
    const { wrapper, elementWidth } = await mountAt(420)

    expect(wrapper.find('[data-testid="workbench-nav"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="workbench-nav-drawer"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="modal-detail"]').exists()).toBe(false)

    await openDrawer(wrapper)

    expect(wrapper.find('[data-testid="workbench-nav-drawer"] [data-testid="nav-content"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="workbench-nav-scrim"]').exists()).toBe(true)
    elementWidth.mockRestore()
  })

  it('shows the compact context with the fixed parent instead of the full path', async () => {
    const { wrapper, elementWidth } = await mountAt(420)

    const context = wrapper.get('[data-testid="workbench-context"]')
    expect(context.text()).toContain('工作集名')
    expect(context.text()).toContain('转换')
    expect(wrapper.get('[data-testid="workbench-back"]').attributes('href')).toBe('/worksets')
    elementWidth.mockRestore()
  })

  it('closes the drawer when a page selection succeeds', async () => {
    await router.push('/')
    const { wrapper, elementWidth } = await mountAt(420)
    await openDrawer(wrapper)
    expect(wrapper.find('[data-testid="workbench-nav-drawer"]').exists()).toBe(true)

    await router.push('/other')
    await nextTick()
    await nextTick()

    expect(wrapper.find('[data-testid="workbench-nav-drawer"]').exists()).toBe(false)
    elementWidth.mockRestore()
  })

  it('keeps the drawer, the route and the form when the navigation is refused', async () => {
    await router.push('/')
    const { wrapper, elementWidth } = await mountAt(420)
    await openDrawer(wrapper)
    const unblock = router.beforeEach(() => false)
    try {
      await router.push('/other')
      await nextTick()

      expect(router.currentRoute.value.path).toBe('/')
      expect(wrapper.find('[data-testid="workbench-nav-drawer"] [data-testid="nav-content"]').exists()).toBe(true)
    } finally {
      unblock()
      elementWidth.mockRestore()
    }
  })

  it('closes the drawer when the page in view is selected again, but not on a fold', async () => {
    await router.push('/')
    const { wrapper, elementWidth } = await mountAt(420)
    await openDrawer(wrapper)

    // A fold control is not a page selection.
    await wrapper.get('[data-testid="fold-action"]').trigger('click')
    await nextTick()
    expect(wrapper.find('[data-testid="workbench-nav-drawer"]').exists()).toBe(true)

    // Selecting the page already in view is still a selection: the route does
    // not change, so nothing else would ever close the drawer.
    await wrapper.get('a[href="/"]').trigger('click')
    await nextTick()
    expect(wrapper.find('[data-testid="workbench-nav-drawer"]').exists()).toBe(false)
    elementWidth.mockRestore()
  })

  it('returns focus to the toggle and closes on Esc', async () => {
    const { wrapper, elementWidth } = await mountAt(420, true)
    const toggle = wrapper.get('[data-testid="workbench-nav-toggle"]')
    await openDrawer(wrapper)

    await wrapper.get('[data-testid="workbench-nav-drawer"]').trigger('keydown', { key: 'Escape' })
    await nextTick()
    await nextTick()

    expect(wrapper.find('[data-testid="workbench-nav-drawer"]').exists()).toBe(false)
    expect(document.activeElement).toBe(toggle.element)
    elementWidth.mockRestore()
  })
})
