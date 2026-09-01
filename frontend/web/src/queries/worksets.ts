import { infiniteQueryOptions, queryOptions, type QueryClient } from '@tanstack/vue-query'
import type {
  ApiClientContract,
  CreateWorksetInput,
  ListWorksetsParams,
  ResolvedPolicy,
  RevisionListResponse,
  Workset,
  WorksetListResponse,
  WorkflowInput,
} from '@/lib/api/types'
import { refreshOrRemoveQueries } from './cache-sync'
import { queryKeys } from './query-keys'

// Workset server-state coordination. All cache writes and invalidations for
// the workset domain live here — pages never touch the QueryClient directly.

const WORKSET_PAGE_SIZE = 50
// Revision history renders in the inspector; a small page keeps the initial
// payload and DOM bounded for worksets with many generations.
const REVISION_PAGE_SIZE = 10

// The global feed is a cursor-paginated list, filter + library scoped. The
// page size mirrors the backend default so "load more" appends one page at a
// time; cached pages are independent cache entries keyed by cursor.
export function worksetFeedInfiniteQueryOptions(api: ApiClientContract, params: { feed?: string; libraryId?: string | null }) {
  return infiniteQueryOptions({
    queryKey: [...queryKeys.worksets.feed(params.feed ?? 'all', params.libraryId ?? null), 'infinite'],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }: { pageParam: string | undefined; signal?: AbortSignal }) => {
      const listParams: ListWorksetsParams = { limit: WORKSET_PAGE_SIZE }
      if (params.feed) listParams.feed = params.feed as ListWorksetsParams['feed']
      if (params.libraryId) listParams.library_id = params.libraryId
      if (pageParam) listParams.cursor = pageParam
      return api.listWorksets(listParams, signal)
    },
    getNextPageParam: (lastPage: WorksetListResponse) => lastPage.next_cursor || undefined,
    staleTime: Infinity,
  })
}

// Authoritative aggregate detail. staleTime is infinite because everything
// that can change the payload flows through an explicit mutation or a
// generation terminal, both of which refresh this key family.
export function worksetDetailQueryOptions(api: ApiClientContract, worksetId: string | null | undefined) {
  return queryOptions({
    queryKey: queryKeys.worksets.detail(worksetId ?? ''),
    enabled: Boolean(worksetId),
    staleTime: Infinity,
    queryFn: ({ signal }: { signal?: AbortSignal }) => api.getWorkset(worksetId as string, signal),
  })
}

// The persisted workflow draft. Refetched explicitly after a version conflict
// so the user can load the server version; version is the If-Match authority.
export function worksetDraftQueryOptions(api: ApiClientContract, worksetId: string | null | undefined) {
  return queryOptions({
    queryKey: queryKeys.worksets.draft(worksetId ?? ''),
    enabled: Boolean(worksetId),
    staleTime: Infinity,
    queryFn: ({ signal }: { signal?: AbortSignal }) => api.getWorksetDraft(worksetId as string, signal),
  })
}

// Immutable revision history summaries, one bounded page at a time (keyset on
// revision_index). Refreshed on generation terminals; "load earlier" pages
// extend the same cache entry.
export function worksetRevisionListInfiniteQueryOptions(api: ApiClientContract, worksetId: string | null | undefined) {
  return infiniteQueryOptions({
    queryKey: queryKeys.worksets.revisionList(worksetId ?? ''),
    enabled: Boolean(worksetId),
    staleTime: Infinity,
    initialPageParam: undefined as number | undefined,
    queryFn: ({ pageParam, signal }: { pageParam: number | undefined; signal?: AbortSignal }) =>
      api.listRevisions(worksetId as string, REVISION_PAGE_SIZE, pageParam, signal),
    getNextPageParam: (lastPage: RevisionListResponse) =>
      lastPage.next_before_index ? lastPage.next_before_index : undefined,
  })
}

// Immutable revision detail (roots + component ownership + workflow outcome).
// staleTime is finite: stale/unavailable validation is derived at read time
// from the live inventory, so a revisit should see fresher validation state.
export function worksetRevisionDetailQueryOptions(
  api: ApiClientContract,
  worksetId: string | null | undefined,
  planId: string | null | undefined,
) {
  return queryOptions({
    queryKey: queryKeys.worksets.revision(worksetId ?? '', planId ?? ''),
    enabled: Boolean(worksetId && planId),
    staleTime: 60_000,
    queryFn: ({ signal }: { signal?: AbortSignal }) =>
      api.getRevision(worksetId as string, planId as string, signal),
  })
}

// The three global policy slots. Global templates: loaded once, refreshed
// after a slot save.
export function policySlotListQueryOptions(api: ApiClientContract) {
  return queryOptions({
    queryKey: queryKeys.policySlots.list(),
    staleTime: Infinity,
    queryFn: ({ signal }: { signal?: AbortSignal }) => api.listPolicySlots(signal),
  })
}

export function savePolicySlotMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (input: { slot: number; name: string; policy: ResolvedPolicy }) =>
      api.savePolicySlot(input.slot, { name: input.name, policy: input.policy }),
    onSuccess: () => {
      void refreshOrRemoveQueries(queryClient, queryKeys.policySlots.list())
    },
  }
}

// Global classifier tag library (defaults from config + custom from DB).
export function classifierTagLibraryQueryOptions(api: ApiClientContract) {
  return queryOptions({
    queryKey: queryKeys.classifierTags.list(),
    staleTime: Infinity,
    queryFn: ({ signal }: { signal?: AbortSignal }) => api.listClassifierTags(signal),
  })
}

export function addClassifierTagMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (tag: string) => api.addClassifierTag(tag),
    onSuccess: () => {
      void refreshOrRemoveQueries(queryClient, queryKeys.classifierTags.list())
    },
  }
}

export function deleteClassifierTagMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (id: number) => api.deleteClassifierTag(id),
    onSuccess: () => {
      void refreshOrRemoveQueries(queryClient, queryKeys.classifierTags.list())
    },
  }
}

// ==================== Mutations ====================

export function createWorksetMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (input: CreateWorksetInput & { idempotencyKey: string }) =>
      api.createWorkset(
        { library_id: input.library_id, title: input.title, folder_ids: input.folder_ids },
        input.idempotencyKey,
      ),
    onSuccess: (result: { workset: Workset; created: boolean }) => {
      queryClient.setQueryData(queryKeys.worksets.detail(result.workset.workset_id), result.workset)
      void refreshOrRemoveQueries(queryClient, queryKeys.worksets.feedPrefix())
    },
  }
}

export function saveDraftMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (input: { worksetId: string; workflow: WorkflowInput; ifMatchVersion: number }) =>
      api.saveWorksetDraft(input.worksetId, input.workflow, input.ifMatchVersion),
    onSuccess: (view: Workset, input: { worksetId: string; workflow: WorkflowInput }) => {
      // The save response is the fresh aggregate view: seed detail, refresh draft.
      queryClient.setQueryData(queryKeys.worksets.detail(input.worksetId), view)
      void refreshOrRemoveQueries(queryClient, queryKeys.worksets.draft(input.worksetId))
    },
  }
}

export function startGenerationMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (input: { worksetId: string; expectedDraftVersion?: number; idempotencyKey: string }) =>
      api.startGeneration(
        input.worksetId,
        { expected_draft_version: input.expectedDraftVersion },
        input.idempotencyKey,
      ),
    onSuccess: (result: { created: boolean }, input: { worksetId: string }) => {
      if (result.created) {
        // 202: the generation SSE composable owns follow-up sync from here.
        void refreshOrRemoveQueries(queryClient, queryKeys.worksets.detail(input.worksetId))
      } else {
        // created:false replay — input unchanged, current revision stands.
        void Promise.all([
          refreshOrRemoveQueries(queryClient, queryKeys.worksets.detail(input.worksetId)),
          refreshOrRemoveQueries(queryClient, queryKeys.worksets.revisionsPrefix(input.worksetId)),
        ])
      }
    },
  }
}

export function cancelGenerationMutationOptions(api: ApiClientContract) {
  return {
    mutationFn: (input: { worksetId: string; generationId: string }) =>
      api.cancelGeneration(input.worksetId, input.generationId),
  }
}

// Generation terminal (completed/failed/canceled/interrupted, or a transport
// end after events): refresh everything the outcome can have changed.
export async function syncAfterGenerationTerminal(
  queryClient: QueryClient,
  worksetId: string,
): Promise<void> {
  await Promise.all([
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.feedPrefix()),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.detail(worksetId)),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.draft(worksetId)),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.revisionsPrefix(worksetId)),
  ])
}

// Conservative sweep after an unclear transport end: the backend may have
// committed anything. Refreshes every cached workset data (small domain).
export async function sweepWorksetCaches(queryClient: QueryClient): Promise<void> {
  await refreshOrRemoveQueries(queryClient, queryKeys.worksets.all())
}

// Draft version conflict (409): reload draft + detail so the user can decide
// between loading the server version or overwriting it. Local form state is
// owned by the component and intentionally untouched.
export async function syncAfterDraftConflict(
  queryClient: QueryClient,
  worksetId: string,
): Promise<void> {
  await Promise.all([
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.detail(worksetId)),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.draft(worksetId)),
  ])
}