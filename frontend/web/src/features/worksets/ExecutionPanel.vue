<script setup lang="ts">
import { computed } from 'vue'
import { ChevronRight, Folder } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { PARTITION_TEXT, keepReasonText } from '@/features/worksets/plan-readers'
import { sessionFailureText } from '@/features/worksets/session-failure'
import type {
  ExecutionComponent,
  ExecutionStatus,
  ExecutionView,
  FileDecision,
  WorksetMember,
} from '@/lib/api/types'

/**
 * Live progress and the observed result of one execution session (M3). Every
 * number here is a count that actually happened — no estimated percentage —
 * and a failure keeps its partial facts: completed components, the operations
 * that never ran, and the files preserved for an operator.
 *
 * One card per folder, because that is the unit the user chose and the unit a
 * result is judged by: a folder's components (its classifier partitions) are
 * listed inside its own card, and the card reports the folder's own state.
 *
 * 保留文件 is the plan's conclusion, not the run's bookkeeping: the files the
 * revision kept untouched, read from that revision and joined by component id.
 */
const props = defineProps<{
  view: ExecutionView
  /** Kept files per component id, as the executed revision concluded them. */
  kept?: Record<string, FileDecision[]>
  /** The record's members, to name a folder the way the user chose it. */
  members?: WorksetMember[]
}>()

function keptFiles(component: ExecutionComponent): FileDecision[] {
  return props.kept?.[component.component_id] ?? []
}

const STATUS: Record<ExecutionStatus, { label: string; tone: 'neutral' | 'brand' | 'success' | 'warning' | 'danger' }> = {
  queued: { label: '排队中', tone: 'neutral' },
  running: { label: '执行中', tone: 'brand' },
  succeeded: { label: '已完成', tone: 'success' },
  failed: { label: '失败', tone: 'danger' },
  canceled: { label: '已取消', tone: 'warning' },
  interrupted: { label: '已中断', tone: 'danger' },
}

const COMPONENT_STATUS: Record<ExecutionComponent['status'], { label: string; tone: 'neutral' | 'success' | 'danger' | 'warning' }> = {
  pending: { label: '未执行', tone: 'neutral' },
  succeeded: { label: '已完成', tone: 'success' },
  failed: { label: '失败', tone: 'danger' },
  canceled: { label: '已取消', tone: 'warning' },
}

const STAGE_LABELS: Record<string, string> = {
  precheck: '预检',
  materialize: '生成输出',
  validate: '校验输出',
  commit: '提交',
  remove: '清理旧音频',
}

const status = computed(() => STATUS[props.view.status])
const running = computed(() => props.view.status === 'running' || props.view.status === 'queued')

/** The preserved files live under Delete/ in soft mode; hard mode removes them. */
const removedHint = computed(() => (props.view.options?.delete_mode === 'hard' ? '已硬删除' : '已清理到 Delete/'))

function shortPath(path: string): string {
  const parts = path.split('/')
  return parts.length <= 2 ? path : `…/${parts.slice(-2).join('/')}`
}

function filePath(path: string, root: string): string {
  const prefix = `${root.replace(/\/$/, '')}/`
  return path.startsWith(prefix) ? path.slice(prefix.length) : path
}

/** One folder's components and its own conclusion, in the panel's words. */
interface FolderCard {
  rootPath: string
  name: string
  components: ExecutionComponent[]
  status: keyof typeof FOLDER_STATUS
  /** The component the run is inside right now, if it is this folder's. */
  running: boolean
  completedComponents: number
  operations: number
  completedOperations: number
}

const FOLDER_STATUS: Record<string, { label: string; tone: 'neutral' | 'brand' | 'success' | 'warning' | 'danger' }> = {
  pending: { label: '未执行', tone: 'neutral' },
  running: { label: '执行中', tone: 'brand' },
  succeeded: { label: '已完成', tone: 'success' },
  failed: { label: '失败', tone: 'danger' },
  canceled: { label: '已取消', tone: 'warning' },
}

/**
 * A folder's own state: done only when all of its partitions are, and a failure
 * or a cancellation anywhere in it is the folder's headline — the components
 * below say which one stopped.
 */
function folderStatus(components: ExecutionComponent[]): keyof typeof FOLDER_STATUS {
  if (components.every((component) => component.status === 'succeeded')) return 'succeeded'
  if (components.some((component) => component.status === 'failed')) return 'failed'
  if (components.some((component) => component.status === 'canceled')) return 'canceled'
  return 'pending'
}

/** The report grouped into the folders the user selected, in frozen order. */
const cards = computed<FolderCard[]>(() => {
  const byRoot = new Map<string, ExecutionComponent[]>()
  for (const component of props.view.components) {
    byRoot.set(component.root_path, [...(byRoot.get(component.root_path) ?? []), component])
  }
  const live = props.view.status === 'running' || props.view.status === 'queued'
  return [...byRoot].map(([rootPath, components]) => {
    const running =
      live && components.some((component) => component.component_id === props.view.current_component_id)
    return {
      rootPath,
      name:
        props.members?.find((member) => member.folder_path === rootPath)?.folder_name ?? shortPath(rootPath),
      components,
      status: running ? 'running' : folderStatus(components),
      running,
      completedComponents: components.filter((component) => component.status === 'succeeded').length,
      operations: components.reduce((total, component) => total + component.operations, 0),
      completedOperations: components.reduce((total, component) => total + component.completed_operations, 0),
    }
  })
})

function partitionText(partition: string): string {
  return PARTITION_TEXT[partition as keyof typeof PARTITION_TEXT] ?? partition
}

/**
 * What this session covered, when it was scoped. A revision still runs once, so
 * a scoped run spends it: the folders it left out are not pending here, they are
 * the next plan's work, and the panel has to say so rather than let the reader
 * find out from a missing card.
 */
const scope = computed(() => {
  const selected = props.view.selected_folders ?? []
  const total = props.members?.length ?? 0
  if (selected.length === 0 || total === 0 || selected.length >= total) return null
  return { selected: selected.length, total }
})
</script>

<template>
  <section aria-label="执行结果" class="px-3 py-2" data-testid="execution-panel">
    <div class="flex flex-wrap items-center gap-2 rounded-lg bg-muted/30 px-3 py-2.5">
      <Badge :tone="status.tone" size="md" data-testid="execution-status">{{ status.label }}</Badge>
      <span class="font-mono text-[11px]" data-testid="execution-progress">
        组件 {{ view.completed_components }}/{{ view.total_components }} · 操作 {{ view.completed_operations }}/{{ view.total_operations }}
      </span>
      <Badge tone="neutral">{{ view.options?.delete_mode === 'hard' ? '硬删除' : '软删除' }}</Badge>
      <span v-if="running && view.current_root" class="font-mono text-[10px] text-[var(--text-muted)]" :title="view.current_root">
        {{ shortPath(view.current_root) }}
        <template v-if="view.current_component_id"> · {{ view.current_component_id }}</template>
      </span>
    </div>

    <p v-if="view.error_code" class="mt-1 text-[11px] text-[var(--danger-ink)]" role="alert" data-testid="execution-error">
      执行失败：{{ sessionFailureText(view.error_code, view.error_message) }}
    </p>
    <p v-if="view.status === 'interrupted'" class="mt-1 text-[11px] text-[var(--danger-ink)]">
      执行被后端中断（进程重启）：已完成的组件结果保留，未执行的操作保持磁盘原样。需重新生成并确认新版本才能继续。
    </p>
    <p v-else-if="view.status === 'canceled'" class="mt-1 text-[11px] text-[var(--warning-ink)]">
      执行已取消：已完成的组件结果保留，未执行的操作保持磁盘原样。
    </p>

    <p
      v-if="scope"
      class="mt-2 rounded-lg bg-[var(--warning-weak,var(--muted))] px-3 py-2 text-[11px]"
      data-testid="execution-scope"
      role="status"
    >
      本次只执行了选中的 {{ scope.selected }} 个文件夹（记录共 {{ scope.total }} 个）；其余文件夹需重新生成计划后执行。
    </p>

    <ul v-if="cards.length > 0" class="mt-3 space-y-3">
      <li
        v-for="card in cards"
        :key="card.rootPath"
        class="overflow-hidden rounded-xl border border-border bg-card"
        :class="card.running ? 'border-[var(--brand-border)]' : ''"
        data-testid="execution-folder"
        :data-folder-status="card.status"
      >
        <!-- The folder is the card's headline: what the user selected, and what
             a result is judged by. -->
        <div class="border-b border-border/60 bg-muted/30 px-3 py-3">
          <div class="flex items-start gap-2.5">
            <Folder class="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span
              class="min-w-0 flex-1 line-clamp-2 break-words font-heading text-xs font-semibold leading-relaxed"
              :title="card.rootPath"
              data-testid="execution-folder-name"
            >
              {{ card.name }}
            </span>
            <Badge :tone="FOLDER_STATUS[card.status]!.tone" class="shrink-0" data-testid="execution-folder-status">
              {{ FOLDER_STATUS[card.status]!.label }}
            </Badge>
          </div>
          <div class="mt-2 pl-6.5 text-[11px] tabular-nums text-muted-foreground" data-testid="execution-folder-counts">
            组件 {{ card.completedComponents }}/{{ card.components.length }} · 操作
            {{ card.completedOperations }}/{{ card.operations }}
          </div>
        </div>

        <ul>
          <li
            v-for="component in card.components"
            :key="component.component_id"
            class="border-b border-border/60 px-3 py-2.5 last:border-b-0"
            data-testid="execution-component"
          >
            <div class="flex flex-wrap items-center gap-1.5">
              <span class="text-[11px] font-medium">{{ partitionText(component.partition) }}</span>
              <Badge :tone="COMPONENT_STATUS[component.status].tone">{{ COMPONENT_STATUS[component.status].label }}</Badge>
              <span class="ml-auto text-[11px] tabular-nums text-muted-foreground">
                操作 {{ component.completed_operations }}/{{ component.operations }}
              </span>
              <span v-if="component.stage" class="text-[10px] text-[var(--danger-ink)]">
                停在{{ STAGE_LABELS[component.stage] ?? component.stage }}
              </span>
            </div>

            <p v-if="component.error_code" class="mt-1 text-[11px] text-[var(--danger-ink)]">
              {{ component.error_message || component.error_code }}
            </p>

            <details
              v-if="component.committed.length || component.removed.length || component.remaining.length || keptFiles(component).length"
              class="group mt-2 rounded-md bg-muted/20 text-[11px]"
            >
              <summary class="flex cursor-pointer list-none flex-wrap items-center gap-x-2 gap-y-1 rounded-md px-2 py-1.5 text-muted-foreground hover:bg-muted/50 focus-visible:outline-2 focus-visible:outline-ring [&::-webkit-details-marker]:hidden">
                <ChevronRight class="size-3 shrink-0 transition-transform group-open:rotate-90" aria-hidden="true" />
                <span>文件明细</span>
                <span v-if="component.committed.length">写入 {{ component.committed.length }}</span>
                <span v-if="component.removed.length">清理 {{ component.removed.length }}</span>
                <span v-if="component.remaining.length">未执行 {{ component.remaining.length }}</span>
                <span v-if="keptFiles(component).length">保留 {{ keptFiles(component).length }}</span>
              </summary>
              <dl class="space-y-3 border-t border-border/50 px-2 py-2">
                <template
                  v-for="group in [
                    { label: '已写入', paths: component.committed, testId: 'execution-committed' },
                    { label: removedHint, paths: component.removed, testId: 'execution-removed' },
                    { label: '未执行', paths: component.remaining, testId: 'execution-remaining' },
                  ]"
                  :key="group.testId"
                >
                  <div v-if="group.paths.length">
                    <dt class="mb-1 text-muted-foreground">{{ group.label }}</dt>
                    <dd class="space-y-1" :data-testid="group.testId">
                      <span v-for="path in group.paths" :key="path" class="block break-all font-mono leading-relaxed" :title="path">
                        {{ filePath(path, component.root_path) }}
                      </span>
                    </dd>
                  </div>
                </template>
                <div v-if="keptFiles(component).length">
                  <dt class="mb-1 text-muted-foreground">保留文件</dt>
                  <dd class="space-y-2" data-testid="execution-kept">
                    <div v-for="kept in keptFiles(component)" :key="kept.path">
                      <span class="block break-all font-mono leading-relaxed" :title="kept.path">{{ filePath(kept.path, component.root_path) }}</span>
                      <span class="text-[10px] text-muted-foreground">{{ keepReasonText(kept.reason_code) }}</span>
                    </div>
                  </dd>
                </div>
              </dl>
            </details>
            <p v-if="component.inventory_sync_error" class="mt-1 text-[10px] text-[var(--warning-ink)]" data-testid="execution-inventory-warning">
              磁盘结果与媒体库索引未完全同步：{{ component.inventory_sync_error }}
            </p>
          </li>
        </ul>
      </li>
    </ul>
  </section>
</template>
