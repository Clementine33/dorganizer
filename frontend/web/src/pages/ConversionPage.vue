<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Sheet } from '@/components/ui/modal'
import WorkbenchShell from '@/components/layout/WorkbenchShell.vue'
import WorkbenchNav from '@/features/worksets/WorkbenchNav.vue'
import { settingsEditBlockedReason } from '@/features/worksets/workbench-nav'
import OperationHeader from '@/features/worksets/OperationHeader.vue'
import MemberList from '@/features/worksets/MemberList.vue'
import {
  memberConclusion,
  partitionFacts,
  revisionComponents,
  type MemberConclusion,
} from '@/features/worksets/plan-readers'
import { useApiClient } from '@/lib/api/client'
import { readMemberFilter, type MemberFilter } from '@/app/route-params'
import { currentRecordQueryOptions } from '@/queries/worksets'
import { useOperationContext } from '@/composables/use-operation-context'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import { useWorksetUiStore } from '@/stores/workset-ui'
import type { ComponentOutcome, WorksetMember } from '@/lib/api/types'

/**
 * The conversion section of one library's workbench (ADR 0001 §1, §2; ADR 0003 §1).
 *
 * The page addresses the library, never a record id: the library's current
 * conversion record is looked up, and its operation — draft, plan, sessions —
 * is what everything below reads. A library without a record says so and
 * points back to the overview, where a scope is chosen.
 *
 * Detail and edit entries are child routes rendered in the carrier the
 * container chose: inline beside the list on wide, a modal sheet on mid, the
 * full main view on narrow, one carrier at a time. A member's file page takes
 * the main area while the list stays mounted behind it, so returning keeps the
 * selection, the filters and the scroll position.
 */
const route = useRoute()
const router = useRouter()
const api = useApiClient()
const ui = useWorksetUiStore()
const editor = useWorksetEditorStore()

const libraryId = computed(() => (route.params.libraryId as string) || '')
const recordQuery = useQuery(() => currentRecordQueryOptions(api, libraryId.value, 'conversion'))
const record = computed(() => recordQuery.data.value?.workset ?? null)
const worksetId = computed(() => record.value?.workset_id ?? null)
const { workspace, generation, execution, executionView, applySession, startGeneration } =
  useOperationContext(worksetId, 'conversion')

const workset = workspace.workset
const operation = workspace.operation
const draft = workspace.draft
const revision = workspace.revision

const members = computed<WorksetMember[]>(() => workset.value?.members ?? [])
// Every jump below is a named route: no page assembles a path from ids and
// names by hand. The list's filter is the one view parameter this subtree
// carries along (ADR 0003 §5), and nothing else is forwarded.
const filter = computed<MemberFilter>(() => readMemberFilter(route.query.filter))
const listQuery = computed(() => (filter.value === 'all' ? {} : { filter: filter.value }))
const listRoute = computed(() => ({
  name: 'conversion' as const,
  params: { libraryId: libraryId.value },
  query: listQuery.value,
}))
const overviewRoute = computed(() => ({
  name: 'workbench-overview' as const,
  params: { libraryId: libraryId.value },
}))

function gotoOverview() {
  void router.push(overviewRoute.value)
}

/** The list's filter is shareable, so it is the page's own URL parameter. */
function setFilter(next: MemberFilter) {
  void router.replace({ query: next === 'all' ? {} : { filter: next } })
}
/** A child route is active: it occupies the carrier of this container tier. */
const detailOpen = computed(() => route.meta.carrier === true)
/** The carrier's own name: a modal sheet, the narrow drill-down and the
 *  desktop breadcrumb all say the same thing, and the folder's name is in the
 *  page itself (the frozen review names it) rather than in three places. */
const carrierTitle = computed(() => (route.meta.title as string | undefined) ?? '详情')
/** A member's file page takes the main area, not a carrier. */
const filesOpen = computed(() => route.name === 'conversion-member-files')

watch(
  () => worksetId.value,
  () => ui.resetForOperation(),
  { immediate: true },
)

/** Components of one member in the current plan, normalized on read. */
function memberComponents(member: WorksetMember): ComponentOutcome[] {
  const view = revision.value
  if (!view) return []
  const root = view.roots.find((r) => r.root_path === member.folder_path)
  if (!root) return []
  const owned = new Set(
    (view.component_roots ?? [])
      .filter((ref) => ref.root_index === root.root_index)
      .map((ref) => ref.component_id),
  )
  return revisionComponents(view).filter((component) => owned.has(component.component_id))
}

/** The visible conclusion of one member row. */
function conclusionFor(member: WorksetMember): MemberConclusion {
  const view = revision.value
  if (!view) return { tone: 'neutral', label: '待规划', detail: '尚未生成计划版本。' }
  const frozen = view.members.find((m) => m.member_id === member.member_id)
  const root = view.roots.find((r) => r.root_path === member.folder_path)
  const components = memberComponents(member)
  return memberConclusion({
    excluded: frozen?.excluded ?? false,
    hasRoot: Boolean(root),
    rootMissing: root?.root_status === 'missing',
    facts: partitionFacts(components),
  })
}

async function openMember(memberId: string) {
  await router.push({
    name: 'conversion-member',
    params: { libraryId: libraryId.value, memberId },
    query: listQuery.value,
  })
}

/** A member's files: the shared module, in this workbench's main area. */
async function openMemberFiles(memberId: string) {
  await router.push({
    name: 'conversion-member-files',
    params: { libraryId: libraryId.value, memberId },
    query: listQuery.value,
  })
}

async function closeDetail() {
  await router.push(listRoute.value)
  await nextTick()
}

/** Continuing a pending edit: the carrier its session targets. */
function continueEditing() {
  const target = editor.session?.target
  if (!target) return
  const params = { libraryId: libraryId.value }
  if (target.kind === 'common') void router.push({ name: 'conversion-settings', params, query: listQuery.value })
  else if (target.kind === 'batch') void router.push({ name: 'conversion-batch-edit', params, query: listQuery.value })
  else {
    void router.push({
      name: 'conversion-member-edit',
      params: { ...params, memberId: target.memberId },
      query: listQuery.value,
    })
  }
}

function startBatchEdit() {
  if (ui.selectionCount === 0) return
  ui.freezeBatchList()
  void router.push({
    name: 'conversion-batch-edit',
    params: { libraryId: libraryId.value },
    query: listQuery.value,
  })
}

const sessionMemberCount = computed(() => {
  const target = editor.session?.target
  if (!target) return 0
  return target.kind === 'batch' ? target.memberIds.length : target.kind === 'member' ? 1 : 0
})

const canGenerate = computed(() => Boolean(draft.value && operation.value && !operation.value.active_generation))
const counts = computed(() => revision.value?.counts ?? operation.value?.current_revision?.counts ?? null)
/** Any live session for this operation, or a start in flight: its own actions
 *  are the only ones offered, and a double click cannot fire two starts. */
const busy = computed(
  () =>
    generation.store.status === 'streaming' ||
    Boolean(operation.value?.active_execution) ||
    execution.startMutation.isPending.value,
)

/**
 * A planned revision is directly executable — generating the plan was the
 * gate, and nothing here writes files until the user starts a run. Eligibility
 * and the session options (the obsolete-audio handling the settings chose) are
 * the server's; the local rule only decides whether the button may open.
 */
const executeError = ref<string | null>(null)
const generateError = ref<string | null>(null)

const GENERATE_CODES: Record<string, string> = {
  INVALID_POLICY: '设置不完整，无法生成：检查分类标签（至少一个非空标签）与目标输出（编码目标必须填码率）',
  NO_ACTIVE_MEMBERS: '所有文件夹都被排除，至少要恢复一个才能生成',
  SCAN_IN_PROGRESS: '媒体库正在扫描，等扫描结束后再生成',
  EXECUTION_IN_PROGRESS: '该操作已有执行在进行中，等它结束后再生成',
  BUSY: '当前有其他任务或后台维护在进行，稍后再生成',
  VERSION_CONFLICT: '操作版本已变化，请刷新后重试',
  ORPHANED_WORKSET: '媒体库已删除：该记录只读',
  DRAFT_NOT_FOUND: '草稿不存在，请重新加载',
}

const EXECUTION_STATES: Record<string, string> = {
  queued: '排队中',
  running: '执行中',
  succeeded: '已完成',
  failed: '失败',
  canceled: '已取消',
  interrupted: '已中断',
}
/** Why the execution entry is unavailable, in the user's words. */
const EXECUTE_REASONS: Record<string, string> = {
  NOT_CURRENT_REVISION: '该版本已不是当前版本，请刷新后重试',
  DRAFT_CHANGED: '草稿在该版本生成后已改变，需重新生成计划版本',
  INPUT_CHANGED: '文件夹输入已变化，需重新生成计划版本',
  BLOCKED_COMPONENTS: '存在阻塞项，处理后才能执行',
  ALREADY_EXECUTED: '该版本已执行过，需重新生成新版本',
}
const EXECUTE_CODES: Record<string, string> = {
  PLAN_NOT_EXECUTABLE: '该版本当前不可执行',
  EXECUTION_IN_PROGRESS: '该操作已有执行在进行中',
  SCAN_IN_PROGRESS: '媒体库正在扫描，稍后再执行',
  BUSY: '当前有其他任务或后台维护在进行，稍后再执行',
  VERSION_CONFLICT: '操作版本已变化，请刷新后重试',
  IDEMPOTENCY_KEY_REUSED: '该请求与之前的执行冲突，请刷新后重试',
  ORPHANED_WORKSET: '媒体库已删除：该记录只读',
  INVALID_DELETE_MODE: '删除模式无效',
}

/** Why 执行当前版本 is disabled right now; null when it may start. */
const executeBlocked = computed(() => {
  const op = operation.value
  if (!op?.current_revision || op.active_execution) return null
  // The terminal fact outranks the derived ones: once a revision ran, that is
  // the reason it cannot run again, whatever else moved since.
  const ran = revision.value?.execution
  if (ran) return `该版本已执行（${EXECUTION_STATES[ran.status] ?? ran.status}），需重新生成新版本`
  if (op.planning_state === 'orphaned') return '媒体库已删除：该记录只读'
  if (op.active_generation || op.planning_state === 'planning') return '生成中，完成后才能执行'
  if (op.planning_state === 'needs_planning') return '草稿已改变，需重新生成计划版本'
  if (op.current_revision.validation_state === 'stale') return '输入已变化，需重新生成计划版本'
  if (op.current_revision.validation_state === 'unavailable') return '媒体库不可用，无法校验该版本'
  if ((counts.value?.blocked ?? 0) > 0) return '存在阻塞项，处理后才能执行'
  return null
})

/** A refused generation start is stated in place, never swallowed. */
async function onGenerate() {
  generateError.value = null
  try {
    await startGeneration()
  } catch (error) {
    const apiError = error as { code?: string; message?: string }
    const reason = (apiError.code ? GENERATE_CODES[apiError.code] : undefined) ?? apiError.message ?? '未知错误'
    generateError.value = `无法生成：${reason}`
  }
}

async function startExecution(folderPaths?: string[]) {
  const op = operation.value
  const planId = op?.current_revision?.plan_id
  if (!worksetId.value || !op || !planId) return
  executeError.value = null
  try {
    await execution.start({
      worksetId: worksetId.value,
      operation: 'conversion',
      planId,
      ifMatchVersion: op.version,
      idempotencyKey: crypto.randomUUID(),
      folderPaths,
    })
    await openExecution()
  } catch (error) {
    // The refusal is explained against refreshed state; the request is never
    // retried with a fresh key (that would be a second run attempt).
    const apiError = error as { code?: string; details?: string[]; message?: string }
    const reasons = (apiError.details ?? []).map((reason) => EXECUTE_REASONS[reason] ?? reason)
    const head = apiError.code ? (EXECUTE_REASONS[apiError.code] ?? EXECUTE_CODES[apiError.code]) : undefined
    executeError.value =
      reasons.length > 0
        ? `无法执行：${reasons.join('；')}`
        : `无法执行：${head ?? apiError.message ?? '未知错误'}`
  }
}

/** The plan may run right now: the header's 执行当前版本 gate, reused. */
const planRunnable = computed(
  () => Boolean(operation.value?.current_revision) && !operation.value?.active_execution && executeBlocked.value === null,
)

/** The selection as the record names it — the scope of a partial run. */
const selectedFolderPaths = computed(() =>
  members.value.filter((member) => ui.selectedMemberIds.has(member.member_id)).map((member) => member.rel_path),
)

async function openExecution() {
  await router.push({
    name: 'conversion-execution',
    params: { libraryId: libraryId.value },
    query: listQuery.value,
  })
}

async function cancelExecution() {
  const active = operation.value?.active_execution
  if (!worksetId.value || !active) return
  await execution.cancel(worksetId.value, 'conversion', active.execution_id)
}

// Reload and route-return recovery: re-attach the live session's stream. The
// store refuses an attach while it already streams the same session, so the
// initial start and this effect never double-stream.
watch(
  () => operation.value?.active_execution?.execution_id ?? null,
  (executionId) => {
    if (!executionId || !worksetId.value) return
    if (execution.store.status === 'streaming' && execution.store.executionId === executionId) return
    execution.attach(worksetId.value, 'conversion', executionId)
  },
  { immediate: true },
)

/**
 * Why 转换全局设置 cannot be edited right now — generating, orphaned or a
 * frozen plan being reviewed. The navigation entry is disabled with the reason
 * instead of opening a form whose save the server would refuse.
 */
const settingsBlockedReason = computed(() => settingsEditBlockedReason(operation.value))

/** The page in view, for the narrow header context. */
const currentPage = computed(() => {
  if (filesOpen.value) return '文件'
  if (detailOpen.value) return carrierTitle.value
  return '转换'
})

/** The fixed parent of the current page, never guessed from history. */
const parentLink = computed(() => {
  if (route.name === 'conversion-member-edit') {
    return {
      to: {
        name: 'conversion-member',
        params: { libraryId: libraryId.value, memberId: route.params.memberId },
        query: listQuery.value,
      },
      label: '文件夹详情',
    }
  }
  if (filesOpen.value || detailOpen.value) {
    return { to: listRoute.value, label: '转换' }
  }
  return { to: overviewRoute.value, label: '概览与成员' }
})
</script>

<template>
  <WorkbenchShell
    :detail-column="!filesOpen"
    :context-title="record?.title ?? '…'"
    :context-page="currentPage"
    :context-back-to="parentLink.to"
    :context-back-label="parentLink.label"
  >
    <template #header>
      <nav aria-label="面包屑" class="flex min-w-0 items-center gap-1.5 text-xs" data-testid="workspace-breadcrumb">
        <RouterLink to="/worksets" class="rounded px-1 py-0.5 text-[var(--text-secondary)] hover:bg-muted">
          工作集
        </RouterLink>
        <span aria-hidden="true" class="text-[var(--text-muted)]">/</span>
        <RouterLink :to="overviewRoute" class="max-w-40 truncate rounded px-1 py-0.5 hover:bg-muted">
          {{ record?.library?.name ?? '…' }}
        </RouterLink>
        <span aria-hidden="true" class="text-[var(--text-muted)]">/</span>
        <RouterLink
          :to="listRoute"
          :class="detailOpen || filesOpen ? 'rounded px-1 py-0.5 hover:bg-muted' : 'rounded px-1 py-0.5 font-medium'"
          :aria-current="detailOpen || filesOpen ? undefined : 'page'"
        >
          转换
        </RouterLink>
        <template v-if="detailOpen">
          <span aria-hidden="true" class="text-[var(--text-muted)]">/</span>
          <span class="max-w-40 truncate px-1 font-medium text-[var(--text-secondary)]" aria-current="page">
            {{ carrierTitle }}
          </span>
        </template>
        <template v-if="filesOpen">
          <span aria-hidden="true" class="text-[var(--text-muted)]">/</span>
          <span class="px-1 font-medium text-[var(--text-secondary)]" aria-current="page">文件</span>
        </template>
      </nav>
    </template>

    <template #nav>
      <WorkbenchNav :library-id="libraryId" :settings-blocked-reason="settingsBlockedReason" />
    </template>

    <template #main="{ tier }">
      <!-- The record is gone or the library has none: the workbench says which
           and offers the one action that makes sense (ADR 0001 §2). -->
      <div
        v-if="!recordQuery.isPending.value && !record"
        class="grid min-h-0 flex-1 place-items-center px-6 text-center"
        data-testid="no-record"
      >
        <div class="max-w-sm">
          <h2 class="font-heading text-sm font-semibold">这个媒体库还没有转换记录</h2>
          <p class="mt-1 text-xs leading-5 text-[var(--text-muted)]">
            回到概览，勾选要转换的文件夹后点击“进入转换”。
          </p>
          <Button class="mt-3" size="sm" data-testid="goto-overview" @click="gotoOverview">
            返回概览
          </Button>
        </div>
      </div>

      <div class="relative flex min-h-0 flex-1 flex-col">
      <!-- The list is covered, never replaced, while a member's files are
           open: it keeps its layout, and with it the selection, the filters
           and the scroll position. A narrow container shows one layer at
           a time, so an open drill-down takes the main area there. -->
      <div
        v-show="record && (tier !== 'narrow' || !detailOpen)"
        class="flex min-h-0 flex-1 flex-col"
      >
        <section aria-label="转换" class="flex min-h-0 flex-1 flex-col">
          <OperationHeader
            :title="record?.title ?? '转换记录'"
            :library-name="record?.library?.name ?? null"
            :operation="operation"
            :counts="counts"
            :validation-state="operation?.current_revision?.validation_state ?? null"
            :revision-label="null"
            :generating="generation.store.status === 'streaming'"
            :can-generate="canGenerate"
            :execute-blocked="executeBlocked"
            :show-execution="Boolean(executionView)"
            :canceling="execution.store.canceling"
            :busy="busy"
            @generate="onGenerate()"
            @cancel="operation?.active_generation && generation.cancel(worksetId!, 'conversion', operation.active_generation.generation_id)"
            @execute="startExecution()"
            @open-execution="openExecution()"
            @cancel-execution="cancelExecution()"
            @restore-current="undefined"
          />

          <!-- A refused start is stated in place; the request is never
               retried with a fresh key. -->
          <p
            v-if="executeError"
            class="border-b border-border bg-card px-3 py-1.5 text-[11px] text-[var(--danger-ink)]"
            role="alert"
            data-testid="execute-error"
          >
            {{ executeError }}
          </p>
          <p
            v-if="generateError"
            class="border-b border-border bg-card px-3 py-1.5 text-[11px] text-[var(--danger-ink)]"
            role="alert"
            data-testid="generate-error"
          >
            {{ generateError }}
          </p>

          <div
            v-if="ui.selectionCount > 0"
            class="flex flex-wrap items-center gap-2 border-b border-border bg-[var(--brand-weak)] px-3 py-1.5"
            data-testid="batch-toolbar"
          >
            <span class="text-xs font-medium">已选 {{ ui.selectionCount }} 个文件夹</span>
            <Button size="xs" variant="secondary" data-testid="batch-edit" @click="startBatchEdit">
              批量修改
            </Button>
            <!-- Executing a part of the revision: the same gate as 执行当前版本,
                 and the sentence that says what happens to the rest. -->
            <Button
              size="xs"
              variant="outline"
              data-testid="execute-selected"
              :disabled="!planRunnable"
              :title="planRunnable ? '只执行选中的文件夹；未选中的需重新生成计划后执行' : (executeBlocked ?? '还没有可执行的计划版本')"
              @click="startExecution(selectedFolderPaths)"
            >
              仅执行选中（{{ selectedFolderPaths.length }}）
            </Button>
            <Button size="xs" variant="ghost" @click="ui.clearSelection()">清除选择</Button>
          </div>

          <div
            v-if="editor.session && sessionMemberCount > 0"
            class="flex flex-wrap items-center gap-2 border-b border-border bg-card px-3 py-1.5"
            data-testid="edit-session-bar"
          >
            <span class="text-[11px]">
              正在编辑：{{ editor.session.target.kind === 'common' ? '全局设置' : `${sessionMemberCount} 个文件夹` }}
            </span>
            <Badge :tone="editor.isDirty ? 'warning' : 'neutral'">{{ editor.isDirty ? '有未应用修改' : '无修改' }}</Badge>
            <Button
              size="xs"
              :disabled="!editor.isDirty || editor.session.applying"
              data-testid="continue-editing"
              @click="continueEditing"
            >
              继续编辑
            </Button>
            <Button
              size="xs"
              :disabled="!editor.isDirty || editor.session.applying"
              data-testid="apply-session"
              @click="applySession()"
            >
              应用
            </Button>
            <Button size="xs" variant="ghost" data-testid="discard-session" @click="editor.close()">放弃修改</Button>
          </div>

          <MemberList
            v-if="draft"
            class="min-h-0 flex-1"
            :members="members"
            :draft="draft.document"
            :selected-ids="ui.selectedMemberIds"
            :hidden-selected-count="ui.hiddenSelectedCount"
            :filter="filter"
            :search="ui.search"
            :conclusion-for="conclusionFor"
            :compact="tier === 'narrow'"
            @toggle="ui.toggleMember($event)"
            @toggle-all="ui.toggleAllVisible($event)"
            @open="openMember($event)"
            @files="openMemberFiles($event)"
            @update:filter="setFilter"
            @update:search="ui.setSearch($event)"
          />
          <p v-else-if="record" class="p-3 text-xs text-[var(--text-muted)]">加载中…</p>
        </section>
      </div>

      <!-- Narrow: a carrier takes the main area, one layer at a time. It is
           rendered outside the list container (which is hidden in this state)
           so the drill-down is actually reachable. -->
      <div v-if="tier === 'narrow' && detailOpen && record && !filesOpen" class="min-h-0 flex-1 overflow-y-auto">
        <RouterView />
      </div>

      <!-- A member's file page: the shared module, drawn over the list. It is
           the only RouterView for this route, so the page mounts exactly once. -->
      <div v-if="record && filesOpen" class="absolute inset-0 z-10 flex min-h-0 flex-col bg-background">
        <RouterView />
      </div>
      </div>
    </template>

    <template #detail>
      <RouterView v-if="!filesOpen" />
      <p v-if="!detailOpen && !filesOpen" class="p-3 text-[11px] text-[var(--text-muted)]">
        选择列表中的文件夹查看其冻结设置与计划结论。
      </p>
    </template>

    <template #detail-modal>
      <Sheet :open="detailOpen" :title="carrierTitle" @close="closeDetail">
        <RouterView />
      </Sheet>
    </template>
  </WorkbenchShell>
</template>
