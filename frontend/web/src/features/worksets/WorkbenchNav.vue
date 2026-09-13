<script setup lang="ts">
import { computed, watch } from 'vue'
import { ChevronDown, ChevronRight } from '@lucide/vue'
import { RouterLink, useRoute } from 'vue-router'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import { useWorkbenchNavStore } from '@/stores/workbench-nav'
import { workbenchNav, workbenchNavPosition, type WorkbenchNavId } from './workbench-nav'

/**
 * The workbench's contextual navigation (N05, N20, N24-N27).
 *
 * One list, two carriers: the inline sidebar and the narrow drawer render this
 * same component, always with labels — the sidebar is either shown in full or
 * hidden as a whole (N04, §10.4), so there is no icon-only mode to style here.
 * 转换's label is a link (it navigates) and the arrow beside it is a separate
 * button that only folds the group — two adjacent controls, never a link
 * wrapping a button.
 */
const props = defineProps<{
  worksetId: string
  /** Why 转换全局设置 is unavailable (generating / orphaned / historical, E09). */
  settingsBlockedReason: string | null
}>()

const route = useRoute()
const editor = useWorksetEditorStore()
const nav = useWorkbenchNavStore()
const items = computed(() => workbenchNav(props.worksetId))
const position = computed(() => workbenchNavPosition(route.name))

// Entering 转换全局设置 unfolds its group, by direct load or by navigation
// (N27). The watch is on the route name, never on query data, so a collapse
// the user made afterwards is not undone by an unrelated refetch.
watch(
  () => position.value?.current,
  (current) => {
    if (current === 'conversion-settings') nav.setGroupCollapsed('conversion', false)
  },
  { immediate: true },
)

function rowClass(current: boolean, parent: boolean, child = false): string {
  return [
    'flex items-center gap-2 rounded-md px-2 text-xs focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none',
    child ? 'ml-4' : 'min-w-0 flex-1',
    // Touch devices get the 44px minimum at any width — the tablet tiers are
    // touch too, so this is a pointer query, not a width one (N25, F15).
    'h-8 pointer-coarse:min-h-11',
    current ? 'bg-sidebar-accent font-medium' : parent ? 'bg-sidebar-accent/60' : 'hover:bg-sidebar-accent',
  ].join(' ')
}

function isCollapsed(id: WorkbenchNavId): boolean {
  return nav.isGroupCollapsed(id)
}
</script>

<template>
  <ul class="p-2">
    <li v-for="item in items" :key="item.id">
      <div class="flex items-stretch gap-0.5">
        <RouterLink
          :to="item.to"
          :aria-current="position?.current === item.id ? 'page' : undefined"
          :class="rowClass(position?.current === item.id, position?.parent === item.id)"
          :data-testid="`nav-${item.id}`"
        >
          <span aria-hidden="true">{{ item.icon }}</span>
          <span class="min-w-0 flex-1 truncate">{{ item.label }}</span>
          <!-- An unapplied edit is never hidden behind the section switch (E04). -->
          <span
            v-if="item.id === 'conversion' && editor.isDirty"
            class="size-1.5 shrink-0 rounded-full bg-[var(--warning-ink)]"
            title="有未应用的修改"
            data-testid="nav-dirty"
          />
        </RouterLink>
        <!-- The fold control is its own button: activating 转换 navigates, the
             arrow only folds (N25). No empty arrow when there is nothing below
             (N24). -->
        <button
          v-if="(item.children?.length ?? 0) > 0"
          type="button"
          :aria-expanded="!isCollapsed(item.id)"
          :aria-controls="`nav-children-${item.id}`"
          :aria-label="`${isCollapsed(item.id) ? '展开' : '收起'}${item.label}`"
          :class="[
            'flex shrink-0 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none',
            'h-8 w-7 pointer-coarse:min-h-11 pointer-coarse:min-w-11',
          ]"
          :data-testid="`nav-group-${item.id}`"
          @click="nav.setGroupCollapsed(item.id, !isCollapsed(item.id))"
        >
          <ChevronDown v-if="!isCollapsed(item.id)" class="size-4" />
          <ChevronRight v-else class="size-4" />
        </button>
      </div>

      <ul
        v-if="(item.children?.length ?? 0) > 0"
        :id="`nav-children-${item.id}`"
        v-show="!isCollapsed(item.id)"
      >
        <li v-for="child in item.children" :key="child.id">
          <RouterLink
            v-if="!settingsBlockedReason || child.id !== 'conversion-settings'"
            :to="child.to"
            :aria-current="position?.current === child.id ? 'page' : undefined"
            :class="rowClass(position?.current === child.id, false, true)"
            :data-testid="`nav-${child.id}`"
          >
            <span aria-hidden="true">{{ child.icon }}</span>
            {{ child.label }}
          </RouterLink>
          <!-- E09: a frozen, orphaned or historical operation disables the edit
               entry instead of letting the form fail on save. -->
          <span
            v-else
            class="ml-4 flex h-8 cursor-not-allowed items-center gap-2 rounded-md px-2 text-xs text-[var(--text-muted)] opacity-70 pointer-coarse:min-h-11"
            aria-disabled="true"
            :title="settingsBlockedReason"
            :data-testid="`nav-${child.id}`"
          >
            {{ child.label }}
          </span>
        </li>
      </ul>
    </li>
  </ul>
</template>
