import { type QueryClient } from '@tanstack/vue-query'
import type { ApiClientContract, CreatePlanInput, PlanInfo, PlanResponse } from '@/lib/api/types'
import { refreshOrRemoveQueries } from './cache-sync'
import { queryKeys } from './query-keys'

// Finds plan list metadata for a plan ID from any cached scoped list, without
// issuing a request. Used by the review page for the status pill; returns
// null when no cached list contains the plan.
export function findCachedPlanInfo(queryClient: QueryClient, planId: string): PlanInfo | null {
  for (const [, data] of queryClient.getQueriesData<PlanInfo[]>({ queryKey: queryKeys.plans.lists() })) {
    const found = data?.find((item) => item.plan_id === planId)
    if (found) return found
  }
  return null
}

// Plan lists are keyed by library and limit; the scoped list never derives
// membership from folders or root paths — plan ownership is persisted.
export function planListQueryOptions(api: ApiClientContract, libraryId: string | null, limit = 100) {
  return {
    queryKey: queryKeys.plans.list(libraryId ?? '', limit),
    enabled: Boolean(libraryId),
    queryFn: ({ signal }: { signal?: AbortSignal }) => {
      if (!libraryId) throw new Error('plan list query requires a library')
      return api.listPlans(libraryId, limit, signal)
    },
  }
}

// Durable plan detail: cold links fetch GET /plans/:id. staleTime is infinite
// because the payload is an immutable snapshot the backend does not mutate;
// list invalidation is handled by the create mutation.
export function planDetailQueryOptions(api: ApiClientContract, planId: string | null) {
  return {
    queryKey: queryKeys.plans.detail(planId ?? ''),
    enabled: Boolean(planId),
    staleTime: Infinity,
    queryFn: ({ signal }: { signal?: AbortSignal }) => {
      if (!planId) throw new Error('plan detail query requires a plan id')
      return api.getPlan(planId, signal)
    },
  }
}

// Plan creation owns the review handoff: the full response is written to the
// durable detail cache before navigation (so the review renders immediately
// without a redundant GET), and only the creating library's plan list is
// invalidated. Plan lists are never invalidated by scans or root updates.
export function createPlanMutationOptions(api: ApiClientContract, queryClient: QueryClient) {
  return {
    mutationFn: (input: CreatePlanInput) => api.createPlan(input),
    onSuccess: (plan: PlanResponse, input: CreatePlanInput, _context: unknown) => {
      queryClient.setQueryData(queryKeys.plans.detail(plan.plan_id), plan)
      // Only the creating library's scoped list is affected: active observers
      // refetch, inactive cached lists are dropped (not refetched in the
      // background for every limit variant).
      void refreshOrRemoveQueries(queryClient, queryKeys.plans.libraryPrefix(input.library_id))
    },
  }
}