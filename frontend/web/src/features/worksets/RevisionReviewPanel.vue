<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { AlertTriangle } from '@lucide/vue'
import { useApiClient } from '@/lib/api/client'
import type { ComponentOutcome, Workset } from '@/lib/api/types'
import { worksetRevisionDetailQueryOptions } from '@/queries/worksets'
import AlbumBatchList from './AlbumBatchList.vue'
import ComponentInspector from './ComponentInspector.vue'
import { useWorksetUiStore } from '@/stores/workset-ui'
import { formatWorksetTime, summaryReasonLabel } from './workset-status'

// Review stage: stable batch grouping via the revision's component_roots
// ownership table + per-batch inspection. History rows switch metadata only
// (full historical read-back is a later phase).

const props = defineProps<{
  workset: Workset
  revisionList: { plan_id: string; revision_index: number; created_at: string; status: string; summary_reason: string; validation_state: string; stale: boolean | null; blocked_count: number }[]
}>()

const api = useApiClient()
const ui = useWorksetUiStore()

// History metadata selection; 0/null = current revision.
const selectedHistoryIndex = computed(() => ui.historyIndex ?? 0)

const currentPlanId = computed(() => {
  if (selectedHistoryIndex.value > 0) {
    const row = props.revisionList[selectedHistoryIndex.value]
    return row?.plan_id ?? null
  }
  return props.workset.current_revision?.plan_id ?? null
})

const revisionQuery = useQuery(
  computed(() => worksetRevisionDetailQueryOptions(api, props.workset.workset_id, currentPlanId.value)),
)
const revision = computed(() => revisionQuery.data.value ?? null)
const revisionError = computed(() => revisionQuery.error.value as Error | null)

const step = computed(() => revision.value?.workflow.steps[0] ?? null)

const componentsByRoot = computed(() => {
  const byRoot = new Map<number, ComponentOutcome[]>()
  if (!revision.value) return byRoot
  const byId = new Map<string, ComponentOutcome>()
  for (const s of revision.value.workflow.steps) {
    for (const c of s.components) byId.set(c.component_id, c)
  }
  for (const ref of revision.value.component_roots) {
    const comp = byId.get(ref.component_id)
    if (!comp) continue
    const list = byRoot.get(ref.root_index) ?? []
    list.push(comp)
    byRoot.set(ref.root_index, list)
  }
  return byRoot
})

const summary = computed(() => step.value?.summary ?? null)

const isHistorical = computed(() => selectedHistoryIndex.value > 0)
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col overflow-hidden" data-testid="revision-review">
    <p v-if="isHistorical" class="shrink-0 border-b border-amber-500/40 bg-amber-500/10 px-5 py-2 text-[11px] text-amber-700 dark:text-amber-400" data-testid="history-readback">
      历史版本回看 · 元信息已切换，组件明细仍为当前快照
    </p>

    <div v-if="revisionError" class="border-b border-destructive/30 bg-destructive/10 px-5 py-3 text-xs text-destructive" data-testid="revision-error">
      <AlertTriangle class="inline size-3.5" />
      无法读取计划版本明细，请重试。
    </div>

    <div v-else-if="!revision" class="grid flex-1 place-items-center text-xs text-muted-foreground" data-testid="revision-loading">
      正在读取计划版本…
    </div>

    <template v-else>
      <!-- Summary strip -->
      <div v-if="summary" class="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-5 py-2.5" data-testid="revision-summary">
        <span class="rounded-full bg-foreground/10 px-2 py-0.5 font-mono text-[10px] font-semibold" data-testid="revision-plan-id">
          {{ revision.plan_id }}
        </span>
        <span class="rounded-full bg-muted px-2 py-0.5 text-[10px] font-semibold">
          {{ summaryReasonLabel[summary.summary_reason] ?? summary.summary_reason }}
        </span>
        <span class="text-[10px] text-muted-foreground">
          {{ summary.component_count }} 组件 · {{ summary.blocked_count }} 阻塞 · {{ summary.operation_count }} 操作
        </span>
        <span class="ml-auto font-mono text-[10px] text-muted-foreground">{{ formatWorksetTime(revision.created_at) }}</span>
      </div>

      <!-- Roots validation strip -->
      <div v-if="revision.roots.length" class="flex shrink-0 flex-wrap gap-1.5 border-b border-border px-5 py-2" data-testid="revision-roots">
        <span
          v-for="root in revision.roots"
          :key="root.root_index"
          class="flex max-w-full items-center gap-1.5 rounded-full border px-2 py-0.5 text-[10px]"
          :class="root.root_status === 'ok' ? 'border-border text-muted-foreground' : 'border-destructive/40 text-destructive'"
          :data-testid="`revision-root-${root.root_index}`"
        >
          <span class="truncate font-mono">{{ root.root_path }}</span>
          <span v-if="root.stale" class="rounded-full bg-amber-500/15 px-1 font-semibold text-amber-600 dark:text-amber-400">stale</span>
          <span v-if="root.root_status !== 'ok'" class="font-semibold">{{ root.root_error_code ?? '缺失' }}</span>
          <span class="shrink-0">{{ root.entry_count }} 项</span>
        </span>
      </div>

      <!-- Batch list + inspector -->
      <div class="flex min-h-0 flex-1">
        <AlbumBatchList
          :workset="workset"
          :components-by-root="componentsByRoot"
          :revision-roots="revision.roots"
        />
        <ComponentInspector
          :components-by-root="componentsByRoot"
          :revision-roots="revision.roots"
        />
      </div>
    </template>
  </div>
</template>
