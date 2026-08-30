<script setup lang="ts">
import { computed } from 'vue'
import type { ComponentOutcome, RootValidation } from '@/lib/api/types'
import { useWorksetUiStore } from '@/stores/workset-ui'
import { laneDecisionLabel, operationKindLabel, partitionLabel, resolutionLabel } from './workset-status'

// Two-level inspector: batch-level summary when no component is selected,
// full component detail (lanes → variant decisions → operations) when one is.

const props = defineProps<{
  componentsByRoot: Map<number, ComponentOutcome[]>
  revisionRoots: RootValidation[]
}>()

const ui = useWorksetUiStore()

function copyComponentId(id: string): void {
  void navigator.clipboard?.writeText(id).catch(() => {})
}

const allComponents = computed(() => Array.from(props.componentsByRoot.values()).flat())

const selectedComponent = computed(() => {
  if (!ui.selectedComponentId) return null
  return allComponents.value.find((c) => c.component_id === ui.selectedComponentId) ?? null
})

// Batch-level inspector aggregates over the selected batch, else everything.
const batchComponents = computed(() => {
  if (ui.selectedBatchIndex === null) return allComponents.value
  return props.componentsByRoot.get(ui.selectedBatchIndex) ?? []
})

const batchStats = computed(() => ({
  components: batchComponents.value.length,
  blocked: batchComponents.value.filter((c) => c.status === 'blocked').length,
  operations: batchComponents.value.reduce((sum, c) => sum + c.operations.length, 0),
  projected: batchComponents.value.reduce((sum, c) => sum + c.projected_inventory.length, 0),
}))

const selectedRoot = computed(() =>
  ui.selectedBatchIndex !== null
    ? props.revisionRoots.find((r) => r.root_index === ui.selectedBatchIndex) ?? null
    : null,
)
</script>

<template>
  <aside class="min-h-0 flex-1 overflow-y-auto p-4" data-testid="component-inspector">
    <!-- Component-level -->
    <template v-if="selectedComponent">
      <div class="flex items-center gap-2">
        <h2 class="font-heading text-sm font-semibold" data-testid="inspector-title">组件</h2>
        <button
          type="button"
          class="font-mono text-[10px] text-muted-foreground underline-offset-2 hover:underline"
          data-testid="inspector-component-id"
          :title="selectedComponent.component_id"
          @click="copyComponentId(selectedComponent.component_id)"
        >
          {{ selectedComponent.component_id.slice(0, 12) }}…
        </button>
        <button type="button" class="ml-auto text-[10px] text-muted-foreground hover:text-foreground" @click="ui.selectComponent(null)">
          返回批次
        </button>
      </div>
      <p class="mt-1 text-[11px]">
        <span class="rounded-full bg-muted px-2 py-0.5 font-semibold">{{ partitionLabel[selectedComponent.partition] ?? selectedComponent.partition }}</span>
        <span
          class="ml-1.5 rounded-full px-2 py-0.5 font-semibold"
          :class="selectedComponent.status === 'blocked' ? 'bg-destructive/15 text-destructive' : 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400'"
          data-testid="inspector-status"
        >
          {{ selectedComponent.status === 'blocked' ? '阻塞' : '正常' }}
        </span>
      </p>

      <div
        v-if="selectedComponent.status === 'blocked'"
        class="mt-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2"
        data-testid="inspector-blocked-reason"
      >
        <p class="font-mono text-[11px] font-semibold text-destructive">{{ selectedComponent.reason_code }}</p>
        <p class="mt-0.5 text-[11px] leading-4">{{ selectedComponent.message }}</p>
      </div>

      <!-- Lanes -->
      <div class="mt-4">
        <p class="text-xs font-semibold">轨道决策</p>
        <div class="mt-1.5 grid grid-cols-2 gap-2" data-testid="inspector-lanes">
          <div
            v-for="lane in selectedComponent.lanes"
            :key="lane.lane"
            class="rounded-md border border-border bg-card px-2.5 py-2"
          >
            <p class="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{{ lane.lane }}</p>
            <p class="mt-0.5 font-heading text-xs font-semibold" data-testid="inspector-lane-decision">
              {{ laneDecisionLabel[lane.decision] ?? lane.decision }}
            </p>
            <p v-if="lane.reason_code" class="font-mono text-[9px] text-muted-foreground">{{ lane.reason_code }}</p>
          </div>
        </div>
      </div>

      <!-- Variant file decisions -->
      <div class="mt-4">
        <p class="text-xs font-semibold">文件决策</p>
        <div class="mt-1.5 space-y-1" data-testid="inspector-variants">
          <div
            v-for="variant in selectedComponent.variant_decisions"
            :key="variant.stem"
            class="rounded-md border border-border bg-card/60 px-2.5 py-1.5"
          >
            <p class="font-mono text-[10px] font-semibold">{{ variant.stem }}</p>
            <div v-for="(file, i) in variant.decisions" :key="`${variant.stem}-${i}`" class="mt-0.5 flex items-center gap-2 text-[10px]">
              <span
                class="shrink-0 rounded-full px-1.5 py-0.5 font-semibold"
                :class="file.resolution === 'delete' ? 'bg-destructive/15 text-destructive' : file.resolution === 'encode' ? 'bg-sky-500/15 text-sky-600 dark:text-sky-400' : 'bg-muted text-muted-foreground'"
                :data-testid="`file-resolution-${file.resolution}`"
              >
                {{ resolutionLabel[file.resolution] ?? file.resolution }}
              </span>
              <span class="min-w-0 flex-1 truncate font-mono text-muted-foreground" :title="file.target_path ?? file.path">
                {{ file.path }}<template v-if="file.target_path"> → {{ file.target_path }}</template>
              </span>
              <span v-if="file.reason_code" class="shrink-0 font-mono text-[9px] text-muted-foreground">{{ file.reason_code }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Operations -->
      <div class="mt-4">
        <p class="text-xs font-semibold">操作（{{ selectedComponent.operations.length }}）</p>
        <div class="mt-1.5 space-y-1" data-testid="inspector-operations">
          <p v-if="selectedComponent.operations.length === 0" class="text-[11px] text-muted-foreground">无需操作。</p>
          <div
            v-for="(op, i) in selectedComponent.operations"
            :key="`${op.kind}-${i}`"
            class="flex items-center gap-2 rounded-md border border-border bg-card/60 px-2.5 py-1.5 text-[10px]"
          >
            <span
              class="shrink-0 rounded-full px-1.5 py-0.5 font-semibold"
              :class="op.kind === 'delete_obsolete' ? 'bg-destructive/15 text-destructive' : 'bg-sky-500/15 text-sky-600 dark:text-sky-400'"
            >
              {{ operationKindLabel[op.kind] ?? op.kind }}
            </span>
            <span class="min-w-0 flex-1 truncate font-mono text-muted-foreground">
              {{ op.source_path }}<template v-if="op.target_path"> → {{ op.target_path }}</template>
            </span>
          </div>
        </div>
      </div>
    </template>

    <!-- Batch-level -->
    <template v-else>
      <h2 class="font-heading text-sm font-semibold" data-testid="inspector-title">批次概览</h2>
      <div class="mt-3 grid grid-cols-2 gap-2" data-testid="batch-inspector-stats">
        <div class="rounded-md border border-border bg-card px-3 py-2.5">
          <p class="font-heading text-lg font-semibold">{{ batchStats.components }}</p>
          <p class="text-[10px] text-muted-foreground">组件</p>
        </div>
        <div class="rounded-md border border-border bg-card px-3 py-2.5">
          <p class="font-heading text-lg font-semibold" :class="batchStats.blocked ? 'text-destructive' : ''">{{ batchStats.blocked }}</p>
          <p class="text-[10px] text-muted-foreground">阻塞</p>
        </div>
        <div class="rounded-md border border-border bg-card px-3 py-2.5">
          <p class="font-heading text-lg font-semibold">{{ batchStats.operations }}</p>
          <p class="text-[10px] text-muted-foreground">操作</p>
        </div>
        <div class="rounded-md border border-border bg-card px-3 py-2.5">
          <p class="font-heading text-lg font-semibold">{{ batchStats.projected }}</p>
          <p class="text-[10px] text-muted-foreground">预期库存条目</p>
        </div>
      </div>

      <p v-if="selectedRoot" class="mt-3 rounded-md border border-border bg-card/60 px-3 py-2 text-[10px] text-muted-foreground" data-testid="batch-inspector-root">
        <span class="font-mono">{{ selectedRoot.root_path }}</span>
        · {{ selectedRoot.entry_count }} 项
        <template v-if="selectedRoot.stale"> · <span class="font-semibold text-amber-600 dark:text-amber-400">stale</span></template>
      </p>

      <p class="mt-3 text-[11px] leading-4 text-muted-foreground">
        选择上方批次中的组件查看轨道决策、文件决策与操作明细。
      </p>
    </template>
  </aside>
</template>
