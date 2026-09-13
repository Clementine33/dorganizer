import type { RouteLocationRaw } from 'vue-router'

/**
 * The workbench navigation definition (N05, N18, N19).
 *
 * The inline sidebar of the mid/wide tiers and the narrow drawer both render
 * this one list, exactly as the global rail and bottom bar share
 * `global-nav` — two renderers, never two hard-coded entry lists (N20).
 * Entries carry only id/label/icon/to/children, and every location is built
 * from a *named* route plus `worksetId` (N18), so a path rename cannot quietly
 * break navigation.
 */
export type WorkbenchNavId = 'overview' | 'conversion' | 'conversion-settings'

export interface WorkbenchNavItem {
  id: WorkbenchNavId
  label: string
  icon: string
  to: RouteLocationRaw
  children?: WorkbenchNavItem[]
}

/** The workbench's own entries for one workset: the two sections, and the
 *  settings page nested under 转换 (the only nesting this round defines). */
export function workbenchNav(worksetId: string): WorkbenchNavItem[] {
  return [
    {
      id: 'overview',
      label: '概览与成员',
      icon: '◱',
      to: { name: 'workset-overview', params: { worksetId } },
    },
    {
      id: 'conversion',
      label: '转换',
      icon: '◈',
      to: { name: 'conversion', params: { worksetId } },
      children: [
        {
          id: 'conversion-settings',
          label: '转换全局设置',
          icon: '⚙',
          to: { name: 'conversion-settings', params: { worksetId } },
        },
      ],
    },
  ]
}

/** Route name → position in the workbench navigation (N21). */
export type WorkbenchRouteName =
  | 'workset-overview'
  | 'conversion'
  | 'conversion-settings'
  | 'conversion-member'
  | 'conversion-member-edit'
  | 'conversion-batch-edit'

export interface WorkbenchNavPosition {
  /** The entry that IS the current page. */
  current: WorkbenchNavId
  /**
   * The entry whose section contains the current page. It is shown as the
   * owning level, never as a second current page (N27).
   */
  parent?: WorkbenchNavId
}

const POSITION_BY_ROUTE_NAME: Record<WorkbenchRouteName, WorkbenchNavPosition> = {
  'workset-overview': { current: 'overview' },
  conversion: { current: 'conversion' },
  'conversion-settings': { current: 'conversion-settings', parent: 'conversion' },
  // The detail and batch entries are carriers of the conversion list, so the
  // list stays the current entry and 转换全局设置 is never faked as current.
  'conversion-member': { current: 'conversion' },
  'conversion-member-edit': { current: 'conversion' },
  'conversion-batch-edit': { current: 'conversion' },
}

export function workbenchNavPosition(routeName: unknown): WorkbenchNavPosition | null {
  return typeof routeName === 'string' && routeName in POSITION_BY_ROUTE_NAME
    ? POSITION_BY_ROUTE_NAME[routeName as WorkbenchRouteName]
    : null
}
