<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { AlertTriangle, ArrowLeft, RefreshCw, WandSparkles } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import FolderTreeCard from '@/features/folders/FolderTreeCard.vue'
import { useApiClient } from '@/lib/api/client'
import type { TreeNode } from '@/lib/api/types'
import { useLibrariesStore } from '@/stores/libraries'
import { usePlansStore } from '@/stores/plans'

const api = useApiClient()
const route = useRoute()
const router = useRouter()
const libraries = useLibrariesStore()
const plans = usePlansStore()

const libraryId = computed(() => route.params.libraryId as string)
const folderId = computed(() => route.params.folderId as string)

const tree = ref<TreeNode | null>(null)
const loading = ref(false)
const treeErrorCode = ref<string | null>(null)
const treeError = ref<string | null>(null)
const selectedFiles = ref<string[]>([])

const activeLibrary = computed(() => libraries.activeLibrary)

const pageError = computed(() => {
  if (plans.error) return { code: plans.errorCode, message: plans.error }
  if (treeError.value) return { code: treeErrorCode.value, message: treeError.value }
  return null
})

let treeRequestSeq = 0

async function loadTree(): Promise<void> {
  const seq = ++treeRequestSeq
  loading.value = true
  treeErrorCode.value = null
  treeError.value = null
  try {
    const result = await api.getFolderTree(libraryId.value, folderId.value)
    if (seq !== treeRequestSeq) return
    tree.value = result
  } catch (error) {
    if (seq !== treeRequestSeq) return
    treeErrorCode.value = error instanceof Error && 'code' in error ? String((error as { code: string }).code) : null
    treeError.value = error instanceof Error ? error.message : '发生未知错误'
    tree.value = null
  } finally {
    if (seq === treeRequestSeq) loading.value = false
  }
}

async function prepareLibrary(): Promise<void> {
  try {
    if (libraries.libraries.length === 0) await libraries.loadLibraries(api)
    if (libraries.activeLibraryId !== libraryId.value) libraries.setActiveLibrary(libraryId.value)
  } catch {
    // The store exposes the recovery message if the page banner is needed.
  }
}

onMounted(async () => {
  await prepareLibrary()
  await loadTree()
})

watch([libraryId, folderId], async () => {
  tree.value = null
  selectedFiles.value = []
  plans.clearError()
  // Resolve the new library first so activeLibrary reflects it before the
  // breadcrumb renders while loadTree is still in flight.
  await prepareLibrary()
  void loadTree()
})

function onSelectionChange(paths: string[]): void {
  selectedFiles.value = paths
}

async function retryLoad(): Promise<void> {
  plans.clearError()
  await loadTree()
}

async function generatePlan(): Promise<void> {
  if (selectedFiles.value.length === 0) return
  try {
    const plan = await plans.createForFiles(libraryId.value, selectedFiles.value, api)
    await router.push(`/plans/${encodeURIComponent(plan.plan_id)}`)
  } catch {
    // The plans store exposes the envelope code via pageError above.
  }
}
</script>

<template>
  <section class="flex h-full min-w-0 flex-col bg-background">
    <header class="flex min-h-14 shrink-0 items-center gap-3 border-b border-border px-5">
      <Button
        data-testid="back-to-libraries"
        variant="ghost"
        size="icon"
        aria-label="返回媒体库"
        @click="router.push('/libraries')"
      >
        <ArrowLeft class="size-4" />
      </Button>
      <div class="min-w-0">
        <nav class="flex items-center gap-1.5 text-xs text-muted-foreground" aria-label="面包屑">
          <span class="truncate font-heading font-semibold text-foreground">
            {{ activeLibrary?.name ?? '…' }}
          </span>
          <span aria-hidden="true">/</span>
          <span v-if="tree" class="truncate font-heading font-semibold text-foreground">{{ tree.name }}</span>
          <span v-else class="truncate">{{ folderId }}</span>
        </nav>
      </div>
    </header>

    <div
      v-if="pageError"
      data-testid="tree-error"
      class="flex items-center gap-3 border-b border-destructive/30 bg-destructive/10 px-5 py-3 text-xs text-destructive"
    >
      <AlertTriangle class="size-4 shrink-0" />
      <span v-if="pageError.code" class="shrink-0 font-mono font-semibold">{{ pageError.code }}</span>
      <span class="min-w-0 flex-1">{{ pageError.message }}。请检查后端连接或目录权限后重试。</span>
      <Button data-testid="retry-tree" variant="outline" size="sm" @click="retryLoad">
        <RefreshCw class="size-3.5" />
        重试
      </Button>
    </div>

    <div v-if="loading" class="grid min-h-0 flex-1 place-items-center text-xs text-muted-foreground">
      正在读取文件夹树…
    </div>
    <div v-else-if="tree" class="flex min-h-0 flex-1 flex-col p-4">
      <FolderTreeCard :root="tree" @selection-change="onSelectionChange" />
    </div>
    <div v-else-if="!pageError" class="grid min-h-0 flex-1 place-items-center text-xs text-muted-foreground">
      无法读取文件夹内容。
    </div>

    <div class="flex min-h-14 shrink-0 items-center gap-3 border-t border-border bg-card px-5 shadow-[0_-8px_24px_rgba(0,0,0,0.12)]">
      <div class="h-5 w-1 rounded-full bg-[var(--brand)]" aria-hidden="true" />
      <div class="min-w-0">
        <p class="font-heading text-xs font-semibold">所选文件</p>
        <p class="truncate font-mono text-[11px] text-muted-foreground">
          {{ selectedFiles.length ? `${selectedFiles.length} 个文件，将按当前媒体库生成计划` : '在树中选择文件或文件夹' }}
        </p>
      </div>
      <Button
        data-testid="generate-plan"
        class="ml-auto bg-[var(--brand)] text-white hover:bg-[var(--brand)]"
        size="sm"
        :disabled="selectedFiles.length === 0 || plans.loading || loading"
        @click="generatePlan"
      >
        <WandSparkles class="size-3.5" />
        {{ plans.loading ? '生成中…' : `对所选文件生成计划${selectedFiles.length ? ` (${selectedFiles.length})` : ''}` }}
      </Button>
    </div>
  </section>
</template>