import { useQuery, type QueryClient } from '@tanstack/vue-query'
import { computed, toValue, watchEffect, type ComputedRef, type MaybeRefOrGetter } from 'vue'
import { useApiClient } from '@/lib/api/client'
import { rootPathIdentityKey } from '@/lib/root-path-identity'
import type { ApiClientContract, CreateLibraryInput, Library, UpdateLibraryInput } from '@/lib/api/types'
import { useLibraryUiStore } from '@/stores/library-ui'
import { refreshOrRemoveQueries } from './cache-sync'
import { TREE_GC_TIME } from './query-client'
import { queryKeys } from './query-keys'

// ---------- query options ----------

export function libraryListQueryOptions(api: ApiClientContract) {
  return {
    queryKey: queryKeys.libraries.list(),
    queryFn: ({ signal }: { signal?: AbortSignal }) => api.listLibraries(signal),
  }
}

export function folderListQueryOptions(
  api: ApiClientContract,
  libraryId: string | null,
  rootIdentity: string | null,
) {
  return {
    queryKey: queryKeys.libraries.folders(libraryId ?? '', rootIdentity ?? ''),
    enabled: Boolean(libraryId && rootIdentity),
    queryFn: ({ signal }: { signal?: AbortSignal }) => {
      if (!libraryId || !rootIdentity) throw new Error('folder query requires a library and root identity')
      return api.listFolders(libraryId, signal)
    },
  }
}

export function folderTreeQueryOptions(
  api: ApiClientContract,
  libraryId: string | null,
  rootIdentity: string | null,
  folderId: string | null,
) {
  return {
    queryKey: queryKeys.libraries.tree(libraryId ?? '', rootIdentity ?? '', folderId ?? ''),
    enabled: Boolean(libraryId && rootIdentity && folderId),
    gcTime: TREE_GC_TIME,
    queryFn: ({ signal }: { signal?: AbortSignal }) => {
      if (!libraryId || !rootIdentity || !folderId) {
        throw new Error('tree query requires a library, root identity and folder')
      }
      return api.getFolderTree(libraryId, folderId, signal)
    },
  }
}

// ---------- library mutations (cache coordination is owned here) ----------

export function createLibraryMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (input: CreateLibraryInput) => api.createLibrary(input),
    onSuccess: (library: Library, _variables: CreateLibraryInput, _context: unknown) => {
      queryClient.setQueryData<Library[]>(queryKeys.libraries.list(), (old) => {
        if (!old) return [library]
        if (old.some((item) => item.id === library.id)) return old
        return [...old, library]
      })
    },
  }
}

export function updateLibraryMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: ({ id, input }: { id: string; input: UpdateLibraryInput }) => api.updateLibrary(id, input),
    onSuccess: (
      updated: Library,
      _variables: { id: string; input: UpdateLibraryInput },
      _context: unknown,
    ) => {
      const previous = queryClient
        .getQueryData<Library[]>(queryKeys.libraries.list())
        ?.find((item) => item.id === updated.id)
      queryClient.setQueryData<Library[]>(queryKeys.libraries.list(), (old) =>
        old ? old.map((item) => (item.id === updated.id ? updated : item)) : old,
      )
      if (previous && rootPathIdentityKey(previous.root_path) !== rootPathIdentityKey(updated.root_path)) {
        // Genuine root identity change: the backend discarded materialized
        // folders, so every derived folder/tree cache for this library is
        // invalid. Mounted observers refetch; inactive entries are dropped.
        // (The caller clears UI selection — the mutation module has no store.)
        void refreshOrRemoveQueries(queryClient, queryKeys.libraries.foldersPrefix(updated.id))
        void refreshOrRemoveQueries(queryClient, queryKeys.libraries.treesPrefix(updated.id))
      }
    },
  }
}

export function deleteLibraryMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (id: string) => api.deleteLibrary(id),
    onSuccess: (_result: void, id: string, _context: unknown) => {
      queryClient.setQueryData<Library[]>(queryKeys.libraries.list(), (old) =>
        old ? old.filter((item) => item.id !== id) : old,
      )
      void refreshOrRemoveQueries(queryClient, queryKeys.libraries.foldersPrefix(id))
      void refreshOrRemoveQueries(queryClient, queryKeys.libraries.treesPrefix(id))
      // Deleting the library orphans its worksets (planning_state flips to
      // orphaned, validation becomes unavailable) — those transitions live in
      // staleTime:Infinity workset caches with no other invalidation driver.
      void refreshOrRemoveQueries(queryClient, queryKeys.worksets.all())
    },
  }
}

// ---------- composables ----------

// Observes the library list and keeps the UI store's active ID reconciled.
// A page mounts this to guarantee data (the app also mounts AppShell as a
// long-lived observer), so any consumer can derive its library list from the
// shared cache. Exposes the derived data and active library so callers do not
// repeat the same unwrapping/computed logic.
export function useLibraryList() {
  const api = useApiClient()
  const ui = useLibraryUiStore()
  const query = useQuery(libraryListQueryOptions(api))
  watchEffect(() => {
    if (query.data.value) ui.reconcileLibraries(query.data.value)
  })
  const librariesData = computed(() => query.data.value ?? [])
  const activeLibrary = computed(
    () => librariesData.value.find((library) => library.id === ui.activeLibraryId) ?? null,
  )
  return { query, librariesData, activeLibrary }
}

// Canonical root identity for a library record, as a reactive value. Used to
// scope folder/tree query keys so spelling-equivalent root edits keep their
// cache while genuine root changes invalidate it.
export function useRootIdentity(
  library: MaybeRefOrGetter<Library | null | undefined>,
): ComputedRef<string | null> {
  return computed(() => {
    const lib = toValue(library)
    return lib ? rootPathIdentityKey(lib.root_path) : null
  })
}