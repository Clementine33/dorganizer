<script setup lang="ts">
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import { sessionFailureText } from '@/features/worksets/session-failure'
import type { ExecutionComponent, ExecutionStatus, ExecutionView } from '@/lib/api/types'

/**
 * Live progress and the observed result of one execution session (M3). Every
 * number here is a count that actually happened — no estimated percentage —
 * and a failure keeps its partial facts: completed components, the operations
 * that never ran, and the files preserved for an operator.
 */
const props = defineProps<{ view: ExecutionView }>()

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
</script>

<template>
  <section aria-label="执行结果" class="px-3 py-2" data-testid="execution-panel">
    <div class="flex flex-wrap items-center gap-2">
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

    <ul v-if="view.components.length > 0" class="mt-2 space-y-1.5">
      <li
        v-for="component in view.components"
        :key="component.component_id"
        class="rounded-md border border-border p-2"
        data-testid="execution-component"
      >
        <div class="flex flex-wrap items-center gap-1.5">
          <Badge :tone="COMPONENT_STATUS[component.status].tone">{{ COMPONENT_STATUS[component.status].label }}</Badge>
          <span class="min-w-0 truncate font-mono text-[10px] text-[var(--text-secondary)]" :title="component.root_path">
            {{ shortPath(component.root_path) }}
          </span>
          <span class="font-mono text-[10px] text-[var(--text-muted)]">
            操作 {{ component.completed_operations }}/{{ component.operations }}
          </span>
          <span v-if="component.stage" class="text-[10px] text-[var(--danger-ink)]">
            停在{{ STAGE_LABELS[component.stage] ?? component.stage }}
          </span>
        </div>

        <p v-if="component.error_code" class="mt-1 text-[11px] text-[var(--danger-ink)]">
          {{ component.error_message || component.error_code }}
        </p>

        <dl class="mt-1 space-y-0.5 text-[10px]">
          <div v-if="component.committed.length > 0" class="flex gap-1.5">
            <dt class="w-16 shrink-0 text-[var(--text-muted)]">已写入</dt>
            <dd class="min-w-0 flex-1">
              <span v-for="path in component.committed" :key="path" class="block truncate font-mono" :title="path">{{ path }}</span>
            </dd>
          </div>
          <div v-if="component.removed.length > 0" class="flex gap-1.5">
            <dt class="w-16 shrink-0 text-[var(--text-muted)]">{{ removedHint }}</dt>
            <dd class="min-w-0 flex-1">
              <span v-for="path in component.removed" :key="path" class="block truncate font-mono" :title="path">{{ path }}</span>
            </dd>
          </div>
          <div v-if="component.remaining.length > 0" class="flex gap-1.5">
            <dt class="w-16 shrink-0 text-[var(--text-muted)]">未执行</dt>
            <dd class="min-w-0 flex-1" data-testid="execution-remaining">
              <span v-for="path in component.remaining" :key="path" class="block truncate font-mono" :title="path">{{ path }}</span>
            </dd>
          </div>
          <div v-if="component.recovery.length > 0" class="flex gap-1.5">
            <dt class="w-16 shrink-0 text-[var(--text-muted)]">保留文件</dt>
            <dd class="min-w-0 flex-1" data-testid="execution-recovery">
              <span v-for="path in component.recovery" :key="path" class="block truncate font-mono" :title="path">{{ path }}</span>
            </dd>
          </div>
        </dl>
        <p v-if="component.inventory_sync_error" class="mt-1 text-[10px] text-[var(--warning-ink)]" data-testid="execution-inventory-warning">
          磁盘结果与媒体库索引未完全同步：{{ component.inventory_sync_error }}
        </p>
      </li>
    </ul>
  </section>
</template>
