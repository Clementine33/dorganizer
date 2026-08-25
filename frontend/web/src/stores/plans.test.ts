import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import type { ApiClientContract, PlanInfo, PlanResponse } from '@/lib/api/types'
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
    getPlan: vi.fn(),
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

describe('plans store races', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('ignores stale plan list responses when the library changes mid-load', async () => {
    let resolveA!: (plans: PlanInfo[]) => void
    const listPlans = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<PlanInfo[]>((resolve) => {
            resolveA = resolve
          }),
      )
      .mockResolvedValue([
        { plan_id: 'plan-b', root_path: '/music', plan_type: 'slim', status: 'ready', created_at: '2026-08-22T00:00:00Z' },
      ])
    const api = apiStub({ listPlans })
    const store = usePlansStore()

    const loadA = store.loadPlans('lib-a', api)
    const loadB = store.loadPlans('lib-b', api)
    await loadB
    expect(store.plans.map((item) => item.plan_id)).toEqual(['plan-b'])
    expect(store.loading).toBe(false)

    // The superseded lib-a request resolves late: it must not overwrite lib-b.
    resolveA([{ plan_id: 'plan-a', root_path: '/music', plan_type: 'slim', status: 'ready', created_at: '2026-08-22T00:00:00Z' }])
    await loadA
    expect(store.plans.map((item) => item.plan_id)).toEqual(['plan-b'])
    expect(store.loading).toBe(false)
  })

  it('ignores stale plan detail responses when navigating quickly', async () => {
    let resolveA!: (p: PlanResponse) => void
    const getPlan = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<PlanResponse>((resolve) => {
            resolveA = resolve
          }),
      )
      .mockResolvedValue({ ...plan, plan_id: 'plan-b' })
    const api = apiStub({ getPlan })
    const store = usePlansStore()

    const loadA = store.loadPlan('plan-a', api)
    const loadB = store.loadPlan('plan-b', api)
    await loadB
    expect(store.currentPlan?.plan_id).toBe('plan-b')
    expect(store.detailLoading).toBe(false)

    resolveA({ ...plan, plan_id: 'plan-a' })
    await loadA
    expect(store.currentPlan?.plan_id).toBe('plan-b')
    expect(store.detailLoading).toBe(false)
  })

  it('clears a stale currentPlan when navigating to a different plan', async () => {
    const api = apiStub({ getPlan: vi.fn().mockResolvedValue({ ...plan, plan_id: 'plan-b' }) })
    const store = usePlansStore()
    store.currentPlan = plan // plan-a cached from a just-created plan

    await store.loadPlan('plan-b', api)
    expect(store.currentPlan?.plan_id).toBe('plan-b')
  })
})
