<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { computed } from 'vue'
import { X } from '@lucide/vue'
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'
import { cn } from '@/lib/utils'

/**
 * Modal dialog on Reka's Dialog primitive: modal semantics, focus trap, Esc to
 * close the topmost layer, and focus restoration to the trigger on close come
 * from the primitive itself. `title` is required so every dialog carries an
 * accessible name (F08).
 */
const props = defineProps<{
  open: boolean
  title: string
  description?: string
  class?: HTMLAttributes['class']
}>()

const contentClass = computed(() =>
  cn(
    'fixed top-1/2 left-1/2 z-50 grid w-[min(32rem,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 gap-3',
    'max-h-[calc(100dvh-2rem)] overflow-y-auto rounded-lg border border-border bg-card p-5 shadow-lg',
    props.class,
  ),
)

const emit = defineEmits<{ 'update:open': [boolean]; close: [] }>()

function onOpenChange(value: boolean) {
  emit('update:open', value)
  if (!value) emit('close')
}
</script>

<template>
  <DialogRoot :open="open" @update:open="onOpenChange">
    <DialogPortal>
      <DialogOverlay
        class="fixed inset-0 z-50 bg-black/50 data-[state=closed]:animate-out data-[state=open]:animate-in data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
      />
      <DialogContent :class="contentClass">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <DialogTitle class="font-heading text-base font-semibold tracking-tight">{{ title }}</DialogTitle>
            <DialogDescription v-if="description" class="mt-0.5 text-[11px] text-[var(--text-muted)]">
              {{ description }}
            </DialogDescription>
          </div>
          <DialogClose
            class="inline-flex size-7 shrink-0 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-muted focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none"
            aria-label="关闭"
          >
            <X class="size-4" />
          </DialogClose>
        </div>
        <slot />
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
