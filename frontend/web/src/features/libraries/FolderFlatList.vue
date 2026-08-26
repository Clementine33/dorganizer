<script setup lang="ts">
import { useVirtualizer } from '@tanstack/vue-virtual'
import { ChevronRight, Folder } from '@lucide/vue'
import { computed, ref } from 'vue'
import type { Folder as LibraryFolder } from '@/lib/api/types'

// Fixed row height is load-bearing for the virtualizer's spacer math: every
// cell is single-line/truncated, so no row can exceed h-14. If a future cell
// starts wrapping, update this constant and the row class together.
const ROW_HEIGHT = 56
const OVERSCAN = 10

const props = defineProps<{
  folders: LibraryFolder[]
  selectedIds: string[]
  allSelected: boolean
  scanStatus: string
}>()
const emit = defineEmits<{
  select: [id: string, selected: boolean]
  selectAll: [selected: boolean]
  open: [id: string]
}>()

const scrollEl = ref<HTMLElement | null>(null)

// @tanstack/vue-virtual owns the scroll observation (it attaches its own
// scroll/resize listeners once getScrollElement resolves) and re-renders
// reactively after each scroll; the total set is count rows + the header.
const virtualizer = useVirtualizer(
  computed(() => ({
    count: props.folders.length,
    getScrollElement: () => scrollEl.value,
    estimateSize: () => ROW_HEIGHT,
    overscan: OVERSCAN,
  })),
)
// Header (1) + every folder row; used for table-wide aria-setsize.
const totalRows = computed(() => props.folders.length + 1)

function checkboxValue(event: Event): boolean {
  return (event.target as HTMLInputElement).checked
}

function folderAt(index: number): LibraryFolder {
  return props.folders[index]
}

function statusLabel(status: string): string {
  if (status === 'completed') return '已扫描'
  if (status === 'failed') return '扫描失败'
  if (status === 'canceled' || status === 'cancelled') return '已取消'
  return '未扫描'
}
</script>

<template>
  <div class="flex h-full min-w-[680px] flex-col" role="table" aria-label="音频文件夹">
    <div
      class="grid h-9 shrink-0 grid-cols-[38px_minmax(220px,1.6fr)_minmax(180px,1fr)_110px_92px_28px] items-center border-y border-border bg-muted/35 px-3 text-[11px] font-medium uppercase tracking-[0.08em] text-muted-foreground"
      role="row"
      :aria-rowindex="1"
      :aria-setsize="totalRows"
    >
      <div role="columnheader">
        <input
          data-testid="select-all-folders"
          type="checkbox"
          :checked="allSelected"
          aria-label="选择全部文件夹"
          class="size-3.5 accent-[var(--ring)]"
          @change="emit('selectAll', checkboxValue($event))"
        />
      </div>
      <div role="columnheader">文件夹</div>
      <div role="columnheader">相对路径</div>
      <div role="columnheader" class="text-right">音频</div>
      <div role="columnheader" class="text-right">上次扫描</div>
      <div />
    </div>

    <div
      ref="scrollEl"
      data-testid="folder-list-scroller"
      class="min-h-0 flex-1 overflow-auto"
      role="presentation"
    >
      <div
        data-testid="folder-list-spacer"
        class="relative w-full"
        :style="{ height: `${virtualizer.getTotalSize()}px` }"
      >
        <div
          v-for="virtualItem in virtualizer.getVirtualItems()"
          :key="folderAt(virtualItem.index).id"
          :style="{ transform: `translateY(${virtualItem.start}px)` }"
          :aria-rowindex="virtualItem.index + 2"
          :aria-setsize="totalRows"
          role="row"
          class="group grid h-14 grid-cols-[38px_minmax(220px,1.6fr)_minmax(180px,1fr)_110px_92px_28px] items-center border-b border-border px-3 transition-colors hover:bg-accent/45"
        >
          <div role="cell">
            <input
              :data-testid="`folder-checkbox-${folderAt(virtualItem.index).id}`"
              type="checkbox"
              :checked="selectedIds.includes(folderAt(virtualItem.index).id)"
              :aria-label="`选择 ${folderAt(virtualItem.index).name}`"
              class="size-3.5 accent-[var(--ring)]"
              @change="emit('select', folderAt(virtualItem.index).id, checkboxValue($event))"
            />
          </div>
          <button
            :data-testid="`folder-link-${folderAt(virtualItem.index).id}`"
            type="button"
            class="flex min-w-0 items-center gap-2 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            @click="emit('open', folderAt(virtualItem.index).id)"
          >
            <Folder class="size-4 shrink-0 text-muted-foreground" />
            <span class="truncate font-heading text-sm font-semibold">{{ folderAt(virtualItem.index).name }}</span>
          </button>
          <div role="cell" class="min-w-0">
            <span class="inline-block max-w-full truncate rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
              {{ folderAt(virtualItem.index).relative_path }}
            </span>
          </div>
          <div role="cell" class="text-right font-mono text-xs text-muted-foreground">
            {{ folderAt(virtualItem.index).audio_file_count }} 个音频文件
          </div>
          <div role="cell" class="text-right">
            <span
              class="inline-flex rounded-full border px-2 py-0.5 text-[10px]"
              :class="scanStatus === 'failed' ? 'border-destructive/40 text-destructive' : 'border-border text-muted-foreground'"
            >
              {{ statusLabel(scanStatus) }}
            </span>
          </div>
          <button
            type="button"
            class="rounded p-1 text-muted-foreground opacity-60 hover:text-foreground group-hover:opacity-100 focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            :aria-label="`打开 ${folderAt(virtualItem.index).name}`"
            @click="emit('open', folderAt(virtualItem.index).id)"
          >
            <ChevronRight class="size-3.5" />
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
