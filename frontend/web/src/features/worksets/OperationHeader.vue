<script setup lang="ts">
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import type { Operation, PlanningState, RevisionCounts } from '@/lib/api/types'

/**
 * Compact operation context: workspace/operation identity, version state and
 * the five independent summary facts (F16). Blocking, unmet-target and stale
 * notices are never hidden for density.
 */
const props = defineProps<{
  title: string
  libraryName: string | null
  operation: Operation | null
  /** Independent plan facts of the revision being shown, when it has one. */
  counts: RevisionCounts | null
  validationState: string | null
  /** Label of the revision being shown; null while editing the current draft. */
  revisionLabel: string | null
  confirmed: boolean
  generating: boolean
  canGenerate: boolean
  /** Why confirmation is unavailable right now (null when it is available). */
  confirmBlocked: string | null
  /** The server's refusal of the last confirmation attempt, if any. */
  error: string | null
  busy: boolean
}>()

const emit = defineEmits<{
  generate: []
  cancel: []
  confirm: []
  'restore-current': []
}>()

const STATE_LABELS: Record<PlanningState, { label: string; tone: 'neutral' | 'brand' | 'success' | 'warning' | 'danger' }> = {
  unplanned: { label: '待规划', tone: 'neutral' },
  planning: { label: '规划中', tone: 'brand' },
  planned: { label: '已规划', tone: 'success' },
  needs_planning: { label: '需重新规划', tone: 'warning' },
  orphaned: { label: '媒体库已删除（只读）', tone: 'danger' },
}

const state = computed(() =>
  props.operation ? STATE_LABELS[props.operation.planning_state] : { label: '未建立', tone: 'neutral' as const },
)

const cells = computed(() =>
  props.counts
    ? [
        { label: '成员', value: props.counts.members },
        { label: '有变化', value: props.counts.changed },
        { label: '目标未满足', value: props.counts.unmet_targets },
        { label: '阻塞', value: props.counts.blocked },
        { label: '无变化', value: props.counts.unchanged },
      ]
    : [],
)
const validationWarning = computed(() => {
  if (props.validationState === 'stale') return '输入已变化，当前版本需要重新规划后才能确认'
  if (props.validationState === 'unavailable') return '媒体库不可用，无法校验当前版本'
  return null
})
</script>

<template>
  <header class="border-b border-border bg-card px-3 py-2" data-testid="operation-header">
    <div class="flex flex-wrap items-center gap-2">
      <div class="min-w-0">
        <h1 class="truncate font-heading text-sm font-semibold tracking-tight">{{ title }}</h1>
        <p v-if="libraryName" class="truncate font-mono text-[10px] text-[var(--text-muted)]">{{ libraryName }}</p>
      </div>
      <Badge :tone="state.tone" size="md">{{ state.label }}</Badge>
      <Badge v-if="operation" tone="neutral">操作版本 v{{ operation.version }}</Badge>
      <Badge v-if="revisionLabel" tone="neutral">{{ revisionLabel }}</Badge>
      <Badge v-if="confirmed" tone="success">已确认</Badge>

      <div class="ml-auto flex flex-wrap items-center gap-1.5">
        <Button v-if="operation?.active_generation" variant="destructive" size="sm" :disabled="busy" @click="emit('cancel')">
          取消生成
        </Button>
        <Button v-else size="sm" :disabled="!canGenerate || busy" data-testid="start-generation" @click="emit('generate')">
          生成计划版本
        </Button>
        <Button
          v-if="operation?.current_revision && !revisionLabel"
          :variant="confirmed ? 'secondary' : 'default'"
          size="sm"
          :disabled="busy || confirmBlocked !== null"
          :title="confirmBlocked ?? undefined"
          data-testid="confirm-revision"
          @click="emit('confirm')"
        >
          {{ confirmed ? '确认（已确认）' : '确认当前版本' }}
        </Button>
        <Button v-if="revisionLabel" variant="ghost" size="sm" @click="emit('restore-current')">回到当前草稿</Button>
      </div>
    </div>

    <div v-if="cells.length" class="mt-2 flex flex-wrap gap-4" data-testid="operation-counts">
      <div v-for="cell in cells" :key="cell.label" class="min-w-14">
        <div class="font-mono text-sm">{{ cell.value }}</div>
        <div class="text-[10px] text-[var(--text-muted)]">{{ cell.label }}</div>
      </div>
    </div>
    <p v-else class="mt-2 text-[11px] text-[var(--text-muted)]">尚无计划版本：当前操作待规划。</p>

    <p v-if="validationWarning" class="mt-1 text-[11px] text-[var(--warning-ink)]" data-testid="validation-warning">
      {{ validationWarning }}
    </p>
    <!-- Why 确认当前版本 is unavailable is stated, never left to a disabled button (F16, T14). -->
    <p v-if="confirmBlocked" class="mt-1 text-[11px] text-[var(--warning-ink)]" data-testid="confirm-blocked">
      暂不能确认：{{ confirmBlocked }}。
    </p>
    <p v-if="error" class="mt-1 text-[11px] text-[var(--danger-ink)]" role="alert" data-testid="confirm-error">
      {{ error }}
    </p>
    <p v-if="operation?.latest_generation?.status === 'failed'" class="mt-1 text-[11px] text-[var(--danger-ink)]">
      上次生成失败：{{ operation.latest_generation.error_message || operation.latest_generation.error_code }}
    </p>
    <p v-if="operation?.active_generation" class="mt-1 text-[11px] text-[var(--text-secondary)]">
      生成中：{{ operation.active_generation.completed_roots }}/{{ operation.active_generation.total_roots }}
      <span v-if="operation.active_generation.current_root" class="font-mono">
        · {{ operation.active_generation.current_root }}
      </span>
    </p>
  </header>
</template>
