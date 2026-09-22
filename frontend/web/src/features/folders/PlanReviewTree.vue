<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChevronDown, ChevronRight, FileAudio, FilePlus2, FileText, Folder } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { useOperationContext } from '@/composables/use-operation-context'
import { decisionGroups, keepReasonText, memberConclusion, partitionFacts, resolutionText, revisionComponents } from '@/features/worksets/plan-readers'
import type { ComponentOutcome } from '@/lib/api/types'

/**
 * The plan's own view of one member (ADR 0001 §3).
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
const operation = workspace.operation

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

/** The frozen member entry behind this directory, when the plan has one. */
const frozenMember = computed(
  () => revision.value?.members.find((candidate) => candidate.folder_path === root.value?.root_path) ?? null,
)

/**
 * What the plan concludes for this folder, read with the same reader the row
 * that opened this page used: the review and the list never disagree, and a
 * folder with nothing to list still says why (已排除 / 未参与 / 无需转换).
 */
const conclusion = computed(() =>
  memberConclusion({
    excluded: frozenMember.value?.excluded ?? false,
    hasRoot: Boolean(root.value),
    rootMissing: root.value?.root_status === 'missing',
    facts: partitionFacts(components.value),
  }),
)

/** The plan is not there yet: the operation says whether it ever was. */
const planMissing = computed(() => Boolean(operation.value) && !operation.value?.current_revision)

/** The frozen proposal stays on screen, with the fact that it is no longer current. */
const needsPlanning = computed(() => {
  const op = operation.value
  if (!op?.current_revision) return false
  return op.planning_state === 'needs_planning' || op.current_revision.validation_state !== 'valid'
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
  /** The plan's own code, as the member review keeps it; the words come from
   *  the same map, at the same point, so the two views cannot drift. */
  reasonCode: string
  /** The plan expects this output and no decision of this component names it. */
  projected: boolean
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
          reasonCode: row.reasonCode,
          projected: false,
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
        reasonCode: '',
        projected: true,
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

/**
 * What the member's files are in for, counted from the same rows the tree
 * renders. A plan whose rows are all `keep` is a review worth reading too: it
 * says the folder needs no change, which is a conclusion, not an empty page.
 */
const tally = computed(() => {
  const counts: Record<string, number> = { keep: 0, delete: 0, encode: 0, pending: 0 }
  let rows = 0
  const walk = (node: DirRow): void => {
    for (const row of node.rows) {
      rows += 1
      counts[row.resolution] = (counts[row.resolution] ?? 0) + 1
      if (row.pending) counts.pending += 1
    }
    node.children.forEach(walk)
  }
  walk(tree.value)
  return { counts, rows }
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
  <div class="flex min-h-0 flex-1 flex-col" data-testid="plan-review-tree">
    <!-- What the plan concludes for this folder, before the rows that carry it. -->
    <div v-if="revision" class="shrink-0 space-y-1 border-b border-border px-3 py-2" data-testid="plan-summary">
      <p class="flex flex-wrap items-center gap-2 text-xs">
        <Badge :tone="conclusion.tone">{{ conclusion.label }}</Badge>
        <span class="text-[var(--text-secondary)]">{{ conclusion.detail }}</span>
      </p>
      <p v-if="tally.rows > 0" class="text-[11px] text-[var(--text-muted)]" data-testid="plan-tally">
        保留 {{ tally.counts.keep }} · 删除 {{ tally.counts.delete }} · 生成 {{ tally.counts.encode }}
        <template v-if="tally.counts.pending">（其中 {{ tally.counts.pending }} 项待生成）</template>
      </p>
    </div>

    <p
      v-if="needsPlanning"
      class="shrink-0 border-b border-border bg-[var(--warning-weak,var(--muted))] px-3 py-1.5 text-[11px]"
      data-testid="plan-needs-regeneration"
      role="status"
    >
      设置或文件夹输入已变化：这里展示的仍是冻结的计划，需重新生成计划版本。
    </p>

    <div class="min-h-0 flex-1 overflow-auto p-3">
      <p v-if="planMissing" class="text-xs text-[var(--text-muted)]">该操作还没有当前计划。先生成计划版本。</p>
      <p v-else-if="!revision" class="text-xs text-[var(--text-muted)]">正在读取计划…</p>
      <template v-else>
        <p class="mb-2 text-[11px] text-[var(--text-muted)]">
          按计划冻结数据展示：保留、删除、生成及预计新增的输出与原因。标记为“待生成”的文件尚未存在于磁盘。计划只列出它涉及的文件。
        </p>
        <p v-if="tally.rows === 0" class="text-xs text-[var(--text-muted)]" data-testid="plan-empty">
          当前计划没有列出这个文件夹的文件，也不会有文件操作。
        </p>
        <ul v-else role="tree" aria-label="计划文件树">
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
          <span v-if="row.projected" class="truncate text-[10px] text-[var(--text-muted)]">计划预计产生</span>
          <!-- Why a file stays is the question worth answering; a generated or
               removed file speaks for itself — the member review's own rule. -->
          <span
            v-else-if="row.resolution === 'keep' && row.reasonCode"
            class="truncate text-[10px] text-[var(--text-muted)]"
          >
            {{ keepReasonText(row.reasonCode) }}
          </span>
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
              <span v-if="row.projected" class="truncate text-[10px] text-[var(--text-muted)]">计划预计产生</span>
              <!-- The same rule as the root rows, and as the member review: why
                   a file stays is the question worth answering; a generated or
                   removed file speaks for itself. -->
              <span
                v-else-if="row.resolution === 'keep' && row.reasonCode"
                class="truncate text-[10px] text-[var(--text-muted)]"
              >
                {{ keepReasonText(row.reasonCode) }}
              </span>
            </li>
          </template>
        </template>
        </ul>
      </template>
    </div>
  </div>
</template>
