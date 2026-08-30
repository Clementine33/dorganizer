<script setup lang="ts">
import { useQuery } from '@tanstack/vue-query'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { AlertTriangle, ArrowLeft, RefreshCw } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import FolderTreeCard from '@/features/folders/FolderTreeCard.vue'
import { useApiClient } from '@/lib/api/client'
import { errorDetails } from '@/lib/api/error'
import { rootPathIdentityKey } from '@/lib/root-path-identity'
import { folderTreeQueryOptions, useLibraryList } from '@/queries/libraries'
import { useLibraryUiStore } from '@/stores/library-ui'

const api = useApiClient()
const route = useRoute()
const router = useRouter()
const ui = useLibraryUiStore()

const libraryId = computed(() => route.params.libraryId as string)
const folderId = computed(() => route.params.folderId as string)
const selectedFiles = ref<string[]>([])

const { query: librariesQuery, librariesData, activeLibrary } = useLibraryList()

// The tree must not wait for the library list: a direct link (or a library
// list failure) must still resolve the tree against the route IDs. The root
// identity is part of the query key so a genuine root change invalidates the
// old-key cache. While the library list is still pending the tree waits;
// once it settles without the library (e.g. the list failed), a placeholder
// identity lets the tree proceed rather than being blocked forever.
const rootIdentity = computed(() => {
  const library = librariesData.value.find((item) => item.id === libraryId.value)
  if (library) return rootPathIdentityKey(library.root_path)
  // 'unresolved-root' is a deliberate fallback (reviewed and accepted): when
  // the library list has settled without this route's library — e.g. the
  // libraries query failed — the tree must still load against the route IDs
  // rather than being blocked forever. A later list arrival swaps the key and
  // refetches under the canonical root identity.
  if (librariesQuery.isSuccess.value || librariesQuery.error.value) return 'unresolved-root'
  return null
})

const treeQuery = useQuery(() =>
  folderTreeQueryOptions(api, libraryId.value, rootIdentity.value, folderId.value),
)
// Query flags are refs; expose a top-level boolean so template v-if sees a
// value instead of a truthy ref object.
const treePending = computed(() => treeQuery.isPending.value)
const tree = computed(() => treeQuery.data.value ?? null)
const treeError = computed(() => {
  const error = treeQuery.error.value
  return error ? errorDetails(error) : null
})

// Route library is authoritative for this page; the UI store keeps the global
// active selection in sync without waiting for the library list.
watch(
  libraryId,
  (id) => {
    if (id) ui.setActiveLibrary(id)
  },
  { immediate: true },
)

// Tree selection is page-local and resets whenever the tree context changes.
watch([libraryId, folderId], () => {
  selectedFiles.value = []
})

function onSelectionChange(paths: string[]): void {
  selectedFiles.value = paths
}

const pageError = computed(() => {
  if (treeError.value) return { code: treeError.value.code, message: treeError.value.message }
  return null
})

async function retryLoad(): Promise<void> {
  // The tree query key depends on the resolved root identity, which comes from
  // the library list. If the list failed (placeholder 'unresolved-root' key)
  // only refetching the tree can never recover — refetch the list too so the
  // key flips to the canonical identity (or the page falls back cleanly).
  await Promise.all([librariesQuery.refetch(), treeQuery.refetch()])
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

    <div v-if="treePending" class="grid min-h-0 flex-1 place-items-center text-xs text-muted-foreground">
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
        <p class="font-heading text-xs font-semibold">文件浏览</p>
        <p class="truncate font-mono text-[11px] text-muted-foreground">
          {{ selectedFiles.length ? `${selectedFiles.length} 个文件已选择` : '在树中选择文件或文件夹' }}
        </p>
      </div>
    </div>
  </section>
</template>