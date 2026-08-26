import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import type { Folder, Library } from '@/lib/api/types'
import { useLibraryUiStore } from './library-ui'

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

const libB: Library = { ...libA, id: 'lib-b', name: 'Brazil', root_path: 'C:\\Audio\\Brazil' }

const folders: Folder[] = [
  { id: 'folder-a', name: 'Alpha', path: '/music/Alpha', relative_path: 'Alpha', audio_file_count: 4 },
  { id: 'folder-b', name: 'Beta', path: '/music/Beta', relative_path: 'Beta', audio_file_count: 9 },
]

describe('library-ui store', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('selects the first library when reconciling an empty active ID', () => {
    const store = useLibraryUiStore()
    store.reconcileLibraries([libA, libB])
    expect(store.activeLibraryId).toBe('lib-a')
  })

  it('keeps the active ID when it still exists and falls back on deletion', () => {
    const store = useLibraryUiStore()
    store.setActiveLibrary('lib-b')
    store.reconcileLibraries([libA, libB])
    expect(store.activeLibraryId).toBe('lib-b')
    store.reconcileLibraries([libA])
    expect(store.activeLibraryId).toBe('lib-a')
    store.reconcileLibraries([])
    expect(store.activeLibraryId).toBeNull()
    store.reconcileLibraries([libB])
    expect(store.activeLibraryId).toBe('lib-b')
  })

  it('clears selection when the active library changes', () => {
    const store = useLibraryUiStore()
    store.setActiveLibrary('lib-a')
    store.toggleFolder('folder-a')
    store.setActiveLibrary('lib-b')
    expect(store.activeLibraryId).toBe('lib-b')
    expect(store.selectedFolderIds).toEqual([])
  })

  it('toggles, selects all and clears selection', () => {
    const store = useLibraryUiStore()
    store.toggleFolder('folder-a')
    expect(store.selectedFolderIds).toEqual(['folder-a'])
    store.toggleFolder('folder-b')
    expect(store.selectedFolderIds).toEqual(['folder-a', 'folder-b'])
    store.selectAllFolders(folders)
    expect(store.selectedFolderIds).toEqual(['folder-a', 'folder-b'])
    store.setFolderSelected('folder-a', false)
    expect(store.selectedFolderIds).toEqual(['folder-b'])
    store.clearSelection()
    expect(store.selectedFolderIds).toEqual([])
  })

  it('reconciles folder selection only against the active library', () => {
    const store = useLibraryUiStore()
    store.setActiveLibrary('lib-a')
    store.toggleFolder('folder-a')
    store.toggleFolder('folder-b')

    // A result belonging to another library must never touch the selection.
    store.reconcileFolders('lib-b', [])
    expect(store.selectedFolderIds).toEqual(['folder-a', 'folder-b'])

    store.reconcileFolders('lib-a', folders.filter((folder) => folder.id !== 'folder-b'))
    expect(store.selectedFolderIds).toEqual(['folder-a'])
  })

  it('leaves the selection alone when no folder IDs were dropped', () => {
    const store = useLibraryUiStore()
    store.setActiveLibrary('lib-a')
    store.toggleFolder('folder-a')
    store.reconcileFolders('lib-a', folders)
    expect(store.selectedFolderIds).toEqual(['folder-a'])
  })
})