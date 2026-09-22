import { describe, expect, it } from 'vitest'
import { router } from '@/app/router'
import { workbenchNav, workbenchNavPosition } from './workbench-nav'

describe('workbench navigation definition', () => {
  it('lists the two sections and nests the settings page under 转换', () => {
    const nav = workbenchNav('lib-1')

    expect(nav.map((item) => item.label)).toEqual(['概览与成员', '转换'])
    expect(nav[0].to).toEqual({ name: 'workbench-overview', params: { libraryId: 'lib-1' } })
    expect(nav[1].to).toEqual({ name: 'conversion', params: { libraryId: 'lib-1' } })
    expect(nav[1].children?.map((child) => child.label)).toEqual(['转换全局设置'])
    expect(nav[1].children?.[0].to).toEqual({
      name: 'conversion-settings',
      params: { libraryId: 'lib-1' },
    })
    // Only the group carries children, so only it gets a fold control.
    expect(nav[0].children).toBeUndefined()
  })

  it('assigns every workbench route a local position', () => {
    const workbenchRoutes = router
      .getRoutes()
      .filter((record) => typeof record.name === 'string' && record.path.startsWith('/worksets/'))
    expect(workbenchRoutes.length).toBeGreaterThan(0)
    for (const record of workbenchRoutes) {
      expect(workbenchNavPosition(record.name), String(record.name)).not.toBeNull()
    }
  })

  it('keeps carrier routes on the conversion list and settings under its parent', () => {
    expect(workbenchNavPosition('workbench-overview')).toEqual({ current: 'overview' })
    // A member's file page belongs to its section, never to a section of its own.
    expect(workbenchNavPosition('overview-files')).toEqual({ current: 'overview' })
    expect(workbenchNavPosition('conversion-member-files')).toEqual({ current: 'conversion' })
    expect(workbenchNavPosition('conversion')).toEqual({ current: 'conversion' })
    expect(workbenchNavPosition('conversion-settings')).toEqual({
      current: 'conversion-settings',
      parent: 'conversion',
    })
    // Details and the batch editor are carriers of the list, not new pages.
    expect(workbenchNavPosition('conversion-member')).toEqual({ current: 'conversion' })
    expect(workbenchNavPosition('conversion-member-edit')).toEqual({ current: 'conversion' })
    expect(workbenchNavPosition('conversion-batch-edit')).toEqual({ current: 'conversion' })
  })

  it('has no position for routes outside the workbench', () => {
    expect(workbenchNavPosition('worksets')).toBeNull()
    expect(workbenchNavPosition(undefined)).toBeNull()
    expect(workbenchNavPosition(Symbol('anonymous'))).toBeNull()
  })
})
