<script setup lang="ts">
import { AudioLines, LibraryBig, ListMusic, Monitor, Moon, Sun } from '@lucide/vue'
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useTheme, type Theme } from '@/composables/use-theme'
import { useLibraryList } from '@/queries/libraries'
import { useLibraryUiStore } from '@/stores/library-ui'

const router = useRouter()
const ui = useLibraryUiStore()
// The shell is mounted for the whole application lifetime, so this is the
// long-lived observer of the library list; pages share the same cache entry.
const { librariesData: libraries } = useLibraryList()
const { theme } = useTheme()
const options: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: 'light', label: '浅色', icon: Sun },
  { value: 'dark', label: '深色', icon: Moon },
  { value: 'system', label: '跟随系统', icon: Monitor },
]

function openLibrary(id: string) {
  ui.setActiveLibrary(id)
  void router.push('/libraries')
}
</script>

<template>
  <div class="flex h-dvh flex-col overflow-hidden bg-background text-foreground">
    <header class="flex h-11 shrink-0 items-center gap-2 border-b border-border bg-card px-3">
      <AudioLines class="size-4 text-[var(--ring)]" />
      <span class="font-heading text-sm font-semibold tracking-tight">Onsei Organizer</span>
      <span class="hidden text-[11px] text-muted-foreground sm:inline">library workbench</span>
      <div class="ml-auto">
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button variant="ghost" size="icon" aria-label="切换主题">
              <Sun class="hidden size-4 dark:block" />
              <Moon class="size-4 dark:hidden" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem v-for="option in options" :key="option.value" @select="theme = option.value">
              <component :is="option.icon" class="size-4" />
              {{ option.label }}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>

    <div class="grid min-h-0 flex-1 grid-cols-1 md:grid-cols-[196px_minmax(0,1fr)]">
      <aside class="hidden min-h-0 flex-col border-r border-sidebar-border bg-sidebar md:flex">
        <nav class="p-2" aria-label="主导航">
          <RouterLink
            to="/libraries"
            class="flex h-8 items-center gap-2 rounded-md px-2 text-xs font-medium text-sidebar-foreground hover:bg-sidebar-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring"
            active-class="bg-sidebar-accent"
          >
            <LibraryBig class="size-3.5" />
            媒体库
          </RouterLink>
          <RouterLink
            to="/plans"
            class="mt-0.5 flex h-8 items-center gap-2 rounded-md px-2 text-xs font-medium text-sidebar-foreground hover:bg-sidebar-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring"
            active-class="bg-sidebar-accent"
          >
            <ListMusic class="size-3.5" />
            计划
          </RouterLink>
        </nav>

        <div class="mx-3 border-t border-sidebar-border" />
        <div class="min-h-0 flex-1 overflow-auto p-2">
          <div class="flex h-7 items-center px-2 text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
            媒体库条目
            <span class="ml-auto font-mono">{{ libraries.length }}</span>
          </div>
          <button
            v-for="library in libraries"
            :key="library.id"
            type="button"
            class="mt-0.5 flex w-full items-center gap-2 rounded-md px-2 py-2 text-left hover:bg-sidebar-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring"
            :class="library.id === ui.activeLibraryId ? 'bg-sidebar-accent' : ''"
            @click="openLibrary(library.id)"
          >
            <span
              class="size-1.5 shrink-0 rounded-full"
              :class="library.last_scan_status === 'completed' ? 'bg-emerald-500' : 'bg-muted-foreground/50'"
            />
            <span class="min-w-0">
              <span class="block truncate font-heading text-xs font-semibold">{{ library.name }}</span>
              <span class="block truncate font-mono text-[9px] text-muted-foreground">{{ library.root_path }}</span>
            </span>
          </button>
          <p v-if="libraries.length === 0" class="px-2 py-3 text-[11px] leading-4 text-muted-foreground">
            添加媒体库后会显示在这里。
          </p>
        </div>
      </aside>

      <main class="min-h-0 min-w-0">
        <slot />
      </main>
    </div>
  </div>
</template>
