<script setup lang="ts">
import { Layers, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'

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
  <div class="flex min-h-14 items-center gap-3 border-t border-border bg-card px-5 shadow-[0_-8px_24px_rgba(0,0,0,0.12)]">
    <div class="h-5 w-1 rounded-full bg-[var(--ring)]" aria-hidden="true" />
    <div>
      <p class="font-heading text-xs font-semibold">批量工作台</p>
      <p class="font-mono text-[11px] text-muted-foreground">已选择 {{ selectedCount }} 个文件夹</p>
    </div>
    <Button v-if="selectedCount" class="ml-auto" variant="ghost" size="sm" :disabled="loading" @click="emit('clear')">
      <X class="size-3.5" />
      清除
    </Button>
    <Button
      data-testid="create-workset"
      :class="selectedCount ? '' : 'ml-auto'"
      size="sm"
      :disabled="selectedCount === 0 || loading"
      @click="emit('generate')"
    >
      <Layers class="size-3.5" />
      {{ loading ? '创建中…' : `创建工作集${selectedCount ? ` (${selectedCount})` : ''}` }}
    </Button>
  </div>
</template>
