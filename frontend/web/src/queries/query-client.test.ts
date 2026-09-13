import { describe, expect, it } from 'vitest'
import { DEFAULT_GC_TIME, TREE_GC_TIME, createAppQueryClient } from './query-client'

describe('createAppQueryClient', () => {
  it('applies the explicit-sync defaults', () => {
    const client = createAppQueryClient()
    const defaults = client.getDefaultOptions()

    expect(defaults.queries).toMatchObject({
      staleTime: Infinity,
      retry: false,
      refetchOnMount: false,
      refetchOnWindowFocus: false,
      refetchOnReconnect: false,
    })
    expect(defaults.mutations).toMatchObject({ retry: false })
  })

  it('keeps cache TTL constants bounded', () => {
    expect(DEFAULT_GC_TIME).toBe(30 * 60 * 1000)
    expect(TREE_GC_TIME).toBe(10 * 60 * 1000)
  })
})