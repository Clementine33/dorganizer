<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { AlertTriangle, ChevronRight, ListMusic, RefreshCw, WandSparkles } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { useApiClient } from '@/lib/api/client'
import { formatPlanCreatedAt, planStatusLabel } from '@/features/plans/plan-status'
import { useLibrariesStore } from '@/stores/libraries'
import { usePlansStore } from '@/stores/plans'

const api = useApiClient()
const router = useRouter()
const libraries = useLibrariesStore()
const plans = usePlansStore()

const libraryId = computed(() => libraries.activeLibraryId)
const activeLibrary = computed(() => libraries.activeLibrary)
const pageError = computed(() => {
  if (libraries.error) return { code: libraries.errorCode, message: libraries.error }
  if (plans.error) return { code: plans.errorCode, message: plans.error }
  return null
})
const loading = computed(() => libraries.loading || plans.loading)

async function loadForLibrary(): Promise<void> {
  if (libraries.libraries.length === 0) await libraries.loadLibraries(api)
  if (libraryId.value) await plans.loadPlans(libraryId.value, api)
}

// Guards against double-loading: loadLibraries sets activeLibraryId, which
// would also fire the watcher below during initial mount.
const initializing = ref(true)

onMounted(async () => {
  try {
    await loadForLibrary()
  } catch {
    // The plans store exposes the envelope code in the page banner.
  } finally {
    initializing.value = false
  }
})

watch(
  () => libraries.activeLibraryId,
  async (id, previous) => {
    if (!id || id === previous || initializing.value) return
    try {
      await plans.loadPlans(id, api)
    } catch {
      // Store error is rendered below.
    }
  },
)

function switchLibrary(event: Event): void {
  libraries.setActiveLibrary((event.target as HTMLSelectElement).value)
}

async function retryLoad(): Promise<void> {
  try {
    if (libraries.libraries.length === 0) await libraries.loadLibraries(api)
    if (libraryId.value) await plans.loadPlans(libraryId.value, api)
  } catch {
    // Store error is rendered below.
  }
}

function statusClass(status: string): string {
  const mapped = status
  if (mapped === 'failed' || mapped === 'error') return 'border-destructive/40 text-destructive'
  if (mapped === 'in_progress' || mapped === 'running') return 'border-[var(--brand)]/40 text-[var(--brand)]'
  return 'border-border text-muted-foreground'
}
</script>

<template>
  <section class="flex h-full min-w-0 flex-col bg-background">
    <header class="flex min-h-14 shrink-0 items-center gap-3 border-b border-border px-5">
      <div class="min-w-0">
        <p class="font-heading text-sm font-semibold tracking-tight">计划</p>
        <p class="text-[11px] text-muted-foreground">点击一行进入计划审阅</p>
      </div>
      <select
        v-if="libraries.libraries.length"
        :value="libraryId ?? ''"
        aria-label="切换媒体库"
        class="ml-auto h-8 max-w-52 rounded-md border border-input bg-background px-2.5 font-heading text-xs font-semibold focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        @change="switchLibrary"
      >
        <option v-for="library in libraries.libraries" :key="library.id" :value="library.id">
          {{ library.name }}
        </option>
      </select>
    </header>

    <div
      v-if="pageError"
      data-testid="plans-error"
      class="flex items-center gap-3 border-b border-destructive/30 bg-destructive/10 px-5 py-3 text-xs text-destructive"
    >
      <AlertTriangle class="size-4 shrink-0" />
      <span v-if="pageError.code" class="shrink-0 font-mono font-semibold">{{ pageError.code }}</span>
      <span class="min-w-0 flex-1">{{ pageError.message }}。请检查后端连接后重试。</span>
      <Button data-testid="retry-plans" variant="outline" size="sm" @click="retryLoad">
        <RefreshCw class="size-3.5" />
        重试
      </Button>
    </div>

    <div v-if="loading" class="grid min-h-0 flex-1 place-items-center text-xs text-muted-foreground">
      正在读取计划…
    </div>
    <div v-else-if="plans.plans.length" class="min-h-0 flex-1 overflow-auto">
      <button
        v-for="plan in plans.plans"
        :key="plan.plan_id"
        :data-testid="`plan-link-${plan.plan_id}`"
        type="button"
        class="flex min-h-14 w-full items-center gap-3 border-b border-border px-5 text-left transition-colors hover:bg-accent/45 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        @click="router.push(`/plans/${encodeURIComponent(plan.plan_id)}`)"
      >
        <div class="min-w-0 flex-1">
          <p class="flex items-center gap-2 font-heading text-sm font-semibold">
            <span class="truncate">{{ plan.plan_type || 'plan' }}</span>
            <span
              class="inline-flex shrink-0 rounded-full border px-2 py-0.5 text-[10px]"
              :class="statusClass(plan.status)"
            >
              {{ planStatusLabel(plan.status) }}
            </span>
          </p>
          <p class="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">{{ plan.root_path }}</p>
        </div>
        <div class="shrink-0 text-right">
          <p class="font-mono text-[11px] text-muted-foreground">{{ formatPlanCreatedAt(plan.created_at) }}</p>
          <p class="mt-0.5 font-mono text-[10px] text-muted-foreground/70">{{ plan.plan_id }}</p>
        </div>
        <ChevronRight class="size-3.5 shrink-0 text-muted-foreground" />
      </button>
    </div>
    <div v-else-if="!pageError" class="grid min-h-0 flex-1 place-items-center px-6 text-center">
      <div class="max-w-sm">
        <ListMusic class="mx-auto size-7 text-muted-foreground" />
        <h2 class="mt-3 font-heading text-base font-semibold">还没有计划</h2>
        <p class="mt-1 text-xs leading-5 text-muted-foreground">
          打开一个文件夹，选择音频文件后生成第一份计划。
        </p>
        <Button data-testid="empty-to-libraries" class="mt-4" size="sm" @click="router.push('/libraries')">
          <WandSparkles class="size-3.5" />
          去媒体库选择文件夹
        </Button>
      </div>
    </div>
    <p
      v-if="activeLibrary"
      class="flex min-h-9 shrink-0 items-center gap-2 border-t border-border px-5 font-mono text-[11px] text-muted-foreground"
    >
      <span class="truncate">{{ activeLibrary.root_path }}</span>
    </p>
  </section>
</template>