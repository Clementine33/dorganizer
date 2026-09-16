import { computed, watch, type Ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useApiClient } from '@/lib/api/client'
import { CONVERSION, type OperationType } from '@/lib/api/types'
import {
  executionQueryOptions,
  operationDraftQueryOptions,
  operationQueryOptions,
  operationRevisionDetailQueryOptions,
  saveOperationDraftMutationOptions,
  startGenerationMutationOptions,
  syncAfterDraftConflict,
} from '@/queries/worksets'
import { worksetDetailQueryOptions } from '@/queries/worksets'
import { useWorksetGeneration } from '@/composables/use-workset-generation'
import { useWorksetExecution } from '@/composables/use-workset-execution'
import { useWorksetEditorStore } from '@/stores/workset-editor'

/**
 * Shared server-state context for the conversion workspace: the workset
 * metadata, the operation aggregate, its sparse draft and — when a revision is
 * being reviewed — that revision's frozen detail.
 *
 * `revision` in the URL means read-only: nothing here creates an edit session
 * from a historical revision (E05).
 */
export function useOperationContext(
  worksetId: Ref<string | null>,
  operation: OperationType = CONVERSION,
  revisionPlanId: Ref<string | null> = computed(() => null),
) {
  const api = useApiClient()
  const queryClient = useQueryClient()
  const editor = useWorksetEditorStore()
  const generation = useWorksetGeneration()
  const execution = useWorksetExecution()

  const worksetQuery = useQuery(computed(() => worksetDetailQueryOptions(api, worksetId.value)))
  const operationQuery = useQuery(computed(() => operationQueryOptions(api, worksetId.value, operation)))
  const draftQuery = useQuery(computed(() => operationDraftQueryOptions(api, worksetId.value, operation)))
  // Without an explicit revision query the workbench reviews the operation's
  // CURRENT revision: the list conclusions and the member review both read the
  // same immutable snapshot, and only `?revision=` switches to history.
  const reviewedPlanId = computed(
    () => revisionPlanId.value ?? operationQuery.data.value?.current_revision?.plan_id ?? null,
  )
  const revisionQuery = useQuery(
    computed(() => operationRevisionDetailQueryOptions(api, worksetId.value, operation, reviewedPlanId.value)),
  )

  const workset = computed(() => worksetQuery.data.value ?? null)
  const operationView = computed(() => operationQuery.data.value ?? null)
  const draft = computed(() => draftQuery.data.value ?? null)
  const revision = computed(() => revisionQuery.data.value ?? null)

  // The session worth showing: the one currently running for this operation,
  // or the one that ran the revision in view (a revision executes at most
  // once, so its ref is final).
  const executionRef = computed<{ id: string; active: boolean } | null>(() => {
    const op = operationQuery.data.value
    if (op?.active_execution) return { id: op.active_execution.execution_id, active: true }
    const rev = revisionQuery.data.value
    if (rev?.execution) return { id: rev.execution.execution_id, active: false }
    return null
  })
  const executionQuery = useQuery(
    computed(() => executionQueryOptions(api, worksetId.value, operation, executionRef.value?.id ?? null)),
  )
  // While the stream is live its snapshot/progress is the freshest truth; once
  // it ends the detail GET owns the full per-component report.
  const executionView = computed(() => {
    const store = execution.store
    if (store.status === 'streaming' && store.view) return store.view
    return executionQuery.data.value ?? store.view
  })

  const saveMutation = useMutation(saveOperationDraftMutationOptions(api, queryClient))
  const startMutation = useMutation(startGenerationMutationOptions(api, queryClient))

  // Every fresh server draft reconciles the open edit session. Generation
  // publication advances the operation version without touching the draft, and
  // a session still echoing the pre-generation version would read as a
  // third-party conflict on its next save; identical content re-bases silently,
  // while a genuinely moved document under unapplied edits stays a conflict the
  // save reports.
  watch(draft, (server) => {
    if (!server || !worksetId.value) return
    editor.syncWithServer({ worksetId: worksetId.value, operation }, server)
  })

  /** Persists the session's pending document; the base becomes what was saved. */
  async function applySession(): Promise<boolean> {
    const session = editor.session
    const document = editor.pendingDocument
    if (!session || !document) return false
    editor.startApplying()
    try {
      const view = await saveMutation.mutateAsync({
        worksetId: session.worksetId,
        operation: session.operation,
        document,
        ifMatchVersion: session.baseVersion,
      })
      editor.markApplied(document, view.version)
      return true
    } catch (error) {
      const failure = error as { status?: number; code?: string; message?: string }
      if (failure.status === 409 && failure.code === 'VERSION_CONFLICT') {
        editor.markStale()
        await syncAfterDraftConflict(queryClient, session.worksetId, session.operation)
      } else {
        editor.failSave(failure)
      }
      return false
    }
  }

  async function startGeneration(): Promise<void> {
    const view = operationView.value
    if (!worksetId.value || !view) return
    await generation.start({
      worksetId: worksetId.value,
      operation,
      ifMatchVersion: view.version,
      idempotencyKey: crypto.randomUUID(),
    })
  }

  return {
    workspace: { workset, operation: operationView, draft, revision },
    queries: { worksetQuery, operationQuery, draftQuery, revisionQuery, executionQuery },
    mutations: { saveMutation, startMutation },
    generation,
    execution,
    executionView,
    editor,
    applySession,
    startGeneration,
  }
}
