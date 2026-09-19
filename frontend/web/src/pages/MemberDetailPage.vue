<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import MemberReview from '@/features/worksets/MemberReview.vue'
import { useCurrentConversion } from '@/composables/use-operation-context'

/**
 * One member's frozen review (`/worksets/:L/conversion/:M`). The page renders
 * inside whichever carrier the container chose; routing here never changes the
 * URL between carriers (R01).
 */
const route = useRoute()
const router = useRouter()
const libraryId = computed(() => (route.params.libraryId as string) || '')
const { workspace } = useCurrentConversion(libraryId)

const memberId = computed(() => (route.params.memberId as string) || null)
const member = computed(() => workspace.workset.value?.members.find((m) => m.member_id === memberId.value) ?? null)
const revision = workspace.revision

function edit() {
  void router.push({
    name: 'conversion-member-edit',
    params: { libraryId: libraryId.value, memberId: memberId.value ?? '' },
  })
}
</script>

<template>
  <div>
    <p v-if="!member" class="p-3 text-xs text-[var(--danger-ink)]" role="alert" data-testid="member-not-found">
      未找到该文件夹：它可能不属于当前工作集。
    </p>
    <p v-else-if="!revision" class="p-3 text-xs text-[var(--text-muted)]">加载中…</p>
    <div v-else class="min-h-0">
      <MemberReview :member="member" :revision="revision" :editable="true" @edit="edit" />
    </div>
  </div>
</template>
