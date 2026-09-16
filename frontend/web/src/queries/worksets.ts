import { infiniteQueryOptions, queryOptions, type QueryClient } from '@tanstack/vue-query'
import type {
  ApiClientContract,
  ExecutionView,
  Operation,
  CreateWorksetInput,
  ListWorksetsParams,
  OperationDraftDocument,
  OperationType,
  ResolvedPolicy,
  RevisionListResponse,
  StartExecutionResponse,
  Workset,
  WorksetListResponse,
} from '@/lib/api/types'
import { refreshOrRemoveQueries } from './cache-sync'
import { queryKeys } from './query-keys'

// Workset server-state coordination. All cache writes and invalidations for
// the workset domain live here — pages never touch the QueryClient directly.
//
// Every operation-scoped key carries (worksetId, operationType): a draft save
// on conversion can never refresh, invalidate or reseed another operation's
// state, and a workset rename touches only the metadata entries.

const WORKSET_PAGE_SIZE = 50
// Revision history renders in the workbench; a small page keeps the initial
// payload bounded for operations with many generations.
const REVISION_PAGE_SIZE = 10

export function worksetListInfiniteQueryOptions(
  api: ApiClientContract,
  params: { libraryId?: string | null } = {},
) {
  return infiniteQueryOptions({
    queryKey: [...queryKeys.worksets.list(params.libraryId ?? null), 'infinite'],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }: { pageParam: string | undefined; signal?: AbortSignal }) => {
      const listParams: ListWorksetsParams = { limit: WORKSET_PAGE_SIZE }
      if (params.libraryId) listParams.library_id = params.libraryId
      if (pageParam) listParams.cursor = pageParam
      return api.listWorksets(listParams, signal)
    },
    getNextPageParam: (lastPage: WorksetListResponse) => lastPage.next_cursor || undefined,
    staleTime: Infinity,
  })
}

// Workset metadata view (title, members, one entry per operation).
export function worksetDetailQueryOptions(api: ApiClientContract, worksetId: string | null | undefined) {
  return queryOptions({
    queryKey: queryKeys.worksets.detail(worksetId ?? ''),
    enabled: Boolean(worksetId),
    staleTime: Infinity,
    queryFn: ({ signal }: { signal?: AbortSignal }) => api.getWorkset(worksetId as string, signal),
  })
}

// One operation's aggregate: version, planning state, current revision and
// session state. This — not the workset — is what the workbench reads.
export function operationQueryOptions(
  api: ApiClientContract,
  worksetId: string | null | undefined,
  operation: OperationType,
) {
  return queryOptions({
    queryKey: queryKeys.worksets.operation(worksetId ?? '', operation),
    enabled: Boolean(worksetId),
    staleTime: Infinity,
    queryFn: ({ signal }: { signal?: AbortSignal }) => api.getOperation(worksetId as string, operation, signal),
  })
}

// The persisted sparse draft document plus the operation version that the next
// save must echo as If-Match.
export function operationDraftQueryOptions(
  api: ApiClientContract,
  worksetId: string | null | undefined,
  operation: OperationType,
) {
  return queryOptions({
    queryKey: queryKeys.worksets.draft(worksetId ?? '', operation),
    enabled: Boolean(worksetId),
    staleTime: Infinity,
    queryFn: ({ signal }: { signal?: AbortSignal }) =>
      api.getOperationDraft(worksetId as string, operation, signal),
  })
}

// Immutable revision history summaries, one bounded page at a time (keyset on
// revision_index).
export function operationRevisionListInfiniteQueryOptions(
  api: ApiClientContract,
  worksetId: string | null | undefined,
  operation: OperationType,
) {
  return infiniteQueryOptions({
    queryKey: queryKeys.worksets.revisionList(worksetId ?? '', operation),
    enabled: Boolean(worksetId),
    staleTime: Infinity,
    initialPageParam: undefined as number | undefined,
    queryFn: ({ pageParam, signal }: { pageParam: number | undefined; signal?: AbortSignal }) =>
      api.listRevisions(worksetId as string, operation, REVISION_PAGE_SIZE, pageParam, signal),
    getNextPageParam: (lastPage: RevisionListResponse) =>
      lastPage.next_before_index ? lastPage.next_before_index : undefined,
  })
}

// Immutable revision detail (frozen members with sources, roots, component
// ownership, confirmation). staleTime is finite: validation is derived at read
// time from the live inventory, so a revisit should see fresher state.
export function operationRevisionDetailQueryOptions(
  api: ApiClientContract,
  worksetId: string | null | undefined,
  operation: OperationType,
  planId: string | null | undefined,
) {
  return queryOptions({
    queryKey: queryKeys.worksets.revision(worksetId ?? '', operation, planId ?? ''),
    enabled: Boolean(worksetId && planId),
    staleTime: 60_000,
    queryFn: ({ signal }: { signal?: AbortSignal }) =>
      api.getRevision(worksetId as string, operation, planId as string, signal),
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
      void refreshOrRemoveQueries(queryClient, queryKeys.worksets.listPrefix())
    },
  }
}

/**
 * Save the full sparse draft document. The response is the fresh operation
 * view, so it seeds the operation entry directly; the draft entry is refreshed
 * because the save may have normalized the document.
 */
export function saveOperationDraftMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (input: {
      worksetId: string
      operation: OperationType
      document: OperationDraftDocument
      ifMatchVersion: number
    }) =>
      api.saveOperationDraft(input.worksetId, input.operation, input.document, input.ifMatchVersion),
    onSuccess: (view: Operation, input: { worksetId: string; operation: OperationType }) => {
      queryClient.setQueryData(queryKeys.worksets.operation(input.worksetId, input.operation), view)
      void Promise.all([
        refreshOrRemoveQueries(queryClient, queryKeys.worksets.draft(input.worksetId, input.operation)),
        refreshOrRemoveQueries(queryClient, queryKeys.worksets.detail(input.worksetId)),
      ])
    },
  }
}

export function startGenerationMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (input: {
      worksetId: string
      operation: OperationType
      ifMatchVersion: number
      idempotencyKey: string
    }) => api.startGeneration(input.worksetId, input.operation, input.ifMatchVersion, input.idempotencyKey),
    onSuccess: (result: { created: boolean }, input: { worksetId: string; operation: OperationType }) => {
      if (result.created) {
        // 202: the generation SSE composable owns follow-up sync from here.
        void refreshOrRemoveQueries(queryClient, queryKeys.worksets.operation(input.worksetId, input.operation))
      } else {
        // created:false replay — input unchanged, current revision stands.
        void Promise.all([
          refreshOrRemoveQueries(queryClient, queryKeys.worksets.operation(input.worksetId, input.operation)),
          refreshOrRemoveQueries(queryClient, queryKeys.worksets.revisionsPrefix(input.worksetId, input.operation)),
        ])
      }
    },
  }
}

export function cancelGenerationMutationOptions(api: ApiClientContract) {
  return {
    mutationFn: (input: { worksetId: string; operation: OperationType; generationId: string }) =>
      api.cancelGeneration(input.worksetId, input.operation, input.generationId),
  }
}

// ==================== Execution sessions ====================

// One session's authoritative report. The SSE stream seeds this same entry
// with snapshots and progress; the GET stays the fallback (a missed terminal
// event) and the way to pick up the full per-component report after a terminal
// event, because progress events carry counts only.
export function executionQueryOptions(
  api: ApiClientContract,
  worksetId: string | null | undefined,
  operation: OperationType,
  executionId: string | null | undefined,
) {
  return queryOptions({
    queryKey: queryKeys.worksets.execution(worksetId ?? '', operation, executionId ?? ''),
    enabled: Boolean(worksetId && executionId),
    staleTime: 0,
    queryFn: ({ signal }: { signal?: AbortSignal }) =>
      api.getExecution(worksetId as string, operation, executionId as string, signal),
  })
}

/**
 * Enqueue the execution of the current revision. The server re-checks every
 * eligibility fact inside the request; a refusal is explained, never retried
 * with a fresh key (a different key would be a second run attempt).
 */
export function startExecutionMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (input: {
      worksetId: string
      operation: OperationType
      planId: string
      ifMatchVersion: number
      idempotencyKey: string
    }) =>
      api.startExecution(input.worksetId, input.operation, input.planId, {
        ifMatchVersion: input.ifMatchVersion,
        idempotencyKey: input.idempotencyKey,
      }),
    onSuccess: (
      result: StartExecutionResponse,
      input: { worksetId: string; operation: OperationType; planId: string },
    ) => {
      queryClient.setQueryData(
        queryKeys.worksets.execution(input.worksetId, input.operation, result.execution.execution_id),
        result.execution,
      )
      void Promise.all([
        refreshOrRemoveQueries(queryClient, queryKeys.worksets.operation(input.worksetId, input.operation)),
        refreshOrRemoveQueries(
          queryClient,
          queryKeys.worksets.revision(input.worksetId, input.operation, input.planId),
        ),
      ])
    },
  }
}

// The cancel response is the authoritative outcome (the server answers with
// the session either way): it seeds the report entry so a client whose SSE
// died still ends on the real state.
export function cancelExecutionMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (input: { worksetId: string; operation: OperationType; executionId: string }) =>
      api.cancelExecution(input.worksetId, input.operation, input.executionId),
    onSuccess: (view: ExecutionView) => {
      queryClient.setQueryData(
        queryKeys.worksets.execution(view.workset_id, view.operation_type, view.execution_id),
        view,
      )
    },
  }
}

// Execution terminal: refresh everything the outcome can have changed. The
// inventory sync moved entries under the owning library (folder trees read the
// live inventory; list counts change on the next scan), and the operation and
// revision views carry the session refs.
export async function syncAfterExecutionTerminal(
  queryClient: QueryClient,
  worksetId: string,
  operation: OperationType,
): Promise<void> {
  const detail = queryClient.getQueryData<Workset>(queryKeys.worksets.detail(worksetId))
  const libraryId = detail?.library?.library_id ?? null
  await Promise.all([
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.listPrefix()),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.detail(worksetId)),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.operation(worksetId, operation)),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.revisionsPrefix(worksetId, operation)),
    // Progress events carry counts only; the refreshed GET brings the full
    // per-component report (committed/removed/recovery) into the open panel.
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.executionsPrefix(worksetId, operation)),
    ...(libraryId
      ? [
          refreshOrRemoveQueries(queryClient, queryKeys.libraries.foldersPrefix(libraryId)),
          refreshOrRemoveQueries(queryClient, queryKeys.libraries.treesPrefix(libraryId)),
        ]
      : []),
  ])
}

// A refused start (an eligibility conflict or a stale version) is answered
// with fresh state: the operation and its revisions are re-read so the UI can
// explain against the truth. The request is never retried with a fresh key —
// that would be a second run attempt against the same revision.
export async function syncAfterExecutionRefusal(
  queryClient: QueryClient,
  worksetId: string,
  operation: OperationType,
): Promise<void> {
  await Promise.all([
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.operation(worksetId, operation)),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.revisionsPrefix(worksetId, operation)),
  ])
}

// Conservative sweep after an unclear transport end: an execution writes to
// disk whenever it ran, so both the workset view and the library inventory may
// have moved.
export async function sweepAfterExecution(queryClient: QueryClient): Promise<void> {
  await Promise.all([
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.all()),
    refreshOrRemoveQueries(queryClient, queryKeys.libraries.all()),
  ])
}

// Generation terminal (completed/failed/canceled/interrupted, or a transport
// end after events): refresh everything the outcome can have changed.
export async function syncAfterGenerationTerminal(
  queryClient: QueryClient,
  worksetId: string,
  operation: OperationType,
): Promise<void> {
  await Promise.all([
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.listPrefix()),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.detail(worksetId)),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.operation(worksetId, operation)),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.draft(worksetId, operation)),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.revisionsPrefix(worksetId, operation)),
  ])
}

// Conservative sweep after an unclear transport end: the backend may have
// committed anything. Refreshes every cached workset resource (small domain).
export async function sweepWorksetCaches(queryClient: QueryClient): Promise<void> {
  await refreshOrRemoveQueries(queryClient, queryKeys.worksets.all())
}

// Draft save conflict (409): reload the operation and its draft so the user
// can decide between loading the server version or overwriting it. Local form
// state belongs to the edit session and is intentionally untouched.
export async function syncAfterDraftConflict(
  queryClient: QueryClient,
  worksetId: string,
  operation: OperationType,
): Promise<void> {
  await Promise.all([
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.operation(worksetId, operation)),
    refreshOrRemoveQueries(queryClient, queryKeys.worksets.draft(worksetId, operation)),
  ])
}
