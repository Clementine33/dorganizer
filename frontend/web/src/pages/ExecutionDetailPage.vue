<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { Button } from '@/components/ui/button'
import ExecutionPanel from '@/features/worksets/ExecutionPanel.vue'
import { keptDecisions, revisionComponents } from '@/features/worksets/plan-readers'
import { useCurrentConversion } from '@/composables/use-operation-context'
import type { FileDecision } from '@/lib/api/types'

/**
 * The execution session of the revision in view
 * (`.../conversion/execution`). Like every other detail it renders in the
 * carrier the container chose — inline beside the list on wide, a modal sheet
 * on mid, the full page on narrow (R01, §7.3) — so progress and the result
 * never crowd the member list. The same page serves a live run (the store's
 * snapshot/progress) and the finished report (the session entry), and it
 * offers the cancel action itself so the run can be stopped from here.
 */
const route = useRoute()
const libraryId = computed(() => (route.params.libraryId as string) || '')
const {
  worksetId,
  execution,
  executionView,
  hasMoreComponents,
  loadMoreComponents,
  loadingMoreComponents,
  queries,
  workspace,
} = useCurrentConversion(libraryId)

/** Each component's kept files, as the executed revision concluded them. */
const kept = computed<Record<string, FileDecision[]>>(() => {
  const executed = workspace.revision.value
  if (!executed) return {}
  return Object.fromEntries(
    revisionComponents(executed).map((component) => [component.component_id, keptDecisions(component)]),
  )
})

const loading = computed(() => queries.operationQuery.isPending.value || queries.revisionQuery.isPending.value)
const running = computed(() => {
  const status = executionView.value?.status
  return status === 'queued' || status === 'running'
})

async function cancel() {
  const view = executionView.value
  if (!worksetId.value || !view) return
  await execution.cancel(worksetId.value, 'conversion', view.execution_id)
}
</script>

<template>
  <div class="flex min-h-0 flex-col">
    <p v-if="loading" class="p-3 text-xs text-[var(--text-muted)]">加载中…</p>
    <p
      v-else-if="!executionView"
      class="p-3 text-xs text-[var(--text-muted)]"
      data-testid="execution-empty"
    >
      当前版本没有执行记录。
    </p>
    <template v-else>
      <ExecutionPanel
        :view="executionView"
        :kept="kept"
        :members="workspace.workset.value?.members ?? []"
        :has-more="hasMoreComponents"
        :loading-more="loadingMoreComponents"
        @load-more="loadMoreComponents()"
      />
      <div v-if="running" class="flex flex-wrap items-center gap-2 px-3 py-2">
        <Button
          variant="destructive"
          size="sm"
          :disabled="execution.store.canceling"
          data-testid="execution-cancel"
          @click="cancel()"
        >
          {{ execution.store.canceling ? '取消中…' : '取消执行' }}
        </Button>
        <span class="text-[11px] text-[var(--text-muted)]">取消在当前组件的安全边界生效，已完成的结果保留。</span>
      </div>
    </template>
  </div>
</template>
