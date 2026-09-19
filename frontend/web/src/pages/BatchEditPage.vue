<script setup lang="ts">
import { computed, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import OverrideEditor from '@/features/worksets/OverrideEditor.vue'
import { useCurrentConversion } from '@/composables/use-operation-context'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import { useWorksetUiStore } from '@/stores/workset-ui'
import { intentUnitCount } from '@/features/worksets/draft-intents'

/**
 * Fixed-name batch editing. The name list is frozen when the entry point is
 * clicked and never follows later list selection or filtering (E02, T18). The
 * read-only list is always available for checking exactly who is affected.
 */
const route = useRoute()
const router = useRouter()
const editor = useWorksetEditorStore()
const ui = useWorksetUiStore()
const libraryId = computed(() => (route.params.libraryId as string) || '')
const { worksetId, workspace, applySession } = useCurrentConversion(libraryId)
const draft = workspace.draft
const listRoute = computed(() => ({
  name: 'conversion' as const,
  params: { libraryId: libraryId.value },
}))

const memberIds = computed(() => ui.batchMemberIds)
const members = computed(() => {
  const all = workspace.workset.value?.members ?? []
  return memberIds.value.map((id) => all.find((m) => m.member_id === id)).filter((m) => m !== undefined)
})

// A batch route without a frozen list is not an error but a missing session:
// the user is returned to the list to choose folders first (R04).
watch(
  [draft, memberIds],
  () => {
    if (!worksetId.value || memberIds.value.length === 0) {
      void router.replace(listRoute.value)
      return
    }
    if (!draft.value) return
    const current = editor.session
    if (current && current.worksetId === worksetId.value && current.target.kind === 'batch') return
    const opened = editor.open({
      worksetId: worksetId.value,
      operation: 'conversion',
      target: { kind: 'batch', memberIds: [...memberIds.value] },
      baseVersion: draft.value.version,
      baseDocument: draft.value.document,
    })
    if (!opened) void router.replace(listRoute.value)
  },
  { immediate: true },
)

const dirty = computed(() => editor.isDirty)
const session = computed(() => editor.session)
const editedUnits = computed(() => (session.value ? intentUnitCount(session.value.intent) : 0))
const excludedTargets = computed(() => {
  const document = draft.value?.document
  if (!document) return []
  return members.value.filter((m) => document.members.find((r) => r.member_id === m.member_id)?.excluded)
})

onBeforeRouteLeave(() => {
  if (!dirty.value) return true
  return window.confirm('有未应用的批量修改，确定放弃吗？')
})

async function apply() {
  const saved = await applySession()
  if (saved) {
    ui.clearSelection()
    ui.clearBatchList()
    await router.push(listRoute.value)
  }
}

function cancel() {
  if (dirty.value && !window.confirm('放弃未应用的修改？')) return
  editor.close()
  ui.clearBatchList()
  void router.push(listRoute.value)
}
</script>

<template>
  <div class="mx-auto max-w-2xl p-3" data-testid="batch-edit-page">
    <div class="mb-2 flex items-center gap-2">
      <RouterLink :to="listRoute" class="text-xs text-[var(--brand-ink)] hover:underline">← 转换列表</RouterLink>
      <h2 class="font-heading text-sm font-semibold">批量修改 {{ members.length }} 个文件夹</h2>
    </div>

    <details class="mb-2 rounded-md border border-border p-2 text-[11px]" open>
      <summary class="cursor-pointer font-medium">作用名单（固定，不随列表变化）</summary>
      <ul class="mt-1 space-y-0.5">
        <li v-for="member in members" :key="member.member_id" class="flex items-center gap-1.5">
          <span class="truncate">{{ member.folder_name }}</span>
          <Badge v-if="excludedTargets.some((m) => m.member_id === member.member_id)" tone="neutral">本操作已排除</Badge>
        </li>
      </ul>
    </details>

    <p v-if="session?.error" class="text-[11px] text-[var(--danger-ink)]" role="alert">{{ session.error }}</p>

    <div v-if="draft && session">
      <OverrideEditor
        :draft="draft.document"
        :target="{ kind: 'batch', memberIds: session.target.kind === 'batch' ? session.target.memberIds : [] }"
        :read-only="session.applying"
        :participation-editable="true"
      />
    </div>
    <p v-else class="text-xs text-[var(--text-muted)]">加载中…</p>

    <div class="mt-3 flex items-center gap-2">
      <Button size="sm" :disabled="!dirty || (session?.applying ?? false)" data-testid="apply-batch" @click="apply">
        修改 {{ members.length }} 个文件夹的 {{ editedUnits }} 项设置
      </Button>
      <Button size="sm" variant="ghost" @click="cancel">取消</Button>
    </div>
  </div>
</template>
