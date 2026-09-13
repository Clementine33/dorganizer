<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
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
 * Modal side sheet on the same Reka Dialog primitive as Modal: a 380px
 * right-aligned carrier for the middle container tier (§7.3). Modal semantics,
 * Esc handling and focus restoration come from the primitive, and the closed
 * sheet unmounts entirely — the wide tier renders its own inline detail
 * instead, so a detail is never mounted twice (F08, F09).
 */
const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    description?: string
    class?: HTMLAttributes['class']
  }>(),
  { description: undefined },
)

const emit = defineEmits<{ close: [] }>()

function onOpenChange(value: boolean) {
  if (!value) emit('close')
}
</script>

<template>
  <DialogRoot :open="open" @update:open="onOpenChange">
    <DialogPortal>
      <DialogOverlay
        class="fixed inset-0 z-50 bg-black/40 data-[state=closed]:animate-out data-[state=open]:animate-in data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
      />
      <DialogContent
        :class="
          cn(
            'fixed inset-y-0 right-0 z-50 flex w-[min(380px,100vw)] flex-col gap-3 overflow-y-auto border-l border-border bg-card p-4 shadow-xl',
            'pb-[max(1rem,env(safe-area-inset-bottom))] data-[state=closed]:animate-out data-[state=open]:animate-in data-[state=closed]:slide-out-to-right data-[state=open]:slide-in-from-right',
            props.class,
          )
        "
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <DialogTitle class="font-heading text-sm font-semibold tracking-tight">{{ title }}</DialogTitle>
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
