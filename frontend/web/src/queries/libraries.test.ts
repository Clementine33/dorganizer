import { describe, expect, it, vi } from 'vitest'
import type { ApiClientContract, Folder, Library, PlanResponse, TreeNode } from '@/lib/api/types'
import { rootPathIdentityKey } from '@/lib/root-path-identity'
import { createTestQueryClient } from '@/test/query-client'
import { queryKeys } from './query-keys'
import {
  createLibraryMutationOptions,
  deleteLibraryMutationOptions,
  updateLibraryMutationOptions,
} from './libraries'
import { createPlanMutationOptions } from './plans'

const libA: Library = {
  id: 'lib-a',
  name: 'Archive',
  root_path: 'C:\\Audio\\Archive',
  created_at: '2026-08-22T00:00:00Z',
  updated_at: '2026-08-22T00:00:00Z',
  last_scan_at: null,
  last_scan_status: '',
  last_scan_error: '',
}

const libADifferentRoot: Library = { ...libA, root_path: 'D:\\Audio\\Archive' }

const newLibrary: Library = { ...libA, id: 'lib-new', name: 'New library' }

const folders: Folder[] = [
  { id: 'folder-a', name: 'Alpha', path: '/music/Alpha', relative_path: 'Alpha', audio_file_count: 4 },
]

const treeRoot: TreeNode = {
  name: 'Alpha',
  path: '/music/Alpha',
  type: 'dir',
  format: '',
  bitrate: null,
  children: [],
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

function seedDerivedCaches(client: ReturnType<typeof createTestQueryClient>) {
  client.setQueryData(queryKeys.libraries.folders('lib-a', rootPathIdentityKey(libA.root_path)), folders)
  client.setQueryData(
    queryKeys.libraries.tree('lib-a', rootPathIdentityKey(libA.root_path), 'folder-a'),
    treeRoot,
  )
}

describe('library mutation cache synchronization', () => {
  it('create upserts the returned library into the cached list', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.libraries.list(), [libA])
    const api = apiStub({ createLibrary: vi.fn().mockResolvedValue(newLibrary) })
    const options = createLibraryMutationOptions(api, client)

    const created = await options.mutationFn({ name: 'New library', root_path: 'C:\\Audio\\New' })
    options.onSuccess?.(created, { name: 'New library', root_path: 'C:\\Audio\\New' }, undefined)

    expect(client.getQueryData<Library[]>(queryKeys.libraries.list())).toContainEqual(newLibrary)
  })

  it('create seeds the cached list with the returned library when no cache exists', async () => {
    const client = createTestQueryClient()
    expect(client.getQueryData(queryKeys.libraries.list())).toBeUndefined()
    const api = apiStub({ createLibrary: vi.fn().mockResolvedValue(newLibrary) })
    const options = createLibraryMutationOptions(api, client)

    const created = await options.mutationFn({ name: 'New library', root_path: 'C:\\Audio\\New' })
    options.onSuccess?.(created, { name: 'New library', root_path: 'C:\\Audio\\New' }, undefined)

    expect(client.getQueryData<Library[]>(queryKeys.libraries.list())).toEqual([newLibrary])
  })

  it('name-only update replaces the row and preserves derived caches', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.libraries.list(), [libA])
    seedDerivedCaches(client)
    const renamed = { ...libA, name: 'Renamed' }
    const api = apiStub({ updateLibrary: vi.fn().mockResolvedValue(renamed) })
    const options = updateLibraryMutationOptions(api, client)

    const updated = await options.mutationFn({ id: 'lib-a', input: { name: 'Renamed' } })
    options.onSuccess?.(updated, { id: 'lib-a', input: { name: 'Renamed' } }, undefined)

    expect(client.getQueryData<Library[]>(queryKeys.libraries.list())).toContainEqual(renamed)
    expect(client.getQueryData(queryKeys.libraries.folders('lib-a', rootPathIdentityKey(libA.root_path)))).toEqual(
      folders,
    )
  })

  it('spelling-equivalent root update preserves derived caches', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.libraries.list(), [libA])
    seedDerivedCaches(client)
    const equivalent = { ...libA, root_path: 'C:\\Audio\\archive' }
    const api = apiStub({ updateLibrary: vi.fn().mockResolvedValue(equivalent) })
    const options = updateLibraryMutationOptions(api, client)

    const updated = await options.mutationFn({ id: 'lib-a', input: { root_path: 'C:\\Audio\\archive' } })
    options.onSuccess?.(updated, { id: 'lib-a', input: { root_path: 'C:\\Audio\\archive' } }, undefined)

    expect(client.getQueryData(queryKeys.libraries.folders('lib-a', rootPathIdentityKey(libA.root_path)))).toEqual(
      folders,
    )
  })

  it('genuine root identity change removes derived folder and tree caches', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.libraries.list(), [libA])
    seedDerivedCaches(client)
    const api = apiStub({ updateLibrary: vi.fn().mockResolvedValue(libADifferentRoot) })
    const options = updateLibraryMutationOptions(api, client)

    const updated = await options.mutationFn({ id: 'lib-a', input: { root_path: 'D:\\Audio\\Archive' } })
    options.onSuccess?.(updated, { id: 'lib-a', input: { root_path: 'D:\\Audio\\Archive' } }, undefined)

    expect(client.getQueryData<Library[]>(queryKeys.libraries.list())).toContainEqual(libADifferentRoot)
    await vi.waitFor(() => {
      expect(client.getQueryData(queryKeys.libraries.folders('lib-a', rootPathIdentityKey(libA.root_path)))).toBeUndefined()
      expect(
        client.getQueryData(queryKeys.libraries.tree('lib-a', rootPathIdentityKey(libA.root_path), 'folder-a')),
      ).toBeUndefined()
    })
  })

  it('delete removes the row and the library-derived cache subtrees and scoped plan list', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.libraries.list(), [libA, newLibrary])
    seedDerivedCaches(client)
    client.setQueryData(queryKeys.plans.list('lib-a', 100), [])
    const api = apiStub({ deleteLibrary: vi.fn().mockResolvedValue(undefined) })
    const options = deleteLibraryMutationOptions(api, client)

    await options.mutationFn('lib-a')
    options.onSuccess?.(undefined, 'lib-a', undefined)

    expect(client.getQueryData<Library[]>(queryKeys.libraries.list())?.map((lib) => lib.id)).toEqual(['lib-new'])
    await vi.waitFor(() => {
      expect(client.getQueryData(queryKeys.libraries.folders('lib-a', rootPathIdentityKey(libA.root_path)))).toBeUndefined()
      expect(
        client.getQueryData(queryKeys.libraries.tree('lib-a', rootPathIdentityKey(libA.root_path), 'folder-a')),
      ).toBeUndefined()
    })
    expect(client.getQueryData(queryKeys.plans.list('lib-a', 100))).toBeUndefined()
  })

  it('delete removes plan details created in-session even when the scoped list cache is gone', async () => {
    const plan: PlanResponse = {
      plan_id: 'plan-1',
      snapshot_token: 'snap-1',
      root_path: 'D:\\Music',
      summary: {
        operation_count: 0,
        error_count: 0,
        total_count: 0,
        actionable_count: 0,
        summary_reason: 'ACTIONABLE',
      },
      operations: [],
      errors: [],
      successful_folders: [],
    }
    const client = createTestQueryClient()
    const api = apiStub({
      deleteLibrary: vi.fn().mockResolvedValue(undefined),
      createPlan: vi.fn().mockResolvedValue(plan),
    })
    // Simulate a session-created plan: the detail is seeded and the scoped
    // list entry is dropped (no active observer), so the usual
    // scoped-list sweep cannot find the plan's detail afterwards.
    const createOptions = createPlanMutationOptions(api, client)
    await createOptions.mutationFn({ library_id: 'lib-a', folder_ids: ['folder-a'] })
    createOptions.onSuccess?.({ ...plan }, { library_id: 'lib-a', folder_ids: ['folder-a'] }, undefined)
    expect(client.getQueryData(queryKeys.plans.detail('plan-1'))).toBeDefined()
    expect(client.getQueryData(queryKeys.plans.list('lib-a', 100))).toBeUndefined()

    const options = deleteLibraryMutationOptions(api, client)
    await options.mutationFn('lib-a')
    options.onSuccess?.(undefined, 'lib-a', undefined)

    // The plan->library mapping must still purge the durable detail cache.
    expect(client.getQueryData(queryKeys.plans.detail('plan-1'))).toBeUndefined()
  })
})