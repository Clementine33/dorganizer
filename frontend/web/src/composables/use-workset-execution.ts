import { useQueryClient, type QueryClient, useMutation } from '@tanstack/vue-query'
import { useApiClient } from '@/lib/api/client'
import type { OperationType } from '@/lib/api/types'
import {
  cancelExecutionMutationOptions,
  startExecutionMutationOptions,
  sweepAfterExecution,
  syncAfterExecutionRefusal,
  syncAfterExecutionTerminal,
} from '@/queries/worksets'
import { useWorksetExecutionStore } from '@/stores/workset-execution'

// Single entry point for execution-session orchestration: pages call
// start/cancel/attach and never assemble cache invalidation themselves. The
// SSE lifecycle lives in the workset-execution Pinia store (the same
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
    await syncAfterExecutionTerminal(queryClient, worksetId, operation)
    return
  }
  if (streamStarted) {
    // Transport failure after events: the backend may have written files.
    await sweepAfterExecution(queryClient)
  }
  // Transport failure before any event: nothing was committed.
}

export function useWorksetExecution() {
  const api = useApiClient()
  const queryClient = useQueryClient()
  const store = useWorksetExecutionStore()

  const startMutation = useMutation(startExecutionMutationOptions(api, queryClient))
  const cancelMutation = useMutation(cancelExecutionMutationOptions(api, queryClient))

  type StartInput = {
    worksetId: string
    operation: OperationType
    planId: string
    ifMatchVersion: number
    idempotencyKey: string
    /** The folders this run covers; absent runs the whole revision. */
    folderPaths?: string[]
  }

  // Starts a session and attaches the SSE stream on a fresh 202. Resolves with
  // the raw response so the caller can distinguish a key replay (200). A
  // refused start re-reads the server state before surfacing the error.
  async function start(input: StartInput) {
    try {
      const result = await startMutation.mutateAsync(input)
      if (result.created) {
        attach(input.worksetId, input.operation, result.execution.execution_id)
      }
      return result
    } catch (error) {
      await syncAfterExecutionRefusal(queryClient, input.worksetId, input.operation)
      throw error
    }
  }

  // Attaches the SSE stream for an already-running session — after a page
  // reload, or when the workspace mounts while the backend session is active.
  // A refused attach (another session is streaming in the singleton store)
  // resolves without synchronizing: there is no terminal to react to.
  function attach(worksetId: string, operation: OperationType, executionId: string): void {
    void store.attach(worksetId, operation, executionId, api).then((attached) => {
      if (!attached) return
      // The attach promise settles when the stream ends. Guard against a
      // superseded session before synchronizing against its outcome.
      if (store.executionId !== executionId) return
      return syncOnTerminal(queryClient, worksetId, operation, store.terminal, store.receivedEvent)
    })
  }

  // Explicit user cancel. The stream stays attached: a running session stops
  // cooperatively at the worker's next safe boundary, and the terminal event
  // that follows is what the UI waits for. The POST response is the fallback
  // outcome when the stream is already gone.
  async function cancel(worksetId: string, operation: OperationType, executionId: string): Promise<void> {
    store.requestCancel(worksetId, executionId)
    try {
      await cancelMutation.mutateAsync({ worksetId, operation, executionId })
    } finally {
      await syncAfterExecutionTerminal(queryClient, worksetId, operation)
    }
  }

  return { start, attach, cancel, store, startMutation, cancelMutation }
}
