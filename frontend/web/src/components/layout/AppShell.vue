<script setup lang="ts">
import { AudioLines } from '@lucide/vue'
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import HeaderMenu from '@/components/layout/HeaderMenu.vue'
import { GLOBAL_NAV, globalNavOwner, type GlobalNavId } from '@/components/layout/global-nav'
import { useLibraryList } from '@/queries/libraries'

/**
 * The application shell owns the whole application height and the global
 * navigation space: a 64px icon rail above 640px application
 * width, a two-entry bottom bar at or below it, and pages use the rest. The
 * switch is pure CSS (the `rail` variant in style.css); no page re-measures the
 * window and the bottom bar is a grid row, not an overlay that pages would have
 * to pad for.
 */
const route = useRoute()
// Ownership comes from the router's own route name, so a workbench
// child route still lights up 工作集 without a second active-id state.
const current = computed(() => globalNavOwner(route.name))

// The shell is mounted for the whole application lifetime, so it stays the
// long-lived observer of the library list: pages share the same cache entry
// and a scan's terminal refresh always has a live observer, whichever module
// is on screen. The list itself renders on the workbench entry page, not here.
useLibraryList()

function isCurrent(id: GlobalNavId): boolean {
  return current.value === id
}
</script>

<template>
  <div
    class="rail:grid-cols-[64px_minmax(0,1fr)] rail:grid-rows-[minmax(0,1fr)] grid h-dvh grid-cols-1 grid-rows-[minmax(0,1fr)_auto] overflow-hidden bg-background text-foreground"
  >
    <!-- Desktop rail: brand on top, the shared definition in the middle, the
         theme entry at the bottom. -->
    <aside
      class="rail:flex hidden min-h-0 flex-col border-r border-sidebar-border bg-sidebar"
      data-testid="global-rail"
    >
      <div class="grid h-14 shrink-0 place-items-center" data-testid="app-brand">
        <AudioLines class="size-5 text-[var(--ring)]" aria-hidden="true" />
        <span class="sr-only">Onsei Organizer</span>
      </div>
      <nav aria-label="全局导航" class="flex flex-col gap-1 p-1">
        <RouterLink
          v-for="item in GLOBAL_NAV"
          :key="item.id"
          :to="item.to"
          :aria-current="isCurrent(item.id) ? 'page' : undefined"
          class="flex flex-col items-center gap-1 rounded-md px-1 py-2 text-[10px] font-medium text-sidebar-foreground hover:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-sidebar-ring focus-visible:outline-none"
          :class="isCurrent(item.id) ? 'bg-sidebar-accent' : ''"
        >
          <component :is="item.icon" class="size-4" aria-hidden="true" />
          {{ item.label }}
        </RouterLink>
      </nav>
      <div class="mt-auto p-1">
        <HeaderMenu />
      </div>
    </aside>

    <main class="min-h-0 min-w-0 overflow-y-auto">
      <slot />
    </main>

    <!-- Mobile bottom bar: the same two entries in the shell's own grid row.
        The outermost edge owns the safe area once. -->
    <nav
      aria-label="全局导航"
      class="rail:hidden grid grid-cols-2 border-t border-sidebar-border bg-sidebar pb-[env(safe-area-inset-bottom)]"
      data-testid="global-bottom-bar"
    >
      <RouterLink
        v-for="item in GLOBAL_NAV"
        :key="item.id"
        :to="item.to"
        :aria-current="isCurrent(item.id) ? 'page' : undefined"
        class="flex min-h-14 flex-col items-center justify-center gap-1 text-[10px] font-medium text-sidebar-foreground hover:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-sidebar-ring focus-visible:outline-none"
        :class="isCurrent(item.id) ? 'bg-sidebar-accent' : ''"
      >
        <component :is="item.icon" class="size-5" aria-hidden="true" />
        {{ item.label }}
      </RouterLink>
    </nav>
  </div>
</template>
