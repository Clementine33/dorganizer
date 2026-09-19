<script setup lang="ts">
import { computed, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import OverrideEditor from '@/features/worksets/OverrideEditor.vue'
import { useCurrentConversion } from '@/composables/use-operation-context'
import { useWorksetEditorStore } from '@/stores/workset-editor'

/**
 * Edit one member's units ("应用到该文件夹"). The session lives in the editor
 * store, so collapsing the carrier, returning to the list or crossing a
 * container breakpoint keeps the unapplied edits (E03, E04).
 *
 * The editor route never carries a history query: a historical revision is
 * read-only and must be left before editing (E05).
 */
const route = useRoute()
const router = useRouter()
const editor = useWorksetEditorStore()
const libraryId = computed(() => (route.params.libraryId as string) || '')
const { worksetId, workspace, applySession } = useCurrentConversion(libraryId)

const memberId = computed(() => (route.params.memberId as string) || null)
const member = computed(() => workspace.workset.value?.members.find((m) => m.member_id === memberId.value) ?? null)
const draft = workspace.draft

watch(
  [draft, member, worksetId],
  () => {
    if (!draft.value || !member.value || !worksetId.value) return
    const current = editor.session
    if (current && current.worksetId === worksetId.value && current.target.kind === 'member' && current.target.memberId === member.value.member_id) {
      return
    }
    const opened = editor.open({
      worksetId: worksetId.value,
      operation: 'conversion',
      target: { kind: 'member', memberId: member.value.member_id },
      baseVersion: draft.value.version,
      baseDocument: draft.value.document,
    })
    if (!opened) {
      // Another target holds unapplied edits: keep them and go back (E04).
      void router.replace(`/worksets/libraries/${encodeURIComponent(libraryId.value)}/conversion`)
    }
  },
  { immediate: true },
)

const session = computed(() => editor.session)
const dirty = computed(() => editor.isDirty)

onBeforeRouteLeave(() => {
  if (!dirty.value) return true
  return window.confirm('有未应用的修改，确定放弃吗？')
})

async function apply() {
  const saved = await applySession()
  if (saved) {
    await router.push(`/worksets/libraries/${encodeURIComponent(libraryId.value)}/conversion/members/${encodeURIComponent(memberId.value ?? '')}`)
  }
}

function cancel() {
  if (dirty.value && !window.confirm('放弃未应用的修改？')) return
  editor.close()
  void router.push(`/worksets/libraries/${encodeURIComponent(libraryId.value)}/conversion/members/${encodeURIComponent(memberId.value ?? '')}`)
}
</script>

<template>
  <div class="p-3" data-testid="member-edit">
    <p v-if="!member" class="text-xs text-[var(--danger-ink)]" role="alert">未找到该文件夹。</p>
    <template v-else-if="session && draft">
      <h2 class="font-heading text-sm font-semibold">{{ member.folder_name }} · 修改此文件夹</h2>
      <p class="mt-0.5 text-[11px] text-[var(--text-muted)]">
        范围：仅此文件夹。全局设置入口请回到转换列表。
      </p>
      <p v-if="session.error" class="mt-2 text-[11px] text-[var(--danger-ink)]" role="alert" data-testid="edit-error">
        {{ session.error }}
      </p>

      <div class="mt-3">
        <OverrideEditor
          :draft="draft.document"
          :target="{ kind: 'member', memberId: member.member_id }"
          :read-only="session.applying"
          :participation-editable="true"
        />
      </div>

      <div class="mt-3 flex items-center gap-2">
        <Button size="sm" :disabled="!dirty || session.applying" data-testid="apply-member" @click="apply">
          应用到该文件夹
        </Button>
        <Button size="sm" variant="ghost" :disabled="session.applying" @click="cancel">取消</Button>
        <span class="text-[11px] text-[var(--text-muted)]">保存后需要重新生成计划版本。</span>
      </div>
    </template>
    <p v-else class="text-xs text-[var(--text-muted)]">加载中…</p>
  </div>
</template>
