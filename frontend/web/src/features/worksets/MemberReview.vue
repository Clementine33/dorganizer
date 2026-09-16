<script setup lang="ts">
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import { componentOperationCount, operationsOf, revisionComponents } from '@/features/worksets/plan-readers'
import type { ComponentOutcome, RevisionDetailResponse, RevisionMember, WorksetMember } from '@/lib/api/types'

/**
 * One member's frozen review: the configuration the revision was planned with
 * (effective values plus per-unit sources), its input status and the planned
 * components. Historical revisions show their frozen data, never the live
 * common values (P01, T13).
 */
const props = defineProps<{
  member: WorksetMember
  revision: RevisionDetailResponse
  /** A historical revision is read-only: it offers no way into an editor (E05). */
  editable: boolean
}>()

const emit = defineEmits<{ edit: [] }>()

const frozen = computed<RevisionMember | null>(
  () => props.revision.members.find((m) => m.member_id === props.member.member_id) ?? null,
)

const UNIT_LABELS: Record<string, string> = {
  mode: '转换模式',
  classifier_tags: '分类标签',
  matched: '无音效目标',
  unmatched: '有音效目标',
}

const rootIndex = computed(
  () => props.revision.roots.find((r) => r.root_path === props.member.folder_path)?.root_index ?? null,
)

const components = computed<ComponentOutcome[]>(() => {
  if (rootIndex.value === null) return []
  const owned = new Set(
    props.revision.component_roots.filter((ref) => ref.root_index === rootIndex.value).map((ref) => ref.component_id),
  )
  return revisionComponents(props.revision).filter((component) => owned.has(component.component_id))
})

const inputStatus = computed(() => props.revision.roots.find((r) => r.root_path === props.member.folder_path) ?? null)

/** Only the member's own settings are called out; inheriting is the default. */
function isOverride(unit: string): boolean {
  return frozen.value?.sources[unit] === 'member'
}

function profileText(profile: { lossless?: { codec?: string }; encoded?: { codec?: string; quality?: { bitrate?: number } } } | undefined): string {
  if (!profile) return '未设置'
  const parts: string[] = []
  if (profile.lossless?.codec) parts.push(profile.lossless.codec.toUpperCase())
  if (profile.encoded?.codec) {
    const bitrate = profile.encoded.quality?.bitrate
    parts.push(bitrate ? `${profile.encoded.codec.toUpperCase()} ${bitrate}` : profile.encoded.codec.toUpperCase())
  }
  return parts.length > 0 ? parts.join(' + ') : '未设置'
}

function componentTone(status: string): 'success' | 'danger' | 'neutral' {
  if (status === 'blocked') return 'danger'
  if (status === 'ok') return 'success'
  return 'neutral'
}
</script>

<template>
  <div class="space-y-3 p-3" data-testid="member-review">
    <div>
      <h2 class="truncate font-heading text-sm font-semibold" :title="member.folder_path">{{ member.folder_name }}</h2>
      <p class="truncate font-mono text-[10px] text-[var(--text-muted)]" :title="member.folder_path">
        {{ member.rel_path }}
      </p>
    </div>

    <div class="flex flex-wrap gap-1.5">
      <Badge v-if="frozen?.excluded" tone="neutral">本操作已排除</Badge>
      <Badge v-if="inputStatus?.root_status === 'missing'" tone="danger">输入缺失</Badge>
      <Badge v-else-if="inputStatus?.stale" tone="warning">输入已变化</Badge>
      <Badge v-else-if="inputStatus" tone="success">输入有效</Badge>
    </div>

    <section aria-label="冻结的有效设置">
      <h3 class="mb-1 text-[11px] font-semibold text-[var(--text-secondary)]">冻结的有效设置</h3>
      <dl class="space-y-1 text-[11px]">
        <div v-for="unit in Object.keys(UNIT_LABELS)" :key="unit" class="flex items-start gap-2">
          <dt class="w-20 shrink-0 text-[var(--text-muted)]">{{ UNIT_LABELS[unit] }}</dt>
          <dd class="min-w-0 flex-1">
            <span v-if="unit === 'mode'">{{ frozen?.effective.mode ?? 'strict' }}</span>
            <span v-else-if="unit === 'classifier_tags'" class="font-mono">
              {{ (frozen?.effective.classifier_tags ?? []).join(', ') || '（空）' }}
            </span>
            <span v-else class="font-mono">
              {{ profileText(unit === 'matched' ? frozen?.effective.matched : frozen?.effective.unmatched) }}
            </span>
            <Badge v-if="isOverride(unit)" tone="brand" class="ml-1">独立设置</Badge>
          </dd>
        </div>
      </dl>
    </section>

    <section aria-label="计划结论">
      <h3 class="mb-1 text-[11px] font-semibold text-[var(--text-secondary)]">计划结论</h3>
      <p v-if="components.length === 0" class="text-[11px] text-[var(--text-muted)]">
        {{ frozen?.excluded ? '本操作已排除该文件夹，未参与规划。' : '该文件夹在此版本中没有可审阅的组件。' }}
      </p>
      <ul v-else class="space-y-2">
        <li v-for="component in components" :key="component.component_id" class="rounded-md border border-border p-2">
          <div class="flex flex-wrap items-center gap-1.5">
            <Badge :tone="componentTone(component.status)">
              {{ component.status === 'blocked' ? '阻塞' : componentOperationCount(component) > 0 ? '有变化' : '无变化' }}
            </Badge>
            <span class="text-[11px] text-[var(--text-muted)]">
              {{ component.partition === 'matched' ? '无音效' : '有音效' }}
            </span>
            <span v-if="component.reason_code" class="font-mono text-[10px] text-[var(--text-muted)]">
              {{ component.reason_code }}
            </span>
          </div>
          <p v-if="component.message" class="mt-1 text-[11px]">{{ component.message }}</p>
          <ul v-if="operationsOf(component).length > 0" class="mt-1 space-y-0.5">
            <li
              v-for="operation in operationsOf(component)"
              :key="`${operation.kind}-${operation.source_path}`"
              class="truncate font-mono text-[10px]"
              :title="operation.source_path"
            >
              {{ operation.kind }} · {{ operation.source_path }}
              <span v-if="operation.target_path">→ {{ operation.target_path }}</span>
            </li>
          </ul>
        </li>
      </ul>
    </section>

    <div v-if="!frozen?.excluded && editable">
      <slot name="actions" :edit="emit">
        <button
          type="button"
          class="text-xs font-medium text-[var(--brand-ink)] hover:underline focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none"
          data-testid="member-review-edit"
          @click="emit('edit')"
        >
          修改此文件夹
        </button>
      </slot>
    </div>
  </div>
</template>
