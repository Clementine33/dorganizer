<script setup lang="ts">
import { computed, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import CommonSettingsForm from '@/features/worksets/CommonSettingsForm.vue'
import { classifierTagLibraryQueryOptions } from '@/queries/worksets'
import { useApiClient } from '@/lib/api/client'
import { useQuery } from '@tanstack/vue-query'
import { useOperationContext } from '@/composables/use-operation-context'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import { useWorksetUiStore } from '@/stores/workset-ui'

/**
 * Common conversion settings. Each unit shows how many members it reaches, so
 * a common change never silently affects folders the user is not looking at
 * (C10). Generation and confirmation stay on the operation list page (F17).
 */
const route = useRoute()
const router = useRouter()
const editor = useWorksetEditorStore()
const ui = useWorksetUiStore()
const worksetId = computed(() => (route.params.worksetId as string) || null)
const { workspace, applySession } = useOperationContext(worksetId)
const draft = workspace.draft
const api = useApiClient()
// 恢复默认 restores the deployment's literal tags, so the form needs them.
const { data: tagLibrary } = useQuery(classifierTagLibraryQueryOptions(api))
const defaultTags = computed(() => tagLibrary.value?.default_tags ?? [])

watch(
  [draft, worksetId],
  () => {
    if (!draft.value || !worksetId.value) return
    const current = editor.session
    if (current && current.worksetId === worksetId.value && current.target.kind === 'common') return
    editor.open({
      worksetId: worksetId.value,
      operation: 'conversion',
      target: { kind: 'common' },
      baseVersion: draft.value.version,
      baseDocument: draft.value.document,
    })
  },
  { immediate: true },
)

const dirty = computed(() => editor.isDirty)
const session = computed(() => editor.session)

/** Per unit: members that inherit it and would therefore be affected. */
const affected = computed(() => {
  const document = draft.value?.document
  const members = workspace.workset.value?.members ?? []
  if (!document) return { total: 0, excluded: 0 }
  let inheriting = 0
  let excluded = 0
  for (const member of members) {
    const record = document.members.find((m) => m.member_id === member.member_id)
    const overridesMode =
      record?.overrides?.mode !== undefined ||
      record?.overrides?.classifier_tags !== undefined ||
      record?.overrides?.matched !== undefined ||
      record?.overrides?.unmatched !== undefined
    void overridesMode
    inheriting++
    if (record?.excluded) excluded++
  }
  return { total: inheriting, excluded }
})

onBeforeRouteLeave(() => {
  if (!dirty.value) return true
  return window.confirm('有未应用的全局设置修改，确定放弃吗？')
})

async function apply() {
  await applySession()
}

function cancel() {
  if (dirty.value && !window.confirm('放弃未应用的修改？')) return
  editor.close()
  void router.push(`/worksets/${encodeURIComponent(worksetId.value ?? '')}/conversion`)
}

const listLink = computed(() => `/worksets/${encodeURIComponent(worksetId.value ?? '')}/conversion${ui.historyPlanId ? `?revision=${ui.historyPlanId}` : ''}`)
</script>

<template>
  <div class="mx-auto max-w-2xl p-3" data-testid="conversion-settings">
    <div class="mb-2 flex items-center gap-2">
      <RouterLink :to="listLink" class="text-xs text-[var(--brand-ink)] hover:underline">← 转换列表</RouterLink>
      <h2 class="font-heading text-sm font-semibold">转换全局设置</h2>
    </div>

    <p v-if="affected.total > 0" class="text-[11px] text-[var(--text-muted)]" data-testid="affected-members">
      影响 {{ affected.total }} 个继承全局设置的文件夹<template v-if="affected.excluded > 0">
        （其中 {{ affected.excluded }} 个已从本操作排除：设置仍会更新）</template
      >。
    </p>
    <p v-if="session?.error" class="mt-2 text-[11px] text-[var(--danger-ink)]" role="alert">{{ session.error }}</p>

    <div v-if="draft" class="mt-3">
      <CommonSettingsForm :draft="draft.document" :default-tags="defaultTags" />
    </div>
    <p v-else class="text-xs text-[var(--text-muted)]">加载中…</p>

    <div class="mt-3 flex items-center gap-2">
      <Button size="sm" :disabled="!dirty || (session?.applying ?? false)" data-testid="apply-common" @click="apply">
        应用全局设置
      </Button>
      <Button size="sm" variant="ghost" @click="cancel">取消</Button>
    </div>
  </div>
</template>
