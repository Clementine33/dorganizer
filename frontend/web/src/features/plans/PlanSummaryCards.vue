<script setup lang="ts">
import { CircleCheck, CircleX, ListChecks } from '@lucide/vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import type { PlanSummary } from '@/lib/api/types'

defineProps<{
  summary: PlanSummary
}>()
</script>

<template>
  <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
    <Card data-testid="summary-actionable" size="sm">
      <CardHeader class="flex flex-row items-center gap-2 !py-3">
        <CircleCheck class="size-4 text-[var(--brand)]" />
        <CardTitle class="text-xs">可操作项</CardTitle>
      </CardHeader>
      <CardContent class="font-mono text-xl font-semibold">{{ summary.actionable_count }}</CardContent>
    </Card>
    <Card data-testid="summary-errors" size="sm">
      <CardHeader class="flex flex-row items-center gap-2 !py-3">
        <CircleX class="size-4" :class="summary.error_count ? 'text-destructive' : 'text-muted-foreground'" />
        <CardTitle class="text-xs">文件夹错误</CardTitle>
      </CardHeader>
      <CardContent
        class="font-mono text-xl font-semibold"
        :class="summary.error_count ? 'text-destructive' : ''"
      >
        {{ summary.error_count }}
      </CardContent>
    </Card>
    <Card data-testid="summary-operations" size="sm">
      <CardHeader class="flex flex-row items-center gap-2 !py-3">
        <ListChecks class="size-4 text-muted-foreground" />
        <CardTitle class="text-xs">操作总数</CardTitle>
      </CardHeader>
      <CardContent class="font-mono text-xl font-semibold">{{ summary.operation_count }}</CardContent>
    </Card>
    <Card data-testid="summary-reason" size="sm">
      <CardHeader class="flex flex-row items-center gap-2 !py-3">
        <CircleCheck class="size-4 text-muted-foreground" />
        <CardTitle class="text-xs">计划结论</CardTitle>
      </CardHeader>
      <CardContent>
        <span class="inline-flex rounded border border-border bg-muted px-2 py-0.5 font-mono text-xs">
          {{ summary.summary_reason }}
        </span>
      </CardContent>
    </Card>
  </div>
</template>