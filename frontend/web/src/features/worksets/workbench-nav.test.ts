import { describe, expect, it } from 'vitest'
import { router } from '@/app/router'
import { workbenchNav, workbenchNavPosition } from './workbench-nav'

describe('workbench navigation definition', () => {
  it('lists the two sections and nests the settings page under 转换', () => {
    const nav = workbenchNav('ws-1')

    expect(nav.map((item) => item.label)).toEqual(['概览与成员', '转换'])
    expect(nav[0].to).toEqual({ name: 'workset-overview', params: { worksetId: 'ws-1' } })
    expect(nav[1].to).toEqual({ name: 'conversion', params: { worksetId: 'ws-1' } })
    expect(nav[1].children?.map((child) => child.label)).toEqual(['转换全局设置'])
    expect(nav[1].children?.[0].to).toEqual({
      name: 'conversion-settings',
      params: { worksetId: 'ws-1' },
    })
    // Only the group carries children, so only it gets a fold control (N24).
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
    expect(workbenchNavPosition('workset-overview')).toEqual({ current: 'overview' })
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
    expect(workbenchNavPosition('libraries')).toBeNull()
    expect(workbenchNavPosition('folder-detail')).toBeNull()
    expect(workbenchNavPosition(undefined)).toBeNull()
    expect(workbenchNavPosition(Symbol('anonymous'))).toBeNull()
  })
})
