<script setup lang="ts">
import { ChevronLeft, PanelLeftClose, PanelLeftOpen } from '@lucide/vue'
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, type RouteLocationRaw } from 'vue-router'
import { Button } from '@/components/ui/button'
import { useContainerTier } from '@/composables/use-container-tier'

/**
 * Container-adaptive workbench shell (§7.3, F11-F13, N09, N28).
 *
 * One named container decides every size in CSS at the shared breakpoints; the
 * composable observes the same element only to pick the interaction model
 * (inline sidebar vs drawer). Widths: <=640 hides the contextual nav behind a
 * drawer; above that it is the inline 200px sidebar, and the header toggle
 * collapses it to fully hidden — there is no icon-only rail (N04, §10.4).
 *
 * The narrow drawer is a Reka Dialog: modal semantics, focus containment over
 * the whole application (the global rail and bottom bar included), Esc on the
 * topmost layer and focus restoration come from the primitive (N17, F08). It
 * is deliberately not portalled — Reka hides every sibling of the content, so
 * the background becomes inert while the drawer stays inside the shell. The
 * named container lives on the inner column, never on the drawer's own
 * ancestor: `container-type` makes an element the containing block for fixed
 * descendants, which would clip the scrim to the shell box and leave the rail
 * and bottom bar undimmed (N17).
 */
withDefaults(
  defineProps<{
    navTitle?: string
    detailColumn?: boolean
    /** Narrow header context (N31): the subject and the page in view. */
    contextTitle?: string
    contextPage?: string
    /** Fixed parent of the current page, for the narrow header's back control
     *  (N32); it is a route, never a guess from history. */
    contextBackTo?: RouteLocationRaw
    contextBackLabel?: string
  }>(),
  {
    navTitle: '工作集',
    detailColumn: true,
    contextTitle: '',
    contextPage: '',
    contextBackTo: undefined,
    contextBackLabel: '',
  },
)

const route = useRoute()
const shell = ref<HTMLElement | null>(null)
const toggle = ref<InstanceType<typeof Button> | null>(null)
const { tier } = useContainerTier(shell)
// Wide containers default to the full 200px sidebar, mid containers to hidden
// (N04, §10.4); the user's toggle then wins until the tier changes.
const expanded = ref(true)
const drawerOpen = ref(false)
const narrow = computed(() => tier.value === 'narrow')

watch(tier, (value) => {
  expanded.value = value === 'wide'
  if (value !== 'narrow') drawerOpen.value = false
})

// The drawer closes once a page selection succeeded (N28). A navigation the
// unapplied-edit guard refused never changes the path, so the drawer, the form
// and the route all stay as they were.
watch(
  () => route.path,
  () => {
    drawerOpen.value = false
  },
)

function toggleNav() {
  if (narrow.value) drawerOpen.value = !drawerOpen.value
  else expanded.value = !expanded.value
}

/** Closing returns focus to the control that opened the drawer (N17). */
function restoreNavFocus(event: Event) {
  event.preventDefault()
  const element = toggle.value?.$el
  if (element instanceof HTMLElement) element.focus()
}

/** A tap on the page you are already on is a selection too (N28), and a
 *  duplicated navigation runs no guards, so closing here cannot hide a refused
 *  leave. Everything else waits for the route watcher. */
function onDrawerClick(event: MouseEvent) {
  const link = (event.target as HTMLElement | null)?.closest('a[href]')
  if (link && link.getAttribute('href') === route.path) drawerOpen.value = false
}
</script>

<template>
  <DialogRoot :open="narrow && drawerOpen" @update:open="(value: boolean) => (drawerOpen = value)">
    <div class="flex h-full min-h-0 flex-col overflow-hidden bg-background text-foreground">
      <header
        class="flex h-[var(--title-h)] shrink-0 items-center gap-2 border-b border-border bg-card px-3"
        data-testid="workbench-header"
      >
        <Button
          ref="toggle"
          variant="ghost"
          size="icon-sm"
          :aria-expanded="narrow ? drawerOpen : expanded"
          aria-controls="workbench-nav"
          :aria-label="narrow ? '打开导航' : expanded ? '收起导航' : '展开导航'"
          data-testid="workbench-nav-toggle"
          @click="toggleNav"
        >
          <PanelLeftClose v-if="!narrow && expanded" class="size-4" />
          <PanelLeftOpen v-else class="size-4" />
        </Button>

        <!-- Narrow: the local navigation button plus the subject and the page in
             view, two truncatable lines with their full text available (N31).
             The desktop tiers keep the complete breadcrumb. -->
        <div
          v-if="narrow && (contextTitle || contextPage)"
          class="flex min-w-0 flex-1 items-center gap-1.5"
          data-testid="workbench-context"
        >
          <RouterLink
            v-if="contextBackTo"
            :to="contextBackTo"
            :aria-label="`返回${contextBackLabel}`"
            class="flex size-11 shrink-0 items-center justify-center rounded-md text-[var(--text-secondary)] hover:bg-muted focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none"
            data-testid="workbench-back"
          >
            <ChevronLeft class="size-4" />
          </RouterLink>
          <div class="min-w-0 flex-1">
            <p class="truncate font-heading text-xs font-semibold" :title="contextTitle">{{ contextTitle }}</p>
            <p v-if="contextPage" class="truncate text-[10px] text-[var(--text-muted)]" :title="contextPage">
              {{ contextPage }}
            </p>
          </div>
        </div>
        <slot v-else name="header">
          <RouterLink
            to="/worksets"
            class="inline-flex items-center gap-1 rounded-md px-1.5 py-1 text-xs font-medium text-[var(--text-secondary)] hover:bg-muted focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none"
          >
            <ChevronLeft class="size-3.5" />
            工作集
          </RouterLink>
        </slot>
      </header>

      <!-- The named container for every §7.3 rule and for the tier this shell
           reports: one measurement source, and no containment on the drawer's
           ancestor. -->
      <div ref="shell" class="@container flex min-h-0 flex-1">
        <!-- Mid/wide: the inline contextual navigation. Collapsing hides the
             whole sidebar instead of shrinking it to an icon rail (N04, §10.4);
             the header toggle brings the same labelled list back. -->
        <nav
          v-if="!narrow"
          v-show="expanded"
          id="workbench-nav"
          :aria-label="navTitle"
          class="min-h-0 w-[200px] shrink-0 overflow-y-auto border-r border-border bg-sidebar"
          data-testid="workbench-nav"
        >
          <slot name="nav" :tier="tier" />
        </nav>

        <main class="flex min-h-0 min-w-0 flex-1" data-testid="workbench-main">
          <!-- The main area is a flex column, so a page's own `flex-1 min-h-0`
               column really fills the shell and scrolls inside itself: a list
               that owns its scrolling needs a definite height, and only this
               box can give it one. A page taller than the shell still scrolls
               here. -->
          <div class="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto">
            <slot name="main" :tier="tier" />
          </div>
          <!-- Wide tier keeps a non-modal, sibling detail: no focus lock, no
               duplicate mount with the modal carrier. -->
          <aside
            v-if="tier === 'wide' && detailColumn"
            class="hidden w-[380px] shrink-0 overflow-y-auto border-l border-border bg-card @[641px]:block"
            aria-label="详情"
            data-testid="workbench-detail-inline"
          >
            <slot name="detail" />
          </aside>
        </main>
      </div>

      <!-- The page's own bottom bar (a selection's actions): it sits below the
           shell's columns so it is reachable at every tier, and only the page
           that has one renders it. -->
      <slot name="bottom" />

      <!-- Mid tier: the same detail content in a modal drawer. -->
      <slot v-if="tier === 'mid'" name="detail-modal" />
    </div>

    <!-- Narrow: one level at a time, over the whole application. Clicking the
         page you are already on is still a selection, and it can never hide a
         refused leave (a duplicated navigation runs no guards), so it closes
         here; every other navigation closes through the route watcher above
         only once it succeeded (N28). -->
    <DialogPortal v-if="narrow" disabled>
      <DialogOverlay class="fixed inset-0 z-40 bg-black/40" data-testid="workbench-nav-scrim" />
      <DialogContent
        id="workbench-nav"
        class="fixed inset-y-0 left-0 z-50 w-[min(16rem,85vw)] overflow-y-auto border-r border-border bg-sidebar pb-[max(0.5rem,env(safe-area-inset-bottom))] shadow-xl"
        data-testid="workbench-nav-drawer"
        @close-auto-focus="restoreNavFocus"
        @click="onDrawerClick"
      >
        <DialogTitle class="sr-only">{{ navTitle }}</DialogTitle>
        <DialogDescription class="sr-only">工作台内的页面入口，一次展示一层</DialogDescription>
        <slot name="nav" :tier="tier" />
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
