<script setup lang="ts">
import { computed } from 'vue'
import { Square, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import type { GenerationProgress as GenProgress } from '@/lib/api/types'
import { generationStatusLabelOf, toneClass } from './workset-status'

const props = defineProps<{
  progress: GenProgress
  canceling: boolean
}>()

const emit = defineEmits<{
  cancel: []
}>()

const percent = computed(() =>
  props.progress.total_roots > 0
    ? Math.round((props.progress.completed_roots / props.progress.total_roots) * 100)
    : 0,
)
</script>

<template>
  <div class="flex items-center gap-3 border-b border-border bg-sky-500/5 px-5 py-2.5" data-testid="generation-progress">
    <div class="min-w-0 flex-1">
      <div class="flex items-center gap-2">
        <span class="text-xs font-semibold">{{ generationStatusLabelOf(progress.status) }}</span>
        <span class="font-mono text-[10px] text-muted-foreground" data-testid="generation-progress-counts">
          {{ progress.completed_roots }}/{{ progress.total_roots }} 批次
        </span>
        <span v-if="progress.error_count > 0" class="rounded-full px-1.5 py-0.5 text-[9px] font-semibold" :class="toneClass.bad">
          {{ progress.error_count }} 个错误
        </span>
      </div>
      <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted">
        <div class="h-full rounded-full bg-sky-500 transition-all" :style="{ width: `${percent}%` }" />
      </div>
      <p v-if="progress.current_root" class="mt-1 truncate font-mono text-[10px] text-muted-foreground">
        {{ progress.current_root }}
      </p>
    </div>
    <Button
      data-testid="cancel-generation"
      variant="ghost"
      size="sm"
      :disabled="canceling"
      @click="emit('cancel')"
    >
      <Square class="size-3" />
      取消
    </Button>
    <span class="sr-only"><X /></span>
  </div>
</template>
