import { type QueryClient } from '@tanstack/vue-query'

// Synchronizes a family of queries under one key prefix after a mutation or
// scan terminal event:
//   - active (mounted) members are invalidated and refetch immediately;
//   - inactive members are removed, so the next mount cold-fetches.
// This avoids the "stale inactive query + refetchOnMount: false" trap where
// an invalidated query would otherwise sit in the cache forever without ever
// refreshing.
export async function refreshOrRemoveQueries(
  queryClient: QueryClient,
  prefix: readonly unknown[],
): Promise<void> {
  await queryClient.invalidateQueries({ queryKey: prefix })
  const inactive = queryClient
    .getQueryCache()
    .findAll({ queryKey: prefix })
    .filter((query) => query.getObserversCount() === 0)
  for (const query of inactive) {
    queryClient.removeQueries({ queryKey: query.queryKey })
  }
}