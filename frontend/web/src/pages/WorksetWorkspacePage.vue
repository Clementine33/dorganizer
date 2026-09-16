<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter, type RouteLocationRaw } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Sheet } from '@/components/ui/modal'
import WorkbenchShell from '@/components/layout/WorkbenchShell.vue'
import WorkbenchNav from '@/features/worksets/WorkbenchNav.vue'
import OperationHeader from '@/features/worksets/OperationHeader.vue'
import { workbenchNav } from '@/features/worksets/workbench-nav'
import MemberList from '@/features/worksets/MemberList.vue'
import {
  memberConclusion,
  partitionStatus,
  revisionComponents,
  type MemberConclusion,
} from '@/features/worksets/plan-readers'
import { useOperationContext } from '@/composables/use-operation-context'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import { useWorksetUiStore } from '@/stores/workset-ui'
import type { ComponentOutcome, PlanningState, WorksetMember } from '@/lib/api/types'

/**
 * The workset workspace: 概览与成员 and 转换 are the two sections of ONE page, so
 * reaching the operation is never a jump to another page and neither section
 * rebuilds the workbench (A1, R01). The URL picks the section —
 * `/worksets/:id` is the overview, `/worksets/:id/conversion` the operation —
 * and the contextual navigation switches between them.
 *
 * Detail and edit entries are child routes rendered in the carrier the
 * container chose: inline beside the list on wide, a modal sheet on mid, the
 * full main view on narrow, one carrier at a time (R01, F09, §7.3).
 */
const route = useRoute()
const router = useRouter()
const ui = useWorksetUiStore()
const editor = useWorksetEditorStore()

const worksetId = computed(() => (route.params.worksetId as string) || null)
const revisionPlanId = computed(() => (route.query.revision as string) || null)
const { workspace, generation, execution, executionView, applySession, startGeneration } =
  useOperationContext(worksetId, 'conversion', revisionPlanId)

const workset = workspace.workset
const operation = workspace.operation
const draft = workspace.draft
const revision = workspace.revision

const members = computed<WorksetMember[]>(() => workset.value?.members ?? [])
const readOnly = computed(() => revisionPlanId.value !== null)
const worksetPath = computed(() => `/worksets/${encodeURIComponent(worksetId.value ?? '')}`)
const listPath = computed(() => `${worksetPath.value}/conversion`)
/** Which section is in view; the route, not a local flag, decides. */
const section = computed<'overview' | 'conversion'>(() =>
  route.name === 'workset-overview' ? 'overview' : 'conversion',
)
/** A child route is active: it occupies the carrier of this container tier. */
const detailOpen = computed(() => route.meta.carrier === true)
const carrierTitle = computed(() => (route.meta.title as string | undefined) ?? '详情')
/** The crumb names the subject: a member route is about that folder. */
const carrierCrumb = computed(() => {
  if (route.name === 'conversion-member') {
    const id = route.params.memberId
    const member = members.value.find((m) => m.member_id === id)
    if (member) return member.folder_name
  }
  return carrierTitle.value
})

watch(
  () => worksetId.value,
  () => ui.resetForOperation(),
  { immediate: true },
)

/** Components of one member in the revision being shown, normalized on read. */
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
    hasOperations: components.some((component) => component.operations.length > 0),
    parts: partitionStatus(components),
  })
}

/** The member's own page in the library it came from (gone when orphaned). */
function memberLibraryHref(member: WorksetMember): string | null {
  const library = workset.value?.library
  if (!library) return null
  return `/libraries/${encodeURIComponent(library.library_id)}/folders/${encodeURIComponent(member.folder_id)}`
}

async function openMember(memberId: string) {
  await router.push({ path: `${listPath.value}/members/${encodeURIComponent(memberId)}`, query: route.query })
}

async function closeDetail() {
  await router.push({ path: listPath.value, query: route.query })
  await nextTick()
}

function startBatchEdit() {
  if (ui.selectionCount === 0) return
  ui.freezeBatchList()
  void router.push(`${listPath.value}/batch-edit`)
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
  VERSION_CONFLICT: '操作版本已变化，请刷新后重试',
  IDEMPOTENCY_KEY_REUSED: '该请求与之前的执行冲突，请刷新后重试',
  ORPHANED_WORKSET: '媒体库已删除：该工作集只读',
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
  if (op.planning_state === 'orphaned') return '媒体库已删除：该工作集只读'
  if (op.active_generation || op.planning_state === 'planning') return '生成中，完成后才能执行'
  if (op.planning_state === 'needs_planning') return '草稿已改变，需重新生成计划版本'
  if (op.current_revision.validation_state === 'stale') return '输入已变化，需重新生成计划版本'
  if (op.current_revision.validation_state === 'unavailable') return '媒体库不可用，无法校验该版本'
  if ((counts.value?.blocked ?? 0) > 0) return '存在阻塞项，处理后才能执行'
  return null
})

async function startExecution() {
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
    })
    // The run just started: open its detail carrier (inline on wide, sheet on
    // mid, full page on narrow) instead of crowding the member list.
    await openExecution()
  } catch (error) {
    // The refusal is explained against refreshed state; the request is never
    // retried with a fresh key (that would be a second run attempt).
    const apiError = error as { code?: string; details?: string[]; message?: string }
    const reasons = (apiError.details ?? []).map((reason) => EXECUTE_REASONS[reason] ?? reason)
    const head = apiError.code ? EXECUTE_REASONS[apiError.code] ?? EXECUTE_CODES[apiError.code] : undefined
    executeError.value =
      reasons.length > 0
        ? `无法执行：${reasons.join('；')}`
        : `无法执行：${head ?? apiError.message ?? '未知错误'}`
  }
}

async function openExecution() {
  await router.push({ path: `${listPath.value}/execution`, query: route.query })
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

const OPERATION_STATES: Record<PlanningState, { tone: 'neutral' | 'brand' | 'success' | 'warning' | 'danger'; label: string }> = {
  unplanned: { tone: 'neutral', label: '待规划' },
  planning: { tone: 'brand', label: '规划中' },
  planned: { tone: 'success', label: '已规划' },
  needs_planning: { tone: 'warning', label: '需重新规划' },
  orphaned: { tone: 'danger', label: '只读（媒体库已删除）' },
}
const operationState = computed(() => OPERATION_STATES[operation.value?.planning_state ?? 'unplanned'])
const revisionLabel = computed(() => {
  const index = operation.value?.current_revision?.revision_index
  return index === undefined ? '尚无版本' : `当前版本 #${index}`
})

/**
 * Why 转换全局设置 cannot be edited right now — generating, orphaned or
 * historical (E09). The navigation entry is disabled with the reason instead of
 * opening a form whose save the server would refuse.
 */
const settingsBlockedReason = computed(() => {
  if (readOnly.value) return '历史版本只读：先回到当前草稿再编辑'
  if (operation.value?.planning_state === 'orphaned') return '媒体库已删除：该工作集只读'
  if (operation.value?.active_generation) return '正在生成计划版本：完成后才能修改设置'
  return null
})

/** The page in view, for the narrow header context (N31). Section labels come
 *  from the navigation definition, so a renamed section is renamed once. */
const currentPage = computed(() => {
  if (detailOpen.value) return carrierTitle.value
  return workbenchNav(worksetId.value ?? '').find((item) => item.id === section.value)?.label ?? carrierTitle.value
})

/** The fixed parent of the current page (N32), never guessed from history. */
const parentLink = computed<{ to: RouteLocationRaw; label: string }>(() => {
  const id = worksetId.value ?? ''
  // A carried-over revision stays read-only: the parent link keeps the query
  // the carrier was opened with, exactly like closing the carrier does (R03).
  if (route.name === 'conversion-member-edit') {
    return {
      to: { name: 'conversion-member', params: { worksetId: id, memberId: route.params.memberId }, query: route.query },
      label: '文件夹详情',
    }
  }
  if (detailOpen.value) return { to: { name: 'conversion', params: { worksetId: id }, query: route.query }, label: '转换' }
  if (section.value === 'conversion') return { to: { name: 'workset-overview', params: { worksetId: id } }, label: '概览与成员' }
  return { to: { name: 'worksets' }, label: '工作集列表' }
})
</script>

<template>
  <WorkbenchShell
    :detail-column="section === 'conversion'"
    :context-title="workset?.title ?? '…'"
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
        <RouterLink :to="worksetPath" class="max-w-40 truncate rounded px-1 py-0.5 font-medium hover:bg-muted">
          {{ workset?.title ?? '…' }}
        </RouterLink>
        <template v-if="section === 'conversion'">
          <span aria-hidden="true" class="text-[var(--text-muted)]">/</span>
          <RouterLink
            :to="listPath"
            :class="detailOpen ? 'rounded px-1 py-0.5 hover:bg-muted' : 'rounded px-1 py-0.5 font-medium'"
            :aria-current="detailOpen ? undefined : 'page'"
          >
            转换
          </RouterLink>
        </template>
        <template v-if="detailOpen">
          <span aria-hidden="true" class="text-[var(--text-muted)]">/</span>
          <span class="max-w-40 truncate px-1 font-medium text-[var(--text-secondary)]" aria-current="page">
            {{ carrierCrumb }}
          </span>
        </template>
      </nav>
    </template>

    <template #nav>
      <WorkbenchNav :workset-id="worksetId ?? ''" :settings-blocked-reason="settingsBlockedReason" />
    </template>

    <template #main="{ tier }">
      <!-- 概览与成员: the workset itself — identity, its operation and the
           folders it holds (R07). -->
      <section v-if="section === 'overview'" class="min-h-0 flex-1 overflow-y-auto" data-testid="workset-overview">
        <div class="mx-auto max-w-3xl p-4">
          <h1 class="font-heading text-base font-semibold tracking-tight">{{ workset?.title ?? '…' }}</h1>
          <p v-if="workset?.library" class="mt-0.5 font-mono text-[10px] text-[var(--text-muted)]">
            {{ workset.library.name }} · {{ workset.library.root_path }}
          </p>

          <section class="mt-4">
            <h2 class="text-xs font-semibold text-[var(--text-secondary)]">操作</h2>
            <RouterLink
              :to="listPath"
              class="mt-1 flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card px-3 py-2 hover:border-[var(--brand-border)] focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none"
              data-testid="overview-operation"
            >
              <span class="text-xs font-medium">转换</span>
              <Badge :tone="operationState.tone">{{ operationState.label }}</Badge>
              <span class="font-mono text-[10px] text-[var(--text-muted)]">{{ revisionLabel }}</span>
              <span v-if="counts" class="text-[10px] text-[var(--text-muted)]">
                {{ counts.changed }} 个有变化 · {{ counts.blocked }} 个阻塞
              </span>
              <span class="ml-auto text-[11px] text-[var(--brand-ink)]">进入转换 →</span>
            </RouterLink>
          </section>

          <section class="mt-4">
            <h2 class="text-xs font-semibold text-[var(--text-secondary)]">成员（{{ members.length }}）</h2>
            <ul class="mt-1 divide-y divide-border rounded-lg border border-border bg-card" data-testid="overview-members">
              <li v-for="member in members" :key="member.member_id">
                <component
                  :is="memberLibraryHref(member) ? RouterLink : 'div'"
                  :to="memberLibraryHref(member) ?? undefined"
                  class="block px-3 py-1.5"
                  :class="memberLibraryHref(member) ? 'hover:bg-muted focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none' : ''"
                  :title="memberLibraryHref(member) ? '在媒体库中查看' : undefined"
                  data-testid="overview-member"
                >
                  <span class="block truncate text-xs">{{ member.folder_name }}</span>
                  <span class="block truncate font-mono text-[10px] text-[var(--text-muted)]" :title="member.folder_path">
                    {{ member.rel_path }}
                  </span>
                </component>
              </li>
              <li v-if="members.length === 0" class="px-3 py-2 text-[11px] text-[var(--text-muted)]">该工作集没有成员。</li>
            </ul>
          </section>
        </div>
      </section>

      <!-- 转换: the operation itself. A narrow container shows one layer at a
           time, so an open drill-down takes the main area (R06, §7.3). -->
      <div v-else-if="tier !== 'narrow' || !detailOpen" class="flex min-h-0 flex-1 flex-col">
        <section aria-label="转换" class="flex min-h-0 flex-1 flex-col">
          <OperationHeader
            :title="workset?.title ?? '工作集'"
            :library-name="workset?.library?.name ?? null"
            :operation="operation"
            :counts="counts"
            :validation-state="revision ? null : operation?.current_revision?.validation_state ?? null"
            :revision-label="revisionPlanId ? `历史版本 #${revision?.revision_index ?? ''}` : null"
            :generating="generation.store.status === 'streaming'"
            :can-generate="canGenerate"
            :execute-blocked="executeBlocked"
            :show-execution="Boolean(executionView)"
            :canceling="execution.store.canceling"
            :busy="busy"
            @generate="startGeneration()"
            @cancel="operation?.active_generation && generation.cancel(worksetId!, 'conversion', operation.active_generation.generation_id)"
            @execute="startExecution()"
            @open-execution="openExecution()"
            @cancel-execution="cancelExecution()"
            @restore-current="revisionPlanId ? router.push({ path: listPath, query: {} }) : undefined"
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

          <div
            v-if="ui.selectionCount > 0"
            class="flex flex-wrap items-center gap-2 border-b border-border bg-[var(--brand-weak)] px-3 py-1.5"
            data-testid="batch-toolbar"
          >
            <span class="text-xs font-medium">已选 {{ ui.selectionCount }} 个文件夹</span>
            <Button size="xs" variant="secondary" :disabled="readOnly" data-testid="batch-edit" @click="startBatchEdit">
              批量修改
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
              @click="router.push(`${listPath}/${editor.session.target.kind === 'common' ? 'settings' : editor.session.target.kind === 'batch' ? 'batch-edit' : `members/${editor.session.target.memberId}/edit`}`)"
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
            :filter="ui.filter"
            :search="ui.search"
            :conclusion-for="conclusionFor"
            :member-library-href="memberLibraryHref"
            :historical="readOnly"
            :compact="tier === 'narrow'"
            @toggle="ui.toggleMember($event)"
            @toggle-all="ui.toggleAllVisible($event)"
            @open="openMember($event)"
            @update:filter="ui.setFilter($event)"
            @update:search="ui.setSearch($event)"
          />
          <p v-else-if="!worksetId" class="p-3 text-xs text-[var(--danger-ink)]" role="alert">未知工作集地址。</p>
          <p v-else class="p-3 text-xs text-[var(--text-muted)]">加载中…</p>
        </section>
      </div>

      <div v-else class="min-h-0 flex-1 overflow-y-auto">
        <RouterView />
      </div>
    </template>

    <template #detail>
      <RouterView />
      <p v-if="!detailOpen" class="p-3 text-[11px] text-[var(--text-muted)]">
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
