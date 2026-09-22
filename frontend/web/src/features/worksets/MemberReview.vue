<script setup lang="ts">
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import {
  componentOperationCount,
  decisionGroups,
  keepReasonText,
  profileText,
  resolutionText,
  revisionComponents,
} from '@/features/worksets/plan-readers'
import type { ComponentOutcome, RevisionDetailResponse, RevisionMember, WorksetMember } from '@/lib/api/types'

/**
 * One member's frozen review: the configuration the revision was planned with
 * (effective values plus per-unit sources), its input status and the planned
 * components. It shows the revision's frozen data, never the live common
 * values.
 */
const props = defineProps<{
  member: WorksetMember
  revision: RevisionDetailResponse
  /** Whether this review offers the way into the editor at all. */
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

/**
 * The component's headline. An unmet target is deliberately not a badge here:
 * every kept file names its own reason below, and the member row already
 * carries the warning.
 */
function componentFacts(component: ComponentOutcome): { label: string; tone: 'success' | 'warning' | 'danger' | 'neutral' }[] {
  if (component.status === 'blocked') return [{ label: '阻塞', tone: 'danger' }]
  return [
    componentOperationCount(component) > 0
      ? { label: '有变化', tone: 'success' }
      : { label: '无变化', tone: 'neutral' },
  ]
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
            <span class="text-[11px] text-[var(--text-muted)]">
              {{ component.partition === 'matched' ? '无音效' : '有音效' }}
            </span>
            <Badge v-for="fact in componentFacts(component)" :key="fact.label" :tone="fact.tone">
              {{ fact.label }}
            </Badge>
            <span v-if="component.reason_code" class="font-mono text-[10px] text-[var(--text-muted)]">
              {{ component.reason_code }}
            </span>
          </div>
          <p v-if="component.message" class="mt-1 text-[11px]">{{ component.message }}</p>
          <!-- Each file once, its folder named once: the plan's own per-file
               resolutions. Text wraps rather than clipping, so a long name is
               never cut into an unreadable one. -->
          <div
            v-for="group in decisionGroups(component, member.folder_path)"
            :key="group.dir"
            class="mt-1"
            data-testid="component-decisions"
          >
            <p v-if="group.dir" class="font-mono text-[10px] text-[var(--text-muted)] [overflow-wrap:anywhere]">
              {{ group.dir }}
            </p>
            <ul class="space-y-0.5">
              <li
                v-for="row in group.rows"
                :key="`${row.resolution}-${row.name}`"
                class="flex gap-1.5 font-mono text-[10px] leading-4"
                data-testid="component-decision"
              >
                <span class="w-8 shrink-0 text-[var(--text-secondary)]">
                  {{ resolutionText(row.resolution) }}
                </span>
                <span class="min-w-0 flex-1 [overflow-wrap:anywhere]">
                  {{ row.name }}
                  <!-- Why a file stays is the question worth answering; a
                       generated or removed file speaks for itself. -->
                  <span
                    v-if="row.resolution === 'keep' && row.reasonCode"
                    class="text-[var(--text-muted)]"
                  >
                    · {{ keepReasonText(row.reasonCode) }}
                  </span>
                </span>
              </li>
            </ul>
          </div>
        </li>
      </ul>
    </section>

    <!-- An excluded member is still editable: participation is one of the
         units the editor writes, so hiding the way in would trap it. The
         caller decides whether the way in is offered at all. -->
    <div v-if="editable">
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
