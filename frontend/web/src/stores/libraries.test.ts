import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api/client'
import type { ApiClientContract, Folder, Library } from '@/lib/api/types'
import { useLibrariesStore } from './libraries'

const libraryA: Library = {
  id: 'lib-a',
  name: 'Archive',
  root_path: 'C:\\Audio\\Archive',
  created_at: '2026-08-22T00:00:00Z',
  updated_at: '2026-08-22T00:00:00Z',
  last_scan_at: null,
  last_scan_status: '',
  last_scan_error: '',
}

const folders: Folder[] = [
  { id: 'folder-a', name: 'Alpha', path: '/music/Alpha', relative_path: 'Alpha', audio_file_count: 4 },
  { id: 'folder-b', name: 'Beta', path: '/music/Beta', relative_path: 'Beta', audio_file_count: 9 },
]

function apiStub(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  return {
    getHealth: vi.fn(),
    listLibraries: vi.fn().mockResolvedValue([libraryA]),
    getLibrary: vi.fn(),
    createLibrary: vi.fn().mockImplementation(async (input) => ({ ...libraryA, ...input, id: 'lib-new' })),
    updateLibrary: vi.fn().mockImplementation(async (id, input) => ({ ...libraryA, ...input, id })),
    deleteLibrary: vi.fn().mockResolvedValue(undefined),
    scanLibrary: vi.fn(),
    listFolders: vi.fn().mockResolvedValue(folders),
    getFolderTree: vi.fn(),
    createPlan: vi.fn(),
    listPlans: vi.fn(),
    ...overrides,
  }
}

describe('libraries store', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('loads libraries and selects the first library without changing its path', async () => {
    const store = useLibrariesStore()
    await store.loadLibraries(apiStub())

    expect(store.libraries).toEqual([libraryA])
    expect(store.activeLibraryId).toBe('lib-a')
    expect(store.libraries[0]?.root_path).toBe('C:\\Audio\\Archive')
  })

  it('creates, updates, and removes libraries through the API', async () => {
    const api = apiStub()
    const store = useLibrariesStore()
    await store.loadLibraries(api)

    await store.createLibrary({ name: 'New', root_path: '/mnt/new' }, api)
    expect(api.createLibrary).toHaveBeenCalledWith({ name: 'New', root_path: '/mnt/new' })
    expect(store.activeLibraryId).toBe('lib-new')

    await store.updateLibrary('lib-new', { name: 'Renamed' }, api)
    expect(store.activeLibrary?.name).toBe('Renamed')

    await store.removeLibrary('lib-new', api)
    expect(api.deleteLibrary).toHaveBeenCalledWith('lib-new')
    expect(store.libraries.map((library) => library.id)).toEqual(['lib-a'])
    expect(store.activeLibraryId).toBe('lib-a')
  })

  it('sets a recoverable error when loading fails', async () => {
    const api = apiStub({ listLibraries: vi.fn().mockRejectedValue(new Error('backend unavailable')) })
    const store = useLibrariesStore()

    await expect(store.loadLibraries(api)).rejects.toThrow('backend unavailable')
    expect(store.error).toBe('backend unavailable')
    store.clearError()
    expect(store.error).toBeNull()
  })

  it('surfaces an API envelope code when creating a library fails', async () => {
    const failure = new ApiError(409, 'LIBRARY_EXISTS', 'library already exists')
    const api = apiStub({ createLibrary: vi.fn().mockRejectedValue(failure) })
    const store = useLibrariesStore()

    await expect(store.createLibrary({ name: 'Duplicate', root_path: '/music' }, api)).rejects.toBe(
      failure,
    )
    expect(store.errorCode).toBe('LIBRARY_EXISTS')
    expect(store.error).toBe('library already exists')

    store.clearError()
    expect(store.errorCode).toBeNull()
    expect(store.error).toBeNull()
  })

  it('loads folders and supports toggle, select-all, and clear selection', async () => {
    const api = apiStub()
    const store = useLibrariesStore()

    await store.loadFolders('lib-a', api)
    store.toggleFolder('folder-a')
    expect(store.selectedFolderIds).toEqual(['folder-a'])

    store.selectAllFolders()
    expect(store.selectedFolderIds).toEqual(['folder-a', 'folder-b'])
    expect(store.allFoldersSelected).toBe(true)

    store.clearSelection()
    expect(store.selectedFolderIds).toEqual([])
  })
})
