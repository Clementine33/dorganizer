import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import WorkbenchNav from './WorkbenchNav.vue'

const router: Router = createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/worksets', name: 'worksets', component: { template: '<div />' } },
    {
      path: '/worksets/:libraryId',
      name: 'workbench-overview',
      component: { template: '<div />' },
    },
    {
      path: '/worksets/:libraryId/conversion',
      name: 'conversion',
      component: { template: '<div />' },
    },
    {
      path: '/worksets/:libraryId/conversion/settings',
      name: 'conversion-settings',
      component: { template: '<div />' },
    },
  ],
})

interface NavProps {
  settingsBlockedReason?: string | null
}

// Every mount gets its own Pinia (so the fold state starts fresh) and the same
// router, which is what the component reads the current section from.
async function mountNav(path: string, props: NavProps = {}): Promise<VueWrapper> {
  await router.push(path)
  await router.isReady()
  return mount(WorkbenchNav, {
    props: {
      libraryId: 'lib-1',
      settingsBlockedReason: null,
      ...props,
    },
    global: { plugins: [createPinia(), router] },
  })
}

describe('workbench navigation', () => {
  it('renders the two sections and the settings entry under 转换', async () => {
    const wrapper = await mountNav('/worksets/lib-1')

    expect(wrapper.get('[data-testid="nav-overview"]').text()).toContain('概览与成员')
    expect(wrapper.get('[data-testid="nav-conversion"]').text()).toContain('转换')
    expect(wrapper.get('[data-testid="nav-conversion-settings"]').text()).toContain('转换全局设置')
    expect(wrapper.get('[data-testid="nav-conversion-settings"]').isVisible()).toBe(true)
  })

  it('marks the current page, and only the current page', async () => {
    const overview = await mountNav('/worksets/lib-1')
    expect(overview.get('[data-testid="nav-overview"]').attributes('aria-current')).toBe('page')
    expect(overview.get('[data-testid="nav-conversion"]').attributes('aria-current')).toBeUndefined()

    const settings = await mountNav('/worksets/lib-1/conversion/settings')
    expect(settings.get('[data-testid="nav-conversion-settings"]').attributes('aria-current')).toBe('page')
    // The parent shows its owning state without pretending to be a second
    // current page (N27).
    expect(settings.get('[data-testid="nav-conversion"]').attributes('aria-current')).toBeUndefined()
    expect(settings.get('[data-testid="nav-conversion"]').classes().join(' ')).toContain('bg-sidebar-accent/60')
  })

  it('keeps a carrier route on the conversion list', async () => {
    const wrapper = await mountNav('/worksets/lib-1/conversion')
    expect(wrapper.get('[data-testid="nav-conversion"]').attributes('aria-current')).toBe('page')
    expect(wrapper.get('[data-testid="nav-conversion-settings"]').attributes('aria-current')).toBeUndefined()
  })

  it('folds the group from the arrow without navigating', async () => {
    const wrapper = await mountNav('/worksets/lib-1/conversion')
    const group = wrapper.get('[data-testid="nav-group-conversion"]')

    expect(group.attributes('aria-expanded')).toBe('true')
    expect(group.attributes('aria-controls')).toBe('nav-children-conversion')

    await group.trigger('click')

    expect(router.currentRoute.value.path).toBe('/worksets/lib-1/conversion')
    expect(group.attributes('aria-expanded')).toBe('false')
    expect(wrapper.get('[data-testid="nav-conversion-settings"]').isVisible()).toBe(false)
  })

  it('navigates from the group label, not from the arrow', async () => {
    const wrapper = await mountNav('/worksets/lib-1')
    await wrapper.get('[data-testid="nav-conversion"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/worksets/lib-1/conversion')
  })

  it('unfolds the group when the settings page is entered, once per entry', async () => {
    const wrapper = await mountNav('/worksets/lib-1/conversion')
    await wrapper.get('[data-testid="nav-group-conversion"]').trigger('click')
    expect(wrapper.get('[data-testid="nav-group-conversion"]').attributes('aria-expanded')).toBe('false')

    await router.push('/worksets/lib-1/conversion/settings')
    await nextTick()
    expect(wrapper.get('[data-testid="nav-group-conversion"]').attributes('aria-expanded')).toBe('true')

    // The user may fold it again while staying on the settings page; nothing
    // re-expands it behind their back (N27).
    await wrapper.get('[data-testid="nav-group-conversion"]').trigger('click')
    await wrapper.vm.$nextTick()
    expect(wrapper.get('[data-testid="nav-group-conversion"]').attributes('aria-expanded')).toBe('false')
  })

  it('disables the settings entry with its reason while edits are blocked', async () => {
    const wrapper = await mountNav('/worksets/lib-1/conversion', {
      settingsBlockedReason: '媒体库已删除：该工作集只读',
    })
    const entry = wrapper.get('[data-testid="nav-conversion-settings"]')

    expect(entry.element.tagName).toBe('SPAN')
    expect(entry.attributes('aria-disabled')).toBe('true')
    expect(entry.attributes('title')).toBe('媒体库已删除：该工作集只读')
  })

  it('asks for a 44px target whenever the pointer is coarse, at any width', async () => {
    // The tablet tiers are touch too, so the minimum is a pointer query rather
    // than a width one (N25, F15).
    const wrapper = await mountNav('/worksets/lib-1')

    expect(wrapper.get('[data-testid="nav-overview"]').classes()).toContain('pointer-coarse:min-h-11')
    const group = wrapper.get('[data-testid="nav-group-conversion"]').classes().join(' ')
    expect(group).toContain('pointer-coarse:min-h-11')
    expect(group).toContain('pointer-coarse:min-w-11')
  })
})
