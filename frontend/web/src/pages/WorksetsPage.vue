<script setup lang="ts">
import { computed } from 'vue'
import { useInfiniteQuery } from '@tanstack/vue-query'
import { RouterLink } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import HeaderMenu from '@/components/layout/HeaderMenu.vue'
import { useApiClient } from '@/lib/api/client'
import { worksetListInfiniteQueryOptions } from '@/queries/worksets'
import type { PlanningState } from '@/lib/api/types'

/**
 * Workset list (`/worksets`). Entering a workset is always an explicit click:
 * the list never navigates into the first entry by itself (R02, §5.1).
 */
const api = useApiClient()
const listQuery = useInfiniteQuery(worksetListInfiniteQueryOptions(api))
const worksets = computed(() => listQuery.data.value?.pages.flatMap((page) => page.worksets) ?? [])
const loading = computed(() => listQuery.isPending.value && !listQuery.data.value)

const STATE_LABELS: Record<PlanningState, string> = {
  unplanned: '待规划',
  planning: '规划中',
  planned: '已规划',
  needs_planning: '需重新规划',
  orphaned: '只读（媒体库已删除）',
}

const STATE_TONES: Record<PlanningState, 'neutral' | 'brand' | 'success' | 'warning' | 'danger'> = {
  unplanned: 'neutral',
  planning: 'brand',
  planned: 'success',
  needs_planning: 'warning',
  orphaned: 'danger',
}

function conversionState(workset: { operations: { operation_type: string; planning_state: PlanningState }[] }): PlanningState | null {
  const conversion = workset.operations.find((op) => op.operation_type === 'conversion')
  return conversion ? conversion.planning_state : null
}
</script>

<template>
  <div class="mx-auto max-w-3xl p-4" data-testid="worksets-page">
    <div class="mb-3 flex items-center gap-2">
      <h1 class="font-heading text-base font-semibold tracking-tight">工作集</h1>
      <span class="text-[11px] text-[var(--text-muted)]">选择一个工作集查看概览与转换操作</span>
      <!-- Mobile theme entry (N14): the rail is off screen at this width. -->
      <div class="ml-auto rail:hidden">
        <HeaderMenu trigger="more" />
      </div>
    </div>

    <p v-if="loading" class="text-xs text-[var(--text-muted)]">加载中…</p>
    <p v-else-if="listQuery.isError.value" class="text-xs text-[var(--danger-ink)]" role="alert">
      工作集列表加载失败：{{ (listQuery.error.value as Error).message }}
    </p>
    <p v-else-if="worksets.length === 0" class="text-xs text-[var(--text-muted)]" data-testid="worksets-empty">
      还没有工作集。在媒体库页面选择专辑文件夹后创建。
    </p>

    <ul class="space-y-1.5">
      <li v-for="workset in worksets" :key="workset.workset_id">
        <RouterLink
          :to="`/worksets/${encodeURIComponent(workset.workset_id)}`"
          class="flex items-center gap-2 rounded-lg border border-border bg-card px-3 py-2 hover:border-[var(--brand-border)] focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none"
          data-testid="workset-item"
        >
          <span class="min-w-0 flex-1">
            <span class="block truncate font-heading text-xs font-semibold">{{ workset.title }}</span>
            <span class="block truncate font-mono text-[10px] text-[var(--text-muted)]">
              {{ workset.members.length }} 个文件夹<template v-if="workset.library"> · {{ workset.library.name }}</template>
            </span>
          </span>
          <Badge v-if="conversionState(workset)" :tone="STATE_TONES[conversionState(workset)!]">
            {{ STATE_LABELS[conversionState(workset)!] }}
          </Badge>
        </RouterLink>
      </li>
    </ul>

    <Button v-if="listQuery.hasNextPage.value" variant="ghost" size="sm" class="mt-3" @click="listQuery.fetchNextPage()">
      加载更多
    </Button>
  </div>
</template>
