<script setup lang="ts">
import { computed, ref, watch, watchEffect } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { AlertTriangle, Pencil, RefreshCw, ScanLine, Square } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import Modal from '@/components/ui/modal/Modal.vue'
import WorkbenchShell from '@/components/layout/WorkbenchShell.vue'
import ScanProgressBar from '@/features/libraries/ScanProgressBar.vue'
import DirList from '@/features/libraries/DirList.vue'
import LibraryManager from '@/features/libraries/LibraryManager.vue'
import BatchPlanBar from '@/features/libraries/BatchPlanBar.vue'
import { useLibraryScan } from '@/composables/use-library-scan'
import { useApiClient } from '@/lib/api/client'
import { errorDetails } from '@/lib/api/error'
import { rootPathIdentityKey } from '@/lib/root-path-identity'
import { dirsQueryOptions, useLibraryList } from '@/queries/libraries'
import { updateLibraryMutationOptions, deleteLibraryMutationOptions } from '@/queries/libraries'
import { createCurrentRecordMutationOptions, currentRecordQueryOptions } from '@/queries/worksets'
import { useLibraryUiStore } from '@/stores/library-ui'
import { useScanStore } from '@/stores/scan'
import { CONVERSION, type SkippedFolder, type SkippedReason } from '@/lib/api/types'

/**
 * The workbench overview of one library (spec N2, N3, R1).
 *
 * The library IS the workbench: entering it lists every direct child directory
 * the last scan saw — empty ones and ones without audio included — and that is
 * where a conversion scope is chosen. Choosing folders creates the library's
 * conversion record explicitly; browsing alone creates nothing.
 */
const route = useRoute()
const router = useRouter()
const api = useApiClient()
const queryClient = useQueryClient()
const ui = useLibraryUiStore()
const scan = useScanStore()
const libraryScan = useLibraryScan()

const libraryId = computed(() => (route.params.libraryId as string) || '')
const filesOpen = computed(() => route.name === 'overview-files')

const { query: librariesQuery, librariesData, activeLibrary } = useLibraryList()
const rootIdentity = computed(() => {
  const library = librariesData.value.find((item) => item.id === libraryId.value)
  if (library) return rootPathIdentityKey(library.root_path)
  if (librariesQuery.isSuccess.value || librariesQuery.error.value) return 'unresolved-root'
  return null
})

watch(
  libraryId,
  (id) => {
    if (id) ui.setActiveLibrary(id)
  },
  { immediate: true },
)

const dirsQuery = useQuery(() => dirsQueryOptions(api, libraryId.value, rootIdentity.value))
watchEffect(() => {
  if (dirsQuery.data.value) ui.reconcileDirs(libraryId.value, dirsQuery.data.value)
})
const dirs = computed(() => dirsQuery.data.value ?? [])
const dirsPending = computed(() => dirsQuery.isPending.value)
const dirsError = computed(() => {
  const error = dirsQuery.error.value
  return error ? errorDetails(error) : null
})

const recordQuery = useQuery(() => currentRecordQueryOptions(api, libraryId.value, CONVERSION))
const record = computed(() => recordQuery.data.value?.workset ?? null)

// ---- scope → record ---------------------------------------------------

const managerOpen = ref(false)
const savingLibrary = ref(false)
const confirmOpen = ref(false)
const createError = ref<string | null>(null)
const skipped = ref<SkippedFolder[]>([])
let idempotencyKey = ''

const createMutation = useMutation(
  createCurrentRecordMutationOptions(api, queryClient, libraryId.value, CONVERSION),
)
const creating = computed(() => createMutation.isPending.value)

const SKIP_TEXT: Record<SkippedReason, string> = {
  no_audio: '没有音频，将跳过',
  missing: '目录不存在',
  recovery_dir: '回收目录不作为成员',
  duplicate: '重复选择',
  invalid_path: '路径不合法',
  not_direct_child: '不是媒体库根的直接子目录',
}

/** The selection as the caller sees it, so the confirmation names it. */
const selectedDirs = computed(() => dirs.value.filter((dir) => ui.selectedDirPaths.includes(dir.rel_path)))
/** A directory the conversion will skip: it is selectable, but holds no audio. */
const selectedWithoutAudio = computed(() => selectedDirs.value.filter((dir) => dir.audio_file_count === 0))

function startEnterConversion() {
  if (ui.selectedDirPaths.length === 0) return
  skipped.value = []
  createError.value = null
  idempotencyKey = crypto.randomUUID()
  if (record.value) {
    // Replacing a record deletes its settings, plan and execution results:
    // that is asked for, never implied.
    confirmOpen.value = true
    return
  }
  void submit()
}

async function submit() {
  createError.value = null
  try {
    const response = await createMutation.mutateAsync({
      request: {
        folder_paths: [...ui.selectedDirPaths],
        // The record the caller saw as current: a concurrent replace is a
        // conflict, not an overwrite.
        expected_current_id: record.value?.workset_id,
      },
      idempotencyKey,
    })
    skipped.value = response.skipped
    confirmOpen.value = false
    ui.clearSelection()
    await router.push({ name: 'conversion', params: { libraryId: libraryId.value } })
  } catch (error) {
    const apiError = error as { code?: string; message?: string; details?: string[] }
    skipped.value = (apiError.details ?? []).map((detail) => {
      const [path, reason] = detail.split(': ')
      return { path, reason: (reason ?? '') as SkippedReason }
    })
    createError.value = apiError.message ?? '无法创建处理记录'
    confirmOpen.value = false
  }
}

// ---- library management ------------------------------------------------

const updateLibraryMutation = useMutation(updateLibraryMutationOptions(api, queryClient))
const deleteLibraryMutation = useMutation(deleteLibraryMutationOptions(api, queryClient))

async function saveLibrary(input: { name: string; root_path: string }) {
  if (!activeLibrary.value) return
  savingLibrary.value = true
  try {
    // A root change is refused by the backend while a record exists; the
    // dialog surfaces that refusal as it is.
    await updateLibraryMutation.mutateAsync({ id: activeLibrary.value.id, input })
    managerOpen.value = false
  } catch {
    // The mutation error is shown in the dialog.
  } finally {
    savingLibrary.value = false
  }
}

async function removeLibrary(id: string) {
  if (
    !window.confirm(
      '删除这个媒体库条目？它的设置、处理记录、计划和执行结果都会一并删除；磁盘上的音频文件与 Delete/ 回收目录不会被删除。',
    )
  ) {
    return
  }
  savingLibrary.value = true
  try {
    await deleteLibraryMutation.mutateAsync(id)
    managerOpen.value = false
    await router.push({ name: 'worksets' })
  } catch {
    // Keep the dialog open and expose the mutation error in it.
  } finally {
    savingLibrary.value = false
  }
}

const managementError = computed(() => {
  const error = updateLibraryMutation.error.value ?? deleteLibraryMutation.error.value
  return error ? errorDetails(error) : null
})

// ---- scanning ---------------------------------------------------------

const scanningThisLibrary = computed(() => scan.status === 'scanning' && scan.libraryId === libraryId.value)

async function runScan() {
  if (!libraryId.value) return
  await libraryScan.start(libraryId.value)
}

// ---- misc -------------------------------------------------------------

/** Opening a member: its files, in this same workbench (N2, N3). The address
 * carries the directory's identity, so the folder's name stays out of it. */
async function openMember(dirId: string) {
  await router.push({
    name: 'overview-files',
    params: { libraryId: libraryId.value, dirId },
  })
}

function onSelectAll(value: boolean) {
  if (value) ui.selectAllDirs(dirs.value)
  else ui.clearSelection()
}

/** The page in view, named as the navigation names it (spec N1, N31). */
const pageTitle = computed(() => (filesOpen.value ? '文件' : '概览与成员'))
const libraryRoot = computed(() => activeLibrary.value?.root_path ?? '')
</script>

<template>
  <WorkbenchShell
    nav-title="工作台"
    :context-title="activeLibrary?.name ?? '…'"
    :context-page="pageTitle"
    :context-back-to="filesOpen ? { name: 'workbench-overview', params: { libraryId } } : { name: 'worksets' }"
    :context-back-label="filesOpen ? '概览与成员' : '工作集列表'"
  >
    <template #header>
      <nav aria-label="面包屑" class="flex min-w-0 items-center gap-1.5 text-xs" data-testid="overview-breadcrumb">
        <RouterLink to="/worksets" class="rounded px-1 py-0.5 text-[var(--text-secondary)] hover:bg-muted">
          工作集
        </RouterLink>
        <span aria-hidden="true" class="text-[var(--text-muted)]">/</span>
        <RouterLink
          :to="{ name: 'workbench-overview', params: { libraryId } }"
          class="max-w-40 truncate rounded px-1 py-0.5 font-medium hover:bg-muted"
        >
          {{ activeLibrary?.name ?? '…' }}
        </RouterLink>
        <template v-if="filesOpen">
          <span aria-hidden="true" class="text-[var(--text-muted)]">/</span>
          <span class="px-1 font-medium text-[var(--text-secondary)]" aria-current="page">文件</span>
        </template>
      </nav>
    </template>

    <template #nav>
      <nav class="p-2" aria-label="工作台导航">
        <RouterLink
          :to="{ name: 'workbench-overview', params: { libraryId } }"
          class="block rounded-md px-2 py-1.5 text-xs hover:bg-muted"
          :class="filesOpen ? '' : 'bg-muted font-medium'"
        >
          概览与成员
        </RouterLink>
        <RouterLink
          v-if="record"
          :to="{ name: 'conversion', params: { libraryId } }"
          class="mt-0.5 block rounded-md px-2 py-1.5 text-xs hover:bg-muted"
        >
          转换
        </RouterLink>
      </nav>
    </template>

    <template #main>
      <div class="relative flex min-h-0 flex-1 flex-col">
      <!-- Files view: a member of this library, addressed by its relative
           path. It covers the list rather than replacing it, so the list keeps
           its layout — and with it the selection, the filters and the scroll
           position (N2). -->
      <div v-if="filesOpen" class="absolute inset-0 z-10 flex min-h-0 flex-col bg-background">
        <RouterView />
      </div>

      <section class="min-h-0 flex-1 overflow-y-auto" data-testid="overview">
        <div class="mx-auto max-w-3xl p-4">
          <!-- Library identity, its scan state and the library-level actions
               (N3): management and scanning live here, not in a second page. -->
          <div class="flex flex-wrap items-start gap-3">
            <div class="min-w-0">
              <h1 class="truncate font-heading text-base font-semibold tracking-tight">
                {{ activeLibrary?.name ?? '…' }}
              </h1>
              <p class="truncate font-mono text-[11px] text-[var(--text-muted)]" :title="libraryRoot">
                {{ libraryRoot }}
              </p>
            </div>
            <div class="ml-auto flex items-center gap-2">
              <Button
                v-if="activeLibrary"
                size="xs"
                variant="outline"
                data-testid="scan-button"
                :disabled="scanningThisLibrary"
                @click="runScan"
              >
                <ScanLine class="size-3.5" />
                {{ scanningThisLibrary ? '扫描中…' : '扫描' }}
              </Button>
              <Button v-if="scanningThisLibrary" size="xs" variant="ghost" data-testid="cancel-scan" @click="libraryScan.cancel">
                <Square class="size-3" />
                取消
              </Button>
              <!-- Library management stays reachable from the workbench (N3):
                   the entry lives here, not only on the selection page. -->
              <Button
                v-if="activeLibrary"
                size="xs"
                variant="ghost"
                data-testid="edit-library"
                @click="managerOpen = true"
              >
                <Pencil class="size-3.5" />
                媒体库
              </Button>
            </div>
          </div>

          <ScanProgressBar
            v-if="scan.libraryId === libraryId"
            class="mt-2"
            :status="scan.status"
            :files-scanned="scan.filesScanned"
            :dirs-scanned="scan.dirsScanned"
            :error-code="scan.errorCode"
            :error-message="scan.errorMessage"
          />

          <!-- The record entry: the current conversion record of this
               library, or the invitation to choose a scope. -->
          <section class="mt-4">
            <h2 class="text-xs font-semibold text-[var(--text-secondary)]">转换</h2>
            <RouterLink
              v-if="record"
              :to="{ name: 'conversion', params: { libraryId } }"
              class="mt-1 flex flex-wrap items-center gap-2 rounded-lg border border-border bg-card px-3 py-2 hover:border-[var(--brand-border)]"
              data-testid="current-record"
            >
              <span class="text-xs font-medium">{{ record.title }}</span>
              <Badge tone="neutral">{{ record.members.length }} 个文件夹</Badge>
              <span class="ml-auto text-[11px] text-[var(--brand-ink)]">进入转换 →</span>
            </RouterLink>
            <p v-else class="mt-1 rounded-lg border border-dashed border-border px-3 py-2 text-[11px] text-[var(--text-muted)]">
              还没有转换记录。在下面勾选要转换的文件夹，然后点击“进入转换”。
            </p>
          </section>

          <p
            v-if="createError"
            class="mt-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-[11px] text-destructive"
            role="alert"
            data-testid="create-error"
          >
            {{ createError }}
          </p>
          <ul v-if="skipped.length" class="mt-3 space-y-0.5" data-testid="skipped-report">
            <li v-for="item in skipped" :key="item.path" class="text-[11px] text-[var(--text-secondary)]">
              <span class="font-mono">{{ item.path }}</span> — {{ SKIP_TEXT[item.reason] ?? item.reason }}
            </li>
          </ul>

          <!-- Members: every direct child directory, with its audio count as
               status. Selecting one is what a conversion scope is built from. -->
          <section class="mt-4">
            <div class="flex items-center gap-2">
              <h2 class="text-xs font-semibold text-[var(--text-secondary)]">文件夹（{{ dirs.length }}）</h2>
              <label class="ml-auto flex items-center gap-1 text-[11px] text-[var(--text-secondary)]">
                <input
                  type="checkbox"
                  :checked="dirs.length > 0 && ui.selectedDirPaths.length === dirs.length"
                  :disabled="dirs.length === 0"
                  data-testid="select-all"
                  @change="onSelectAll(($event.target as HTMLInputElement).checked)"
                />
                全选
              </label>
            </div>

            <p v-if="dirsPending" class="mt-1 text-[11px] text-[var(--text-muted)]">正在读取文件夹…</p>
            <p v-else-if="dirsError" class="mt-1 flex items-center gap-1.5 text-[11px] text-destructive">
              <AlertTriangle class="size-3.5" />{{ dirsError.code }} {{ dirsError.message }}
              <Button size="xs" variant="outline" @click="dirsQuery.refetch()">
                <RefreshCw class="size-3" />重试
              </Button>
            </p>
            <p v-else-if="dirs.length === 0" class="mt-1 text-[11px] text-[var(--text-muted)]" data-testid="no-dirs">
              还没有扫描结果。先扫描媒体库，这里会列出根目录下的所有文件夹。
            </p>

            <div v-else class="mt-1 h-80 min-h-0 overflow-hidden rounded-lg border border-border bg-card">
              <DirList
                :dirs="dirs"
                :selected-paths="ui.selectedDirPaths"
                :all-selected="dirs.length > 0 && ui.selectedDirPaths.length === dirs.length"
                @select="(relPath, selected) => ui.setDirSelected(relPath, selected, dirs)"
                @select-all="onSelectAll"
                @open="openMember"
              />
            </div>
          </section>
        </div>
      </section>
      </div>
    </template>

    <!-- The scope bar belongs to the selection and lives in the page's own
         column, so it is reachable at every container tier. -->
    <template #bottom>
      <BatchPlanBar
        v-if="!filesOpen && ui.selectedDirPaths.length > 0"
        :selected-count="ui.selectedDirPaths.length"
        :loading="creating"
        :replacing="Boolean(record)"
        :audio-less-count="selectedWithoutAudio.length"
        @clear="ui.clearSelection()"
        @generate="startEnterConversion"
      />
    </template>
  </WorkbenchShell>

  <!-- The library's own management dialog (rename, root, delete): reachable
       from the workbench, and portalled so it works at every tier. -->
  <LibraryManager
    v-if="managerOpen"
    :open="managerOpen"
    :library="activeLibrary"
    :saving="savingLibrary"
    @close="managerOpen = false"
    @save="saveLibrary"
    @remove="removeLibrary"
  />
  <p v-if="managementError" class="sr-only" role="alert">{{ managementError.message }}</p>

  <!-- The replace confirmation is a page-level decision, not a carrier: it is
       portalled, so it reaches the user at every container tier. -->
  <Modal :open="confirmOpen" title="替换当前转换记录？" @update:open="confirmOpen = false">
    <p class="text-xs">
      这个媒体库已有一条转换记录。创建新记录会删除它的设置、当前计划与执行结果，且无法恢复。
    </p>
    <p class="text-[11px] text-[var(--text-muted)]">文件本身不会被修改。</p>
    <div class="flex justify-end gap-2">
      <Button variant="ghost" size="sm" @click="confirmOpen = false">取消</Button>
      <Button size="sm" :disabled="creating" data-testid="confirm-replace" @click="submit">替换</Button>
    </div>
  </Modal>
</template>
