<script setup lang="ts">
import { computed } from 'vue'
import { ChevronDown, ChevronRight } from '@lucide/vue'
import type { ComponentOutcome, RootValidation, Workset } from '@/lib/api/types'
import { useWorksetUiStore } from '@/stores/workset-ui'
import { memberStateLabel, partitionLabel, toneClass } from './workset-status'

// Album batch list: one card per workset member (planning root), grouped
// against the revision's components via the ownership map. Members not
// covered by the revision show an honest "awaiting planning" empty state.

const props = defineProps<{
  workset: Workset
  componentsByRoot: Map<number, ComponentOutcome[]>
  revisionRoots: RootValidation[]
}>()

const ui = useWorksetUiStore()

const batches = computed(() =>
  props.workset.members.map((member, index) => {
    const components = props.componentsByRoot.get(index) ?? []
    const root = props.revisionRoots.find((r) => r.root_index === index) ?? null
    const blocked = components.filter((c) => c.status === 'blocked').length
    const operations = components.reduce((sum, c) => sum + c.operations.length, 0)
    return { member, index, components, root, blocked, operations, planned: components.length > 0 }
  }),
)

function batchStats(components: ComponentOutcome[]) {
  const matched = components.filter((c) => c.partition === 'matched').length
  const unmatched = components.filter((c) => c.partition === 'unmatched').length
  return { matched, unmatched }
}

function selectBatch(index: number) {
  ui.selectBatch(index)
}
</script>

<template>
  <div class="min-h-0 w-[420px] shrink-0 overflow-y-auto border-r border-border p-3" data-testid="album-batch-list">
    <div
      v-for="batch in batches"
      :key="batch.member.folder_id"
      class="mb-2 rounded-md border bg-card"
      :class="ui.selectedBatchIndex === batch.index ? 'border-foreground/25' : 'border-border'"
      :data-testid="`batch-${batch.index}`"
    >
      <button
        type="button"
        class="flex w-full items-center gap-2 px-2.5 py-2 text-left"
        @click="selectBatch(batch.index)"
      >
        <component
          :is="ui.openBatchIndexes.has(batch.index) ? ChevronDown : ChevronRight"
          class="size-3.5 shrink-0 text-muted-foreground"
          @click.stop="ui.toggleBatchOpen(batch.index)"
        />
        <span class="min-w-0 flex-1">
          <span class="block truncate font-heading text-xs font-semibold">{{ batch.member.folder_name }}</span>
          <span class="block truncate font-mono text-[10px] text-muted-foreground">{{ batch.member.folder_path }}</span>
        </span>
        <span
          class="shrink-0 rounded-full px-1.5 py-0.5 text-[9px] font-semibold"
          :class="toneClass[batch.planned ? 'ok' : 'neutral']"
        >
          {{ batch.planned ? memberStateLabel.planned : memberStateLabel.pending }}
        </span>
      </button>

      <div v-if="ui.openBatchIndexes.has(batch.index) || ui.selectedBatchIndex === batch.index" class="border-t border-border px-2.5 py-2">
        <div v-if="!batch.planned" class="py-3 text-center text-[11px] text-muted-foreground" data-testid="batch-empty">
          该批次尚未包含在当前计划版本中
        </div>
        <template v-else>
          <div class="flex flex-wrap gap-1.5 text-[10px]">
            <span
              v-for="(comp, i) in batch.components"
              :key="comp.component_id"
              class="rounded-full border px-1.5 py-0.5 font-medium"
              :class="comp.status === 'blocked' ? 'border-destructive/40 text-destructive' : 'border-border text-muted-foreground'"
              :data-testid="`batch-component-${batch.index}-${i}`"
              @click="ui.selectComponent(comp.component_id)"
            >
              {{ partitionLabel[comp.partition] ?? comp.partition }}
              <template v-if="comp.status === 'blocked'"> · 阻塞</template>
            </span>
          </div>
          <p class="mt-1.5 text-[10px] text-muted-foreground">
            {{ batch.components.length }} 组件 · {{ batch.blocked }} 阻塞 · {{ batch.operations }} 操作
            <template v-if="batchStats(batch.components).matched"> · matched {{ batchStats(batch.components).matched }}</template>
            <template v-if="batchStats(batch.components).unmatched"> · unmatched {{ batchStats(batch.components).unmatched }}</template>
          </p>
        </template>
      </div>
    </div>
  </div>
</template>
