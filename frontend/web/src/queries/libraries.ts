import { useQuery, type QueryClient } from '@tanstack/vue-query'
import { computed, toValue, watchEffect, type ComputedRef, type MaybeRefOrGetter } from 'vue'
import { useApiClient } from '@/lib/api/client'
import { rootPathIdentityKey } from '@/lib/root-path-identity'
import type {
  ApiClientContract,
  CreateLibraryInput,
  FileOperation,
  FileOperationItem,
  FileOperationResult,
  Library,
  MemberTreeResponse,
  UpdateLibraryInput,
} from '@/lib/api/types'
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

/**
 * The overview listing: every direct child directory of the library root,
 * including empty ones and ones holding no audio, with the audio count as a
 * status rather than a filter.
 */
export function dirsQueryOptions(
  api: ApiClientContract,
  libraryId: string | null,
  rootIdentity: string | null,
) {
  return {
    queryKey: queryKeys.libraries.dirs(libraryId ?? '', rootIdentity ?? ''),
    enabled: Boolean(libraryId && rootIdentity),
    queryFn: ({ signal }: { signal?: AbortSignal }) => {
      if (!libraryId || !rootIdentity) throw new Error('dirs query requires a library and root identity')
      return api.listDirs(libraryId, signal)
    },
  }
}

/**
 * One member tree, addressed by the member's library-relative path. The stored
 * tree is shown while the next refresh lands (gcTime keeps it around after a
 * page leaves), so entering a member page never shows an empty tree first.
 */
export function memberTreeQueryOptions(
  api: ApiClientContract,
  libraryId: string | null,
  rootIdentity: string | null,
  relPath: string | null,
) {
  return {
    queryKey: queryKeys.libraries.memberTree(libraryId ?? '', rootIdentity ?? '', relPath ?? ''),
    enabled: Boolean(libraryId && rootIdentity && relPath),
    gcTime: TREE_GC_TIME,
    queryFn: ({ signal }: { signal?: AbortSignal }) => {
      if (!libraryId || !rootIdentity || !relPath) {
        throw new Error('member tree query requires a library, root identity and member path')
      }
      return api.getMemberTree(libraryId, relPath, signal)
    },
  }
}

/**
 * Refresh one member tree: a scoped scan of that directory. It is refused
 * while a file operation (or a session) holds the admission slot, and the
 * refreshed tree is written straight into the cache the page reads.
 */
export function refreshMemberTreeMutationOptions(
  api: ApiClientContract,
  queryClient: QueryClient,
  libraryId: string,
  rootIdentity: string,
) {
  return {
    mutationFn: (relPath: string) => api.refreshMemberTree(libraryId, relPath),
    onSuccess: (result: MemberTreeResponse, relPath: string) => {
      queryClient.setQueryData(
        queryKeys.libraries.memberTree(libraryId, rootIdentity, relPath),
        result,
      )
    },
  }
}

/**
 * One direct file-management request. The backend answers per item and also
 * reports whether it could refresh the inventory afterwards; both facts reach
 * the caller unchanged. Whatever happened, the caches that describe the disk
 * are refreshed here: the member tree, the overview counts, and the library's
 * current record (whose plan validity the change may have moved).
 */
export function fileOperationMutationOptions(
  api: ApiClientContract,
  queryClient: QueryClient,
  libraryId: string,
  rootIdentity: string,
) {
  return {
    mutationFn: (input: {
      member_path: string
      operation: FileOperation
      items: FileOperationItem[]
    }) => api.applyFileOperation(libraryId, input),
    onSuccess: (result: FileOperationResult, input: { member_path: string }) => {
      void refreshOrRemoveQueries(
        queryClient,
        queryKeys.libraries.memberTree(libraryId, rootIdentity, input.member_path),
      )
      void refreshOrRemoveQueries(queryClient, queryKeys.libraries.dirs(libraryId, rootIdentity))
      // A rename, move or deletion inside a member changes the inputs a plan
      // was frozen from, so the record's own view is refetched too.
      void refreshOrRemoveQueries(queryClient, queryKeys.worksets.current(libraryId, 'conversion'))
      return result
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
        void refreshOrRemoveQueries(queryClient, queryKeys.libraries.dirsPrefix(updated.id))
        void refreshOrRemoveQueries(queryClient, queryKeys.libraries.memberTreesPrefix(updated.id))
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
      void refreshOrRemoveQueries(queryClient, queryKeys.libraries.dirsPrefix(id))
      void refreshOrRemoveQueries(queryClient, queryKeys.libraries.memberTreesPrefix(id))
      // Deleting a library takes its processing record with it, so every
      // record-scoped cache of that library is dropped rather than refreshed.
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