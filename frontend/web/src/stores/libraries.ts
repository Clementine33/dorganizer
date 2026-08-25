import { defineStore } from 'pinia'
import { ApiError } from '@/lib/api/client'
import type {
  ApiClientContract,
  CreateLibraryInput,
  Folder,
  Library,
  UpdateLibraryInput,
} from '@/lib/api/types'

// Monotonic token for folder loads: bumped on every load and whenever the
// active library changes, so results from superseded or inactive-library
// requests can be discarded instead of overwriting the current state.
let foldersRequestSeq = 0

// rootPathIdentityKey mirrors the backend's pathnorm.RootPathKey: cleaned
// forward-slash form, with letter case folded only for syntactically
// Windows-style paths. The backend judges root changes by this key, so the
// frontend must too — otherwise a spelling/case-equivalent edit would clear
// folders the backend deliberately retained.
function rootPathIdentityKey(path: string): string {
  let p = path.replace(/\\/g, '/')
  const isWindowsPath = /^[a-zA-Z]:\//.test(p) || /^\/\/\?/.test(p) || p.startsWith('//')
  if (isWindowsPath && p.length >= 3 && p[1] === ':') p = p.toLowerCase()
  return p
}

function errorDetails(error: unknown): { code: string | null; message: string } {
  return {
    code: error instanceof ApiError ? error.code : null,
    message: error instanceof Error ? error.message : '发生未知错误',
  }
}

export const useLibrariesStore = defineStore('libraries', {
  state: () => ({
    libraries: [] as Library[],
    folders: [] as Folder[],
    activeLibraryId: null as string | null,
    selectedFolderIds: [] as string[],
    loading: false,
    foldersLoading: false,
    errorCode: null as string | null,
    error: null as string | null,
  }),
  getters: {
    activeLibrary(state): Library | null {
      return state.libraries.find((library) => library.id === state.activeLibraryId) ?? null
    },
    allFoldersSelected(state): boolean {
      return state.folders.length > 0 && state.selectedFolderIds.length === state.folders.length
    },
  },
  actions: {
    async loadLibraries(client: ApiClientContract) {
      this.loading = true
      this.clearError()
      try {
        const libraries = await client.listLibraries()
        this.libraries = libraries
        if (!libraries.some((library) => library.id === this.activeLibraryId)) {
          this.activeLibraryId = libraries[0]?.id ?? null
          this.folders = []
          this.clearSelection()
        }
      } catch (error) {
        this.setError(error)
        throw error
      } finally {
        this.loading = false
      }
    },
    async createLibrary(input: CreateLibraryInput, client: ApiClientContract) {
      this.clearError()
      try {
        const library = await client.createLibrary(input)
        this.libraries.push(library)
        this.activeLibraryId = library.id
        this.folders = []
        this.clearSelection()
        return library
      } catch (error) {
        this.setError(error)
        throw error
      }
    },
    async updateLibrary(id: string, input: UpdateLibraryInput, client: ApiClientContract) {
      this.clearError()
      try {
        const previous = this.libraries.find((library) => library.id === id)
        const updated = await client.updateLibrary(id, input)
        const index = this.libraries.findIndex((library) => library.id === id)
        if (index !== -1) this.libraries[index] = updated

        // The backend deletes all materialized folders and resets scan state
        // when the canonical root changes; mirror that invalidation locally so
        // stale folders/selection are not sent back for now-deleted rows.
        // Compare canonical identity (see rootPathIdentityKey) so spelling-only
        // edits do not over-invalidate state the backend kept.
        const rootChanged =
          previous !== undefined &&
          input.root_path !== undefined &&
          rootPathIdentityKey(previous.root_path) !== rootPathIdentityKey(updated.root_path)
        if (rootChanged && this.activeLibraryId === id) {
          foldersRequestSeq++ // invalidate any in-flight folder load
          this.folders = []
          this.foldersLoading = false
          this.clearSelection()
        }
        return updated
      } catch (error) {
        this.setError(error)
        throw error
      }
    },
    async removeLibrary(id: string, client: ApiClientContract) {
      this.clearError()
      try {
        await client.deleteLibrary(id)
        this.libraries = this.libraries.filter((library) => library.id !== id)
        if (this.activeLibraryId === id) {
          this.activeLibraryId = this.libraries[0]?.id ?? null
          this.folders = []
          this.clearSelection()
        }
      } catch (error) {
        this.setError(error)
        throw error
      }
    },
    setActiveLibrary(id: string) {
      if (id === this.activeLibraryId) return
      foldersRequestSeq++ // invalidate any in-flight folder load
      this.activeLibraryId = id
      this.folders = []
      this.foldersLoading = false
      this.clearSelection()
    },
    async loadFolders(libraryId: string, client: ApiClientContract) {
      const seq = ++foldersRequestSeq
      this.foldersLoading = true
      this.clearError()
      try {
        const folders = await client.listFolders(libraryId)
        if (seq !== foldersRequestSeq || libraryId !== this.activeLibraryId) return
        this.folders = folders
        this.selectedFolderIds = this.selectedFolderIds.filter((id) =>
          this.folders.some((folder) => folder.id === id),
        )
      } catch (error) {
        if (seq !== foldersRequestSeq || libraryId !== this.activeLibraryId) return
        this.setError(error)
        throw error
      } finally {
        if (seq === foldersRequestSeq) this.foldersLoading = false
      }
    },
    toggleFolder(id: string) {
      if (this.selectedFolderIds.includes(id)) {
        this.selectedFolderIds = this.selectedFolderIds.filter((selected) => selected !== id)
      } else if (this.folders.some((folder) => folder.id === id)) {
        this.selectedFolderIds.push(id)
      }
    },
    setFolderSelected(id: string, selected: boolean) {
      const currentlySelected = this.selectedFolderIds.includes(id)
      if (selected !== currentlySelected) this.toggleFolder(id)
    },
    selectAllFolders() {
      this.selectedFolderIds = this.folders.map((folder) => folder.id)
    },
    clearSelection() {
      this.selectedFolderIds = []
    },
    setError(error: unknown) {
      const details = errorDetails(error)
      this.errorCode = details.code
      this.error = details.message
    },
    clearError() {
      this.errorCode = null
      this.error = null
    },
  },
})
