import { describe, expect, it, vi } from 'vitest'
import type { Workset } from '@/lib/api/types'
import { apiStub } from '@/test/api-stub'
import { createTestQueryClient } from '@/test/query-client'
import { queryKeys } from './query-keys'
import {
  createWorksetMutationOptions,
  saveDraftMutationOptions,
  startGenerationMutationOptions,
  syncAfterDraftConflict,
  syncAfterGenerationTerminal,
} from './worksets'

const workset: Workset = {
  workset_id: 'ws-1',
  title: '夏季整理',
  version: 2,
  library: { library_id: 'lib-a', name: 'Lib', root_path: 'D:\\Music' },
  planning_state: 'planned',
  current_revision: {
    plan_id: 'plan-1', revision_index: 0, created_at: '2026-08-30T00:00:00Z', status: 'ready',
    summary_reason: 'ACTIONABLE', blocked_count: 0, validation_state: 'valid', stale: false,
  },
  active_generation: null,
  latest_generation: null,
  members: [],
  updated_at: '2026-08-30T00:00:00Z',
  created_at: '2026-08-30T00:00:00Z',
}

function makeWorkset(overrides: Partial<Workset> = {}): Workset {
  return { ...workset, ...overrides }
}

describe('workset mutation cache synchronization', () => {
  it('create seeds the detail cache and refreshes the feed prefix', async () => {
    const client = createTestQueryClient()
    client.setQueryData([...queryKeys.worksets.feed('all', null), 'infinite'], { pages: [], pageParams: [] })
    const api = apiStub({ createWorkset: vi.fn().mockResolvedValue({ workset, created: true }) })
    const options = createWorksetMutationOptions(api, client)

    await options.mutationFn({ library_id: 'lib-a', title: 't', folder_ids: ['f-1'], idempotencyKey: 'k1' })
    options.onSuccess?.({ workset, created: true })
    await vi.waitFor(() => {})

    expect(client.getQueryData(queryKeys.worksets.detail('ws-1'))).toEqual(workset)
    expect(client.getQueryState([...queryKeys.worksets.feed('all', null), 'infinite'])?.isInvalidated).toBe(true)
  })

  it('save seeds detail from the response view and refreshes the draft key', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.worksets.detail('ws-1'), workset)
    client.setQueryData(queryKeys.worksets.draft('ws-1'), { workset_id: 'ws-1', version: 2, workflow_schema_version: 1, workflow: { schema_version: 1, steps: [] }, updated_at: '' })
    const api = apiStub({ saveWorksetDraft: vi.fn().mockResolvedValue(makeWorkset({ version: 3 })) })
    const options = saveDraftMutationOptions(api, client)
    const input = { worksetId: 'ws-1', workflow: { schema_version: 1 as const, steps: [] }, ifMatchVersion: 2 }

    await options.mutationFn(input)
    options.onSuccess?.(makeWorkset({ version: 3 }), input)
    await vi.waitFor(() => {})

    expect(client.getQueryData<Workset>(queryKeys.worksets.detail('ws-1'))?.version).toBe(3)
    expect(client.getQueryState(queryKeys.worksets.draft('ws-1'))?.isInvalidated).toBe(true)
  })

  it('start 202 refreshes detail; created:false refreshes revisions too', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.worksets.detail('ws-1'), workset)
    client.setQueryData(queryKeys.worksets.revisionList('ws-1'), [])
    const replay = { created: false, revision: workset.current_revision! }
    const api = apiStub({ startGeneration: vi.fn().mockResolvedValue(replay) })
    const options = startGenerationMutationOptions(api, client)
    const input = { worksetId: 'ws-1', idempotencyKey: 'g1' }

    await options.mutationFn(input)
    options.onSuccess?.(replay, input)

    // With no mounted observers, refreshOrRemoveQueries drops the cached
    // entries (removal is the correct "refresh" for inactive caches).
    await vi.waitFor(() => {
      expect(client.getQueryData(queryKeys.worksets.detail('ws-1'))).toBeUndefined()
      expect(client.getQueryData(queryKeys.worksets.revisionList('ws-1'))).toBeUndefined()
    })
  })

  it('generation terminal refreshes feed, detail, draft, and revision caches', async () => {
    const client = createTestQueryClient()
    client.setQueryData([...queryKeys.worksets.feed('all', null), 'infinite'], { pages: [], pageParams: [] })
    client.setQueryData(queryKeys.worksets.detail('ws-1'), workset)
    client.setQueryData(queryKeys.worksets.draft('ws-1'), { workset_id: 'ws-1', version: 2, workflow_schema_version: 1, workflow: { schema_version: 1, steps: [] }, updated_at: '' })
    client.setQueryData(queryKeys.worksets.revisionList('ws-1'), [])

    await syncAfterGenerationTerminal(client, 'ws-1')

    for (const key of [
      [...queryKeys.worksets.feed('all', null), 'infinite'],
      queryKeys.worksets.detail('ws-1'),
      queryKeys.worksets.draft('ws-1'),
      queryKeys.worksets.revisionList('ws-1'),
    ]) {
      expect(client.getQueryData(key)).toBeUndefined()
    }
  })

  it('draft conflict sync refreshes detail and draft only', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.worksets.detail('ws-1'), workset)
    client.setQueryData(queryKeys.worksets.draft('ws-1'), { workset_id: 'ws-1', version: 2, workflow_schema_version: 1, workflow: { schema_version: 1, steps: [] }, updated_at: '' })
    client.setQueryData([...queryKeys.worksets.feed('all', null), 'infinite'], { pages: [], pageParams: [] })

    await syncAfterDraftConflict(client, 'ws-1')

    expect(client.getQueryData(queryKeys.worksets.detail('ws-1'))).toBeUndefined()
    expect(client.getQueryData(queryKeys.worksets.draft('ws-1'))).toBeUndefined()
    // The feed is intentionally untouched by a draft conflict sync.
    expect(client.getQueryData([...queryKeys.worksets.feed('all', null), 'infinite'])).toEqual({ pages: [], pageParams: [] })
  })
})
