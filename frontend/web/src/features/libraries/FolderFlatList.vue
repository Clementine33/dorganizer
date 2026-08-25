<script setup lang="ts">
import { ChevronRight, Folder } from '@lucide/vue'
import type { Folder as LibraryFolder } from '@/lib/api/types'

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

function checkboxValue(event: Event): boolean {
  return (event.target as HTMLInputElement).checked
}

function statusLabel(status: string): string {
  if (status === 'completed') return '已扫描'
  if (status === 'failed') return '扫描失败'
  if (status === 'canceled' || status === 'cancelled') return '已取消'
  return '未扫描'
}
</script>

<template>
  <div class="min-w-[680px]" role="table" aria-label="音频文件夹">
    <div
      class="grid h-9 grid-cols-[38px_minmax(220px,1.6fr)_minmax(180px,1fr)_110px_92px_28px] items-center border-y border-border bg-muted/35 px-3 text-[11px] font-medium uppercase tracking-[0.08em] text-muted-foreground"
      role="row"
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
      v-for="folder in props.folders"
      :key="folder.id"
      class="group grid min-h-14 grid-cols-[38px_minmax(220px,1.6fr)_minmax(180px,1fr)_110px_92px_28px] items-center border-b border-border px-3 transition-colors hover:bg-accent/45"
      role="row"
    >
      <div role="cell">
        <input
          :data-testid="`folder-checkbox-${folder.id}`"
          type="checkbox"
          :checked="selectedIds.includes(folder.id)"
          :aria-label="`选择 ${folder.name}`"
          class="size-3.5 accent-[var(--ring)]"
          @change="emit('select', folder.id, checkboxValue($event))"
        />
      </div>
      <button
        :data-testid="`folder-link-${folder.id}`"
        type="button"
        class="flex min-w-0 items-center gap-2 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        @click="emit('open', folder.id)"
      >
        <Folder class="size-4 shrink-0 text-muted-foreground" />
        <span class="truncate font-heading text-sm font-semibold">{{ folder.name }}</span>
      </button>
      <div role="cell" class="min-w-0">
        <span class="inline-block max-w-full truncate rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
          {{ folder.relative_path }}
        </span>
      </div>
      <div role="cell" class="text-right font-mono text-xs text-muted-foreground">
        {{ folder.audio_file_count }} 个音频文件
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
        :aria-label="`打开 ${folder.name}`"
        @click="emit('open', folder.id)"
      >
        <ChevronRight class="size-3.5" />
      </button>
    </div>
  </div>
</template>
