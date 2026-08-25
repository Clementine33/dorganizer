<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, FileSearch } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import FolderErrorsPanel from '@/features/plans/FolderErrorsPanel.vue'
import OperationsTable from '@/features/plans/OperationsTable.vue'
import PlanSummaryCards from '@/features/plans/PlanSummaryCards.vue'
import { planStatusLabel } from '@/features/plans/plan-status'
import { usePlansStore } from '@/stores/plans'

const route = useRoute()
const router = useRouter()
const plans = usePlansStore()

const planId = computed(() => route.params.id as string)
const currentPlan = computed(() =>
  plans.currentPlan && plans.currentPlan.plan_id === planId.value ? plans.currentPlan : null,
)
const planInfo = computed(() => plans.plans.find((item) => item.plan_id === planId.value) ?? null)
const status = computed(() => planInfo.value?.status ?? (currentPlan.value ? 'ready' : ''))
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
          {{ currentPlan?.root_path ?? planInfo?.root_path ?? '' }}
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

    <template v-if="currentPlan">
      <div class="grid min-h-0 flex-1 auto-rows-min gap-4 overflow-auto p-4">
        <PlanSummaryCards :summary="currentPlan.summary" />
        <OperationsTable
          :operations="currentPlan.operations"
          :successful-folders="currentPlan.successful_folders"
        />
        <FolderErrorsPanel :errors="currentPlan.errors" />
      </div>
    </template>

    <div v-else class="grid min-h-0 flex-1 place-items-center px-6 text-center">
      <div class="max-w-sm">
        <FileSearch class="mx-auto size-7 text-muted-foreground" />
        <h2 class="mt-3 font-heading text-base font-semibold">计划详情不可用</h2>
        <p class="mt-1 text-xs leading-5 text-muted-foreground">
          后端不提供单计划详情接口；重新生成计划后即可在这里审阅操作与错误。
          当前计划状态：{{ planInfo ? planStatusLabel(planInfo.status) : '未知' }}。
        </p>
        <Button class="mt-4" size="sm" @click="router.push('/plans')">返回计划列表</Button>
      </div>
    </div>
  </section>
</template>