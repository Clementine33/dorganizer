<script setup lang="ts">
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, FileSearch, LoaderCircle, RefreshCw } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import FolderErrorsPanel from '@/features/plans/FolderErrorsPanel.vue'
import OperationsTable from '@/features/plans/OperationsTable.vue'
import PlanSummaryCards from '@/features/plans/PlanSummaryCards.vue'
import { planStatusLabel } from '@/features/plans/plan-status'
import { useApiClient } from '@/lib/api/client'
import { errorDetails } from '@/lib/api/error'
import { findCachedPlanInfo, planDetailQueryOptions } from '@/queries/plans'

const api = useApiClient()
const route = useRoute()
const router = useRouter()
const queryClient = useQueryClient()

const planId = computed(() => route.params.id as string)

// Durable plan detail: a freshly created plan was seeded into this key before
// navigation (so the review renders immediately), and a cold deep link or
// reload fetches GET /plans/:id normally.
const detailQuery = useQuery(() => planDetailQueryOptions(api, planId.value))
const detail = computed(() => detailQuery.data.value ?? null)
const detailPending = computed(() => detailQuery.isPending.value)
const detailError = computed(() => {
  const error = detailQuery.error.value
  return error ? errorDetails(error) : null
})

// A background refetch failure must not hide the cached content: keep the
// detail visible and surface a non-blocking warning instead.
const refreshWarning = computed(() => (detail.value && detailError.value ? detailError.value : null))

// Status pill comes from cached plan-list metadata when available; a freshly
// created (cached) detail has no status field on the durable snapshot, so it
// falls back to `ready` — the same behavior the pre-migration page had when a
// plan response was in memory (review accepted this fallback).
//
// findCachedPlanInfo reads the cache imperatively (getQueriesData is not
// reactive), so the computed would otherwise freeze at its first evaluation:
// a list refresh, an invalidation, or the create-mutation dropping the list
// cache would never reach the pill. Bump a ref on every query-cache write to
// re-derive the pill from the current cache.
const cacheEvents = ref(0)
let unsubscribeCache: (() => void) | null = null
onMounted(() => {
  unsubscribeCache = queryClient.getQueryCache().subscribe((event) => {
    // Only plan-list writes can change the pill's metadata; ignore every
    // other cache event so unrelated query activity does not re-scan all
    // plan lists on each write.
    if (event.query.queryKey[0] === 'plans' && event.query.queryKey[1] === 'list') {
      cacheEvents.value++
    }
  })
})
onBeforeUnmount(() => {
  unsubscribeCache?.()
})
const planInfo = computed(() => {
  void cacheEvents.value
  return findCachedPlanInfo(queryClient, planId.value)
})
const status = computed(() => planInfo.value?.status ?? (detail.value ? 'ready' : ''))
</script>

<template>
  <section class="flex h-full min-w-0 flex-col bg-background">
    <header class="flex min-h-14 shrink-0 items-center gap-3 border-b border-border px-5">
      <Button
        data-testid="back-to-plans"
        variant="ghost"
        size="icon"
        aria-label="返回计划列表"
        @click="router.push('/plans')"
      >
        <ArrowLeft class="size-4" />
      </Button>
      <div class="min-w-0">
        <nav class="flex items-center gap-1.5 text-xs text-muted-foreground" aria-label="面包屑">
          <span class="font-heading font-semibold text-foreground">计划审阅</span>
          <span aria-hidden="true">/</span>
          <span class="truncate font-mono">{{ planId }}</span>
        </nav>
        <p class="truncate font-mono text-[10px] text-muted-foreground/70">
          {{ detail?.root_path ?? planInfo?.root_path ?? '' }}
        </p>
      </div>
      <div class="ml-auto">
        <span
          v-if="status"
          data-testid="review-status-pill"
          class="inline-flex rounded-full border border-border px-2.5 py-1 text-[11px] text-muted-foreground"
        >
          {{ planStatusLabel(status) }}
        </span>
      </div>
    </header>

    <template v-if="detail">
      <div
        v-if="refreshWarning"
        data-testid="plan-detail-refresh-warning"
        class="flex items-center gap-3 border-b border-amber-300/40 bg-amber-500/10 px-5 py-2 text-xs text-amber-600"
      >
        <span v-if="refreshWarning.code" class="shrink-0 font-mono font-semibold">{{ refreshWarning.code }}</span>
        <span class="min-w-0 flex-1">刷新详情失败，显示的是上次内容。{{ refreshWarning.message }}</span>
      </div>
      <div class="grid min-h-0 flex-1 auto-rows-min gap-4 overflow-auto p-4">
        <PlanSummaryCards :summary="detail.summary" />
        <OperationsTable
          :operations="detail.operations"
          :successful-folders="detail.successful_folders"
        />
        <FolderErrorsPanel :errors="detail.errors" />
      </div>
    </template>

    <div
      v-else-if="detailPending"
      data-testid="plan-detail-loading"
      class="grid min-h-0 flex-1 place-items-center text-xs text-muted-foreground"
    >
      <div class="flex items-center gap-2">
        <LoaderCircle class="size-4 animate-spin" />
        正在读取计划详情…
      </div>
    </div>

    <div v-else-if="detailError" data-testid="plan-detail-error" class="grid min-h-0 flex-1 place-items-center px-6 text-center">
      <div class="max-w-sm">
        <FileSearch class="mx-auto size-7 text-muted-foreground" />
        <h2 class="mt-3 font-heading text-base font-semibold">
          {{ detailError.code === 'PLAN_NOT_FOUND' ? '计划不存在' : '计划详情读取失败' }}
        </h2>
        <p class="mt-1 text-xs leading-5 text-muted-foreground">
          <span v-if="detailError.code" class="mr-1 font-mono">{{ detailError.code }}</span>{{ detailError.message }}。
          当前计划状态：{{ planInfo ? planStatusLabel(planInfo.status) : '未知' }}。
        </p>
        <div class="mt-4 flex justify-center gap-2">
          <Button data-testid="retry-plan-detail" size="sm" @click="detailQuery.refetch">
            <RefreshCw class="size-3.5" />
            重试
          </Button>
          <Button variant="outline" size="sm" @click="router.push('/plans')">返回计划列表</Button>
        </div>
      </div>
    </div>

    <div v-else class="grid min-h-0 flex-1 place-items-center px-6 text-center">
      <div class="max-w-sm">
        <FileSearch class="mx-auto size-7 text-muted-foreground" />
        <h2 class="mt-3 font-heading text-base font-semibold">计划详情不可用</h2>
        <p class="mt-1 text-xs leading-5 text-muted-foreground">
          当前计划状态：{{ planInfo ? planStatusLabel(planInfo.status) : '未知' }}。
        </p>
        <Button class="mt-4" size="sm" @click="router.push('/plans')">返回计划列表</Button>
      </div>
    </div>
  </section>
</template>