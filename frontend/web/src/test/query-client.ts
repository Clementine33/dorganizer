import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'

// Each unit test must mount with its own QueryClient so cached data never
// leaks between tests. Retries are disabled for deterministic errors and
// gcTime is infinite to avoid live GC timers after the test finishes.
// Test behavior matches the application's explicit-sync model.
export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: Infinity,
        gcTime: Infinity,
        retry: false,
        refetchOnMount: false,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
      },
      mutations: {
        retry: false,
      },
    },
  })
}

// Mountable plugin tuple for @vue/test-utils `global.plugins`.
export function installTestQueryPlugin(): [typeof VueQueryPlugin, { queryClient: QueryClient }] {
  return [VueQueryPlugin, { queryClient: createTestQueryClient() }]
}