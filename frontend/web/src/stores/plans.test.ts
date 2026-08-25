import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import type { ApiClientContract, PlanResponse } from '@/lib/api/types'
import { usePlansStore } from './plans'

const plan: PlanResponse = {
  plan_id: 'plan-1',
  snapshot_token: 'snapshot-1',
  root_path: '/music',
  summary: {
    operation_count: 1,
    error_count: 0,
    total_count: 1,
    actionable_count: 1,
    summary_reason: 'ACTIONABLE',
  },
  operations: [{ type: 'delete', source_path: '/music/A/a.flac', target_path: '' }],
  errors: [],
  successful_folders: ['/music/A'],
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
    createPlan: vi.fn().mockResolvedValue(plan),
    listPlans: vi.fn().mockResolvedValue([
      { plan_id: 'plan-1', root_path: '/music', plan_type: 'slim', status: 'planned', created_at: '2026-08-22T00:00:00Z' },
    ]),
    ...overrides,
  }
}

describe('plans store', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('creates one plan request for all selected folder IDs', async () => {
    const api = apiStub()
    const store = usePlansStore()

    const result = await store.createForFolders('lib-a', ['folder-a', 'folder-b'], api)

    expect(api.createPlan).toHaveBeenCalledOnce()
    expect(api.createPlan).toHaveBeenCalledWith({
      library_id: 'lib-a',
      folder_ids: ['folder-a', 'folder-b'],
      plan_type: 'slim',
      target_format: 'slim:mode1',
      prune_matched_excluded: false,
    })
    expect(result).toEqual(plan)
    expect(store.currentPlan).toEqual(plan)
  })

  it('creates one plan request from a per-file source_files scope', async () => {
    const api = apiStub()
    const store = usePlansStore()
    const files = ['/music/albumA/track1.flac', '/music/albumA/track2.flac']

    const result = await store.createForFiles('lib-a', files, api)

    expect(api.createPlan).toHaveBeenCalledOnce()
    expect(api.createPlan).toHaveBeenCalledWith({
      library_id: 'lib-a',
      source_files: ['/music/albumA/track1.flac', '/music/albumA/track2.flac'],
    })
    expect(result).toEqual(plan)
    expect(store.currentPlan).toEqual(plan)
  })

  it('loads library-scoped plans and exposes API failures for recovery', async () => {
    const store = usePlansStore()
    await store.loadPlans('lib-a', apiStub())
    expect(store.plans.map((item) => item.plan_id)).toEqual(['plan-1'])

    const failure = new ApiError(400, 'SCOPE_REQUIRED', 'folder_ids or source_files is required')
    const api = apiStub({ createPlan: vi.fn().mockRejectedValue(failure) })
    await expect(store.createForFolders('lib-a', ['folder-a'], api)).rejects.toBe(failure)
    expect(store.errorCode).toBe('SCOPE_REQUIRED')
    expect(store.error).toBe('folder_ids or source_files is required')

    store.clearError()
    expect(store.errorCode).toBeNull()
    expect(store.error).toBeNull()
  })
})
