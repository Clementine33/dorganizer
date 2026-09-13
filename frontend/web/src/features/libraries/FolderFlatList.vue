<script setup lang="ts">
import { useVirtualizer } from '@tanstack/vue-virtual'
import { ChevronRight, Folder } from '@lucide/vue'
import { computed, ref } from 'vue'
import type { Folder as LibraryFolder } from '@/lib/api/types'

/**
 * The folder list (L03, L05, L06).
 *
 * One template, two layouts: the desktop tier keeps the four-column grid
 * (checkbox, name, audio count, view) and the phone tier merges it into a
 * compact row — checkbox, name with the count under it, view action. The width
 * classes never force a minimum, so nothing is hidden behind a horizontal
 * overflow (the old min-w-[680px] is gone); the count that used to sit under a
 * second line is the same element with a `rail:` variant.
 *
 * Fixed row height is load-bearing for the virtualizer's spacer math and is the
 * SAME in both tiers, so crossing a breakpoint never invalidates a measured
 * offset. Every cell is single-line/truncated, so no row can exceed h-14; if a
 * future cell starts wrapping, update this constant and the row class together.
 */
const ROW_HEIGHT = 56
const OVERSCAN = 10

const props = defineProps<{
  folders: LibraryFolder[]
  selectedIds: string[]
  allSelected: boolean
}>()
const emit = defineEmits<{
  select: [id: string, selected: boolean]
  selectAll: [selected: boolean]
  open: [id: string]
}>()

const scrollEl = ref<HTMLElement | null>(null)

// @tanstack/vue-virtual owns the scroll observation (it attaches its own
// scroll/resize listeners once getScrollElement resolves) and re-renders
// reactively after each scroll; the window is a slice of the same single data
// source in every layout (L05: no second mobile list).
const virtualizer = useVirtualizer(
  computed(() => ({
    count: props.folders.length,
    getScrollElement: () => scrollEl.value,
    estimateSize: () => ROW_HEIGHT,
    overscan: OVERSCAN,
  })),
)

function checkboxValue(event: Event): boolean {
  return (event.target as HTMLInputElement).checked
}

function folderAt(index: number): LibraryFolder {
  return props.folders[index]
}
</script>

<template>
  <div class="flex h-full min-w-0 flex-col" data-testid="folder-list">
    <!-- Desktop keeps the column labels; the phone tier shows the count and the
         select-all control instead, because the rows are merged there. -->
    <div
      class="grid h-9 shrink-0 grid-cols-[44px_minmax(0,1fr)_44px] items-center border-y border-border bg-muted/35 px-2 text-[11px] font-medium tracking-[0.08em] text-muted-foreground uppercase rail:grid-cols-[38px_minmax(220px,1fr)_110px_92px] rail:px-3"
    >
      <label class="flex h-full w-full cursor-pointer items-center justify-center rail:justify-start">
        <input
          data-testid="select-all-folders"
          type="checkbox"
          :checked="allSelected"
          aria-label="选择全部文件夹"
          class="size-4 accent-[var(--ring)] rail:size-3.5"
          @change="emit('selectAll', checkboxValue($event))"
        />
      </label>
      <div class="min-w-0 truncate">
        <span class="hidden rail:inline">文件夹</span>
        <span class="rail:hidden" data-testid="folder-count">共 {{ folders.length }} 个文件夹</span>
      </div>
      <div class="hidden text-right rail:block">音频</div>
      <div class="hidden rail:block" />
    </div>

    <div ref="scrollEl" data-testid="folder-list-scroller" class="min-h-0 flex-1 overflow-y-auto">
      <ul
        data-testid="folder-list-spacer"
        class="relative m-0 w-full list-none p-0"
        :style="{ height: `${virtualizer.getTotalSize()}px` }"
      >
        <li
          v-for="virtualItem in virtualizer.getVirtualItems()"
          :key="folderAt(virtualItem.index).id"
          :style="{ transform: `translateY(${virtualItem.start}px)` }"
          class="absolute top-0 left-0 grid h-14 w-full grid-cols-[44px_minmax(0,1fr)_44px] items-center overflow-hidden border-b border-border px-2 transition-colors hover:bg-accent/45 rail:grid-cols-[38px_minmax(220px,1fr)_110px_92px] rail:px-3"
        >
          <label class="flex h-full w-full cursor-pointer items-center justify-center rail:justify-start">
            <input
              :data-testid="`folder-checkbox-${folderAt(virtualItem.index).id}`"
              type="checkbox"
              :checked="selectedIds.includes(folderAt(virtualItem.index).id)"
              :aria-label="`选择 ${folderAt(virtualItem.index).name}`"
              class="size-4 accent-[var(--ring)] rail:size-3.5"
              @change="emit('select', folderAt(virtualItem.index).id, checkboxValue($event))"
            />
          </label>
          <button
            :data-testid="`folder-link-${folderAt(virtualItem.index).id}`"
            type="button"
            class="flex min-w-0 items-center gap-2 text-left focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
            @click="emit('open', folderAt(virtualItem.index).id)"
          >
            <Folder class="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span class="min-w-0 flex-1">
              <span
                class="block truncate font-heading text-sm font-semibold"
                :title="folderAt(virtualItem.index).name"
              >
                {{ folderAt(virtualItem.index).name }}
              </span>
              <!-- The phone tier's second line; the desktop tier has its own
                   column for the same fact. -->
              <span class="block truncate font-mono text-[11px] text-muted-foreground rail:hidden">
                {{ folderAt(virtualItem.index).audio_file_count }} 个音频文件
              </span>
            </span>
          </button>
          <div class="hidden text-right font-mono text-xs text-muted-foreground rail:block">
            {{ folderAt(virtualItem.index).audio_file_count }} 个音频文件
          </div>
          <!-- Always on screen, never hover-only (L06); the phone tier keeps a
               44px target while the desktop tier stays compact. -->
          <button
            type="button"
            class="flex h-full w-full items-center justify-center rounded text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none rail:h-8 rail:w-8"
            :aria-label="`打开 ${folderAt(virtualItem.index).name}`"
            @click="emit('open', folderAt(virtualItem.index).id)"
          >
            <ChevronRight class="size-4" aria-hidden="true" />
          </button>
        </li>
      </ul>
    </div>
  </div>
</template>
