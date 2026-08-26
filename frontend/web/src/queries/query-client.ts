import { QueryClient } from '@tanstack/vue-query'

// Cached data has no time-based refresh: freshness changes only through
// explicit mutation/scan synchronization in the domain query modules.
export const REFETCH_ON_MOUNT = false
export const REFETCH_ON_WINDOW_FOCUS = false
export const REFETCH_ON_RECONNECT = false

// Inactive caches are retained for a bounded window; large trees use a
// shorter TTL via per-query override so a long session cannot grow unbounded.
export const DEFAULT_GC_TIME = 30 * 60 * 1000
export const TREE_GC_TIME = 10 * 60 * 1000

export function createAppQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: Infinity,
        gcTime: DEFAULT_GC_TIME,
        retry: false,
        refetchOnMount: REFETCH_ON_MOUNT,
        refetchOnWindowFocus: REFETCH_ON_WINDOW_FOCUS,
        refetchOnReconnect: REFETCH_ON_RECONNECT,
      },
      mutations: {
        retry: false,
      },
    },
  })
}