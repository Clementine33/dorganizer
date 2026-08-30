<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { AlertTriangle } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { ApiError, useApiClient } from '@/lib/api/client'
import { errorDetails } from '@/lib/api/error'
import type { DraftResponse, Workset, WorkflowInput } from '@/lib/api/types'
import {
  saveDraftMutationOptions,
  syncAfterDraftConflict,
  workflowPresetListQueryOptions,
} from '@/queries/worksets'
import { useWorksetGeneration } from '@/composables/use-workset-generation'
import PolicyEditor from './PolicyEditor.vue'
import RevisionReviewPanel from './RevisionReviewPanel.vue'
import GenerationProgressBar from './GenerationProgressBar.vue'
import { formatWorksetTime, generationStatusLabelOf, planningStateLabelOf, planningStateToneOf, toneClass } from './workset-status'

// The right pane of the workset workbench: header + stage strip
// (configure/review, with execute/result honestly locked) + the draft
// composer or the revision review. Generation SSE state is attached here.

const props = defineProps<{
  workset: Workset | null
  detailError: Error | null
  detailLoading: boolean
  draftQueryData: DraftResponse | null
  revisionList: { plan_id: string; revision_index: number; created_at: string; status: string; summary_reason: string; validation_state: string; stale: boolean | null; blocked_count: number }[]
}>()

const api = useApiClient()
const queryClient = useQueryClient()
const generation = useWorksetGeneration()

const stage = ref<'configure' | 'review'>('configure')
const dirty = ref(false)
const conflict = ref(false)
const conflictMessage = ref<string | null>(null)

const presetsQuery = useQuery(workflowPresetListQueryOptions(api))
const presets = computed(() => presetsQuery.data.value ?? [])

const saveMutation = useMutation(saveDraftMutationOptions(api, queryClient))

const activeGeneration = computed(() => props.workset?.active_generation ?? null)

// Attach the SSE stream for an already-running session (page reload while a
// generation is active on the backend). Idempotent: the store refuses a new
// attach while streaming.
watch(
  [activeGeneration, () => props.workset?.workset_id],
  ([gen, wsId]) => {
    if (!gen || !wsId || !props.workset) return
    generation.attach(wsId, gen.generation_id)
  },
  { immediate: true },
)

// Terminal SSE outcome: decide the follow-up cache sync. The composable's
// attach already syncs on settle; the flags below drive the inline banner.
const sseError = computed(() =>
  generation.store.terminal === 'transport' && generation.store.status === 'error'
    ? generation.store.errorMessage
    : null,
)

const readOnly = computed(() => props.workset?.planning_state === 'orphaned' || activeGeneration.value !== null)

const worksetId = computed(() => props.workset?.workset_id ?? null)

watch(
  () => props.workset?.workset_id,
  () => {
    dirty.value = false
    conflict.value = false
    conflictMessage.value = null
    stage.value = props.workset?.current_revision ? 'review' : 'configure'
  },
  { immediate: true },
)

async function onSave(input: { workflow: WorkflowInput }) {
  if (!worksetId.value || !props.draftQueryData) return
  conflict.value = false
  try {
    await saveMutation.mutateAsync({
      worksetId: worksetId.value,
      workflow: input.workflow,
      ifMatchVersion: props.draftQueryData.version,
    })
    dirty.value = false
  } catch (error) {
    if (error instanceof ApiError && (error.code === 'VERSION_CONFLICT' || error.code === 'DRAFT_VERSION_CONFLICT')) {
      conflict.value = true
      conflictMessage.value = errorDetails(error).message
      await syncAfterDraftConflict(queryClient, worksetId.value)
    }
  }
}

async function onSaveAndGenerate(input: { workflow: WorkflowInput }) {
  if (!worksetId.value) return
  // Dirty changes are saved first (serial), then the generation starts with
  // the fresh aggregate version from the save response.
  const saved = dirty.value || props.draftQueryData === null
  let expectedVersion: number | undefined
  if (saved) {
    conflict.value = false
    try {
      const view = await saveMutation.mutateAsync({
        worksetId: worksetId.value,
        workflow: input.workflow,
        ifMatchVersion: props.draftQueryData?.version ?? props.workset?.version ?? 0,
      })
      dirty.value = false
      expectedVersion = view.version
    } catch (error) {
      if (error instanceof ApiError && (error.code === 'VERSION_CONFLICT' || error.code === 'DRAFT_VERSION_CONFLICT')) {
        conflict.value = true
        conflictMessage.value = errorDetails(error).message
        await syncAfterDraftConflict(queryClient, worksetId.value)
        return
      }
      throw error
    }
  }
  if (expectedVersion === undefined) expectedVersion = props.draftQueryData?.version
  // One idempotency key per user intent (this click).
  try {
    const result = await generation.start({
      worksetId: worksetId.value,
      expectedDraftVersion: expectedVersion,
      idempotencyKey: crypto.randomUUID(),
    })
    if (!result.created) {
      // Unchanged-input replay: draft + inventory unchanged, current revision
      // stands. (A key replay of an in-flight/accepted request instead
      // returns created:false with the generation payload — handled the same
      // way, via the caches the mutation options refresh.)
      if ('revision' in result) {
        unchangedNotice.value = `配置与文件库存未变化，继续使用 Revision v${result.revision.revision_index + 1}`
      } else {
        unchangedNotice.value = '该生成请求已在进行中，正在展示其进度。'
      }
    }
  } catch (error) {
    // The start mutation's error observer drives the action bar banner below;
    // catching here only stops the async handler from rejecting.
    void error
  }
}

const unchangedNotice = ref<string | null>(null)

async function onCancelGeneration() {
  const gen = activeGeneration.value
  if (!gen || !worksetId.value) return
  await generation.cancel(worksetId.value, gen.generation_id)
}

function loadServerVersion() {
  conflict.value = false
  dirty.value = false
}

function discardChanges() {
  conflict.value = false
  dirty.value = false
}

// Generation-start pending state and errors are owned by the composable's
// mutation (the local startMutation above is unused for start flows — the
// composable's save-and-generate path drives it).
const generating = computed(() => generation.startMutation.isPending.value || activeGeneration.value !== null)

// A failed generation start (409 conflicts, network errors) must be visible:
// without this banner the click would silently do nothing.
const startErrorDetails = computed(() =>
  generation.startMutation.error.value ? errorDetails(generation.startMutation.error.value) : null,
)

const detailErrorDetails = computed(() =>
  props.detailError ? errorDetails(props.detailError) : null,
)
</script>

<template>
  <div class="flex h-full min-w-0 flex-col overflow-hidden">
    <!-- Error / loading states for the detail query -->
    <div
      v-if="detailErrorDetails"
      class="flex items-center gap-3 border-b border-destructive/30 bg-destructive/10 px-5 py-3 text-xs text-destructive"
      data-testid="workset-detail-error"
    >
      <AlertTriangle class="size-4 shrink-0" />
      <span class="shrink-0 font-mono font-semibold">{{ detailErrorDetails.code }}</span>
      <span class="min-w-0 flex-1">{{ detailErrorDetails.message }}</span>
      <Button variant="outline" size="sm" @click="queryClient.invalidateQueries()">重试</Button>
    </div>
    <div v-else-if="detailLoading || !workset" class="grid flex-1 place-items-center text-xs text-muted-foreground" data-testid="workset-detail-loading">
      正在读取工作集…
    </div>

    <template v-else>
      <!-- Header -->
      <header class="shrink-0 border-b border-border px-5 py-3" data-testid="workset-header">
        <div class="flex items-center gap-2.5">
          <h1 class="min-w-0 truncate font-heading text-lg font-semibold tracking-tight">{{ workset.title }}</h1>
          <span class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold" :class="toneClass[planningStateToneOf(workset.planning_state)]" data-testid="workset-planning-state">
            {{ planningStateLabelOf(workset.planning_state) }}
          </span>
          <span
            v-if="workset.current_revision?.stale === true"
            class="shrink-0 rounded-full px-2 py-0.5 text-[10px] font-semibold"
            :class="toneClass.warn"
            data-testid="workset-stale-badge"
          >
            已过期
          </span>
          <span class="ml-auto shrink-0 font-mono text-[10px] text-muted-foreground">v{{ workset.version }}</span>
        </div>
        <p class="mt-0.5 truncate text-[11px] text-muted-foreground">
          {{ workset.library ? `${workset.library.name} · ${workset.members.length} 个专辑批次 · ` : '' }}更新于 {{ formatWorksetTime(workset.updated_at) }}
        </p>
      </header>

      <!-- Active generation progress -->
      <GenerationProgressBar
        v-if="activeGeneration"
        :progress="activeGeneration"
        :canceling="generation.cancelMutation.isPending.value"
        @cancel="onCancelGeneration"
      />

      <!-- Generation-start failure (409 conflicts, network errors) -->
      <div
        v-if="startErrorDetails"
        class="flex items-start gap-2 border-b border-destructive/30 bg-destructive/10 px-5 py-2 text-[11px] text-destructive"
        data-testid="generation-start-error"
      >
        <AlertTriangle class="mt-0.5 size-3.5 shrink-0" />
        <span>
          无法开始生成：<span class="font-mono font-semibold">{{ startErrorDetails.code }}</span>
          {{ startErrorDetails.message }}
        </span>
      </div>

      <!-- Transport error / latest generation failure banners -->
      <div
        v-else-if="sseError"
        class="border-b border-amber-500/40 bg-amber-500/10 px-5 py-2 text-[11px] text-amber-700 dark:text-amber-400"
        data-testid="generation-transport-error"
      >
        {{ sseError }}
      </div>
      <div
        v-else-if="!activeGeneration && workset.latest_generation && (workset.latest_generation.status === 'failed' || workset.latest_generation.status === 'interrupted')"
        class="flex items-start gap-2 border-b border-destructive/30 bg-destructive/10 px-5 py-2 text-[11px] text-destructive"
        data-testid="latest-generation-failure"
      >
        <AlertTriangle class="mt-0.5 size-3.5 shrink-0" />
        <span>
          最近一次生成{{ generationStatusLabelOf(workset.latest_generation.status) }}：
          <span class="font-mono font-semibold">{{ workset.latest_generation.error_code }}</span>
          {{ workset.latest_generation.error_message }}
        </span>
      </div>

      <!-- Stage strip (execute/result honestly locked — no execute API) -->
      <nav class="flex shrink-0 items-center gap-1 border-b border-border px-5 py-2" aria-label="工作台阶段">
        <button
          type="button"
          class="rounded-md px-2.5 py-1 text-[11px] font-semibold"
          :class="stage === 'configure' ? 'bg-foreground/10 text-foreground' : 'text-muted-foreground hover:text-foreground'"
          data-testid="stage-configure"
          @click="stage = 'configure'"
        >
          配置流程
        </button>
        <button
          type="button"
          class="rounded-md px-2.5 py-1 text-[11px] font-semibold"
          :class="stage === 'review' ? 'bg-foreground/10 text-foreground' : 'text-muted-foreground hover:text-foreground'"
          :disabled="!workset.current_revision"
          data-testid="stage-review"
          @click="workset.current_revision && (stage = 'review')"
        >
          审阅计划
        </button>
        <span class="mx-1 text-[10px] text-muted-foreground">·</span>
        <span class="flex items-center gap-1 rounded-md px-2.5 py-1 text-[11px] font-semibold text-muted-foreground/50" title="尚未接入执行">
          执行 <span class="rounded border border-border px-1 font-mono text-[9px]">锁定</span>
        </span>
        <span class="flex items-center gap-1 rounded-md px-2.5 py-1 text-[11px] font-semibold text-muted-foreground/50" title="尚未接入执行">
          结果 <span class="rounded border border-border px-1 font-mono text-[9px]">锁定</span>
        </span>
      </nav>

      <p v-if="unchangedNotice && stage === 'configure'" class="shrink-0 border-b border-sky-500/30 bg-sky-500/10 px-5 py-2 text-[11px] text-sky-700 dark:text-sky-400" data-testid="unchanged-replay-notice">
        {{ unchangedNotice }}
      </p>

      <!-- Configure stage -->
      <PolicyEditor
        v-if="stage === 'configure'"
        :draft="draftQueryData"
        :presets="presets"
        :saving="saveMutation.isPending.value"
        :generating="generating"
        :conflict="conflict"
        :conflict-message="conflictMessage"
        :dirty="dirty"
        :read-only="readOnly"
        @save="onSave"
        @save-and-generate="onSaveAndGenerate"
        @load-server-version="loadServerVersion"
        @discard="discardChanges"
        @update:dirty="dirty = $event"
      />

      <!-- Review stage -->
      <RevisionReviewPanel
        v-else-if="stage === 'review'"
        :workset="workset"
        :revision-list="revisionList"
      />
    </template>
  </div>
</template>
