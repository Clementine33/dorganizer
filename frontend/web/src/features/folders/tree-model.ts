import { computed, reactive } from 'vue'
import type { TreeNode } from '@/lib/api/types'

/**
 * Render/selection model over the server folder tree.
 *
 * The server JSON is the single source of truth: node names are basenames
 * (echoed verbatim), and full paths are only stored for payload use — this
 * module never derives names from paths and never normalizes any path.
 */

export type DirSelection = 'checked' | 'indeterminate' | 'unchecked'

export interface TreeModelNode {
  /** Stable identity — the server-provided full path, never rendered. */
  id: string
  /** Basename only, echoed verbatim from the server. */
  name: string
  /** Full path, echoed verbatim; used only for request payloads. */
  path: string
  type: 'dir' | 'file'
  format: string
  bitrate: number | null
  size: number | null
  depth: number
  children: TreeModelNode[]
}

export interface TreeModel {
  root: TreeModelNode
  expandedIds: Set<string>
  selectedFilePaths: Set<string>
  /** Depth-first flattened visible rows; re-read the return value after toggles. */
  getVisibleNodes(): TreeModelNode[]
  isExpanded(id: string): boolean
  dirSelection(id: string): DirSelection
  toggleDir(id: string): void
  selectDir(id: string, selected: boolean): void
  selectFile(path: string, selected: boolean): void
}

/** Dirs first, stable within each group (server order is preserved). */
function dirsFirst(children: TreeNode[]): TreeNode[] {
  return [...children].sort((a, b) => (a.type === b.type ? 0 : a.type === 'dir' ? -1 : 1))
}

function buildNode(node: TreeNode, depth: number): TreeModelNode {
  const children = (node.children ?? []).length
    ? dirsFirst(node.children ?? []).map((child) => buildNode(child, depth + 1))
    : []
  return {
    id: node.path,
    name: node.name,
    path: node.path,
    type: node.type,
    format: node.format ?? '',
    bitrate: node.bitrate ?? null,
    size: node.size ?? null,
    depth,
    children,
  }
}

function collectDescendantFilePaths(node: TreeModelNode): string[] {
  if (node.type === 'file') return [node.path]
  return node.children.flatMap(collectDescendantFilePaths)
}

export function createTreeModel(rootNode: TreeNode): TreeModel {
  const root = buildNode(rootNode, 0)
  // The root is expanded by default; nested directories start collapsed.
  const expandedIds = reactive(new Set<string>([root.id]))
  const selectedFilePaths = reactive(new Set<string>())

  const visibleNodes = computed(() => {
    const out: TreeModelNode[] = []
    const walk = (node: TreeModelNode): void => {
      out.push(node)
      if (node.type === 'dir' && expandedIds.has(node.id)) {
        for (const child of node.children) walk(child)
      }
    }
    walk(root)
    return out
  })

  function findDir(id: string): TreeModelNode | null {
    if (root.id === id) return root
    let found: TreeModelNode | null = null
    const walk = (node: TreeModelNode): void => {
      if (found) return
      if (node.id === id) {
        found = node
        return
      }
      for (const child of node.children) walk(child)
    }
    walk(root)
    return found
  }

  function dirSelection(id: string): DirSelection {
    const node = findDir(id)
    if (!node) return 'unchecked'
    const paths = collectDescendantFilePaths(node)
    if (paths.length === 0) return 'unchecked'
    let checked = 0
    for (const path of paths) {
      if (selectedFilePaths.has(path)) checked += 1
    }
    if (checked === paths.length) return 'checked'
    if (checked > 0) return 'indeterminate'
    return 'unchecked'
  }

  function selectDir(id: string, selected: boolean): void {
    const node = findDir(id)
    if (!node) return
    for (const path of collectDescendantFilePaths(node)) {
      if (selected) selectedFilePaths.add(path)
      else selectedFilePaths.delete(path)
    }
  }

  function selectFile(path: string, selected: boolean): void {
    if (selected) selectedFilePaths.add(path)
    else selectedFilePaths.delete(path)
  }

  function toggleDir(id: string): void {
    if (expandedIds.has(id)) expandedIds.delete(id)
    else expandedIds.add(id)
  }

  return {
    root,
    expandedIds,
    selectedFilePaths,
    getVisibleNodes: () => visibleNodes.value,
    isExpanded: (id) => expandedIds.has(id),
    dirSelection,
    toggleDir,
    selectDir,
    selectFile,
  }
}