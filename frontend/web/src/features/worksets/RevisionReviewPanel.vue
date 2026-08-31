<script setup lang="ts">
import { computed, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { AlertTriangle, ArrowRight, LockKeyhole } from '@lucide/vue'
import { useApiClient } from '@/lib/api/client'
import type { ComponentOutcome, Workset } from '@/lib/api/types'
import { worksetRevisionDetailQueryOptions } from '@/queries/worksets'
import AlbumBatchList from './AlbumBatchList.vue'
import ComponentInspector from './ComponentInspector.vue'
import { useWorksetUiStore } from '@/stores/workset-ui'
import { formatWorksetTime, summaryReasonLabel } from './workset-status'

const props = defineProps<{
  workset: Workset
  revisionList: { plan_id: string; revision_index: number; created_at: string; status: string; summary_reason: string; validation_state: string; stale: boolean | null; blocked_count: number }[]
  hasMore: boolean
  loadingMore: boolean
}>()

const emit = defineEmits<{
  'load-earlier-revisions': []
}>()

const api = useApiClient()
const ui = useWorksetUiStore()

// History selection is a stable plan_id (null = current revision), so a newly
// generated revision or a loaded earlier page cannot shift the read-back.
const currentPlanId = computed(() => {
  if (ui.historyPlanId) return ui.historyPlanId
  return props.workset.current_revision?.plan_id ?? null
})

const revisionQuery = useQuery(
  computed(() => worksetRevisionDetailQueryOptions(api, props.workset.workset_id, currentPlanId.value)),
)
const revision = computed(() => revisionQuery.data.value ?? null)
const revisionError = computed(() => revisionQuery.error.value as Error | null)
const step = computed(() => revision.value?.workflow.steps[0] ?? null)
const summary = computed(() => step.value?.summary ?? null)

const revisionRoots = computed(() => revision.value?.roots ?? [])

const componentsByRoot = computed(() => {
  const byRoot = new Map<number, ComponentOutcome[]>()
  if (!revision.value) return byRoot
  const byId = new Map<string, ComponentOutcome>()
  for (const workflowStep of revision.value.workflow.steps ?? []) {
    for (const rawComponent of workflowStep.components ?? []) {
      const component: ComponentOutcome = {
        ...rawComponent,
        lanes: rawComponent.lanes ?? [],
        variant_decisions: rawComponent.variant_decisions ?? [],
        operations: rawComponent.operations ?? [],
        projected_inventory: rawComponent.projected_inventory ?? [],
        files: rawComponent.files ?? [],
      }
      byId.set(component.component_id, component)
    }
  }
  for (const ref of revision.value.component_roots ?? []) {
    const component = byId.get(ref.component_id)
    if (!component) continue
    const components = byRoot.get(ref.root_index) ?? []
    components.push(component)
    byRoot.set(ref.root_index, components)
  }
  return byRoot
})

const isHistorical = computed(() => ui.historyPlanId !== null)
const staleRoots = computed(() => revisionRoots.value.filter((root) => root.stale).length)
const invalidRoots = computed(() => revisionRoots.value.filter((root) => root.root_status !== 'ok').length)
const actionableBatches = computed(() => {
  let count = 0
  for (const components of componentsByRoot.value.values()) {
    if (components.some((component) => component.status !== 'blocked')) count += 1
  }
  return count
})
const blockedBatches = computed(() => {
  let count = 0
  for (const components of componentsByRoot.value.values()) {
    if (components.some((component) => component.status === 'blocked')) count += 1
  }
  return count
})

watch(
  [() => props.workset.workset_id, () => revision.value?.plan_id],
  () => {
    if (!revision.value || ui.selectedBatchIndex !== null) return
    const candidates = [...componentsByRoot.value.entries()]
    const first = candidates.find(([, components]) => components.some((component) => component.status === 'blocked'))
      ?? candidates.find(([, components]) => components.some((component) => component.operations.length > 0))
      ?? candidates[0]
    if (!first) return
    const [rootIndex, components] = first
    ui.selectBatch(rootIndex)
    ui.openBatch(rootIndex)
    if (components[0]) ui.selectComponent(components[0].component_id)
  },
  { immediate: true },
)
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col overflow-hidden" data-testid="revision-review">
    <p v-if="isHistorical" class="shrink-0 border-b border-sky-500/30 bg-sky-500/10 px-5 py-2 text-[11px] text-sky-700 dark:text-sky-400" data-testid="history-readback">
      历史版本回看 · Revision v{{ revision?.revision_index ?? 0 }} · 只读
    </p>

    <div v-if="revisionError" class="border-b border-destructive/30 bg-destructive/10 px-5 py-3 text-xs text-destructive" data-testid="revision-error">
      <AlertTriangle class="mr-1 inline size-3.5" />无法读取计划版本明细，请重试。
    </div>

    <div v-else-if="!revision" class="grid flex-1 place-items-center text-xs text-muted-foreground" data-testid="revision-loading">
      正在读取计划版本…
    </div>

    <template v-else>
      <div v-if="staleRoots || invalidRoots" class="flex shrink-0 items-center gap-2 border-b border-amber-500/30 bg-amber-500/10 px-5 py-2 text-[11px] text-amber-700 dark:text-amber-400" data-testid="revision-root-warning">
        <AlertTriangle class="size-3.5 shrink-0" />
        <span>
          <template v-if="staleRoots">{{ staleRoots }} 个专辑批次的文件库存已变化</template>
          <template v-if="staleRoots && invalidRoots"> · </template>
          <template v-if="invalidRoots">{{ invalidRoots }} 个根目录不可用</template>
        </span>
        <span class="ml-auto text-[10px] opacity-80">选择批次可在检查器中查看路径与详情</span>
      </div>

      <div class="flex shrink-0 items-stretch gap-3 border-b border-border px-5 py-2.5" data-testid="revision-summary">
        <div class="flex min-w-0 flex-1 items-center gap-3 rounded-md border border-border bg-card/60 px-3 py-2" data-testid="workflow-ribbon">
          <span class="grid size-6 shrink-0 place-items-center rounded bg-foreground/10 font-mono text-[10px] font-semibold">01</span>
          <span class="min-w-0">
            <span class="flex min-w-0 items-baseline gap-2">
              <strong class="truncate text-xs">音频输出协调</strong>
              <span class="truncate font-mono text-[9px] text-muted-foreground">reconcile_audio_outputs</span>
            </span>
            <span class="mt-0.5 flex flex-wrap items-center gap-x-2 text-[9px] text-muted-foreground">
              <span :class="summary?.blocked_count ? 'text-destructive' : 'text-emerald-600 dark:text-emerald-400'">
                {{ summary ? (summaryReasonLabel[summary.summary_reason] ?? summary.summary_reason) : step?.status }}
              </span>
              <span v-if="summary">{{ workset.members.length }} 个专辑 · {{ summary.operation_count }} 项操作</span>
              <span v-if="step" :title="(step.classifier.tags ?? []).join('、')">分类标签 {{ (step.classifier.tags ?? []).length }} 项</span>
            </span>
          </span>
          <span class="ml-auto shrink-0 font-mono text-[9px] text-muted-foreground" :title="revision.plan_id">
            v{{ revision.revision_index }} · {{ formatWorksetTime(revision.created_at) }}
          </span>
        </div>
        <ArrowRight class="size-4 shrink-0 self-center text-muted-foreground" />
        <div class="flex min-w-56 items-center gap-2 rounded-md border border-dashed border-border px-3 py-2 text-[10px] text-muted-foreground">
          <LockKeyhole class="size-3.5 shrink-0" />
          <span><strong class="font-medium text-foreground/70">未来步骤</strong><br />rename · organize · metadata</span>
        </div>
      </div>

      <div class="flex min-h-0 flex-1">
        <AlbumBatchList :workset="workset" :components-by-root="componentsByRoot" :revision-roots="revisionRoots" />
        <ComponentInspector
          :components-by-root="componentsByRoot"
          :revision-roots="revisionRoots"
          :revision-list="revisionList"
          :selected-plan-id="ui.historyPlanId"
          :has-more="hasMore"
          :loading-more="loadingMore"
          @select-history="ui.selectHistoryPlan($event)"
          @load-more="emit('load-earlier-revisions')"
        />
      </div>

      <footer class="flex min-h-14 shrink-0 items-center gap-4 border-t border-border bg-card/40 px-5" data-testid="review-summary-footer">
        <p class="flex flex-wrap items-baseline gap-1.5 text-[10px] text-muted-foreground">
          <span class="font-mono text-sm font-semibold text-foreground">{{ actionableBatches }}</span> 个可执行批次
          <span>·</span>
          <span class="font-mono text-sm font-semibold" :class="blockedBatches ? 'text-destructive' : 'text-foreground'">{{ blockedBatches }}</span> 个阻塞批次
          <span v-if="summary">· <span class="font-mono text-sm font-semibold text-foreground">{{ summary.operation_count }}</span> 项文件操作</span>
        </p>
        <button type="button" disabled class="ml-auto flex items-center gap-1.5 rounded-md border border-border bg-muted px-3 py-1.5 text-[11px] font-semibold text-muted-foreground" title="执行阶段将在后续版本接入">
          <LockKeyhole class="size-3" />执行计划（尚未启用）
        </button>
      </footer>
    </template>
  </div>
</template>
