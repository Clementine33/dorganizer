<script setup lang="ts">
import { ref } from 'vue'
import { ChevronDown, ChevronRight, TriangleAlert } from '@lucide/vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import type { PlanFolderError } from '@/lib/api/types'

const props = defineProps<{
  errors: PlanFolderError[]
}>()

const open = ref(false)
</script>

<template>
  <Card data-testid="folder-errors-panel" size="sm">
    <CardHeader class="border-b border-border">
      <button
        data-testid="toggle-folder-errors"
        type="button"
        class="flex w-full items-center gap-2 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        :aria-expanded="open"
        @click="open = !open"
      >
        <TriangleAlert class="size-4 shrink-0" :class="props.errors.length ? 'text-destructive' : 'text-muted-foreground'" />
        <CardTitle class="text-xs">文件夹错误</CardTitle>
        <span
          class="ml-auto shrink-0 font-mono text-[11px] font-normal"
          :class="props.errors.length ? 'text-destructive' : 'text-muted-foreground'"
        >
          {{ props.errors.length }}
        </span>
        <ChevronDown v-if="open" class="size-3.5 text-muted-foreground" />
        <ChevronRight v-else class="size-3.5 text-muted-foreground" />
      </button>
    </CardHeader>
    <div v-if="open" data-testid="folder-errors-content">
      <CardContent v-if="props.errors.length" class="flex flex-col gap-1 p-2">
        <div
          v-for="error in props.errors"
          :key="`${error.folder_path}-${error.code}`"
          class="flex min-h-10 items-start gap-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2"
        >
          <div class="min-w-0 flex-1">
            <p class="truncate font-mono text-[11px] text-foreground/90">{{ error.folder_path }}</p>
            <p class="mt-0.5 text-xs text-muted-foreground">{{ error.message }}</p>
          </div>
          <div class="flex shrink-0 flex-col items-end gap-1">
            <span class="rounded border border-destructive/40 px-1.5 py-0.5 font-mono text-[10px] text-destructive">
              {{ error.code }}
            </span>
            <span class="font-mono text-[10px] text-muted-foreground">
              {{ error.retryable ? '可重试' : '不可重试' }}
            </span>
          </div>
        </div>
      </CardContent>
      <CardContent v-else class="px-4 py-2 text-xs text-muted-foreground">没有文件夹错误。</CardContent>
    </div>
  </Card>
</template>