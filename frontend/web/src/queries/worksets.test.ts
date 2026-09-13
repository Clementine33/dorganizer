import { describe, expect, it, vi } from 'vitest'
import type { Operation, Workset } from '@/lib/api/types'
import { apiStub } from '@/test/api-stub'
import { createTestQueryClient } from '@/test/query-client'
import { queryKeys } from './query-keys'
import {
  createWorksetMutationOptions,
  saveOperationDraftMutationOptions,
  startGenerationMutationOptions,
  syncAfterDraftConflict,
  syncAfterGenerationTerminal,
} from './worksets'

const operation: Operation = {
  workset_id: 'ws-1',
  operation_type: 'conversion',
  version: 2,
  planning_state: 'planned',
  current_revision: {
    plan_id: 'plan-1',
    revision_index: 1,
    created_at: '2026-08-30T00:00:00Z',
    status: 'ready',
    summary_reason: 'ACTIONABLE',
    counts: { members: 2, changed: 1, unmet_targets: 0, blocked: 0, unchanged: 1 },
    validation_state: 'valid',
    stale: false,
  },
  active_generation: null,
  latest_generation: null,
}

const workset: Workset = {
  workset_id: 'ws-1',
  title: '夏季整理',
  version: 2,
  library: { library_id: 'lib-a', name: 'Lib', root_path: 'D:\\Music' },
  members: [],
  operations: [operation],
  updated_at: '2026-08-30T00:00:00Z',
  created_at: '2026-08-30T00:00:00Z',
}

/** One turn of the microtask queue for fire-and-forget refreshes. */
function flush(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0))
}

const draftDocument = {
  schema_version: 1,
  classifier_tags: ['A'],
  matched: {},
  unmatched: {},
  members: [],
}

describe('workset mutation cache synchronization', () => {
  it('create seeds the detail cache and refreshes the list prefix', async () => {
    const client = createTestQueryClient()
    client.setQueryData([...queryKeys.worksets.list(null), 'infinite'], { pages: [], pageParams: [] })
    const api = apiStub({ createWorkset: vi.fn().mockResolvedValue({ workset, created: true }) })
    const options = createWorksetMutationOptions(api, client)

    await options.onSuccess({ workset, created: true })
    // The prefix refresh runs in a microtask; give it one turn before asserting.
    await flush()

    expect(client.getQueryData(queryKeys.worksets.detail('ws-1'))).toEqual(workset)
    expect(client.getQueryData([...queryKeys.worksets.list(null), 'infinite'])).toBeUndefined()
  })

  it('draft save seeds the operation entry and refreshes only that operation', async () => {
    const client = createTestQueryClient()
    const otherOperationKey = queryKeys.worksets.operation('ws-1', 'rename')
    const otherDraftKey = queryKeys.worksets.draft('ws-1', 'rename')
    client.setQueryData(otherOperationKey, { marker: 'other-operation' })
    client.setQueryData(otherDraftKey, { marker: 'other-draft' })
    const api = apiStub()
    const options = saveOperationDraftMutationOptions(api, client)

    options.onSuccess(operation, { worksetId: 'ws-1', operation: 'conversion' })

    expect(client.getQueryData(queryKeys.worksets.operation('ws-1', 'conversion'))).toEqual(operation)
    // Another operation's state is never touched by this operation's save.
    expect(client.getQueryData(otherOperationKey)).toEqual({ marker: 'other-operation' })
    expect(client.getQueryData(otherDraftKey)).toEqual({ marker: 'other-draft' })
  })

  it('generation start refreshes the operation entry only for a fresh session', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.worksets.operation('ws-1', 'conversion'), operation)
    const api = apiStub()
    const options = startGenerationMutationOptions(api, client)

    options.onSuccess({ created: false } as { created: boolean }, {
      worksetId: 'ws-1',
      operation: 'conversion',
    })
    await flush()

    expect(client.getQueryData(queryKeys.worksets.operation('ws-1', 'conversion'))).toBeUndefined()
  })

  it('a terminal sync refreshes the operation, its draft and its revisions', async () => {
    const client = createTestQueryClient()
    const keys = [
      queryKeys.worksets.operation('ws-1', 'conversion'),
      queryKeys.worksets.draft('ws-1', 'conversion'),
      queryKeys.worksets.revisionList('ws-1', 'conversion'),
    ]
    for (const key of keys) client.setQueryData(key, { marker: 'stale' })

    await syncAfterGenerationTerminal(client, 'ws-1', 'conversion')

    for (const key of keys) expect(client.getQueryData(key)).toBeUndefined()
  })

  it('a draft conflict reloads the operation and its draft', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.worksets.operation('ws-1', 'conversion'), operation)
    client.setQueryData(queryKeys.worksets.draft('ws-1', 'conversion'), { version: 1, document: draftDocument })

    await syncAfterDraftConflict(client, 'ws-1', 'conversion')

    expect(client.getQueryData(queryKeys.worksets.operation('ws-1', 'conversion'))).toBeUndefined()
    expect(client.getQueryData(queryKeys.worksets.draft('ws-1', 'conversion'))).toBeUndefined()
  })
})
