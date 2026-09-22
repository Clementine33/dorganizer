<script setup lang="ts">
import { Monitor, Moon, MoreHorizontal, Sun } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useTheme, type Theme } from '@/composables/use-theme'

/**
 * The header menu.
 *
 * The shared theme options always come last, so every entry is the same theme
 * state and action. A page may pass secondary actions into the default slot —
 * that is how the Libraries toolbar keeps its management actions reachable on a
 * phone without a third button row — while the desktop rail renders the menu
 * with the theme options only.
 */
withDefaults(defineProps<{ trigger?: 'theme' | 'more' }>(), { trigger: 'theme' })

const { theme } = useTheme()
const options: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: 'light', label: '浅色', icon: Sun },
  { value: 'dark', label: '深色', icon: Moon },
  { value: 'system', label: '跟随系统', icon: Monitor },
]
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button
        variant="ghost"
        size="icon"
        class="size-11 rail:size-8"
        :aria-label="trigger === 'more' ? '更多' : '切换主题'"
      >
        <MoreHorizontal v-if="trigger === 'more'" class="size-4" />
        <template v-else>
          <Sun class="hidden size-4 dark:block" />
          <Moon class="size-4 dark:hidden" />
        </template>
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end">
      <slot />
      <DropdownMenuSeparator v-if="$slots.default" />
      <DropdownMenuItem v-for="option in options" :key="option.value" @select="theme = option.value">
        <component :is="option.icon" class="size-4" />
        {{ option.label }}
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
