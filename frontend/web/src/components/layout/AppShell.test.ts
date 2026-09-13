import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { apiClientKey } from '@/lib/api/client'
import { apiStub } from '@/test/api-stub'
import { installTestQueryPlugin } from '@/test/query-client'
// The shell derives the current entry from the router's own route name (N21),
// so the test drives the real route table. The slot stands in for the page:
// AppShell renders no RouterView, so no page component is mounted here.
import { router } from '@/app/router'
import AppShell from './AppShell.vue'

function mountShell(): VueWrapper {
  return mount(AppShell, {
    global: {
      plugins: [createPinia(), router, installTestQueryPlugin()],
      provide: { [apiClientKey as symbol]: apiStub() },
    },
    slots: { default: '<div data-testid="page">page</div>' },
  })
}

async function at(path: string): Promise<void> {
  await router.push(path)
  await router.isReady()
}

beforeEach(() => {
  localStorage.clear()
})

describe('AppShell global navigation', () => {
  it('renders the same entries, in the same order and with the same targets, in rail and bottom bar', async () => {
    await at('/libraries')
    const wrapper = mountShell()

    for (const nav of ['global-rail', 'global-bottom-bar'] as const) {
      const links = wrapper.get(`[data-testid="${nav}"]`).findAll('a')
      expect(links.map((link) => link.text())).toEqual(['媒体库', '工作集'])
      expect(links.map((link) => link.attributes('href'))).toEqual(['/libraries', '/worksets'])
    }
  })

  it('switches rail and bottom bar in CSS at the shared 641px breakpoint', async () => {
    await at('/libraries')
    const wrapper = mountShell()

    const rail = wrapper.get('[data-testid="global-rail"]')
    expect(rail.classes()).toContain('hidden')
    expect(rail.classes()).toContain('rail:flex')
    expect(wrapper.get('[data-testid="global-bottom-bar"]').classes()).toContain('rail:hidden')
  })

  it('keeps the brand a non-navigating mark with an accessible product name', async () => {
    await at('/libraries')
    const wrapper = mountShell()

    const brand = wrapper.get('[data-testid="app-brand"]')
    expect(brand.text()).toContain('Onsei Organizer')
    expect(brand.find('a').exists()).toBe(false)
  })

  it('does not render the media-library list as global navigation', async () => {
    await at('/libraries')
    const wrapper = mountShell()

    expect(wrapper.text()).not.toContain('媒体库条目')
    const railNav = wrapper.get('[data-testid="global-rail"] nav')
    expect(railNav.findAll('a')).toHaveLength(2)
    expect(railNav.find('button').exists()).toBe(false)
  })

  it('marks only the owning entry current on a plain route', async () => {
    await at('/libraries')
    const links = mountShell().get('[data-testid="global-rail"]').findAll('a')

    expect(links[0].attributes('aria-current')).toBe('page')
    expect(links[1].attributes('aria-current')).toBeUndefined()
  })

  it('keeps 媒体库 current on the folder detail route', async () => {
    await at('/libraries/lib-a/folders/folder-1')
    const links = mountShell().get('[data-testid="global-rail"]').findAll('a')

    expect(links[0].attributes('aria-current')).toBe('page')
    expect(links[1].attributes('aria-current')).toBeUndefined()
  })

  it('keeps 工作集 current on a workbench child route', async () => {
    await at('/worksets/ws-1/conversion/settings')
    const links = mountShell().get('[data-testid="global-bottom-bar"]').findAll('a')

    expect(links[1].attributes('aria-current')).toBe('page')
    expect(links[0].attributes('aria-current')).toBeUndefined()
  })

  it('carries the theme entry at the bottom of the rail', async () => {
    await at('/libraries')
    const wrapper = mountShell()

    const rail = wrapper.get('[data-testid="global-rail"]')
    expect(rail.find('[aria-label="切换主题"]').exists()).toBe(true)
  })
})
