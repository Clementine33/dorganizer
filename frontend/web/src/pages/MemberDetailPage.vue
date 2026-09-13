<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MemberReview from '@/features/worksets/MemberReview.vue'
import { useOperationContext } from '@/composables/use-operation-context'

/**
 * One member's frozen review (`.../conversion/members/:memberId`). The page
 * renders inside whichever carrier the container chose; routing here never
 * changes the URL between carriers (R01).
 */
const route = useRoute()
const router = useRouter()
const worksetId = computed(() => (route.params.worksetId as string) || null)
const revisionPlanId = computed(() => (route.query.revision as string) || null)
const { workspace } = useOperationContext(worksetId, 'conversion', revisionPlanId)

const memberId = computed(() => (route.params.memberId as string) || null)
const member = computed(() => workspace.workset.value?.members.find((m) => m.member_id === memberId.value) ?? null)
const revision = workspace.revision

function edit() {
  void router.push(`/worksets/${encodeURIComponent(worksetId.value ?? '')}/conversion/members/${encodeURIComponent(memberId.value ?? '')}/edit`)
}
</script>

<template>
  <div>
    <p v-if="!member" class="p-3 text-xs text-[var(--danger-ink)]" role="alert" data-testid="member-not-found">
      未找到该文件夹：它可能不属于当前工作集。
    </p>
    <p v-else-if="!revision" class="p-3 text-xs text-[var(--text-muted)]">加载中…</p>
    <div v-else class="min-h-0">
      <p v-if="revisionPlanId" class="border-b border-border bg-muted px-3 py-1.5 text-[11px] text-[var(--text-secondary)]">
        历史版本为只读审阅：回到当前草稿后才能编辑。
      </p>
      <MemberReview :member="member" :revision="revision" :editable="!revisionPlanId" @edit="edit" />
    </div>
  </div>
</template>
