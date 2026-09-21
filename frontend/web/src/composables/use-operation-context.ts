import { computed, ref, watch, type Ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useApiClient } from '@/lib/api/client'
import { CONVERSION, type ExecutionComponent, type OperationType } from '@/lib/api/types'
import {
  currentRecordQueryOptions,
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

/** How many components one page of a large run reads. */
const EXECUTION_COMPONENT_PAGE = 200

/**
 * The library's current conversion record and its operation context.
 *
 * Carrier pages (settings, member edit, batch edit, execution) are entered
 * from the conversion URL, which names a library — never a record: the record
 * is looked up here, and every carrier therefore follows the same identity the
 * workbench does.
 */
export function useCurrentConversion(libraryId: Ref<string>) {
  const api = useApiClient()
  const recordQuery = useQuery(() => currentRecordQueryOptions(api, libraryId.value, CONVERSION))
  const record = computed(() => recordQuery.data.value?.workset ?? null)
  const worksetId = computed(() => record.value?.workset_id ?? null)
  const context = useOperationContext(worksetId, CONVERSION)
  return { record, worksetId, ...context }
}

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
  // A large run is read a page at a time: these are the components the client
  // asked for on top of the ones the read or the stream carried.
  const extraComponents = ref<ExecutionComponent[]>([])
  const loadingMore = ref(false)
  watch(executionRef, () => {
    extraComponents.value = []
  })

  // While the stream is live its view is the freshest truth — the snapshot,
  // then each component result as it lands. A component the stream never sent
  // (attached late, or beyond the components read so far) is taken from the
  // detail query, which the snapshot seeds; once the stream has ended that
  // query owns everything. Components fetched by index are merged on top of
  // whichever of the two is current.
  const executionView = computed(() => {
    const store = execution.store
    const detail = executionQuery.data.value
    const base = store.status === 'streaming' && store.view ? store.view : (detail ?? store.view)
    if (!base) return base
    const known = new Set(base.components.map((component) => component.component_index))
    const missing = extraComponents.value.filter(
      (component) => !known.has(component.component_index),
    )
    if (missing.length === 0) return base
    return {
      ...base,
      components: [...base.components, ...missing].sort(
        (a, b) => a.component_index - b.component_index,
      ),
    }
  })

  const hasMoreComponents = computed(
    () => (executionView.value?.components.length ?? 0) < (executionView.value?.total_components ?? 0),
  )

  /**
   * Reads the components after the last one held, for a run too large to render
   * in one read. The stream keeps filling in components that commit next; this
   * only reaches back for what a bounded read left out.
   */
  async function loadMoreComponents() {
    const view = executionView.value
    if (!view || loadingMore.value) return
    const from = view.components.reduce(
      (last, component) => Math.max(last, component.component_index + 1),
      0,
    )
    loadingMore.value = true
    try {
      const page = await api.getExecution(worksetId.value ?? '', operation, view.execution_id, undefined, {
        from,
        limit: EXECUTION_COMPONENT_PAGE,
      })
      extraComponents.value = [...extraComponents.value, ...page.components]
    } finally {
      loadingMore.value = false
    }
  }

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
    hasMoreComponents,
    loadMoreComponents,
    loadingMoreComponents: loadingMore,
    editor,
    applySession,
    startGeneration,
  }
}
