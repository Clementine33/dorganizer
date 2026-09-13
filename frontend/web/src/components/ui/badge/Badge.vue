<script setup lang="ts">
import type { HTMLAttributes } from 'vue'
import { computed } from 'vue'
import { cn } from '@/lib/utils'

/**
 * Status badge. Tones map to role tokens only; a badge always carries text, so
 * status is never communicated by color alone.
 */
const props = withDefaults(
  defineProps<{
    tone?: 'neutral' | 'brand' | 'success' | 'warning' | 'danger' | 'info'
    size?: 'sm' | 'md'
    class?: HTMLAttributes['class']
  }>(),
  { tone: 'neutral', size: 'sm' },
)

const TONES: Record<string, string> = {
  neutral: 'border-border bg-muted text-[var(--text-secondary)]',
  brand: 'border-[var(--brand-border)] bg-[var(--brand-weak)] text-[var(--brand-ink)]',
  success: 'border-[var(--success-border)] bg-[var(--success-weak)] text-[var(--success-ink)]',
  warning: 'border-[var(--warning-border)] bg-[var(--warning-weak)] text-[var(--warning-ink)]',
  danger: 'border-[var(--danger-border)] bg-[var(--danger-weak)] text-[var(--danger-ink)]',
  info: 'border-[var(--info-border)] bg-[var(--info-weak)] text-[var(--info-ink)]',
}

const classes = computed(() =>
  cn(
    'inline-flex items-center gap-1 rounded-full border font-medium whitespace-nowrap',
    props.size === 'sm' ? 'h-5 px-2 text-[11px]' : 'h-6 px-2.5 text-xs',
    TONES[props.tone] ?? TONES.neutral,
    props.class,
  ),
)
</script>

<template>
  <span data-slot="badge" :class="classes">
    <slot />
  </span>
</template>
