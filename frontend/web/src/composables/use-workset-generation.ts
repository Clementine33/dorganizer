import { useQueryClient, type QueryClient } from '@tanstack/vue-query'
import { useApiClient } from '@/lib/api/client'
import type { OperationType } from '@/lib/api/types'
import {
  startGenerationMutationOptions,
  cancelGenerationMutationOptions,
  syncAfterGenerationTerminal,
  sweepWorksetCaches,
} from '@/queries/worksets'
import { useMutation } from '@tanstack/vue-query'
import { useWorksetGenerationStore } from '@/stores/workset-generation'

// Single entry point for planning-session orchestration: pages call
// start/cancel/attach and never assemble cache invalidation themselves.
// The SSE lifecycle lives in the workset-generation Pinia store (the same
// exception as the scan store); this composable owns Vue Query coordination.

async function syncOnTerminal(
  queryClient: QueryClient,
  worksetId: string,
  operation: OperationType,
  terminal: string | null,
  streamStarted: boolean,
): Promise<void> {
  if (terminal === 'event') {
    // Confirmed terminal: refresh everything the outcome can have changed.
    await syncAfterGenerationTerminal(queryClient, worksetId, operation)
    return
  }
  if (streamStarted) {
    // Transport failure after events: the backend may have committed.
    await sweepWorksetCaches(queryClient)
    return
  }
  // Transport failure before any event: nothing was committed.
}

export function useWorksetGeneration() {
  const api = useApiClient()
  const queryClient = useQueryClient()
  const store = useWorksetGenerationStore()

  const startMutation = useMutation(startGenerationMutationOptions(api, queryClient))
  const cancelMutation = useMutation(cancelGenerationMutationOptions(api))

  type StartInput = {
    worksetId: string
    operation: OperationType
    ifMatchVersion: number
    idempotencyKey: string
  }

  // Starts a generation and attaches the SSE stream on a fresh 202. Resolves
  // with the raw response so the caller can distinguish created:false replays.
  async function start(input: StartInput) {
    const result = await startMutation.mutateAsync(input)
    if (result.created) {
      attach(input.worksetId, input.operation, result.generation.generation_id)
    }
    return result
  }

  // Attaches the SSE stream for an already-running session (e.g. after a page
  // reload while the backend session is still active). A refused attach
  // (another session is streaming in the singleton store) resolves without
  // synchronizing — there is no terminal to react to, and sweeping caches
  // here would feed the detail-refetch → re-attach loop.
  function attach(worksetId: string, operation: OperationType, generationId: string): void {
    void store.attach(worksetId, operation, generationId, api).then((attached) => {
      if (!attached) return
      // The attach promise settles when the stream ends. Guard against a
      // superseded session before synchronizing against its outcome.
      if (store.generationId !== generationId) return
      return syncOnTerminal(queryClient, worksetId, operation, store.terminal, store.receivedEvent)
    })
  }

  // Explicit user cancel: abort the SSE (scoped to this session), then POST
  // cancel. The canceled POST response is the authoritative outcome even when
  // the stream dies first via the abort — the terminal sync below runs in
  // both cases, exactly once.
  async function cancel(worksetId: string, operation: OperationType, generationId: string): Promise<void> {
    store.cancel(worksetId, generationId)
    try {
      await cancelMutation.mutateAsync({ worksetId, operation, generationId })
    } finally {
      await syncAfterGenerationTerminal(queryClient, worksetId, operation)
    }
  }

  function stop(): void {
    store.reset()
  }

  return { start, attach, cancel, stop, store, startMutation, cancelMutation }
}
