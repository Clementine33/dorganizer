import { flushPromises, mount } from '@vue/test-utils'
import { useQuery, VueQueryPlugin } from '@tanstack/vue-query'
import { defineComponent } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { createTestQueryClient } from '@/test/query-client'
import { refreshOrRemoveQueries } from './cache-sync'

describe('refreshOrRemoveQueries', () => {
  it('refetches active members and removes inactive members under the prefix', async () => {
    const queryClient = createTestQueryClient()

    const activeCall = vi.fn().mockResolvedValueOnce('first').mockResolvedValueOnce('second')
    const activeKey = ['family', 'active'] as const
    const inactiveCall = vi.fn().mockResolvedValue('inactive-data')
    const inactiveKey = ['family', 'inactive'] as const

    // Seed an inactive (never observed) query so it is a cache member.
    await queryClient.fetchQuery({ queryKey: inactiveKey, queryFn: inactiveCall })

    // Mount one active observer under the family prefix.
    const Active = defineComponent({
      setup() {
        const query = useQuery({ queryKey: activeKey, queryFn: activeCall })
        return { query }
      },
      template: `<div>{{ query.data ?? 'none' }}</div>`,
    })
    const wrapper = mount(Active, {
      global: { plugins: [[VueQueryPlugin, { queryClient }]] },
    })
    await flushPromises()
    expect(wrapper.text()).toBe('first')
    expect(inactiveCall).toHaveBeenCalledTimes(1)

    await refreshOrRemoveQueries(queryClient, ['family'] as const)
    await flushPromises()

    // Active member refetched and now holds the fresh data.
    expect(activeCall).toHaveBeenCalledTimes(2)
    expect(queryClient.getQueryData(activeKey)).toBe('second')
    // Inactive members were removed — the next mount cold-fetches.
    expect(queryClient.getQueryData(inactiveKey)).toBeUndefined()
  })

  it('leaves keys outside the prefix untouched', async () => {
    const queryClient = createTestQueryClient()
    const outsideKey = ['plans', 'list', 'lib-a', 100] as const
    const outsideCall = vi.fn().mockResolvedValue([])
    await queryClient.fetchQuery({ queryKey: outsideKey, queryFn: outsideCall })

    await refreshOrRemoveQueries(queryClient, ['family'] as const)

    expect(queryClient.getQueryData(outsideKey)).toEqual([])
    expect(outsideCall).toHaveBeenCalledTimes(1)
  })
})