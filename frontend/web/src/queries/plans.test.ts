import { describe, expect, it, vi } from 'vitest'
import type { ApiClientContract, PlanResponse } from '@/lib/api/types'
import { createTestQueryClient } from '@/test/query-client'
import { queryKeys } from './query-keys'
import { createPlanMutationOptions } from './plans'

const plan: PlanResponse = {
  plan_id: 'plan-1',
  snapshot_token: 'snap-1',
  root_path: 'D:\\Music',
  summary: {
    operation_count: 1,
    error_count: 0,
    total_count: 1,
    actionable_count: 1,
    summary_reason: 'ACTIONABLE',
  },
  operations: [],
  errors: [],
  successful_folders: [],
}

function apiStub(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  return {
    getHealth: vi.fn(),
    listLibraries: vi.fn(),
    getLibrary: vi.fn(),
    createLibrary: vi.fn(),
    updateLibrary: vi.fn(),
    deleteLibrary: vi.fn(),
    scanLibrary: vi.fn(),
    listFolders: vi.fn(),
    getFolderTree: vi.fn(),
    getPlan: vi.fn(),
    createPlan: vi.fn(),
    listPlans: vi.fn(),
    ...overrides,
  }
}

describe('plan create mutation cache synchronization', () => {
  it('seeds the durable detail cache before returning', async () => {
    const queryClient = createTestQueryClient()
    const api = apiStub({ createPlan: vi.fn().mockResolvedValue(plan) })
    const options = createPlanMutationOptions(api, queryClient)

    const created = await options.mutationFn({
      library_id: 'lib-a',
      folder_ids: ['folder-a'],
      plan_type: 'slim',
      target_format: 'slim:mode1',
      prune_matched_excluded: false,
    })
    options.onSuccess?.(created, { library_id: 'lib-a', folder_ids: ['folder-a'] }, undefined)

    expect(queryClient.getQueryData(queryKeys.plans.detail('plan-1'))).toEqual(plan)
  })

  it('refreshes active and removes inactive members only for the creating library', async () => {
    const queryClient = createTestQueryClient()
    const listKey = queryKeys.plans.list('lib-a', 100)
    const otherKey = queryKeys.plans.list('lib-b', 100)
    // Both lists are inactive (no observers): the creating library's list is
    // dropped (next mount cold-fetches), the other library's list is kept.
    queryClient.setQueryData(listKey, [])
    queryClient.setQueryData(otherKey, [])
    const api = apiStub({ createPlan: vi.fn().mockResolvedValue(plan) })
    const options = createPlanMutationOptions(api, queryClient)

    const created = await options.mutationFn({ library_id: 'lib-a', source_files: ['a.flac'] })
    options.onSuccess?.(created, { library_id: 'lib-a', source_files: ['a.flac'] }, undefined)

    await vi.waitFor(() => expect(queryClient.getQueryData(listKey)).toBeUndefined())
    expect(queryClient.getQueryData(otherKey)).toEqual([])
  })
})