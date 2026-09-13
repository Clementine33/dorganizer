<script setup lang="ts">
import { Layers, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'

/**
 * The page's batch bar (L07). It belongs to a selection, so the page mounts it
 * only while something is selected (N15) — there is never an empty strip above
 * the bottom bar. It sits in the page's own column, so the bottom bar and this
 * bar never overlap (N08, N15).
 */
defineProps<{
  selectedCount: number
  loading: boolean
}>()
const emit = defineEmits<{
  clear: []
  generate: []
}>()
</script>

<template>
  <div
    class="flex min-h-14 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-t border-border bg-card px-4 py-1.5 shadow-[0_-8px_24px_rgba(0,0,0,0.12)] rail:px-5"
  >
    <div class="h-5 w-1 shrink-0 rounded-full bg-[var(--ring)]" aria-hidden="true" />
    <div class="min-w-0">
      <p class="font-heading text-xs font-semibold">批量工作台</p>
      <p class="font-mono text-[11px] text-muted-foreground">已选择 {{ selectedCount }} 个文件夹</p>
    </div>
    <div class="ml-auto flex shrink-0 items-center gap-2">
      <Button
        data-testid="clear-selection"
        class="h-11 rail:h-7"
        variant="ghost"
        size="sm"
        :disabled="loading"
        @click="emit('clear')"
      >
        <X class="size-3.5" />
        清除
      </Button>
      <Button
        data-testid="create-workset"
        class="h-11 rail:h-7"
        size="sm"
        :disabled="loading"
        @click="emit('generate')"
      >
        <Layers class="size-3.5" />
        {{ loading ? '创建中…' : `创建工作集 (${selectedCount})` }}
      </Button>
    </div>
  </div>
</template>
