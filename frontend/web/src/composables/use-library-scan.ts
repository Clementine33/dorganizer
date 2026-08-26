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
// longer depends on materialized folders.
//
// Materialized folder rows are replaced in one transaction only when the scan
// completes, so any non-completed outcome — user cancel (abort or SSE),
// confirmed error, or a transport failure before the first event — leaves
// folders/trees unchanged and only library metadata needs refreshing. The one
// conservative case is a transport terminal after events: the backend may
// have committed before the stream died.
export async function syncAfterScan(
  queryClient: QueryClient,
  libraryId: string,
  status: ScanStatus,
  terminal: ScanTerminal | null,
  streamStarted = true,
): Promise<void> {
  // A cancelled scan never materializes (the backend replaces folders in one
  // transaction only on completion), so derived caches stay valid.
  if (status === 'cancelled') {
    await refreshLibraries(queryClient)
    return
  }
  // Completed scan, or a transport failure after events (may have committed):
  // refresh library metadata and the library's derived caches (active
  // folders/trees refetch, inactive entries are dropped).
  if (status === 'completed' || (terminal !== 'event' && streamStarted)) {
    await Promise.all([refreshLibraries(queryClient), refreshLibraryDerived(queryClient, libraryId)])
    return
  }
  // Confirmed cancel/error over SSE, or a transport terminal before any event:
  // nothing was committed — only scan metadata changed.
  await refreshLibraries(queryClient)
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
    // The store may have been reset or repurposed while this stream was in
    // flight (e.g. switchLibrary cleared a terminal, a newer scan started).
    // Only synchronize when this call's library still owns the store state.
    if (scan.libraryId !== libraryId) return
    await syncAfterScan(queryClient, libraryId, scan.status, scan.terminal, scan.receivedEvent)
  }

  function cancel(): void {
    scan.cancel()
  }

  return { start, cancel }
}