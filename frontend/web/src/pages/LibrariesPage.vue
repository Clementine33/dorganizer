<script setup lang="ts">
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, ref, watchEffect } from 'vue'
import { AlertTriangle, Pencil, Plus, RefreshCw, ScanLine, Square } from '@lucide/vue'
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import { useLibraryScan } from '@/composables/use-library-scan'
import BatchPlanBar from '@/features/libraries/BatchPlanBar.vue'
import FolderFlatList from '@/features/libraries/FolderFlatList.vue'
import LibraryManager from '@/features/libraries/LibraryManager.vue'
import ScanProgressBar from '@/features/libraries/ScanProgressBar.vue'
import { useApiClient } from '@/lib/api/client'
import { errorDetails } from '@/lib/api/error'
import { rootPathIdentityKey } from '@/lib/root-path-identity'
import type { CreateLibraryInput } from '@/lib/api/types'
import {
  createLibraryMutationOptions,
  deleteLibraryMutationOptions,
  folderListQueryOptions,
  updateLibraryMutationOptions,
  useLibraryList,
  useRootIdentity,
} from '@/queries/libraries'
import { createPlanMutationOptions } from '@/queries/plans'
import { useLibraryUiStore } from '@/stores/library-ui'
import { useScanStore } from '@/stores/scan'

const api = useApiClient()
const router = useRouter()
const queryClient = useQueryClient()
const ui = useLibraryUiStore()
const scan = useScanStore()
const libraryScan = useLibraryScan()
const managerOpen = ref(false)
const editing = ref(false)
const savingLibrary = ref(false)

const { query: librariesQuery, librariesData, activeLibrary } = useLibraryList()
// Query result flags are refs; expose them as plain top-level computeds so
// template expressions (v-if/v-else-if) see booleans instead of ref objects.
const librariesPending = computed(() => librariesQuery.isPending.value)
const librariesSuccess = computed(() => librariesQuery.isSuccess.value)
const rootIdentity = useRootIdentity(activeLibrary)
const foldersQuery = useQuery(() => folderListQueryOptions(api, ui.activeLibraryId, rootIdentity.value))
const foldersPending = computed(() => foldersQuery.isPending.value)
const foldersSuccess = computed(() => foldersQuery.isSuccess.value)
const foldersError = computed(() => foldersQuery.isError.value)
const folders = computed(() => foldersQuery.data.value ?? [])
const allFoldersSelected = computed(
  () => folders.value.length > 0 && ui.selectedFolderIds.length === folders.value.length,
)

// Selection reconciliation: whenever the active library's folder result
// changes, drop IDs that no longer exist. Old-library results are ignored
// inside the store (guarded by the active ID).
watchEffect(() => {
  if (foldersQuery.data.value) ui.reconcileFolders(ui.activeLibraryId ?? '', foldersQuery.data.value)
})

const createMutation = useMutation(createLibraryMutationOptions(api, queryClient))
const updateMutation = useMutation(updateLibraryMutationOptions(api, queryClient))
const deleteMutation = useMutation(deleteLibraryMutationOptions(api, queryClient))
const createPlanMutation = useMutation(createPlanMutationOptions(api, queryClient))
const planPending = computed(() => createPlanMutation.isPending.value)

// A scan running (or just finished) for another library refreshes the shared
// library/folder queries in the background while this page is open; a
// transient failure of that refresh would otherwise surface an unrelated
// error banner here. User-invoked mutation errors still show.
const backgroundScanOnOtherLibrary = computed(
  () => scan.libraryId !== null && scan.libraryId !== ui.activeLibraryId && scan.status !== 'idle',
)

const pageError = computed(() => {
  // Current query failures take precedence over mutation errors: a mutation
  // error (e.g. a failed createPlan) lingers on its observer until the next
  // mutation, so it must not mask a fresh query failure. Without this, a
  // stale plan error would pin the banner (and its retry) in a dead state.
  if (!backgroundScanOnOtherLibrary.value) {
    const libraryError = librariesQuery.error.value
    if (libraryError) return { ...errorDetails(libraryError), source: 'library' as const }
    const folderError = foldersQuery.error.value
    if (folderError) return { ...errorDetails(folderError), source: 'library' as const }
  }
  for (const mutation of [createMutation, updateMutation, deleteMutation]) {
    if (mutation.error.value) return { ...errorDetails(mutation.error.value), source: 'library' as const }
  }
  if (createPlanMutation.error.value) {
    return { ...errorDetails(createPlanMutation.error.value), source: 'plan' as const }
  }
  return null
})

async function retryPage() {
  if (pageError.value?.source === 'plan') {
    // A plan error can only be fixed by generating another plan. With no
    // folders selected the retry can never succeed, so clear the stale error
    // instead of leaving a permanently dead banner.
    if (ui.selectedFolderIds.length === 0) {
      createPlanMutation.reset()
      return
    }
    await generatePlan()
    return
  }
  // Library mutations never auto-clear their error; the retry button must
  // dismiss the stale mutation error before reloading the queries.
  for (const mutation of [createMutation, updateMutation, deleteMutation]) mutation.reset()
  await Promise.all([librariesQuery.refetch(), foldersQuery.refetch()])
}

function switchLibrary(event: Event) {
  const next = (event.target as HTMLSelectElement).value
  // A terminal scan result belongs to the library it ran on. Clear it when
  // the user switches to another library; otherwise the '扫描完成/扫描失败'
  // banner of the previous library would stay pinned forever (nothing ever
  // resets it). A *running* scan is deliberately kept alive in the background
  // (see scanningActiveLibrary) and must not be reset here.
  if (scan.status !== 'scanning' && scan.status !== 'idle' && scan.libraryId !== null && scan.libraryId !== next) {
    scan.reset()
  }
  ui.setActiveLibrary(next)
}

// Scan UI is scoped to the library the scan belongs to. Switching libraries
// does not cancel the backend scan — it keeps running in the background and
// its progress is hidden until the user switches back.
const scanningActiveLibrary = computed(
  () => scan.status === 'scanning' && scan.libraryId === ui.activeLibraryId,
)

async function runScan() {
  const libraryId = ui.activeLibraryId
  if (!libraryId) return
  await libraryScan.start(libraryId)
}

function setAllFolders(selected: boolean) {
  if (selected) ui.selectAllFolders(folders.value)
  else ui.clearSelection()
}

async function generatePlan() {
  const libraryId = ui.activeLibraryId
  if (!libraryId || ui.selectedFolderIds.length === 0) return
  try {
    const plan = await createPlanMutation.mutateAsync({
      library_id: libraryId,
      folder_ids: [...ui.selectedFolderIds],
      plan_type: 'slim',
      target_format: 'slim:mode1',
      prune_matched_excluded: false,
    })
    await router.push(`/plans/${encodeURIComponent(plan.plan_id)}`)
  } catch {
    // The mutation error surfaces in the page banner via pageError.
  }
}

function openAdd() {
  editing.value = false
  managerOpen.value = true
}

function openEdit() {
  editing.value = true
  managerOpen.value = true
}

async function saveLibrary(input: CreateLibraryInput) {
  savingLibrary.value = true
  try {
    if (editing.value && activeLibrary.value) {
      const previousRoot = activeLibrary.value.root_path
      const updated = await updateMutation.mutateAsync({ id: activeLibrary.value.id, input })
      if (rootPathIdentityKey(previousRoot) !== rootPathIdentityKey(updated.root_path)) {
        // Genuine root change: the backend discarded materialized folders, so
        // any selection now points at rows that no longer exist.
        ui.clearSelection()
      }
    } else {
      const created = await createMutation.mutateAsync(input)
      ui.setActiveLibrary(created.id)
    }
    managerOpen.value = false
  } catch {
    // Mutation errors surface in the page banner via pageError.
  } finally {
    savingLibrary.value = false
  }
}

async function removeLibrary(id: string) {
  if (!window.confirm('删除这个媒体库条目？磁盘上的音频文件不会被删除。')) return
  savingLibrary.value = true
  try {
    await deleteMutation.mutateAsync(id)
    // The delete mutation updates the libraries list in the cache; the
    // useLibraryList watchEffect reconciles the UI store (active ID fallback
    // and selection clearing) on the same data.
    managerOpen.value = false
  } catch {
    // Keep the dialog open and expose the mutation error in the banner.
  } finally {
    savingLibrary.value = false
  }
}
</script>

<template>
  <section class="flex h-full min-w-0 flex-col bg-background">
    <header class="flex min-h-14 shrink-0 items-center gap-3 border-b border-border px-5">
      <div class="min-w-0">
        <p class="font-heading text-sm font-semibold tracking-tight">媒体库文件夹</p>
        <p class="text-[11px] text-muted-foreground">每行是根目录下的一个音频文件夹</p>
      </div>

      <select
        v-if="librariesData.length"
        :value="ui.activeLibraryId ?? ''"
        aria-label="切换媒体库"
        class="ml-auto h-8 max-w-52 rounded-md border border-input bg-background px-2.5 font-heading text-xs font-semibold focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        @change="switchLibrary"
      >
        <option v-for="library in librariesData" :key="library.id" :value="library.id">
          {{ library.name }}
        </option>
      </select>
      <Button v-if="activeLibrary" variant="ghost" size="icon" aria-label="编辑媒体库" @click="openEdit">
        <Pencil class="size-3.5" />
      </Button>
      <Button variant="outline" size="sm" @click="openAdd">
        <Plus class="size-3.5" />
        添加媒体库
      </Button>
    </header>

    <div
      v-if="pageError"
      data-testid="page-error"
      class="flex items-center gap-3 border-b border-destructive/30 bg-destructive/10 px-5 py-3 text-xs text-destructive"
    >
      <AlertTriangle class="size-4 shrink-0" />
      <span v-if="pageError.code" class="shrink-0 font-mono font-semibold">{{ pageError.code }}</span>
      <span class="min-w-0 flex-1">{{ pageError.message }}。请检查后端连接或目录权限后重试。</span>
      <Button data-testid="retry-page" variant="outline" size="sm" @click="retryPage">
        <RefreshCw class="size-3.5" />
        重试
      </Button>
    </div>

    <template v-if="activeLibrary">
      <div class="flex min-h-14 shrink-0 items-center gap-4 border-b border-border px-5">
        <div class="min-w-0">
          <h1 class="truncate font-heading text-lg font-semibold tracking-tight">{{ activeLibrary.name }}</h1>
          <p class="truncate font-mono text-[11px] text-muted-foreground">{{ activeLibrary.root_path }}</p>
        </div>
        <div class="ml-auto flex items-center gap-2">
          <Button
            data-testid="scan-button"
            variant="outline"
            size="sm"
            :disabled="scan.status === 'scanning'"
            @click="runScan"
          >
            <ScanLine class="size-3.5" />
            {{ scan.status === 'scanning' ? '扫描中…' : '扫描' }}
          </Button>
          <Button
            v-if="scanningActiveLibrary"
            data-testid="cancel-scan"
            variant="ghost"
            size="sm"
            @click="libraryScan.cancel"
          >
            <Square class="size-3" />
            取消
          </Button>
        </div>
      </div>

      <ScanProgressBar
        v-if="scan.libraryId === ui.activeLibraryId"
        :status="scan.status"
        :files-scanned="scan.filesScanned"
        :dirs-scanned="scan.dirsScanned"
        :error-code="scan.errorCode"
        :error-message="scan.errorMessage"
      />

      <div class="min-h-0 flex-1">
        <div v-if="foldersPending" class="grid h-full place-items-center text-xs text-muted-foreground">
          正在读取文件夹…
        </div>
        <FolderFlatList
          v-else-if="folders.length"
          :folders="folders"
          :selected-ids="ui.selectedFolderIds"
          :all-selected="allFoldersSelected"
          :scan-status="activeLibrary.last_scan_status"
          @select="(id, selected) => ui.setFolderSelected(id, selected, folders)"
          @select-all="setAllFolders"
          @open="router.push(`/libraries/${encodeURIComponent(activeLibrary.id)}/folders/${encodeURIComponent($event)}`)"
        />
        <div
          v-else-if="foldersError"
          class="grid h-full min-h-64 place-items-center px-6 text-center"
        >
          <div class="max-w-sm">
            <AlertTriangle class="mx-auto size-7 text-destructive" />
            <h2 class="mt-3 font-heading text-base font-semibold">无法读取文件夹列表</h2>
            <p class="mt-1 text-xs leading-5 text-muted-foreground">请检查后端连接后重试。</p>
            <Button
              data-testid="retry-folders"
              class="mt-4"
              size="sm"
              variant="outline"
              @click="foldersQuery.refetch"
            >
              <RefreshCw class="size-3.5" />
              重试
            </Button>
          </div>
        </div>
        <div v-else-if="foldersSuccess" class="grid h-full min-h-64 place-items-center px-6 text-center">
          <div class="max-w-sm">
            <ScanLine class="mx-auto size-7 text-muted-foreground" />
            <h2 class="mt-3 font-heading text-base font-semibold">还没有音频文件夹</h2>
            <p class="mt-1 text-xs leading-5 text-muted-foreground">
              扫描媒体库后，这里会显示根目录下包含音频的文件夹。
            </p>
            <Button class="mt-4" size="sm" :disabled="scan.status === 'scanning'" @click="runScan">开始扫描</Button>
          </div>
        </div>
      </div>

      <BatchPlanBar
        :selected-count="ui.selectedFolderIds.length"
        :loading="planPending"
        @clear="ui.clearSelection"
        @generate="generatePlan"
      />
    </template>

    <div
      v-else-if="librariesSuccess && librariesData.length === 0"
      class="grid min-h-0 flex-1 place-items-center px-6 text-center"
    >
      <div class="max-w-md rounded-lg border border-dashed border-border bg-card/35 px-8 py-10">
        <div class="mx-auto grid size-10 place-items-center rounded-full border border-border bg-muted">
          <Plus class="size-4 text-muted-foreground" />
        </div>
        <h1 class="mt-4 font-heading text-lg font-semibold">还没有媒体库</h1>
        <p class="mt-2 text-xs leading-5 text-muted-foreground">
          添加一个音频根目录，然后扫描它的直接子文件夹。可以按用途建立多个媒体库条目。
        </p>
        <Button data-testid="empty-add-library" class="mt-5" size="sm" @click="openAdd">添加媒体库</Button>
      </div>
    </div>
    <div v-else-if="librariesPending" class="grid flex-1 place-items-center text-xs text-muted-foreground">正在连接媒体库…</div>

    <LibraryManager
      v-if="managerOpen"
      :open="managerOpen"
      :library="editing ? activeLibrary : null"
      :saving="savingLibrary"
      @close="managerOpen = false"
      @save="saveLibrary"
      @remove="removeLibrary"
    />
  </section>
</template>