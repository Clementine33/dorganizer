<script setup lang="ts">
import { computed } from 'vue'
import { Copy, RotateCcw } from '@lucide/vue'
import type { ComponentOutcome, RootValidation } from '@/lib/api/types'
import { useWorksetUiStore } from '@/stores/workset-ui'
import {
  formatWorksetTime,
  laneDecisionLabel,
  operationKindLabel,
  partitionLabel,
  resolutionLabel,
  summaryReasonLabel,
} from './workset-status'

const props = defineProps<{
  componentsByRoot: Map<number, ComponentOutcome[]>
  revisionRoots: RootValidation[]
  revisionList: { plan_id: string; revision_index: number; created_at: string; status: string; summary_reason: string; validation_state: string; stale: boolean | null; blocked_count: number }[]
  /** Stable history selection: null = current revision. */
  selectedPlanId: string | null
  hasMore: boolean
  loadingMore: boolean
}>()

const emit = defineEmits<{
  /** null selects back to the current revision. */
  'select-history': [planId: string | null]
  'load-more': []
}>()

const ui = useWorksetUiStore()

function copyComponentId(id: string): void {
  void navigator.clipboard?.writeText(id).catch(() => {})
}

const allComponents = computed(() => Array.from(props.componentsByRoot.values()).flat())

const selectedComponent = computed(() => {
  if (!ui.selectedComponentId) return null
  return allComponents.value.find((component) => component.component_id === ui.selectedComponentId) ?? null
})

const batchComponents = computed(() => {
  if (ui.selectedBatchIndex === null) return allComponents.value
  return props.componentsByRoot.get(ui.selectedBatchIndex) ?? []
})

const batchStats = computed(() => ({
  components: batchComponents.value.length,
  blocked: batchComponents.value.filter((component) => component.status === 'blocked').length,
  operations: batchComponents.value.reduce((sum, component) => sum + component.operations.length, 0),
  projected: batchComponents.value.reduce((sum, component) => sum + component.projected_inventory.length, 0),
}))

const selectedRoot = computed(() =>
  ui.selectedBatchIndex !== null
    ? props.revisionRoots.find((root) => root.root_index === ui.selectedBatchIndex) ?? null
    : null,
)

function laneTone(decision: string): string {
  if (decision === 'BLOCKED') return 'border-destructive/40 bg-destructive/10 text-destructive'
  if (decision === 'REBUILD' || decision === 'REBUILD_ALL') return 'border-amber-500/35 bg-amber-500/10 text-amber-700 dark:text-amber-400'
  return 'border-border bg-card text-muted-foreground'
}
</script>

<template>
  <aside class="min-h-0 w-[360px] shrink-0 overflow-y-auto border-l border-border p-4" data-testid="component-inspector">
    <template v-if="selectedComponent">
      <div class="flex items-start gap-2">
        <div class="min-w-0 flex-1">
          <p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">组件</p>
          <h2 class="mt-1 truncate font-mono text-xs font-semibold" data-testid="inspector-title" :title="selectedComponent.component_id">
            {{ selectedComponent.component_id }}
          </h2>
        </div>
        <button
          type="button"
          class="grid size-7 shrink-0 place-items-center rounded-md border border-border text-muted-foreground hover:bg-muted hover:text-foreground"
          aria-label="复制组件 ID"
          @click="copyComponentId(selectedComponent.component_id)"
        >
          <Copy class="size-3" />
        </button>
      </div>

      <div class="mt-2 flex items-center gap-1.5 text-[10px]">
        <span class="rounded-full bg-muted px-2 py-0.5 font-semibold">{{ partitionLabel[selectedComponent.partition] ?? selectedComponent.partition }}</span>
        <span
          class="rounded-full px-2 py-0.5 font-semibold"
          :class="selectedComponent.status === 'blocked' ? 'bg-destructive/15 text-destructive' : 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'"
          data-testid="inspector-status"
        >
          {{ selectedComponent.status === 'blocked' ? '阻塞' : '正常' }}
        </span>
        <button type="button" class="ml-auto flex items-center gap-1 text-muted-foreground hover:text-foreground" @click="ui.selectComponent(null)">
          <RotateCcw class="size-3" /> 返回批次
        </button>
      </div>

      <div
        v-if="selectedComponent.status === 'blocked'"
        class="mt-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2"
        data-testid="inspector-blocked-reason"
      >
        <p class="font-mono text-[10px] font-semibold text-destructive">{{ selectedComponent.reason_code }}</p>
        <p class="mt-1 text-[11px] leading-4">{{ selectedComponent.message }}</p>
        <p class="mt-1 text-[10px] text-muted-foreground">本组件不会产生可执行操作，其他组件仍可继续。</p>
      </div>

      <section class="mt-5">
        <h3 class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">输出期望与决策</h3>
        <div class="mt-2 space-y-2" data-testid="inspector-lanes">
          <div
            v-for="lane in selectedComponent.lanes"
            :key="lane.lane"
            class="rounded-md border px-3 py-2"
            :class="laneTone(lane.decision)"
          >
            <div class="flex items-center gap-2">
              <span class="text-[11px] font-medium">{{ lane.lane === 'lossless' ? '无损输出' : lane.lane === 'encoded' ? '编码输出' : lane.lane }}</span>
              <span class="ml-auto font-mono text-[10px] font-semibold" data-testid="inspector-lane-decision">
                {{ laneDecisionLabel[lane.decision] ?? lane.decision }}
              </span>
            </div>
            <p v-if="lane.reason_code" class="mt-1 font-mono text-[9px] opacity-80">{{ lane.reason_code }}</p>
          </div>
        </div>
      </section>

      <section class="mt-5">
        <h3 class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">文件变化明细</h3>
        <div class="mt-2 space-y-1.5" data-testid="inspector-variants">
          <p v-if="selectedComponent.variant_decisions.length === 0" class="text-[11px] text-muted-foreground">无文件变化。</p>
          <div v-for="variant in selectedComponent.variant_decisions" :key="variant.stem" class="rounded-md border border-border bg-card/60 px-2.5 py-2">
            <p class="truncate font-mono text-[10px] font-semibold" :title="variant.stem">{{ variant.stem }}</p>
            <div v-for="(file, index) in variant.decisions" :key="`${variant.stem}-${index}`" class="mt-1.5 border-t border-border/70 pt-1.5 text-[10px]">
              <div class="flex min-w-0 items-center gap-2">
                <span
                  class="w-14 shrink-0 rounded px-1.5 py-0.5 text-center font-mono text-[9px] font-semibold"
                  :class="file.resolution === 'delete' ? 'bg-destructive/15 text-destructive' : file.resolution === 'encode' ? 'bg-amber-500/15 text-amber-700 dark:text-amber-400' : 'bg-muted text-muted-foreground'"
                  :data-testid="`file-resolution-${file.resolution}`"
                >
                  {{ resolutionLabel[file.resolution] ?? file.resolution }}
                </span>
                <span class="min-w-0 flex-1 truncate font-mono text-muted-foreground" :title="file.path">{{ file.path }}</span>
              </div>
              <p v-if="file.target_path" class="mt-1 truncate pl-16 font-mono text-[9px] text-muted-foreground" :title="file.target_path">→ {{ file.target_path }}</p>
              <p v-if="file.reason_code" class="mt-0.5 truncate pl-16 font-mono text-[9px] text-muted-foreground">{{ file.reason_code }}</p>
            </div>
          </div>
        </div>
      </section>

      <section class="mt-5">
        <h3 class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">操作（{{ selectedComponent.operations.length }}）</h3>
        <div class="mt-2 space-y-1.5" data-testid="inspector-operations">
          <p v-if="selectedComponent.operations.length === 0" class="text-[11px] text-muted-foreground">无需操作。</p>
          <div v-for="(operation, index) in selectedComponent.operations" :key="`${operation.kind}-${index}`" class="rounded-md border border-border bg-card/60 px-2.5 py-2 text-[10px]">
            <span class="rounded px-1.5 py-0.5 font-semibold" :class="operation.kind === 'delete_obsolete' ? 'bg-destructive/15 text-destructive' : 'bg-amber-500/15 text-amber-700 dark:text-amber-400'">
              {{ operationKindLabel[operation.kind] ?? operation.kind }}
            </span>
            <p class="mt-1.5 truncate font-mono text-muted-foreground" :title="operation.source_path">{{ operation.source_path }}</p>
            <p v-if="operation.target_path" class="mt-0.5 truncate font-mono text-muted-foreground" :title="operation.target_path">→ {{ operation.target_path }}</p>
          </div>
        </div>
      </section>
    </template>

    <template v-else>
      <p class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">批次概览</p>
      <h2 class="mt-1 font-heading text-sm font-semibold" data-testid="inspector-title">
        {{ ui.selectedBatchIndex === null ? '全部批次' : `批次 ${ui.selectedBatchIndex + 1}` }}
      </h2>

      <div class="mt-3 grid grid-cols-2 gap-2" data-testid="batch-inspector-stats">
        <div class="rounded-md border border-border bg-card px-3 py-2.5"><p class="font-heading text-lg font-semibold">{{ batchStats.components }}</p><p class="text-[10px] text-muted-foreground">组件</p></div>
        <div class="rounded-md border border-border bg-card px-3 py-2.5"><p class="font-heading text-lg font-semibold" :class="batchStats.blocked ? 'text-destructive' : ''">{{ batchStats.blocked }}</p><p class="text-[10px] text-muted-foreground">阻塞</p></div>
        <div class="rounded-md border border-border bg-card px-3 py-2.5"><p class="font-heading text-lg font-semibold">{{ batchStats.operations }}</p><p class="text-[10px] text-muted-foreground">操作</p></div>
        <div class="rounded-md border border-border bg-card px-3 py-2.5"><p class="font-heading text-lg font-semibold">{{ batchStats.projected }}</p><p class="text-[10px] text-muted-foreground">计划后文件</p></div>
      </div>

      <div v-if="selectedRoot" class="mt-4 rounded-md border border-border bg-card/60 px-3 py-2.5 text-[10px]" data-testid="batch-inspector-root">
        <p class="break-all font-mono leading-4 text-muted-foreground">{{ selectedRoot.root_path }}</p>
        <p class="mt-2 flex flex-wrap items-center gap-1.5">
          <span>{{ selectedRoot.entry_count }} 项</span>
          <span v-if="selectedRoot.stale" class="rounded-full bg-amber-500/15 px-1.5 py-0.5 font-semibold text-amber-700 dark:text-amber-400">已过期</span>
          <span v-if="selectedRoot.root_status !== 'ok'" class="rounded-full bg-destructive/15 px-1.5 py-0.5 font-semibold text-destructive">{{ selectedRoot.root_error_code || selectedRoot.root_status }}</span>
        </p>
      </div>

      <p class="mt-3 text-[11px] leading-4 text-muted-foreground">展开专辑批次并选择组件，可查看输出决策、文件变化和操作明细。</p>
    </template>

    <section class="mt-6 border-t border-border pt-4">
      <h3 class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">计划版本</h3>
      <div class="mt-2 space-y-1.5">
        <p v-if="revisionList.length === 0" class="text-[11px] text-muted-foreground">尚无计划版本。</p>
        <button
          v-for="row in revisionList"
          :key="row.plan_id"
          type="button"
          class="flex w-full items-center gap-2 rounded-md border px-2.5 py-2 text-left font-mono text-[9px] transition-colors"
          :class="selectedPlanId === row.plan_id ? 'border-sky-500/40 bg-sky-500/10 text-foreground' : 'border-border text-muted-foreground hover:bg-muted'"
          @click="emit('select-history', row.plan_id)"
        >
          <span class="min-w-0 flex-1 truncate">{{ row.plan_id }}</span>
          <span class="shrink-0 font-mono text-[9px]">v{{ row.revision_index }}</span>
          <span class="shrink-0 font-sans text-[9px]">{{ formatWorksetTime(row.created_at) }}</span>
          <span class="sr-only">{{ summaryReasonLabel[row.summary_reason] ?? row.summary_reason }}</span>
        </button>
        <button
          v-if="selectedPlanId !== null"
          type="button"
          class="w-full rounded-md border border-sky-500/30 px-2.5 py-1.5 text-left text-[9px] font-semibold text-sky-700 hover:bg-sky-500/10 dark:text-sky-400"
          data-testid="back-to-current"
          @click="emit('select-history', null)"
        >
          返回当前版本
        </button>
        <button
          v-if="hasMore"
          type="button"
          class="w-full rounded-md border border-border px-2.5 py-1.5 text-[9px] text-muted-foreground hover:bg-muted"
          data-testid="load-earlier-revisions"
          :disabled="loadingMore"
          @click="emit('load-more')"
        >
          {{ loadingMore ? '加载中…' : '加载更早版本' }}
        </button>
      </div>
    </section>
  </aside>
</template>
