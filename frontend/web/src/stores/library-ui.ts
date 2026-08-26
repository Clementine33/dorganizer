import { defineStore } from 'pinia'
import type { Folder, Library } from '@/lib/api/types'

// UI-only store: active library and folder selection. Server-derived data
// lives in Vue Query; this store never holds libraries, folders, loading, or
// error state.
export const useLibraryUiStore = defineStore('library-ui', {
  state: () => ({
    activeLibraryId: null as string | null,
    selectedFolderIds: [] as string[],
  }),
  actions: {
    setActiveLibrary(id: string) {
      if (id === this.activeLibraryId) return
      this.activeLibraryId = id
      this.selectedFolderIds = []
    },
    // Keeps the active ID valid against a freshly fetched library list: keep
    // the current ID when it still exists, otherwise fall back to the first
    // library (or null when there are none).
    reconcileLibraries(libraries: Library[]) {
      const next =
        this.activeLibraryId !== null && libraries.some((library) => library.id === this.activeLibraryId)
          ? this.activeLibraryId
          : (libraries[0]?.id ?? null)
      if (next !== this.activeLibraryId) {
        this.activeLibraryId = next
        this.selectedFolderIds = []
      }
    },
    // Drops selected folder IDs that no longer exist in a successful folder
    // result. Results from another library are ignored so a late response or
    // a background refetch can never mutate the current selection.
    reconcileFolders(libraryId: string, folders: Folder[]) {
      if (libraryId !== this.activeLibraryId) return
      if (this.selectedFolderIds.length === 0) return
      const ids = new Set(folders.map((folder) => folder.id))
      const next = this.selectedFolderIds.filter((id) => ids.has(id))
      if (next.length !== this.selectedFolderIds.length) this.selectedFolderIds = next
    },
    // The optional `folders` argument restores the pre-migration existence
    // guard: a click that lands after reconcileFolders dropped the folder
    // (e.g. a scan-sync refresh removed it right before the event) must not
    // push a stale ID into the selection — the backend would reject it.
    toggleFolder(id: string, folders?: Folder[]) {
      if (this.selectedFolderIds.includes(id)) {
        this.selectedFolderIds = this.selectedFolderIds.filter((selected) => selected !== id)
      } else {
        if (folders && !folders.some((folder) => folder.id === id)) return
        this.selectedFolderIds.push(id)
      }
    },
    setFolderSelected(id: string, selected: boolean, folders?: Folder[]) {
      if (selected !== this.selectedFolderIds.includes(id)) this.toggleFolder(id, folders)
    },
    selectAllFolders(folders: Folder[]) {
      this.selectedFolderIds = folders.map((folder) => folder.id)
    },
    clearSelection() {
      this.selectedFolderIds = []
    },
  },
})