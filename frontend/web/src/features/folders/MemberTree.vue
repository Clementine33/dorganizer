<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ChevronDown, ChevronRight, FileAudio, FileText, Folder, Music, Pencil, Trash2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import type { TreeNode } from '@/lib/api/types'
import { createTreeModel, type TreeModel, type TreeModelNode } from './tree-model'

/**
 * The shared member tree: the same renderer for the workbench overview's file
 * page and a conversion member's file page. It reads, selects and manages the
 * files of ONE member directory; it knows nothing about plans, drafts or
 * revisions — the plan's own review view is a separate component fed by the
 * frozen plan (ADR 0001 §3).
 *
 * Selection is by member-relative path, which is also how every file
 * operation addresses its items. A symlinked path never appears as a
 * manageable file: links are not recursed into, and the backend refuses them.
 */
const props = withDefaults(
  defineProps<{
    root: TreeNode
    /** File modification is unavailable: refreshing, or the app is busy. */
    frozen?: boolean
    /** Why modification is unavailable, in the user's words. */
    frozenReason?: string
    /** A refresh failed: the tree below is the cached one, and says so. */
    stale?: boolean
  }>(),
  { frozen: false, frozenReason: '', stale: false },
)

const emit = defineEmits<{
  selectionChange: [paths: string[]]
  rename: [relPath: string]
  move: [relPath: string]
  remove: [relPath: string]
}>()

const model = ref<TreeModel>(createTreeModel(props.root))

watch(
  () => props.root,
  (root) => {
    model.value = createTreeModel(root)
    emit('selectionChange', [])
  },
)

const fileCount = computed(() => {
  let count = 0
  const walk = (node: TreeModelNode): void => {
    if (node.type === 'file') count += 1
    else node.children.forEach(walk)
  }
  walk(model.value.root)
  return count
})

function onFileToggle(node: TreeModelNode, event: Event): void {
  model.value.selectFile(node.relPath, (event.target as HTMLInputElement).checked)
  emit('selectionChange', model.value.selectedPaths())
}

function onDirToggle(node: TreeModelNode, event: Event): void {
  model.value.selectDir(node.id, (event.target as HTMLInputElement).checked)
  emit('selectionChange', model.value.selectedPaths())
}

function formatBitrate(bitrate: number | null): string {
  if (!bitrate || bitrate <= 0) return ''
  return `${Math.round(bitrate / 1000)} kbps`
}

function formatBytes(size: number | null): string {
  if (size === null || size < 0) return ''
  if (size >= 1024 * 1024) return `${(size / (1024 * 1024)).toFixed(1)} MB`
  if (size >= 1024) return `${Math.round(size / 1024)} KB`
  return `${size} B`
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col" data-testid="member-tree">
    <p
      v-if="stale"
      class="border-b border-border bg-muted px-3 py-1.5 text-[11px] text-[var(--warning-ink,var(--text-secondary))]"
      data-testid="tree-stale"
    >
      未能刷新：下面是上次扫描的内容，可能已过期。
    </p>
    <div class="min-h-0 flex-1 overflow-auto">
      <div v-if="fileCount > 0" role="tree" aria-label="文件夹内容" class="pb-2">
        <div
          v-for="node in model.getVisibleNodes()"
          :key="node.id"
          :data-testid="`tree-row-${node.relPath || 'root'}`"
          :data-rel-path="node.relPath"
          :data-indent="node.depth"
          role="treeitem"
          :aria-level="node.depth + 1"
          :aria-expanded="node.type === 'dir' ? model.isExpanded(node.id) : undefined"
          class="group flex min-h-9 items-center gap-2 border-b border-border/60 last:border-b-0 pr-2 hover:bg-accent/40"
          :style="{ paddingLeft: `${8 + node.depth * 18}px` }"
        >
          <button
            v-if="node.type === 'dir'"
            type="button"
            class="grid size-4 shrink-0 place-items-center rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            :aria-label="`${model.isExpanded(node.id) ? '折叠' : '展开'} ${node.name}`"
            @click="model.toggleDir(node.id)"
          >
            <ChevronDown v-if="model.isExpanded(node.id)" class="size-3.5" />
            <ChevronRight v-else class="size-3.5" />
          </button>

          <input
            v-if="node.type === 'file'"
            :data-testid="`file-checkbox-${node.relPath}`"
            type="checkbox"
            :checked="model.selectedFilePaths.has(node.relPath)"
            :aria-label="`选择 ${node.name}`"
            :disabled="frozen"
            class="size-3.5 shrink-0 accent-[var(--ring)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            @change="onFileToggle(node, $event)"
          />
          <input
            v-else
            :data-testid="`dir-checkbox-${node.relPath || 'root'}`"
            type="checkbox"
            :checked="model.dirSelection(node.id) === 'checked'"
            :indeterminate="model.dirSelection(node.id) === 'indeterminate'"
            :aria-label="`选择 ${node.name} 下的所有文件`"
            :disabled="frozen"
            class="size-3.5 shrink-0 accent-[var(--ring)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            @change="onDirToggle(node, $event)"
          />

          <span
            v-if="node.type === 'dir'"
            class="grid size-4 shrink-0 place-items-center"
            :class="model.isExpanded(node.id) ? 'text-[var(--brand)]' : 'text-muted-foreground'"
            aria-hidden="true"
          >
            <Folder class="size-4" />
          </span>
          <span v-else class="grid size-4 shrink-0 place-items-center text-muted-foreground" aria-hidden="true">
            <component :is="node.format === 'm4a' ? Music : node.format ? FileAudio : FileText" class="size-4" />
          </span>

          <span class="min-w-0 truncate font-heading text-xs font-semibold">{{ node.name }}</span>

          <template v-if="node.type === 'file'">
            <span
              class="ml-auto shrink-0 rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-[10px] uppercase text-muted-foreground"
            >
              {{ node.format ? node.format.toUpperCase() : '—' }}
            </span>
            <span
              v-if="formatBitrate(node.bitrate)"
              class="shrink-0 rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground"
            >
              {{ formatBitrate(node.bitrate) }}
            </span>
            <span
              v-if="formatBytes(node.size)"
              class="w-20 shrink-0 text-right font-mono text-[11px] text-muted-foreground"
            >
              {{ formatBytes(node.size) }}
            </span>
          </template>

          <!-- File management: one row at a time, and only when the member is
               modifiable. The member root itself has no row to act on. The
               buttons are always in the DOM — a hover-only control is
               unreachable by keyboard — and revealed on hover or focus. -->
          <span
            v-if="!frozen"
            class="ml-2 flex shrink-0 items-center gap-0.5 opacity-0 focus-within:opacity-100 group-hover:opacity-100"
            data-testid="row-actions"
          >
            <Button
              variant="ghost"
              size="icon-sm"
              :aria-label="`重命名 ${node.name}`"
              :data-testid="`rename-${node.relPath}`"
              @click="emit('rename', node.relPath)"
            >
              <Pencil class="size-3.5" />
            </Button>
            <Button
              v-if="node.type === 'file'"
              variant="ghost"
              size="icon-sm"
              :aria-label="`移动 ${node.name}`"
              :data-testid="`move-${node.relPath}`"
              @click="emit('move', node.relPath)"
            >
              <Folder class="size-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              :aria-label="`删除 ${node.name}`"
              :data-testid="`remove-${node.relPath}`"
              @click="emit('remove', node.relPath)"
            >
              <Trash2 class="size-3.5" />
            </Button>
          </span>
        </div>
      </div>

      <div v-else class="grid min-h-40 place-items-center px-4 text-center">
        <div class="max-w-xs">
          <Music class="mx-auto size-6 text-muted-foreground" />
          <h3 class="mt-2 font-heading text-sm font-semibold">这个文件夹是空的</h3>
          <p class="mt-1 text-xs leading-5 text-muted-foreground">
            扫描后这里会显示目录内容；空目录也可以管理。
          </p>
        </div>
      </div>
    </div>
  </div>
</template>
