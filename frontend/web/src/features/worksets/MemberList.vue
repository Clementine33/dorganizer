<script setup lang="ts">
import { computed } from 'vue'
import { Circle, CircleAlert, CircleCheck, Eye, FolderOpen, TriangleAlert } from '@lucide/vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { OVERRIDE_UNITS, readUnit, type EditTarget } from '@/features/worksets/draft-intents'
import type { MemberConclusion } from '@/features/worksets/plan-readers'
import type { OperationDraftDocument, WorksetMember } from '@/lib/api/types'

/**
 * The operation's member list.
 *
 * Clicking a row body opens the member (view), the checkbox only selects:
 * selection never drills in and viewing never selects. Filtering never
 * clears the selection, and a filter that hides selected rows reports how many
 * are hidden so "修改 N 个文件夹" never counts invisible names.
 */
const props = defineProps<{
  members: WorksetMember[]
  draft: OperationDraftDocument
  selectedIds: Set<string>
  hiddenSelectedCount: number
  filter: 'all' | 'change' | 'warn' | 'blocked' | 'excluded'
  search: string
  conclusionFor: (member: WorksetMember) => MemberConclusion
  /**
   * Drill-down tier: the row keeps only the name, a status icon and the view
   * action, so a narrow container is not spent on text that the member detail
   * repeats in full.
   */
  compact?: boolean
}>()

const emit = defineEmits<{
  /** Opening a member's files: the shared file module, in this workbench. */
  files: [memberId: string]
  toggle: [memberId: string]
  toggleAll: [ids: string[]]
  open: [memberId: string]
  'update:filter': [value: 'all' | 'change' | 'warn' | 'blocked' | 'excluded']
  'update:search': [value: string]
}>()

const FILTERS: { value: 'all' | 'change' | 'warn' | 'blocked' | 'excluded'; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'change', label: '有变化' },
  { value: 'warn', label: '目标未满足' },
  { value: 'blocked', label: '阻塞' },
  { value: 'excluded', label: '已排除' },
]

const excluded = computed(() => {
  const set = new Set<string>()
  for (const record of props.draft.members) {
    if (record.excluded) set.add(record.member_id)
  }
  return set
})

const visible = computed(() =>
  props.members.filter((member) => {
    const query = props.search.trim().toLowerCase()
    if (query && !member.folder_name.toLowerCase().includes(query) && !member.rel_path.toLowerCase().includes(query)) {
      return false
    }
    switch (props.filter) {
      case 'excluded':
        return excluded.value.has(member.member_id)
      case 'change':
      case 'warn':
      case 'blocked':
        return props.conclusionFor(member).tone === tonalFilter(props.filter)
      default:
        return true
    }
  }),
)

function tonalFilter(filter: 'change' | 'warn' | 'blocked') {
  if (filter === 'change') return 'success'
  if (filter === 'warn') return 'warning'
  return 'danger'
}

const visibleIds = computed(() => visible.value.map((m) => m.member_id))
const allVisibleSelected = computed(
  () => visibleIds.value.length > 0 && visibleIds.value.every((id) => props.selectedIds.has(id)),
)

/**
 * Whether a member carries any setting of its own. Which units are overridden
 * is the detail view's job: in the list a single 默认／已修改 state says what
 * the user needs to know without four unreadable per-unit tags.
 */
function hasOverride(member: WorksetMember): boolean {
  return OVERRIDE_UNITS.some((unit) => {
    const value = readUnit(props.draft, { kind: 'member', memberId: member.member_id } as EditTarget, unit)
    return value.source === 'member'
  })
}

/** Shape carries the conclusion in compact rows; the tone carries the status. */
const CONCLUSION_ICONS = {
  success: CircleCheck,
  warning: TriangleAlert,
  danger: CircleAlert,
  neutral: Circle,
} as const

const CONCLUSION_TONES: Record<MemberConclusion['tone'], string> = {
  success: 'text-[var(--success-ink)]',
  warning: 'text-[var(--warning-ink)]',
  danger: 'text-[var(--danger-ink)]',
  neutral: 'text-[var(--text-muted)]',
}

function conclusionTitle(member: WorksetMember): string {
  const conclusion = props.conclusionFor(member)
  return `${conclusion.label}：${conclusion.detail}`
}
</script>

<template>
  <div class="flex min-h-0 flex-col">
    <!-- The toolbar wraps instead of pushing the count out of the container. -->
    <div
      class="flex min-h-[var(--toolbar-h)] shrink-0 flex-wrap items-center gap-2 border-b border-border px-3 py-1"
      data-testid="member-toolbar"
    >
      <input
        :value="search"
        type="search"
        placeholder="搜索文件夹"
        aria-label="搜索文件夹"
        class="h-7 w-full rounded-md border border-[var(--control-border)] bg-background px-2 text-xs focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none @[641px]:w-40"
        @input="emit('update:search', ($event.target as HTMLInputElement).value)"
      />
      <div class="flex items-center gap-1" role="group" aria-label="筛选">
        <Button
          v-for="item in FILTERS"
          :key="item.value"
          :variant="filter === item.value ? 'secondary' : 'ghost'"
          size="xs"
          :aria-pressed="filter === item.value"
          @click="emit('update:filter', item.value)"
        >
          {{ item.label }}
        </Button>
      </div>
      <span v-if="hiddenSelectedCount > 0" class="text-[11px] text-[var(--text-muted)]" data-testid="hidden-selection">
        另有 {{ hiddenSelectedCount }} 个已选文件夹被筛选隐藏
      </span>
      <span class="ml-auto font-mono text-[11px] text-[var(--text-muted)]">
        {{ visible.length }} / {{ members.length }}
      </span>
    </div>

    <div class="min-h-0 flex-1 overflow-auto">
      <table class="w-full border-collapse text-xs">
        <caption class="sr-only">工作集成员与转换结论</caption>
        <thead class="sticky top-0 z-10 bg-card">
          <tr class="h-8 border-b border-border text-left text-[11px] text-[var(--text-muted)]">
            <th scope="col" class="w-9 px-2">
              <input
                type="checkbox"
                aria-label="全选当前筛选结果"
                :checked="allVisibleSelected"
                @change="emit('toggleAll', visibleIds)"
              />
            </th>
            <th scope="col" class="px-2 font-medium">文件夹</th>
            <th scope="col" :class="compact ? 'w-10 px-2 font-medium' : 'px-2 font-medium'">结论</th>
            <th v-if="!compact" scope="col" class="px-2 font-medium">独立设置</th>
            <th scope="col" :class="compact ? 'w-12 px-2 font-medium' : 'w-20 px-2 font-medium'">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="member in visible"
            :key="member.member_id"
            class="border-b border-border/60"
            :style="{ minHeight: 'var(--row-h)' }"
          >
            <td class="px-2 align-middle">
              <input
                type="checkbox"
                :checked="selectedIds.has(member.member_id)"
                :aria-label="`选择 ${member.folder_name}`"
                @change="emit('toggle', member.member_id)"
              />
            </td>
            <td class="min-w-0 px-2 py-1 align-middle">
              <button
                type="button"
                class="block max-w-full truncate text-left font-medium hover:underline focus-visible:ring-2 focus-visible:ring-[var(--brand)] focus-visible:outline-none"
                :title="member.folder_path"
                data-testid="member-open"
                @click="emit('open', member.member_id)"
              >
                {{ member.folder_name }}
              </button>
              <span class="block truncate font-mono text-[10px] text-[var(--text-muted)]" :title="member.folder_path">
                {{ member.rel_path }}
              </span>
            </td>
            <td class="px-2 align-middle">
              <span
                v-if="compact"
                class="inline-flex items-center"
                :title="conclusionTitle(member)"
                data-testid="member-conclusion-icon"
              >
                <component
                  :is="CONCLUSION_ICONS[conclusionFor(member).tone]"
                  class="size-4"
                  :class="CONCLUSION_TONES[conclusionFor(member).tone]"
                />
                <span class="sr-only">{{ conclusionFor(member).label }}</span>
              </span>
              <span v-else class="flex items-center gap-1">
                <Badge :tone="conclusionFor(member).tone" :title="conclusionFor(member).detail">
                  {{ conclusionFor(member).label }}
                </Badge>
              </span>
            </td>
            <td v-if="!compact" class="px-2 align-middle">
              <Badge :tone="hasOverride(member) ? 'brand' : 'neutral'" data-testid="member-override-state">
                {{ hasOverride(member) ? '已修改' : '默认' }}
              </Badge>
            </td>
            <td class="px-2 align-middle">
              <div class="flex items-center gap-0.5">
                <!-- The member's files: the shared file module, opened inside
                     this workbench (ADR 0001 §1, §3). -->
                <Button
                  variant="ghost"
                  size="icon-sm"
                  :aria-label="`打开 ${member.folder_name} 的文件`"
                  title="当前文件"
                  data-testid="member-files"
                  @click="emit('files', member.member_id)"
                >
                  <FolderOpen class="size-4" />
                </Button>
                <Button
                  v-if="compact"
                  variant="ghost"
                  size="icon-sm"
                  :aria-label="`查看 ${member.folder_name}`"
                  data-testid="member-open-icon"
                  @click="emit('open', member.member_id)"
                >
                  <Eye class="size-4" />
                </Button>
                <Button v-else variant="ghost" size="xs" @click="emit('open', member.member_id)">查看</Button>
              </div>
            </td>
          </tr>
          <tr v-if="visible.length === 0">
            <td :colspan="compact ? 4 : 5" class="px-3 py-6 text-center text-[11px] text-[var(--text-muted)]">
              没有符合当前筛选的文件夹。
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
