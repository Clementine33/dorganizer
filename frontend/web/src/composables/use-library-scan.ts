import { useQueryClient, type QueryClient } from '@tanstack/vue-query'
import { useApiClient } from '@/lib/api/client'
import { refreshOrRemoveQueries } from '@/queries/cache-sync'
import { queryKeys } from '@/queries/query-keys'
import { useScanStore, type ScanStatus, type ScanTerminal } from '@/stores/scan'

async function refreshLibraries(queryClient: QueryClient): Promise<void> {
  await queryClient.invalidateQueries({ queryKey: queryKeys.libraries.list() })
}

async function refreshLibraryDerived(queryClient: QueryClient, libraryId: string): Promise<void> {
  // Folders and trees are independent key families — refetching them
  // serially would add one full request round-trip per prefix after every
  // scan terminal. The pre-migration store parallelized the same refresh.
  await Promise.all([
    refreshOrRemoveQueries(queryClient, queryKeys.libraries.foldersPrefix(libraryId)),
    refreshOrRemoveQueries(queryClient, queryKeys.libraries.treesPrefix(libraryId)),
  ])
}

// Maps the terminal scan outcome to the exact cache synchronization it
// requires. Plan lists are deliberately never touched: plan membership no
// longer depends on materialized folders. `streamStarted` tells whether any
// SSE event arrived: a transport terminal without events means the scan never
// began (e.g. the POST /scans request was rejected), so folders/trees cannot
// have changed and the conservative derived refresh would be wasted work.
export async function syncAfterScan(
  queryClient: QueryClient,
  libraryId: string,
  status: ScanStatus,
  terminal: ScanTerminal | null,
  streamStarted = true,
): Promise<void> {
  if (status === 'completed') {
    // Confirmed commit: refresh library metadata and the library's derived
    // caches (active folders/trees refetch, inactive entries are dropped).
    await Promise.all([refreshLibraries(queryClient), refreshLibraryDerived(queryClient, libraryId)])
    return
  }
  // The backend confirmed cancel/error over SSE: only library scan metadata
  // changed; folders/trees stay valid.
  if (terminal === 'event') {
    await refreshLibraries(queryClient)
    return
  }
  // Transport failure or premature stream end: the backend may or may not
  // have committed, so conservatively refresh the derived caches too — unless
  // the stream never delivered an event, in which case nothing was committed.
  if (!streamStarted) {
    await refreshLibraries(queryClient)
    return
  }
  await Promise.all([refreshLibraries(queryClient), refreshLibraryDerived(queryClient, libraryId)])
}

// Single entry point for scan orchestration: pages call start/cancel and
// never assemble invalidation themselves. The scan SSE lifecycle stays in the
// scan Pinia store; this module owns cache coordination.
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
    await syncAfterScan(queryClient, libraryId, scan.status, scan.terminal, scan.receivedEvent)
  }

  function cancel(): void {
    scan.cancel()
  }

  return { start, cancel }
}