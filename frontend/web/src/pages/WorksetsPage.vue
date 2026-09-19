<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation } from '@tanstack/vue-query'
import { useQueryClient } from '@tanstack/vue-query'
import { RouterLink } from 'vue-router'
import { AlertTriangle, LibraryBig, Pencil, Plus, RefreshCw } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import HeaderMenu from '@/components/layout/HeaderMenu.vue'
import LibraryManager from '@/features/libraries/LibraryManager.vue'
import { useApiClient } from '@/lib/api/client'
import { errorDetails } from '@/lib/api/error'
import {
  createLibraryMutationOptions,
  deleteLibraryMutationOptions,
  updateLibraryMutationOptions,
  useLibraryList,
} from '@/queries/libraries'
import { useLibraryUiStore } from '@/stores/library-ui'
import type { CreateLibraryInput } from '@/lib/api/types'

/**
 * 工作集 — the media-library selection (`/worksets`, spec N1, N2).
 *
 * The product entry keeps the name 工作集, and what it selects is a media
 * library: choosing one enters its workbench, where browsing, conversion and
 * file management all happen. The old global 媒体库 entry is gone with this
 * page, and no old link is maintained.
 */
const api = useApiClient()
const queryClient = useQueryClient()
const ui = useLibraryUiStore()

const { query, librariesData } = useLibraryList()
const libraries = computed(() => librariesData.value)
const loading = computed(() => query.isPending.value && !query.data.value)

const managerOpen = ref(false)
const editing = ref(false)
const saving = ref(false)

const createMutation = useMutation(createLibraryMutationOptions(api, queryClient))
const updateMutation = useMutation(updateLibraryMutationOptions(api, queryClient))
const deleteMutation = useMutation(deleteLibraryMutationOptions(api, queryClient))

const editingLibrary = computed(() =>
  editing.value ? (libraries.value.find((library) => library.id === ui.activeLibraryId) ?? null) : null,
)

const pageError = computed(() => {
  const queryError = query.error.value
  if (queryError) return errorDetails(queryError)
  for (const mutation of [createMutation, updateMutation, deleteMutation]) {
    if (mutation.error.value) return errorDetails(mutation.error.value)
  }
  return null
})

function openAdd() {
  editing.value = false
  managerOpen.value = true
}

function openEdit(libraryId: string) {
  editing.value = true
  ui.setActiveLibrary(libraryId)
  managerOpen.value = true
}

async function saveLibrary(input: CreateLibraryInput) {
  saving.value = true
  try {
    if (editing.value && editingLibrary.value) {
      await updateMutation.mutateAsync({ id: editingLibrary.value.id, input })
    } else {
      const created = await createMutation.mutateAsync(input)
      ui.setActiveLibrary(created.id)
    }
    managerOpen.value = false
  } catch {
    // Mutation errors surface in the page banner; the dialog stays open.
  } finally {
    saving.value = false
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
  saving.value = true
  try {
    await deleteMutation.mutateAsync(id)
    managerOpen.value = false
  } catch {
    // Keep the dialog open and expose the mutation error in the banner.
  } finally {
    saving.value = false
  }
}

async function retryPage() {
  for (const mutation of [createMutation, updateMutation, deleteMutation]) mutation.reset()
  await query.refetch()
}
</script>

<template>
  <section class="flex h-full min-w-0 flex-col bg-background" data-testid="worksets-page">
    <header class="flex min-h-14 shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-border px-4 py-1.5 rail:px-5">
      <div class="w-full min-w-0 rail:w-auto">
        <h1 class="font-heading text-sm font-semibold tracking-tight">工作集</h1>
        <p class="truncate text-[11px] text-muted-foreground">选择一个媒体库进入工作台</p>
      </div>
      <Button class="ml-auto hidden h-11 rail:inline-flex rail:h-7" variant="outline" size="sm" @click="openAdd">
        <Plus class="size-3.5" />
        添加媒体库
      </Button>
      <div class="ml-auto shrink-0 rail:hidden">
        <HeaderMenu trigger="more">
          <button type="button" class="sr-only" @click="openAdd">添加媒体库</button>
        </HeaderMenu>
      </div>
    </header>

    <div
      v-if="pageError"
      data-testid="page-error"
      class="flex items-center gap-3 border-b border-destructive/30 bg-destructive/10 px-5 py-3 text-xs text-destructive"
    >
      <AlertTriangle class="size-4 shrink-0" />
      <span v-if="pageError.code" class="shrink-0 font-mono font-semibold">{{ pageError.code }}</span>
      <span class="min-w-0 flex-1">{{ pageError.message }}</span>
      <Button data-testid="retry-page" class="h-11 rail:h-7" variant="outline" size="sm" @click="retryPage">
        <RefreshCw class="size-3.5" />
        重试
      </Button>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto">
      <div v-if="loading" class="grid h-full place-items-center text-xs text-muted-foreground">
        正在连接媒体库…
      </div>

      <div
        v-else-if="libraries.length === 0"
        class="grid h-full place-items-center px-6 text-center"
        data-testid="libraries-empty"
      >
        <div class="max-w-md rounded-lg border border-dashed border-border bg-card/35 px-8 py-10">
          <div class="mx-auto grid size-10 place-items-center rounded-full border border-border bg-muted">
            <Plus class="size-4 text-muted-foreground" />
          </div>
          <h1 class="mt-4 font-heading text-lg font-semibold">还没有媒体库</h1>
          <p class="mt-2 text-xs leading-5 text-muted-foreground">
            添加一个音频根目录，扫描它的直接子文件夹，然后进入工作台浏览与转换。
          </p>
          <Button data-testid="empty-add-library" class="mt-5 h-11 rail:h-7" size="sm" @click="openAdd">
            添加媒体库
          </Button>
        </div>
      </div>

      <ul v-else class="mx-auto max-w-3xl space-y-1.5 p-4">
        <li v-for="library in libraries" :key="library.id" class="flex items-center gap-2">
          <RouterLink
            :to="{ name: 'workbench-overview', params: { libraryId: library.id } }"
            class="flex min-w-0 flex-1 items-center gap-2 rounded-lg border border-border bg-card px-3 py-2 hover:border-[var(--brand-border)] focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none"
            :data-testid="`library-${library.id}`"
          >
            <LibraryBig class="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <span class="min-w-0 flex-1">
              <span class="block truncate font-heading text-xs font-semibold">{{ library.name }}</span>
              <span class="block truncate font-mono text-[10px] text-[var(--text-muted)]">{{ library.root_path }}</span>
            </span>
            <span class="shrink-0 text-[10px] text-[var(--text-muted)]">
              {{ library.last_scan_status === 'completed' ? '已扫描' : library.last_scan_status === 'failed' ? '扫描失败' : '未扫描' }}
            </span>
          </RouterLink>
          <Button
            variant="ghost"
            size="icon"
            :aria-label="`编辑 ${library.name}`"
            :data-testid="`edit-${library.id}`"
            @click="openEdit(library.id)"
          >
            <Pencil class="size-3.5" />
          </Button>
        </li>
      </ul>
    </div>

    <LibraryManager
      v-if="managerOpen"
      :open="managerOpen"
      :library="editingLibrary"
      :saving="saving"
      @close="managerOpen = false"
      @save="saveLibrary"
      @remove="removeLibrary"
    />
  </section>
</template>
