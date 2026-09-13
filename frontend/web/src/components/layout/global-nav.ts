import { LibraryBig, ListMusic } from '@lucide/vue'
import type { Component } from 'vue'
import type { RouteLocationRaw } from 'vue-router'

/**
 * The global navigation definition (N05, N18-N20).
 *
 * One list, two renderers: the desktop rail and the mobile bottom bar both
 * iterate this array, so their labels, order and targets cannot drift apart.
 * Entries carry only id/label/icon/to — no permissions, no business state, no
 * plugin registry. The media-library *list* is page content on /libraries,
 * never a global entry (N03).
 */
export type GlobalNavId = 'libraries' | 'worksets'

export interface GlobalNavItem {
  id: GlobalNavId
  label: string
  icon: Component
  to: RouteLocationRaw
}

export const GLOBAL_NAV: readonly GlobalNavItem[] = [
  { id: 'libraries', label: '媒体库', icon: LibraryBig, to: '/libraries' },
  { id: 'worksets', label: '工作集', icon: ListMusic, to: '/worksets' },
]

/**
 * Route name → owning global entry (N21). The current item is derived from the
 * router's own route name, never from a stored `activeNavId` that could drift
 * from the URL. An explicit mapping — not a URL-prefix guess — keeps every
 * workbench child route under 工作集 without pretending to be a second route
 * table: the test asserts it covers exactly the application router's names.
 */
const NAV_OWNER_BY_ROUTE_NAME = {
  libraries: 'libraries',
  'folder-detail': 'libraries',
  worksets: 'worksets',
  'workset-overview': 'worksets',
  conversion: 'worksets',
  'conversion-settings': 'worksets',
  'conversion-member': 'worksets',
  'conversion-member-edit': 'worksets',
  'conversion-batch-edit': 'worksets',
} as const satisfies Record<string, GlobalNavId>

export type AppRouteName = keyof typeof NAV_OWNER_BY_ROUTE_NAME

export function globalNavOwner(routeName: unknown): GlobalNavId | null {
  return typeof routeName === 'string' && routeName in NAV_OWNER_BY_ROUTE_NAME
    ? NAV_OWNER_BY_ROUTE_NAME[routeName as AppRouteName]
    : null
}
