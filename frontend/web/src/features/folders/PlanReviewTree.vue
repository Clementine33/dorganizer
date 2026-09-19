<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronDown, ChevronRight, FileAudio, FilePlus2, FileText, Folder } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { useOperationContext } from '@/composables/use-operation-context'
import { decisionGroups, keepReasonText, resolutionText, revisionComponents } from '@/features/worksets/plan-readers'
import type { ComponentOutcome } from '@/lib/api/types'

/**
 * The plan's own view of one member (spec T3).
 *
 * It is built from the FROZEN plan — never from the latest scan — so what it
 * shows is what the plan will do, not what the disk happens to hold now. It is
 * read-only by construction: it renders no selection and no action, and direct
 * file management stays on the current-files view.
 *
 * A file the plan will produce does not exist yet, so it is marked as pending
 * and never presented as an item on disk.
 */
const props = defineProps<{
  worksetId: string
  /** The member's library-relative path, as the current-files view knows it. */
  memberPath: string
}>()

const { workspace } = useOperationContext(computed(() => props.worksetId), 'conversion')
const revision = workspace.revision

/** The frozen root that corresponds to this member. */
const root = computed(() => {
  const view = revision.value
  if (!view) return null
  return (
    view.roots.find((candidate) => candidate.root_path.endsWith(`/${props.memberPath}`)) ??
    view.roots.find((candidate) => candidate.root_path === props.memberPath) ??
    null
  )
})

const components = computed<ComponentOutcome[]>(() => {
  const view = revision.value
  const rootIndex = root.value?.root_index
  if (!view || rootIndex === undefined) return []
  const owned = new Set(
    view.component_roots.filter((ref) => ref.root_index === rootIndex).map((ref) => ref.component_id),
  )
  return revisionComponents(view).filter((component) => owned.has(component.component_id))
})

interface Row {
  key: string
  name: string
  pending: boolean
  resolution: string
  reason: string
}

interface DirRow {
  key: string
  name: string
  depth: number
  rows: Row[]
  children: DirRow[]
}

/** The plan's per-file conclusions, grouped into the member's directory shape. */
const tree = computed<DirRow>(() => {
  const rootNode: DirRow = { key: '', name: '', depth: 0, rows: [], children: [] }
  const byPath = new Map<string, DirRow>([['', rootNode]])

  function dirNode(dir: string): DirRow {
    const existing = byPath.get(dir)
    if (existing) return existing
    const parentPath = dir.includes('/') ? dir.slice(0, dir.lastIndexOf('/')) : ''
    const parent = dirNode(parentPath)
    const node: DirRow = {
      key: dir,
      name: dir.slice(dir.lastIndexOf('/') + 1),
      depth: parent.depth + 1,
      rows: [],
      children: [],
    }
    parent.children.push(node)
    byPath.set(dir, node)
    return node
  }

  for (const component of components.value) {
    for (const group of decisionGroups(component, root.value?.root_path ?? '')) {
      const node = dirNode(group.dir)
      for (const row of group.rows) {
        node.rows.push({
          key: `${component.component_id}:${group.dir}/${row.name}`,
          name: row.name,
          // An encode names the output it will write: that file is not on disk.
          pending: row.resolution === 'encode',
          resolution: row.resolution,
          reason: row.reasonCode ? keepReasonText(row.reasonCode) : '',
        })
      }
    }
    // Outputs the plan expects to exist but that no decision named: they are
    // the plan's projected inventory, and they are pending by definition.
    for (const projected of component.projected_inventory ?? []) {
      const rel = relativeTo(projected, root.value?.root_path ?? '')
      if (rel === '') continue
      const dir = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/')) : ''
      const name = rel.slice(rel.lastIndexOf('/') + 1)
      const node = dirNode(dir)
      if (node.rows.some((row) => row.name === name)) continue
      node.rows.push({
        key: `${component.component_id}:projected:${rel}`,
        name,
        pending: true,
        resolution: 'encode',
        reason: '计划预计产生',
      })
    }
  }

  const sort = (node: DirRow) => {
    node.children.sort((a, b) => a.name.localeCompare(b.name))
    node.rows.sort((a, b) => a.name.localeCompare(b.name))
    node.children.forEach(sort)
  }
  sort(rootNode)
  return rootNode
})

const collapsed = ref<Set<string>>(new Set())

function toggle(key: string) {
  if (collapsed.value.has(key)) collapsed.value.delete(key)
  else collapsed.value.add(key)
  collapsed.value = new Set(collapsed.value)
}

function relativeTo(path: string, memberRoot: string): string {
  const prefix = memberRoot && !memberRoot.endsWith('/') ? `${memberRoot}/` : memberRoot
  return prefix && path.startsWith(prefix) ? path.slice(prefix.length) : ''
}

const RESOLUTION_TONES: Record<string, 'neutral' | 'success' | 'danger' | 'warning'> = {
  keep: 'neutral',
  delete: 'danger',
  encode: 'success',
}
</script>

<template>
  <div class="min-h-0 flex-1 overflow-auto p-3" data-testid="plan-review-tree">
    <p v-if="!revision" class="text-xs text-[var(--text-muted)]">该操作还没有当前计划。先生成计划版本。</p>
    <template v-else>
      <p class="mb-2 text-[11px] text-[var(--text-muted)]">
        按计划冻结数据展示：保留、删除、生成及预计新增的输出。标记为“待生成”的文件尚未存在于磁盘。
      </p>
      <ul role="tree" aria-label="计划文件树">
        <!-- Files that sit in the member root itself have no directory row of
             their own; they are the first thing the tree shows. -->
        <li
          v-for="row in tree.rows"
          :key="row.key"
          class="flex min-h-8 items-center gap-2 pl-6"
          :data-resolution="row.resolution"
          :data-pending="row.pending ? 'true' : 'false'"
          role="treeitem"
        >
          <component
            :is="row.pending ? FilePlus2 : FileAudio"
            class="size-3.5 shrink-0 text-muted-foreground"
            aria-hidden="true"
          />
          <span class="min-w-0 truncate text-xs" :class="row.pending ? 'italic' : ''">{{ row.name }}</span>
          <Badge :tone="RESOLUTION_TONES[row.resolution] ?? 'neutral'">{{ resolutionText(row.resolution) }}</Badge>
          <span v-if="row.pending" class="text-[10px] text-[var(--text-muted)]" data-testid="pending-output">待生成</span>
          <span v-if="row.reason" class="truncate text-[10px] text-[var(--text-muted)]">{{ row.reason }}</span>
        </li>
        <template v-for="dir in tree.children" :key="dir.key">
          <li
            class="flex min-h-8 items-center gap-1.5"
            :style="{ paddingLeft: `${dir.depth * 16}px` }"
            role="treeitem"
            :aria-expanded="!collapsed.has(dir.key)"
          >
            <button
              type="button"
              class="grid size-4 place-items-center rounded text-muted-foreground hover:text-foreground"
              :aria-label="`${collapsed.has(dir.key) ? '展开' : '折叠'} ${dir.name}`"
              @click="toggle(dir.key)"
            >
              <ChevronDown v-if="!collapsed.has(dir.key)" class="size-3.5" />
              <ChevronRight v-else class="size-3.5" />
            </button>
            <Folder class="size-3.5 text-[var(--brand)]" aria-hidden="true" />
            <span class="font-heading text-xs font-semibold">{{ dir.name }}</span>
          </li>
          <template v-if="!collapsed.has(dir.key)">
            <li
              v-for="row in dir.rows"
              :key="row.key"
              class="flex min-h-8 items-center gap-2"
              :style="{ paddingLeft: `${(dir.depth + 1) * 16 + 8}px` }"
              :data-resolution="row.resolution"
              :data-pending="row.pending ? 'true' : 'false'"
              role="treeitem"
            >
              <component
                :is="row.pending ? FilePlus2 : row.name.includes('.') ? FileAudio : FileText"
                class="size-3.5 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
              <span class="min-w-0 truncate text-xs" :class="row.pending ? 'italic' : ''">{{ row.name }}</span>
              <Badge :tone="RESOLUTION_TONES[row.resolution] ?? 'neutral'">
                {{ resolutionText(row.resolution) }}
              </Badge>
              <span v-if="row.pending" class="text-[10px] text-[var(--text-muted)]" data-testid="pending-output">
                待生成
              </span>
              <span v-if="row.reason" class="truncate text-[10px] text-[var(--text-muted)]">{{ row.reason }}</span>
            </li>
          </template>
        </template>
      </ul>
    </template>
  </div>
</template>
