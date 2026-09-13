import { describe, expect, it } from 'vitest'
import { router } from '@/app/router'
import { GLOBAL_NAV, globalNavOwner } from './global-nav'

describe('global navigation definition', () => {
  it('lists exactly the two existing modules, in rail and bottom-bar order', () => {
    expect(GLOBAL_NAV.map((item) => ({ id: item.id, label: item.label, to: item.to }))).toEqual([
      { id: 'libraries', label: '媒体库', to: '/libraries' },
      { id: 'worksets', label: '工作集', to: '/worksets' },
    ])
  })
})

describe('global navigation ownership', () => {
  // The mapping is explicit on purpose (N18): asserting it against the real
  // route table means a renamed or added route fails here instead of silently
  // losing its highlight.
  it('assigns every application route to a global entry', () => {
    const named = router.getRoutes().filter((record) => typeof record.name === 'string')
    expect(named.length).toBeGreaterThan(0)
    for (const record of named) {
      expect(globalNavOwner(record.name), String(record.name)).not.toBeNull()
    }
  })

  it('keeps workbench child routes under 工作集 and the folder detail under 媒体库', () => {
    expect(globalNavOwner('workset-overview')).toBe('worksets')
    expect(globalNavOwner('conversion')).toBe('worksets')
    expect(globalNavOwner('conversion-settings')).toBe('worksets')
    expect(globalNavOwner('conversion-member')).toBe('worksets')
    expect(globalNavOwner('conversion-member-edit')).toBe('worksets')
    expect(globalNavOwner('conversion-batch-edit')).toBe('worksets')
    expect(globalNavOwner('folder-detail')).toBe('libraries')
  })

  it('has no owner for unknown or missing route names', () => {
    expect(globalNavOwner(undefined)).toBeNull()
    expect(globalNavOwner(Symbol('anonymous'))).toBeNull()
    expect(globalNavOwner('not-a-route')).toBeNull()
  })
})
