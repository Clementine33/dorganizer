import { useQueryClient, type QueryClient } from '@tanstack/vue-query'
import { useApiClient } from '@/lib/api/client'
import { refreshOrRemoveQueries } from '@/queries/cache-sync'
import { queryKeys } from '@/queries/query-keys'
import { useScanStore, type ScanStatus, type ScanTerminal } from '@/stores/scan'

async function refreshLibraries(queryClient: QueryClient): Promise<void> {
  await queryClient.invalidateQueries({ queryKey: queryKeys.libraries.list() })
}

async function refreshLibraryDerived(queryClient: QueryClient, libraryId: string): Promise<void> {
  await refreshOrRemoveQueries(queryClient, queryKeys.libraries.foldersPrefix(libraryId))
  await refreshOrRemoveQueries(queryClient, queryKeys.libraries.treesPrefix(libraryId))
}

// Maps the terminal scan outcome to the exact cache synchronization it
// requires. Plan lists are deliberately never touched: plan membership no
// longer depends on materialized folders.
export async function syncAfterScan(
  queryClient: QueryClient,
  libraryId: string,
  status: ScanStatus,
  terminal: ScanTerminal | null,
): Promise<void> {
  if (status === 'completed') {
    // Confirmed commit: refresh library metadata and the library's derived
    // caches (active folders/trees refetch, inactive entries are dropped).
    await refreshLibraries(queryClient)
    await refreshLibraryDerived(queryClient, libraryId)
    return
  }
  // The backend confirmed cancel/error over SSE: only library scan metadata
  // changed; folders/trees stay valid.
  if (terminal === 'event') {
    await refreshLibraries(queryClient)
    return
  }
  // Transport failure or premature stream end: the backend may or may not
  // have committed, so conservatively refresh the derived caches too.
  await refreshLibraries(queryClient)
  await refreshLibraryDerived(queryClient, libraryId)
}

// Single entry point for scan orchestration: pages call start/cancel/reset
// and never assemble invalidation themselves. The scan SSE lifecycle stays in
// the scan Pinia store; this module owns cache coordination.
export function useLibraryScan() {
  const api = useApiClient()
  const queryClient = useQueryClient()
  const scan = useScanStore()

  async function start(libraryId: string): Promise<void> {
    await scan.start(libraryId, api)
    // scan.start refuses to run while another scan is active and leaves
    // status 'scanning'. In that case this call did not begin a scan, so
    // synchronizing against the other scan's in-flight state would be wrong.
    if (scan.status === 'scanning') return
    await syncAfterScan(queryClient, libraryId, scan.status, scan.terminal)
  }

  function cancel(): void {
    scan.cancel()
  }

  function reset(): void {
    scan.reset()
  }

  return { start, cancel, reset }
}