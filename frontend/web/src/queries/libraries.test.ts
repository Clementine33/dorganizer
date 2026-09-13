import { describe, expect, it, vi } from 'vitest'
import type { ApiClientContract, Folder, Library, TreeNode } from '@/lib/api/types'
import { apiStub as sharedApiStub } from '@/test/api-stub'
import { rootPathIdentityKey } from '@/lib/root-path-identity'
import { createTestQueryClient } from '@/test/query-client'
import { queryKeys } from './query-keys'
import {
  createLibraryMutationOptions,
  deleteLibraryMutationOptions,
  updateLibraryMutationOptions,
} from './libraries'

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
  return sharedApiStub(overrides)
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

  it('delete removes the row and the library-derived cache subtrees', async () => {
    const client = createTestQueryClient()
    client.setQueryData(queryKeys.libraries.list(), [libA, newLibrary])
    seedDerivedCaches(client)
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
  })
})