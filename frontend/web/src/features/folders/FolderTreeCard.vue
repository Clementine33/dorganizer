<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ChevronDown, ChevronRight, FileAudio, Folder, Music } from '@lucide/vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import type { TreeNode } from '@/lib/api/types'
import { createTreeModel, type TreeModel, type TreeModelNode } from './tree-model'

const props = defineProps<{
  root: TreeNode
}>()

const emit = defineEmits<{
  selectionChange: [paths: string[]]
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

function selectedPaths(): string[] {
  const all: string[] = []
  const walk = (node: TreeModelNode): void => {
    if (node.type === 'file') all.push(node.path)
    else node.children.forEach(walk)
  }
  walk(model.value.root)
  return all.filter((path) => model.value.selectedFilePaths.has(path))
}

function onFileToggle(node: TreeModelNode, event: Event): void {
  model.value.selectFile(node.path, (event.target as HTMLInputElement).checked)
  emit('selectionChange', selectedPaths())
}

function onDirToggle(node: TreeModelNode, event: Event): void {
  model.value.selectDir(node.id, (event.target as HTMLInputElement).checked)
  emit('selectionChange', selectedPaths())
}

function expandToggle(node: TreeModelNode): void {
  model.value.toggleDir(node.id)
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
  <Card data-testid="folder-tree-card" class="min-h-0 flex-1" size="sm">
    <CardHeader>
      <CardTitle class="flex items-baseline gap-2">
        <Folder class="size-4 shrink-0 self-center text-muted-foreground" />
        <span class="truncate">{{ model.root.name }}</span>
        <span class="ml-auto shrink-0 font-mono text-[11px] font-normal text-muted-foreground">
          {{ fileCount }} 个文件
        </span>
      </CardTitle>
    </CardHeader>
    <CardContent class="min-h-0 flex-1 overflow-auto">
      <div v-if="fileCount > 0" role="tree" aria-label="文件夹内容" class="pb-2">
        <div
          v-for="(node, index) in model.getVisibleNodes()"
          :key="node.id"
          :data-testid="`tree-row-${index}`"
          :data-indent="node.depth"
          role="treeitem"
          :aria-level="node.depth + 1"
          :aria-expanded="node.type === 'dir' ? model.isExpanded(node.id) : undefined"
          class="flex min-h-9 items-center gap-2 border-b border-border/60 last:border-b-0 pr-2 hover:bg-accent/40"
          :style="{ paddingLeft: `${8 + node.depth * 18}px` }"
        >
          <button
            v-if="node.type === 'dir'"
            type="button"
            class="grid size-4 shrink-0 place-items-center rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            :aria-label="`${model.isExpanded(node.id) ? '折叠' : '展开'} ${node.name}`"
            @click="expandToggle(node)"
          >
            <ChevronDown v-if="model.isExpanded(node.id)" class="size-3.5" />
            <ChevronRight v-else class="size-3.5" />
          </button>

          <input
            v-if="node.type === 'file'"
            :data-testid="`file-checkbox-${index}`"
            type="checkbox"
            :checked="model.selectedFilePaths.has(node.path)"
            :aria-label="`选择 ${node.name}`"
            class="size-3.5 shrink-0 accent-[var(--ring)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            @change="onFileToggle(node, $event)"
          />
          <input
            v-else
            :data-testid="`dir-checkbox-${index}`"
            type="checkbox"
            :checked="model.dirSelection(node.id) === 'checked'"
            :indeterminate="model.dirSelection(node.id) === 'indeterminate'"
            :aria-label="`选择 ${node.name} 下的所有文件`"
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
            <component :is="node.format === 'm4a' ? Music : FileAudio" class="size-4" />
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
        </div>
      </div>

      <div v-else class="grid min-h-40 place-items-center px-4 text-center">
        <div class="max-w-xs">
          <Music class="mx-auto size-6 text-muted-foreground" />
          <h3 class="mt-2 font-heading text-sm font-semibold">还没有音频文件</h3>
          <p class="mt-1 text-xs leading-5 text-muted-foreground">
            这个文件夹里没有可生成计划的音频文件。可以在媒体库页扫描后重试。
          </p>
        </div>
      </div>
    </CardContent>
  </Card>
</template>