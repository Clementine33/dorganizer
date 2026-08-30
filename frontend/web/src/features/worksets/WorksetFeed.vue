<script setup lang="ts">
import { computed } from 'vue'
import { Layers } from '@lucide/vue'
import type { Library, Workset } from '@/lib/api/types'
import type { WorksetFeedFilter } from '@/lib/api/types'
import {
  formatWorksetTime,
  generationStatusLabel,
  planningStateLabel,
  planningStateTone,
  summaryReasonLabel,
  toneClass,
  worksetBucket,
} from './workset-status'

const props = defineProps<{
  worksets: Workset[]
  libraries: Library[]
  activeFilter: WorksetFeedFilter
  activeLibraryId: string | null
  selectedId: string | null
  selectedExcludedByFilter: boolean
  hasMore: boolean
  loadingMore: boolean
  initialLoading: boolean
  error: string | null
  errorCode: string | null
}>()

const emit = defineEmits<{
  select: [worksetId: string]
  'update:activeFilter': [filter: WorksetFeedFilter]
  'update:activeLibraryId': [libraryId: string | null]
  'load-more': []
  retry: []
}>()

const filters: { value: WorksetFeedFilter; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'pending', label: '待处理' },
  { value: 'normal', label: '正常' },
  { value: 'error', label: '异常' },
]

const activeLibraryName = computed(() => {
  if (!props.activeLibraryId) return '全部媒体库'
  return props.libraries.find((l) => l.id === props.activeLibraryId)?.name ?? '全部媒体库'
})

function bucketTone(bucket: 'pending' | 'normal' | 'error') {
  return bucket === 'error' ? 'bad' : bucket === 'pending' ? 'info' : 'ok'
}
</script>

<template>
  <aside class="flex min-h-0 w-72 shrink-0 flex-col border-r border-border bg-card" aria-label="工作集列表" data-testid="workset-feed">
    <div class="border-b border-border px-3 py-2.5">
      <div class="flex items-center justify-between">
        <span class="font-heading text-xs font-semibold">工作集</span>
        <span class="font-mono text-[10px] text-muted-foreground">{{ worksets.length }}</span>
      </div>
      <div class="mt-2 flex flex-wrap gap-1" role="group" aria-label="状态筛选">
        <button
          v-for="f in filters"
          :key="f.value"
          type="button"
          class="rounded-full border px-2 py-0.5 text-[10px] font-medium"
          :class="
            activeFilter === f.value
              ? 'border-foreground/30 bg-foreground/10 text-foreground'
              : 'border-border text-muted-foreground hover:text-foreground'
          "
          :data-testid="`feed-filter-${f.value}`"
          @click="emit('update:activeFilter', f.value)"
        >
          {{ f.label }}
        </button>
      </div>
      <select
        v-if="libraries.length"
        :value="activeLibraryId ?? ''"
        aria-label="按媒体库筛选"
        class="mt-2 h-7 w-full rounded-md border border-input bg-background px-2 text-[11px] font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        data-testid="feed-library-filter"
        @change="emit('update:activeLibraryId', ($event.target as HTMLSelectElement).value || null)"
      >
        <option value="">全部媒体库</option>
        <option v-for="library in libraries" :key="library.id" :value="library.id">{{ library.name }}</option>
      </select>
      <p v-else-if="activeLibraryId" class="mt-1 text-[10px] text-muted-foreground">{{ activeLibraryName }}</p>
    </div>

    <div v-if="error" class="border-b border-destructive/30 bg-destructive/10 px-3 py-2 text-[11px] text-destructive" data-testid="worksets-error">
      <p class="font-mono font-semibold">{{ errorCode ?? 'ERROR' }}</p>
      <p class="mt-0.5 leading-4">{{ error }}</p>
      <button
        data-testid="retry-worksets"
        class="mt-1 rounded-md border border-destructive/40 px-2 py-0.5 text-[10px] font-semibold hover:bg-destructive/10"
        @click="emit('retry')"
      >
        重试
      </button>
    </div>

    <p
      v-if="selectedExcludedByFilter"
      class="border-b border-amber-500/40 bg-amber-500/10 px-3 py-1.5 text-[10px] text-amber-700 dark:text-amber-400"
      data-testid="feed-selected-excluded"
    >
      当前选中的工作集不符合筛选条件
    </p>

    <div v-if="initialLoading" class="flex-1 space-y-2 p-3" data-testid="worksets-loading">
      <div v-for="i in 4" :key="i" class="h-16 animate-pulse rounded-md bg-muted" />
    </div>

    <div v-else-if="worksets.length === 0" class="flex flex-1 flex-col items-center justify-center px-6 text-center" data-testid="worksets-empty">
      <Layers class="size-7 text-muted-foreground" />
      <p class="mt-3 text-xs font-medium">还没有工作集</p>
      <p class="mt-1 text-[11px] leading-4 text-muted-foreground">在媒体库中选择专辑批次，创建第一个工作集。</p>
    </div>

    <div v-else class="min-h-0 flex-1 overflow-y-auto p-2">
      <button
        v-for="ws in worksets"
        :key="ws.workset_id"
        type="button"
        class="mt-0.5 w-full rounded-md border px-2.5 py-2 text-left"
        :class="ws.workset_id === selectedId ? 'border-foreground/25 bg-accent' : 'border-transparent hover:bg-sidebar-accent'"
        :data-testid="`workset-item-${ws.workset_id}`"
        @click="emit('select', ws.workset_id)"
      >
        <div class="flex items-center gap-2">
          <span class="min-w-0 flex-1 truncate font-heading text-xs font-semibold">{{ ws.title }}</span>
          <span class="shrink-0 rounded-full px-1.5 py-0.5 text-[9px] font-semibold" :class="toneClass[planningStateTone[ws.planning_state]]">
            {{ planningStateLabel[ws.planning_state] }}
          </span>
        </div>
        <div class="mt-1 flex items-center gap-2 text-[10px] text-muted-foreground">
          <span class="truncate">{{ ws.library?.name ?? '无所属媒体库' }}</span>
          <span class="shrink-0">{{ ws.members.length }} 个批次</span>
        </div>
        <div class="mt-1 flex flex-wrap items-center gap-1">
          <span
            v-if="ws.current_revision"
            class="rounded-full px-1.5 py-0.5 text-[9px] font-semibold"
            :class="toneClass[bucketTone(worksetBucket(ws))]"
          >
            v{{ ws.current_revision.revision_index + 1 }}
            <template v-if="ws.current_revision.summary_reason">
              · {{ summaryReasonLabel[ws.current_revision.summary_reason] ?? ws.current_revision.summary_reason }}
            </template>
          </span>
          <span
            v-if="ws.active_generation"
            class="rounded-full bg-sky-500/15 px-1.5 py-0.5 text-[9px] font-semibold text-sky-600 dark:text-sky-400"
            data-testid="feed-active-generation"
          >
            {{ generationStatusLabel[ws.active_generation.status] ?? ws.active_generation.status }}
            {{ ws.active_generation.completed_roots }}/{{ ws.active_generation.total_roots }}
          </span>
          <span
            v-if="ws.latest_generation && !ws.active_generation && ws.latest_generation.status !== 'completed'"
            class="rounded-full px-1.5 py-0.5 text-[9px] font-semibold"
            :class="toneClass[ws.latest_generation.status === 'failed' ? 'bad' : 'warn']"
          >
            {{ generationStatusLabel[ws.latest_generation.status] ?? ws.latest_generation.status }}
          </span>
        </div>
        <p class="mt-1 text-[9px] text-muted-foreground">{{ formatWorksetTime(ws.updated_at) }}</p>
      </button>

      <button
        v-if="hasMore"
        type="button"
        class="mt-2 w-full rounded-md border border-border py-1.5 text-[11px] font-medium text-muted-foreground hover:text-foreground disabled:opacity-50"
        data-testid="load-more-worksets"
        :disabled="loadingMore"
        @click="emit('load-more')"
      >
        {{ loadingMore ? '加载中…' : '加载更多' }}
      </button>
    </div>
  </aside>
</template>
