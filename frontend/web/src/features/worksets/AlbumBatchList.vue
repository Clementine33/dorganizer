<script setup lang="ts">
import { computed } from 'vue'
import { ChevronDown, ChevronRight, Search } from '@lucide/vue'
import type { ComponentOutcome, RootValidation, Workset } from '@/lib/api/types'
import { useWorksetUiStore } from '@/stores/workset-ui'
import { laneDecisionLabel, memberStateLabel, partitionLabel, toneClass } from './workset-status'

const props = defineProps<{
  workset: Workset
  componentsByRoot: Map<number, ComponentOutcome[]>
  revisionRoots: RootValidation[]
}>()

const ui = useWorksetUiStore()

const batches = computed(() =>
  props.workset.members.map((member, index) => {
    const components = props.componentsByRoot.get(index) ?? []
    const root = props.revisionRoots.find((candidate) => candidate.root_index === index) ?? null
    const blocked = components.filter((component) => component.status === 'blocked').length
    const operations = components.reduce((sum, component) => sum + component.operations.length, 0)
    return {
      member,
      index,
      components,
      root,
      blocked,
      operations,
      planned: components.length > 0,
      hasChanges: operations > 0,
    }
  }),
)

const visibleBatches = computed(() => {
  const query = ui.batchSearch.trim().toLocaleLowerCase()
  return batches.value.filter((batch) => {
    if (ui.batchFilter === 'change' && !batch.hasChanges) return false
    if (ui.batchFilter === 'blocked' && batch.blocked === 0) return false
    if (ui.batchFilter === 'pending' && batch.planned) return false
    if (!query) return true
    return `${batch.member.folder_name} ${batch.member.folder_path}`.toLocaleLowerCase().includes(query)
  })
})

const filters = [
  { value: 'all', label: '全部' },
  { value: 'change', label: '有变化' },
  { value: 'blocked', label: '已阻止' },
  { value: 'pending', label: '待规划' },
] as const

function toggleBatch(index: number) {
  ui.selectBatch(index)
  ui.toggleBatchOpen(index)
}

function selectComponent(batchIndex: number, componentId: string) {
  ui.selectBatch(batchIndex)
  ui.selectComponent(componentId)
}

function componentInventory(component: ComponentOutcome) {
  const generated = component.operations.filter((operation) => operation.kind === 'encode').length
  const removed = component.operations.filter((operation) => operation.kind === 'delete_obsolete').length
  const kept = Math.max(component.projected_inventory.length - generated, 0)
  return { generated, removed, kept }
}

function laneName(lane: string): string {
  if (lane === 'lossless') return '无损输出'
  if (lane === 'encoded') return '编码输出'
  return lane
}

function laneTone(decision: string): string {
  if (decision === 'BLOCKED') return 'border-destructive/40 bg-destructive/10 text-destructive'
  if (decision === 'REBUILD' || decision === 'REBUILD_ALL') return 'border-amber-500/35 bg-amber-500/10 text-amber-700 dark:text-amber-400'
  return 'border-border bg-background/50 text-muted-foreground'
}
</script>

<template>
  <section class="flex min-h-0 min-w-0 flex-1 flex-col" data-testid="album-batch-list">
    <div class="flex min-h-11 shrink-0 items-center gap-2 border-b border-border px-4 py-2">
      <button
        v-for="filter in filters"
        :key="filter.value"
        type="button"
        class="rounded-full border px-2.5 py-1 text-[10px] font-semibold transition-colors"
        :class="ui.batchFilter === filter.value ? 'border-foreground/20 bg-foreground/10 text-foreground' : 'border-border text-muted-foreground hover:bg-muted hover:text-foreground'"
        @click="ui.batchFilter = filter.value"
      >
        {{ filter.label }}
      </button>
      <label class="ml-auto flex w-56 max-w-[40%] items-center gap-2 rounded-md border border-border bg-background/60 px-2.5 py-1.5">
        <Search class="size-3 shrink-0 text-muted-foreground" />
        <input
          v-model="ui.batchSearch"
          type="search"
          class="min-w-0 flex-1 bg-transparent text-[11px] outline-none placeholder:text-muted-foreground"
          placeholder="搜索专辑或路径"
          aria-label="搜索专辑或路径"
        />
      </label>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto px-4 py-3">
      <div v-if="visibleBatches.length === 0" class="grid min-h-40 place-items-center rounded-md border border-dashed border-border text-center">
        <div>
          <p class="text-xs font-semibold">没有匹配的专辑批次</p>
          <p class="mt-1 text-[11px] text-muted-foreground">清除筛选或搜索后再试。</p>
        </div>
      </div>

      <article
        v-for="batch in visibleBatches"
        :key="batch.member.folder_id"
        class="mb-3 overflow-hidden rounded-lg border bg-card/60"
        :class="ui.selectedBatchIndex === batch.index ? 'border-foreground/25 bg-card' : 'border-border'"
        :data-testid="`batch-${batch.index}`"
      >
        <button
          type="button"
          class="grid w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-4 px-4 py-3 text-left transition-colors hover:bg-muted/40"
          :aria-expanded="ui.openBatchIndexes.has(batch.index)"
          @click="toggleBatch(batch.index)"
        >
          <span class="min-w-0">
            <span class="block truncate font-heading text-sm font-semibold">{{ batch.member.folder_name }}</span>
            <span class="mt-0.5 block truncate font-mono text-[10px] text-muted-foreground" :title="batch.member.folder_path">
              {{ batch.member.folder_path }}
            </span>
            <span class="mt-1 block font-mono text-[9px] text-muted-foreground">
              {{ batch.root?.entry_count ?? 0 }} 个文件 · {{ batch.components.length }} 组件 · {{ batch.operations }} 操作
            </span>
          </span>
          <span class="flex items-center gap-2">
            <span
              v-if="batch.root?.stale"
              class="rounded-full px-2 py-0.5 text-[9px] font-semibold"
              :class="toneClass.warn"
            >
              已过期
            </span>
            <span
              class="rounded-full px-2 py-0.5 text-[9px] font-semibold"
              :class="batch.blocked ? toneClass.bad : batch.planned ? toneClass.ok : toneClass.neutral"
            >
              {{ batch.blocked ? `${batch.blocked} 个阻塞` : batch.planned ? memberStateLabel.planned : memberStateLabel.pending }}
            </span>
            <component :is="ui.openBatchIndexes.has(batch.index) ? ChevronDown : ChevronRight" class="size-4 text-muted-foreground" />
          </span>
        </button>

        <div v-if="ui.openBatchIndexes.has(batch.index)" class="border-t border-border px-4 py-3">
          <div v-if="!batch.planned" class="rounded-md border border-dashed border-border px-4 py-5 text-center text-[11px] text-muted-foreground" data-testid="batch-empty">
            该专辑已加入工作集，但当前计划版本尚未覆盖。生成新版本后可在这里审阅组件。
          </div>

          <div v-else class="space-y-3">
            <section
              v-for="partition in (['matched', 'unmatched'] as const)"
              v-show="batch.components.some((component) => component.partition === partition)"
              :key="partition"
              class="rounded-md border border-border bg-background/40 p-2.5"
            >
              <div class="mb-2 flex items-baseline gap-2 px-0.5">
                <h3 class="text-[11px] font-semibold">{{ partitionLabel[partition] }}</h3>
                <span class="font-mono text-[9px] text-muted-foreground">{{ partition }}</span>
                <span class="ml-auto font-mono text-[9px] text-muted-foreground">
                  {{ batch.components.filter((component) => component.partition === partition).length }} 个组件
                </span>
              </div>

              <button
                v-for="(component, componentIndex) in batch.components.filter((candidate) => candidate.partition === partition)"
                :key="component.component_id"
                type="button"
                class="mb-2 grid w-full grid-cols-[minmax(0,1.2fr)_minmax(0,.8fr)] gap-3 rounded-md border px-3 py-2.5 text-left transition-colors last:mb-0 hover:bg-muted/40"
                :class="ui.selectedComponentId === component.component_id ? 'border-foreground/25 bg-muted/40' : 'border-border bg-card/50'"
                :data-testid="`batch-component-${batch.index}-${componentIndex}`"
                @click.stop="selectComponent(batch.index, component.component_id)"
              >
                <span class="grid min-w-0 grid-cols-2 gap-2">
                  <span
                    v-for="lane in component.lanes"
                    :key="lane.lane"
                    class="min-w-0 rounded border px-2 py-1.5"
                    :class="laneTone(lane.decision)"
                  >
                    <span class="block text-[9px] font-medium">{{ laneName(lane.lane) }}</span>
                    <span class="mt-0.5 block truncate font-mono text-[10px] font-semibold">
                      {{ laneDecisionLabel[lane.decision] ?? lane.decision }}
                    </span>
                    <span v-if="lane.reason_code" class="mt-0.5 block truncate font-mono text-[8px] opacity-80">{{ lane.reason_code }}</span>
                  </span>
                </span>
                <span class="min-w-0 self-center">
                  <span class="block text-[9px] text-muted-foreground">计划后库存</span>
                  <span v-if="component.status === 'blocked'" class="mt-1 block font-mono text-[10px] text-destructive">
                    0 可执行操作 · 决策可审阅
                  </span>
                  <span v-else class="mt-1 flex flex-wrap items-baseline gap-2 font-mono text-[10px]">
                    <span v-if="componentInventory(component).generated" class="text-amber-600 dark:text-amber-400">+{{ componentInventory(component).generated }} 生成</span>
                    <span v-if="componentInventory(component).removed" class="text-destructive">−{{ componentInventory(component).removed }} 移除</span>
                    <span class="text-muted-foreground">{{ componentInventory(component).kept }} 保留</span>
                  </span>
                </span>
              </button>
            </section>
          </div>
        </div>
      </article>
    </div>
  </section>
</template>
