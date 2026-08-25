<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { AlertTriangle, Pencil, Plus, RefreshCw, ScanLine, Square } from '@lucide/vue'
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import BatchPlanBar from '@/features/libraries/BatchPlanBar.vue'
import FolderFlatList from '@/features/libraries/FolderFlatList.vue'
import LibraryManager from '@/features/libraries/LibraryManager.vue'
import ScanProgressBar from '@/features/libraries/ScanProgressBar.vue'
import { useApiClient } from '@/lib/api/client'
import type { CreateLibraryInput } from '@/lib/api/types'
import { useLibrariesStore } from '@/stores/libraries'
import { usePlansStore } from '@/stores/plans'
import { useScanStore } from '@/stores/scan'

const api = useApiClient()
const router = useRouter()
const libraries = useLibrariesStore()
const scan = useScanStore()
const plans = usePlansStore()
const managerOpen = ref(false)
const editing = ref(false)
const savingLibrary = ref(false)

const activeLibrary = computed(() => libraries.activeLibrary)
const pageError = computed(() => {
  if (libraries.error) return { code: libraries.errorCode, message: libraries.error, source: 'library' as const }
  if (plans.error) return { code: plans.errorCode, message: plans.error, source: 'plan' as const }
  return null
})

onMounted(async () => {
  try {
    await libraries.loadLibraries(api)
    if (libraries.activeLibraryId) await libraries.loadFolders(libraries.activeLibraryId, api)
  } catch {
    // Stores own the recovery message shown in the page.
  }
})

watch(
  () => libraries.activeLibraryId,
  async (id, previous) => {
    if (!id || id === previous) return
    try {
      await libraries.loadFolders(id, api)
    } catch {
      // Store error is rendered below.
    }
  },
)

function switchLibrary(event: Event) {
  libraries.setActiveLibrary((event.target as HTMLSelectElement).value)
  scan.reset()
}

async function retryLoad() {
  try {
    if (libraries.libraries.length === 0) await libraries.loadLibraries(api)
    if (libraries.activeLibraryId) await libraries.loadFolders(libraries.activeLibraryId, api)
  } catch {
    // Store error is rendered below.
  }
}

async function retryPage() {
  if (pageError.value?.source === 'plan') {
    await generatePlan()
    return
  }
  await retryLoad()
}

async function runScan() {
  const libraryId = libraries.activeLibraryId
  if (!libraryId) return
  await scan.start(libraryId, api)
  if (scan.foldersRefreshNeeded) {
    try {
      await Promise.all([libraries.loadFolders(libraryId, api), libraries.loadLibraries(api)])
      scan.acknowledgeFoldersRefresh()
    } catch {
      // Keep the refresh flag so the recovery action can try again.
    }
  }
}

function setAllFolders(selected: boolean) {
  if (selected) libraries.selectAllFolders()
  else libraries.clearSelection()
}

async function generatePlan() {
  const libraryId = libraries.activeLibraryId
  if (!libraryId || libraries.selectedFolderIds.length === 0) return
  try {
    const plan = await plans.createForFolders(libraryId, libraries.selectedFolderIds, api)
    await router.push(`/plans/${encodeURIComponent(plan.plan_id)}`)
  } catch {
    // Plans store exposes the request failure with a retryable action.
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
      await libraries.updateLibrary(activeLibrary.value.id, input, api)
    } else {
      await libraries.createLibrary(input, api)
    }
    managerOpen.value = false
  } catch {
    // Keep the dialog open so the user can correct the values and retry.
  } finally {
    savingLibrary.value = false
  }
}

async function removeLibrary(id: string) {
  if (!window.confirm('删除这个媒体库条目？磁盘上的音频文件不会被删除。')) return
  savingLibrary.value = true
  try {
    await libraries.removeLibrary(id, api)
    managerOpen.value = false
  } catch {
    // Keep the dialog open and expose the store error.
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
        v-if="libraries.libraries.length"
        :value="libraries.activeLibraryId ?? ''"
        aria-label="切换媒体库"
        class="ml-auto h-8 max-w-52 rounded-md border border-input bg-background px-2.5 font-heading text-xs font-semibold focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        @change="switchLibrary"
      >
        <option v-for="library in libraries.libraries" :key="library.id" :value="library.id">
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
            v-if="scan.status === 'scanning'"
            data-testid="cancel-scan"
            variant="ghost"
            size="sm"
            @click="scan.cancel"
          >
            <Square class="size-3" />
            取消
          </Button>
        </div>
      </div>

      <ScanProgressBar
        :status="scan.status"
        :files-scanned="scan.filesScanned"
        :dirs-scanned="scan.dirsScanned"
        :error-code="scan.errorCode"
        :error-message="scan.errorMessage"
      />

      <div class="min-h-0 flex-1 overflow-auto">
        <div v-if="libraries.foldersLoading" class="grid h-full place-items-center text-xs text-muted-foreground">
          正在读取文件夹…
        </div>
        <FolderFlatList
          v-else-if="libraries.folders.length"
          :folders="libraries.folders"
          :selected-ids="libraries.selectedFolderIds"
          :all-selected="libraries.allFoldersSelected"
          :scan-status="activeLibrary.last_scan_status"
          @select="libraries.setFolderSelected"
          @select-all="setAllFolders"
          @open="router.push(`/libraries/${encodeURIComponent(activeLibrary.id)}/folders/${encodeURIComponent($event)}`)"
        />
        <div v-else class="grid h-full min-h-64 place-items-center px-6 text-center">
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
        :selected-count="libraries.selectedFolderIds.length"
        :loading="plans.loading"
        @clear="libraries.clearSelection"
        @generate="generatePlan"
      />
    </template>

    <div v-else-if="!libraries.loading && !libraries.error" class="grid min-h-0 flex-1 place-items-center px-6 text-center">
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
    <div v-else-if="libraries.loading" class="grid flex-1 place-items-center text-xs text-muted-foreground">正在连接媒体库…</div>

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
