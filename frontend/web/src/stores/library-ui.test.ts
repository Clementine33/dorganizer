import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import type { Library, LibraryDir } from '@/lib/api/types'
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

const dirs: LibraryDir[] = [
  { name: 'Alpha', path: '/music/Alpha', rel_path: 'Alpha', dir_id: 'dir-alpha', audio_file_count: 4, file_count: 4 },
  { name: 'Beta', path: '/music/Beta', rel_path: 'Beta', dir_id: 'dir-beta', audio_file_count: 9, file_count: 9 },
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
    store.toggleDir('Alpha')
    store.setActiveLibrary('lib-b')
    expect(store.activeLibraryId).toBe('lib-b')
    expect(store.selectedDirPaths).toEqual([])
  })

  it('toggles, selects all and clears selection', () => {
    const store = useLibraryUiStore()
    store.toggleDir('Alpha')
    expect(store.selectedDirPaths).toEqual(['Alpha'])
    store.toggleDir('Beta')
    expect(store.selectedDirPaths).toEqual(['Alpha', 'Beta'])
    store.selectAllDirs(dirs)
    expect(store.selectedDirPaths).toEqual(['Alpha', 'Beta'])
    store.setDirSelected('Alpha', false)
    expect(store.selectedDirPaths).toEqual(['Beta'])
    store.clearSelection()
    expect(store.selectedDirPaths).toEqual([])
  })

  it('reconciles the directory selection only against the active library', () => {
    const store = useLibraryUiStore()
    store.setActiveLibrary('lib-a')
    store.toggleDir('Alpha')
    store.toggleDir('Beta')

    // A result belonging to another library must never touch the selection.
    store.reconcileDirs('lib-b', [])
    expect(store.selectedDirPaths).toEqual(['Alpha', 'Beta'])

    store.reconcileDirs('lib-a', dirs.filter((dir) => dir.rel_path !== 'Beta'))
    expect(store.selectedDirPaths).toEqual(['Alpha'])
  })

  it('leaves the selection alone when no paths were dropped', () => {
    const store = useLibraryUiStore()
    store.setActiveLibrary('lib-a')
    store.toggleDir('Alpha')
    store.reconcileDirs('lib-a', dirs)
    expect(store.selectedDirPaths).toEqual(['Alpha'])
  })

  it('refuses to select a path that is no longer in the listing', () => {
    const store = useLibraryUiStore()
    store.setActiveLibrary('lib-a')
    // A late click for a folder the reconciled list no longer contains must
    // not enter the selection (the backend would reject the plan payload).
    store.toggleDir('Alpha', [])
    expect(store.selectedDirPaths).toEqual([])
    // A folder still in the list selects normally…
    store.toggleDir('Beta', dirs)
    expect(store.selectedDirPaths).toEqual(['Beta'])
    // …and toggling off an already-selected ID stays allowed without the list.
    store.setDirSelected('Beta', false, [])
    expect(store.selectedDirPaths).toEqual([])
  })
})