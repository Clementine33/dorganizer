<script setup lang="ts">
import {
  SelectContent, SelectIcon, SelectItem, SelectItemText, SelectPortal,
  SelectRoot, SelectTrigger, SelectValue, SelectViewport,
} from 'reka-ui'

defineOptions({ inheritAttrs: false })
defineProps<{
  value: string
  options: { value: string; label: string }[]
  disabled?: boolean
}>()
const emit = defineEmits<{ change: [value: string] }>()
// Reka reserves the empty string for its placeholder; our codec fields use it
// for the selectable “不需要” value. Keep that UI sentinel out of the draft.
const NONE = '__none'
</script>

<template>
  <SelectRoot
    :model-value="value || NONE"
    :disabled="disabled"
    @update:model-value="emit('change', $event === NONE ? '' : String($event))"
  >
    <SelectTrigger
      v-bind="$attrs"
      class="inline-flex max-w-full items-center justify-between gap-2 disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-ring"
    >
      <SelectValue class="truncate" />
      <SelectIcon class="shrink-0" aria-hidden="true">▾</SelectIcon>
    </SelectTrigger>
    <SelectPortal>
      <SelectContent
        position="popper"
        align="start"
        :side-offset="4"
        :collision-padding="8"
        class="z-50 max-h-[var(--reka-select-content-available-height)] min-w-[var(--reka-select-trigger-width)] max-w-[calc(100vw-16px)] overflow-y-auto rounded-md border border-[var(--control-border)] bg-popover p-1 text-xs text-popover-foreground shadow-md"
      >
        <SelectViewport>
          <SelectItem
            v-for="option in options"
            :key="option.value"
            :value="option.value || NONE"
            class="cursor-default rounded-sm px-2 py-2 outline-none data-[highlighted]:bg-accent data-[highlighted]:text-accent-foreground data-[state=checked]:font-semibold"
          >
            <SelectItemText>{{ option.label }}</SelectItemText>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>
