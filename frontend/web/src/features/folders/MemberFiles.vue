<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useRoute, useRouter } from 'vue-router'
import { AlertTriangle, FolderInput, RefreshCw, Trash2 } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import Modal from '@/components/ui/modal/Modal.vue'
import MemberTree from '@/features/folders/MemberTree.vue'
import PlanReviewTree from '@/features/folders/PlanReviewTree.vue'
import { itemStatusText, refreshText, requestRefusalText } from '@/features/folders/file-ops'
import { useApiClient } from '@/lib/api/client'
import { ApiError } from '@/lib/api/client'
import { errorDetails } from '@/lib/api/error'
import { rootPathIdentityKey } from '@/lib/root-path-identity'
import {
  fileOperationMutationOptions,
  memberTreeQueryOptions,
  refreshMemberTreeMutationOptions,
  useLibraryList,
} from '@/queries/libraries'
import { currentRecordQueryOptions } from '@/queries/worksets'
import { CONVERSION, type FileOperation, type FileOperationResult, type TreeNode } from '@/lib/api/types'

/**
 * The shared current-files page (spec T1, T2).
 *
 * One page, two entries: the overview browses a member by its library-relative
 * path, and a conversion member resolves the same path from its stable member
 * id. The module receives (library, member path, where to go back to) and owns
 * reading, refreshing, selecting and modifying the files inside that member —
 * it never reads a conversion draft or interprets a plan (ADR 0007 §3).
 *
 * The plan review is a *view of this same page* on the conversion entry only:
 * it renders the frozen plan, is read-only, and switching back to 当前文件 is
 * what enables modification.
 */
const api = useApiClient()
const queryClient = useQueryClient()
const route = useRoute()
const router = useRouter()
const libraryId = computed(() => (route.params.libraryId as string) || '')

// The member is addressed differently by the two entries and resolved here
// once: the conversion entry carries a stable member id, the overview carries
// the library-relative path directly (spec T1).
const memberId = computed(() => (route.params.memberId as string) || null)
const recordQuery = useQuery(() => currentRecordQueryOptions(api, libraryId.value, CONVERSION))
const record = computed(() => recordQuery.data.value?.workset ?? null)
const memberPath = computed(() => {
  if (memberId.value) {
    return record.value?.members.find((member) => member.member_id === memberId.value)?.rel_path ?? null
  }
  return (route.query.folder as string) || null
})
const worksetId = computed(() => (memberId.value ? (record.value?.workset_id ?? null) : null))
// A member link that no longer resolves against the current record: it was
// replaced by another page, and the only honest thing to offer is the way back
// to the conversion entry (N2).
const recordChanged = computed(
  () => Boolean(memberId.value) && recordQuery.isSuccess.value && memberPath.value === null,
)

const { query: librariesQuery, librariesData } = useLibraryList()

// The root identity scopes the cache: a genuine root change invalidates the
// stored trees instead of showing another directory's contents.
const rootIdentity = computed(() => {
  const library = librariesData.value.find((item) => item.id === libraryId.value)
  if (library) return rootPathIdentityKey(library.root_path)
  if (librariesQuery.isSuccess.value || librariesQuery.error.value) return 'unresolved-root'
  return null
})

const treeQuery = useQuery(() =>
  memberTreeQueryOptions(api, libraryId.value, rootIdentity.value, memberPath.value),
)
const tree = computed<TreeNode | null>(() => treeQuery.data.value?.tree ?? null)
const treePending = computed(() => treeQuery.isPending.value)
const treeError = computed(() => {
  const error = treeQuery.error.value
  return error ? errorDetails(error) : null
})

// View mode: the conversion entry offers 当前文件 / 计划审阅; the overview has
// only current files. The mode is part of the URL, so a return visit and the
// browser's back button land where the user left off.
const mode = computed<'current' | 'plan'>(() => (route.query.view === 'plan' ? 'plan' : 'current'))
const canReview = computed(() => Boolean(worksetId.value))

function setMode(next: 'current' | 'plan') {
  void router.replace({ query: { ...route.query, view: next === 'plan' ? 'plan' : undefined } })
}

// ---- refreshing -------------------------------------------------------

const refreshMutation = useMutation(
  refreshMemberTreeMutationOptions(api, queryClient, libraryId.value, rootIdentity.value ?? ''),
)
const refreshError = ref<string | null>(null)

/** Entering the page refreshes the member: the cached tree shows first. */
const refreshing = computed(() => refreshMutation.isPending.value)

async function refresh() {
  if (!memberPath.value) return
  refreshError.value = null
  try {
    await refreshMutation.mutateAsync(memberPath.value)
  } catch (error) {
    const apiError = error as { code?: string; message?: string }
    refreshError.value = apiError.message ?? '刷新失败'
  }
}

watch(
  () => [libraryId.value, memberPath.value, mode.value] as const,
  ([, path, nextMode]) => {
    refreshError.value = null
    // The plan review is a frozen snapshot: it needs no disk read at all.
    if (path && nextMode === 'current') void refresh()
  },
  { immediate: true },
)

// ---- file operations --------------------------------------------------

const selection = ref<string[]>([])
const dialog = ref<'none' | 'rename' | 'move' | 'delete'>('none')
const target = ref<string>('')
const renameValue = ref('')
const moveDir = ref('')
const result = ref<FileOperationResult | null>(null)
const opError = ref<string | null>(null)

const fileOps = useMutation(
  fileOperationMutationOptions(api, queryClient, libraryId.value, rootIdentity.value ?? ''),
)
const busyApplying = computed(() => fileOps.isPending.value)
const modifyDisabled = computed(() => busyApplying.value || refreshing.value || Boolean(treeError.value))
const modifyDisabledReason = computed(() => {
  if (refreshing.value) return '正在刷新该文件夹：刷新完成前不能修改。'
  if (treeError.value) return '未能读取该文件夹，不能修改。'
  if (busyApplying.value) return '正在应用上一项修改。'
  return ''
})

/** The directories a move can target: the member's own subdirectories. */
const directories = computed(() => {
  const out: { relPath: string; label: string }[] = []
  if (!tree.value) return out
  const walk = (node: TreeNode) => {
    for (const child of node.children ?? []) {
      if (child.type !== 'dir') continue
      out.push({ relPath: child.rel_path, label: child.rel_path })
      walk(child)
    }
  }
  walk(tree.value)
  return out
})

function openRename(relPath: string) {
  target.value = relPath
  renameValue.value = relPath.split('/').pop() ?? relPath
  opError.value = null
  dialog.value = 'rename'
}

function openMove(relPath: string) {
  target.value = relPath
  moveDir.value = ''
  opError.value = null
  dialog.value = 'move'
}

function openDelete(relPath?: string) {
  target.value = relPath ?? ''
  opError.value = null
  dialog.value = 'delete'
}

/** The batch delete's items: the selection, or the one row that asked. */
const deleteItems = computed(() => (target.value ? [target.value] : selection.value))

async function apply(operation: FileOperation, items: { source: string; name?: string; target_dir?: string }[]) {
  opError.value = null
  result.value = null
  try {
    result.value = await fileOps.mutateAsync({
      member_path: memberPath.value ?? '',
      operation,
      items,
    })
    dialog.value = 'none'
    selection.value = []
  } catch (error) {
    // The request never became a scope: nothing ran, and it says why.
    if (error instanceof ApiError) {
      opError.value = requestRefusalText(error.code, error.message)
      return
    }
    opError.value = (error as Error).message
  }
}

function submitRename() {
  const name = renameValue.value.trim()
  if (!name) return
  void apply('rename', [{ source: target.value, name }])
}

function submitMove() {
  if (!moveDir.value) return
  void apply('move', [{ source: target.value, target_dir: moveDir.value }])
}

function submitDelete() {
  void apply(
    'soft_delete',
    deleteItems.value.map((source) => ({ source })),
  )
}

</script>

<template>
  <section class="flex min-h-0 flex-1 flex-col" data-testid="member-files">
    <header class="flex min-h-12 shrink-0 flex-wrap items-center gap-2 border-b border-border px-3">
      <div v-if="canReview" class="flex items-center gap-1" role="tablist" aria-label="视图">
        <Button
          size="xs"
          :variant="mode === 'current' ? 'default' : 'ghost'"
          role="tab"
          :aria-selected="mode === 'current'"
          data-testid="view-current"
          @click="setMode('current')"
        >
          当前文件
        </Button>
        <Button
          size="xs"
          :variant="mode === 'plan' ? 'default' : 'ghost'"
          role="tab"
          :aria-selected="mode === 'plan'"
          data-testid="view-plan"
          @click="setMode('plan')"
        >
          计划审阅
        </Button>
      </div>
      <span v-else class="text-xs font-medium">当前文件</span>

      <span class="ml-auto flex items-center gap-2">
        <span v-if="refreshing" class="text-[11px] text-[var(--text-muted)]" data-testid="refreshing">
          正在刷新…
        </span>
        <Button
          v-else-if="mode === 'current'"
          size="xs"
          variant="outline"
          data-testid="refresh-tree"
          @click="refresh"
        >
          <RefreshCw class="size-3.5" />
          刷新
        </Button>
      </span>
    </header>

    <p
      v-if="refreshError"
      class="border-b border-border bg-[var(--warning-weak,var(--muted))] px-3 py-1.5 text-[11px]"
      data-testid="refresh-error"
      role="alert"
    >
      未能刷新该文件夹：{{ refreshError }}。下面是上次扫描的内容。
    </p>
    <p
      v-if="treeError"
      class="flex items-center gap-2 border-b border-destructive/30 bg-destructive/10 px-3 py-2 text-[11px] text-destructive"
      data-testid="tree-error"
      role="alert"
    >
      <AlertTriangle class="size-3.5 shrink-0" />
      <span>{{ treeError.code }} {{ treeError.message }}</span>
    </p>

    <!-- A member link that no longer resolves against the current record: the
         record was replaced by another page, and the only honest thing to
         offer is the way back to the conversion entry (spec N2). -->
    <div
      v-if="recordChanged"
      class="grid min-h-0 flex-1 place-items-center px-4 text-center"
      data-testid="record-replaced"
      role="status"
    >
      <div class="max-w-sm">
        <p class="text-xs">处理记录已更新：这个成员属于上一条记录。</p>
        <Button
          v-if="libraryId"
          class="mt-2"
          size="xs"
          variant="outline"
          data-testid="back-to-conversion"
          @click="router.push({ name: 'conversion', params: { libraryId } })"
        >
          返回转换
        </Button>
      </div>
    </div>

    <div
      v-else-if="treePending && !tree"
      class="grid min-h-0 flex-1 place-items-center text-xs text-muted-foreground"
    >
      正在读取文件夹…
    </div>

    <template v-else-if="tree">
      <PlanReviewTree
        v-if="mode === 'plan'"
        class="min-h-0 flex-1"
        :workset-id="worksetId ?? ''"
        :member-path="memberPath ?? ''"
      />
      <MemberTree
        v-else
        class="min-h-0 flex-1"
        :root="tree"
        :frozen="modifyDisabled"
        :frozen-reason="modifyDisabledReason"
        :stale="Boolean(refreshError)"
        @selection-change="selection = $event"
        @rename="openRename"
        @move="openMove"
        @remove="openDelete"
      />
    </template>


    <div
      v-else-if="!treeError && memberPath"
      class="grid min-h-0 flex-1 place-items-center px-4 text-center text-xs text-muted-foreground"
      data-testid="member-missing"
    >
      这个文件夹不存在或已被删除，未自动创建同名目录。
    </div>

    <!-- Selection and modification actions: only in the current-files view,
         and only while modification is allowed. -->
    <div
      v-if="mode === 'current' && tree"
      class="flex min-h-12 shrink-0 flex-wrap items-center gap-2 border-t border-border bg-card px-3"
      data-testid="file-toolbar"
    >
      <span class="text-[11px] text-[var(--text-muted)]">
        {{ selection.length ? `${selection.length} 项已选择` : '在树中选择文件或文件夹' }}
      </span>
      <span v-if="modifyDisabled" class="text-[11px] text-[var(--warning-ink,var(--text-secondary))]">
        <FolderInput class="mr-1 inline size-3" />{{ modifyDisabledReason }}
      </span>
      <Button
        v-if="selection.length"
        size="xs"
        variant="outline"
        class="ml-auto"
        :disabled="modifyDisabled"
        data-testid="delete-selection"
        @click="openDelete()"
      >
        <Trash2 class="size-3.5" />
        移入回收目录（{{ selection.length }}）
      </Button>
    </div>

    <div
      v-if="result"
      class="max-h-56 shrink-0 overflow-y-auto border-t border-border bg-card px-3 py-2"
      data-testid="file-op-result"
      role="status"
    >
      <p class="text-[11px] font-medium">
        完成 {{ result.succeeded }} 项<template v-if="result.failed">，失败 {{ result.failed }} 项</template
        ><template v-if="result.untouched">，未处理 {{ result.untouched }} 项</template>
      </p>
      <p class="mt-0.5 text-[11px]" :class="result.refresh.ok ? 'text-[var(--text-muted)]' : 'text-[var(--danger-ink)]'">
        {{ refreshText(result.refresh) }}
      </p>
      <ul class="mt-1 space-y-0.5">
        <li v-for="item in result.items" :key="item.source" class="text-[11px]" :data-status="item.status">
          <span class="font-mono">{{ item.source }}</span>
          <span class="text-[var(--text-secondary)]"> — {{ itemStatusText(item) }}</span>
        </li>
      </ul>
    </div>
    <p v-if="opError" class="border-t border-border px-3 py-1.5 text-[11px] text-[var(--danger-ink)]" role="alert">
      {{ opError }}
    </p>

    <!-- Rename: a name, never a path. -->
    <Modal :open="dialog === 'rename'" title="重命名" :description="target" @update:open="dialog = 'none'">
      <label class="block text-xs">
        新名称
        <input
          v-model="renameValue"
          data-testid="rename-input"
          class="mt-1 h-9 w-full rounded-md border border-input bg-background px-2 text-xs focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
        />
      </label>
      <p class="text-[11px] text-[var(--text-muted)]">只能改名称：要换目录请用“移动”。目标已存在时不会覆盖。</p>
      <div class="flex justify-end gap-2">
        <Button variant="ghost" size="sm" @click="dialog = 'none'">取消</Button>
        <Button size="sm" :disabled="busyApplying" data-testid="rename-submit" @click="submitRename">重命名</Button>
      </div>
    </Modal>

    <!-- Move: an existing directory of the same member. -->
    <Modal :open="dialog === 'move'" title="移动到" :description="target" @update:open="dialog = 'none'">
      <label class="block text-xs">
        目标目录
        <select
          v-model="moveDir"
          data-testid="move-target"
          class="mt-1 h-9 w-full rounded-md border border-input bg-background px-2 text-xs"
        >
          <option value="" disabled>选择一个目录</option>
          <option v-for="dir in directories" :key="dir.relPath" :value="dir.relPath">{{ dir.label }}</option>
        </select>
      </label>
      <p v-if="directories.length === 0" class="text-[11px] text-[var(--text-muted)]">
        该成员里还没有子目录，先创建目录（本版本不提供新建目录）。
      </p>
      <div class="flex justify-end gap-2">
        <Button variant="ghost" size="sm" @click="dialog = 'none'">取消</Button>
        <Button size="sm" :disabled="busyApplying || !moveDir" data-testid="move-submit" @click="submitMove">
          移动
        </Button>
      </div>
    </Modal>

    <!-- Soft delete: confirm, then recycle into the library's Delete/. -->
    <Modal :open="dialog === 'delete'" title="移入回收目录" @update:open="dialog = 'none'">
      <p class="text-xs">将把 {{ deleteItems.length }} 项移入媒体库的 Delete/ 目录，目录层级会保留。</p>
      <p class="text-[11px] text-[var(--text-muted)]">
        已成功的项目不会自动撤销；恢复请在系统文件管理器里完成。
      </p>
      <ul class="max-h-32 overflow-y-auto text-[11px]">
        <li v-for="source in deleteItems" :key="source" class="font-mono">{{ source }}</li>
      </ul>
      <div class="flex justify-end gap-2">
        <Button variant="ghost" size="sm" @click="dialog = 'none'">取消</Button>
        <Button size="sm" :disabled="busyApplying" data-testid="delete-submit" @click="submitDelete">
          移入回收目录
        </Button>
      </div>
    </Modal>
  </section>
</template>
