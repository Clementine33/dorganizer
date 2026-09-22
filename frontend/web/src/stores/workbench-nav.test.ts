import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { useWorkbenchNavStore } from './workbench-nav'

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('workbench navigation store', () => {
  it('starts with every group unfolded', () => {
    const nav = useWorkbenchNavStore()
    expect(nav.isGroupCollapsed('conversion')).toBe(false)
  })

  it('keeps the fold preference per stable group id', () => {
    const nav = useWorkbenchNavStore()
    nav.setGroupCollapsed('conversion', true)

    expect(nav.isGroupCollapsed('conversion')).toBe(true)
    expect(nav.isGroupCollapsed('overview')).toBe(false)

    nav.setGroupCollapsed('conversion', false)
    expect(nav.isGroupCollapsed('conversion')).toBe(false)
  })
})
