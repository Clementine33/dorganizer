import { describe, expect, it } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { cleanedQuery, installAddressHygiene, readMemberFilter } from './route-params'

/**
 * Address hygiene (ADR 0003 §5): a page keeps only the parameters it
 * declares, with values from a closed vocabulary, and a cleaned address is
 * rewritten in place — no extra history entry, no redirect loop.
 *
 * Folder names and search terms are what an address must never carry, so the
 * retired `folder` parameter and a `q` are the cases that matter here.
 */

const DIR_ID = 'a1b2c3d4e5f60718293a4b5c6d7e8f90'

function guardRouter(): Router {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/worksets', name: 'worksets', component: { template: '<div />' } },
      { path: '/worksets/:libraryId', name: 'workbench-overview', component: { template: '<div />' } },
      {
        path: '/worksets/:libraryId/f/:dirId',
        name: 'overview-files',
        component: { template: '<div />' },
      },
      { path: '/worksets/:libraryId/conversion', name: 'conversion', component: { template: '<div />' } },
      {
        path: '/worksets/:libraryId/conversion/settings',
        name: 'conversion-settings',
        component: { template: '<div />' },
      },
      {
        path: '/worksets/:libraryId/conversion/:memberId/files',
        name: 'conversion-member-files',
        component: { template: '<div />' },
      },
      { path: '/:pathMatch(.*)*', name: 'not-found', component: { template: '<div />' } },
    ],
  })
  installAddressHygiene(router)
  return router
}

function routeOf(router: Router, fullPath: string) {
  return router.resolve(fullPath)
}

describe('cleaned query', () => {
  it('keeps a page parameter that carries an allowed value', () => {
    expect(cleanedQuery(routeOf(guardRouter(), `/worksets/lib-1/conversion?filter=warn`))).toBeNull()
    expect(cleanedQuery(routeOf(guardRouter(), `/worksets/lib-1/f/${DIR_ID}?nope=1`))).toEqual({})
  })

  it('drops the retired folder parameter and any other extra, keeping what is declared', () => {
    const route = routeOf(guardRouter(), `/worksets/lib-1/conversion?filter=change&folder=albumA&q=hot`)
    expect(cleanedQuery(route)).toEqual({ filter: 'change' })
  })

  it('drops a declared parameter whose value is outside its vocabulary', () => {
    const route = routeOf(guardRouter(), '/worksets/lib-1/conversion?filter=anything&view=plan')
    expect(cleanedQuery(route)).toEqual({})
  })

  it('keeps no parameter on a page that declares none', () => {
    const route = routeOf(guardRouter(), `/worksets/lib-1/f/${DIR_ID}?view=plan&q=x`)
    expect(cleanedQuery(route)).toEqual({})
  })

  it('removes a fragment even when the query is already clean', () => {
    const route = routeOf(guardRouter(), '/worksets/lib-1/conversion#frag')
    expect(cleanedQuery(route)).toEqual({})
  })

  it('reports an already clean address as clean', () => {
    expect(cleanedQuery(routeOf(guardRouter(), '/worksets/lib-1/conversion?filter=all'))).toBeNull()
  })

  it('reads the filter of an address, falling back to the unfiltered list', () => {
    expect(readMemberFilter('blocked')).toBe('blocked')
    expect(readMemberFilter('anything')).toBe('all')
    expect(readMemberFilter(undefined)).toBe('all')
  })
})

describe('address hygiene guard', () => {
  it('rewrites an unclean address in place, adding no history entry of its own', async () => {
    const router = guardRouter()
    await router.push(`/worksets/lib-1/f/${DIR_ID}`)
    const cleanDelta = window.history.length
    await router.push(`/worksets/lib-1/f/${DIR_ID}`)
    const cleanSteps = window.history.length - cleanDelta

    const before = window.history.length
    await router.push(`/worksets/lib-1/f/${DIR_ID}?folder=albumA&q=hot#top`)
    expect(router.currentRoute.value.fullPath).toBe(`/worksets/lib-1/f/${DIR_ID}`)
    expect(window.history.length - before).toBe(cleanSteps)
  })

  it('keeps the page parameters a page declares, and settles instead of looping', async () => {
    const router = guardRouter()
    await router.push('/worksets/lib-1/conversion?filter=warn&q=hot')
    expect(router.currentRoute.value.fullPath).toBe('/worksets/lib-1/conversion?filter=warn')

    // A second pass over the same address changes nothing: an idempotent guard
    // is what keeps this from becoming a redirect loop.
    await router.push('/worksets/lib-1/conversion?filter=warn')
    expect(router.currentRoute.value.fullPath).toBe('/worksets/lib-1/conversion?filter=warn')
    expect(router.currentRoute.value.name).toBe('conversion')
  })

  it('leaves an unknown address alone, so the not-found page keeps its path', async () => {
    const router = guardRouter()
    await router.push('/worksets/libraries/lib-1?folder=albumA')
    expect(router.currentRoute.value.name).toBe('not-found')
    expect(router.currentRoute.value.fullPath).toBe('/worksets/libraries/lib-1')
  })
})
