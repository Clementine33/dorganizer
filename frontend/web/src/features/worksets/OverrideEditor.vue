<script setup lang="ts">
import { computed } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  OVERRIDE_UNITS,
  readParticipation,
  readUnit,
  sameUnitValue,
  type EditTarget,
} from '@/features/worksets/draft-intents'
import { unitValueText } from '@/features/worksets/plan-readers'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import type { OperationDraftDocument, OverrideUnit } from '@/lib/api/types'
import UnitFields from './UnitFields.vue'

/**
 * Member and batch editing: each setting group is either the default (it
 * follows the common value) or an override (an explicit value of its own).
 * Picking 默认 removes the override and restores inheritance; picking 覆盖
 * reveals the fields and writes an explicit value.
 *
 * A group whose members disagree shows the current state as 多种值 and starts
 * with neither choice made — that display state is never saved as one value.
 */
const props = defineProps<{
  draft: OperationDraftDocument
  target: EditTarget
  readOnly: boolean
  participationEditable: boolean
}>()

const editor = useWorksetEditorStore()

const UNIT_LABELS: Record<OverrideUnit, string> = {
  mode: '转换模式',
  classifier_tags: '分类标签',
  matched: '无音效目标',
  unmatched: '有音效目标',
}

type Choice = 'default' | 'override' | 'unset'

/** What the group is set to right now, before this session's edits. */
function storedSource(unit: OverrideUnit): 'common' | 'member' | 'mixed' {
  return readUnit(props.draft, props.target, unit).source
}

function choiceOf(unit: OverrideUnit): Choice {
  const pending = editor.session?.intent.units[unit]
  if (pending?.intent === 'inherit') return 'default'
  if (pending?.intent === 'set') return 'override'
  if (pending?.intent === 'keep') return 'unset'
  return 'unset'
}

function chooseDefault(unit: OverrideUnit) {
  if (props.target.kind === 'common') return
  editor.setUnit(unit, { intent: 'inherit' })
}

function chooseOverride(unit: OverrideUnit) {
  const stored = readUnit(props.draft, props.target, unit)
  const seed = stored.value !== undefined ? stored.value : commonFallback(unit)
  editor.setUnit(unit, { intent: 'set', value: seed })
}

/** Mixed or absent values start the override from the common value. */
function commonFallback(unit: OverrideUnit): unknown {
  switch (unit) {
    case 'mode':
      return props.draft.mode ?? 'available_sources'
    case 'classifier_tags':
      return [...(props.draft.classifier_tags ?? [])]
    case 'matched':
      return props.draft.matched
    case 'unmatched':
      return props.draft.unmatched
  }
}

function valueOf(unit: OverrideUnit): unknown {
  const pending = editor.session?.intent.units[unit]
  if (pending?.intent === 'set') return pending.value
  const stored = readUnit(props.draft, props.target, unit)
  if (stored.value !== undefined) return stored.value
  return commonFallback(unit)
}

function statusLabel(unit: OverrideUnit): string {
  switch (choiceOf(unit)) {
    case 'default':
      return `将回到全局值（${unitValueText(unit, commonFallback(unit))}）`
    case 'override':
      return `将写入 ${unitValueText(unit, valueOf(unit))}`
    default:
      return storedLabel(unit)
  }
}

/**
 * The stored state in full: what the value is, and — for an override — whether
 * it merely spells out the common value. Equal values with different sources
 * stay a real configuration relationship, so they are said out loud instead of
 * letting the source word stand in for the value.
 */
function storedLabel(unit: OverrideUnit): string {
  const stored = readUnit(props.draft, props.target, unit)
  if (stored.source === 'mixed') return '当前：多种值'
  const relation =
    stored.source === 'member'
      ? sameUnitValue(stored.value, commonFallback(unit))
        ? '（与全局相同）'
        : '（与全局不同）'
      : ''
  return `当前：${unitValueText(unit, stored.value)}${relation}`
}

function sourceText(source: 'common' | 'member' | 'mixed'): string {
  if (source === 'member') return '独立值'
  if (source === 'mixed') return '多种值'
  return '默认'
}

const participation = computed(() => readParticipation(props.draft, props.target))
const participationIntent = computed(() => editor.session?.intent.participation ?? 'keep')
/**
 * The choice in force: a pending intent wins, otherwise the stored state. It is
 * deliberately binary — participate or exclude — so the current position is
 * always one of the two buttons, never an ambiguous "no change".
 */
const participationState = computed<'participate' | 'exclude' | 'mixed'>(() =>
  participationIntent.value === 'keep' ? participation.value : participationIntent.value,
)
const targetLabel = computed(() => {
  switch (props.target.kind) {
    case 'common':
      return '全局设置'
    case 'member':
      return '此文件夹'
    default:
      return `${props.target.memberIds.length} 个文件夹`
  }
})
const effectivelyExcluded = computed(() => participationState.value === 'exclude')
</script>

<template>
  <div class="space-y-3" data-testid="override-editor">
    <p class="text-[11px] text-[var(--text-muted)]">
      每项设置只有两种选择：默认（跟随全局设置）或覆盖（为此范围写入独立值）。未改动的项保持现状。
    </p>

    <fieldset
      v-for="unit in OVERRIDE_UNITS"
      :key="unit"
      class="rounded-lg border border-border p-3"
      :data-testid="`unit-${unit}`"
    >
      <legend class="px-1 text-xs font-medium">{{ UNIT_LABELS[unit] }}</legend>
      <div class="mb-2 flex flex-wrap items-center gap-2 text-[11px] text-[var(--text-muted)]">
        <Badge
          :tone="storedSource(unit) === 'mixed' ? 'warning' : storedSource(unit) === 'member' ? 'brand' : 'neutral'"
        >
          {{ sourceText(storedSource(unit)) }}
        </Badge>
        <span data-testid="unit-status">{{ statusLabel(unit) }}</span>
      </div>

      <div class="flex flex-wrap gap-2" role="radiogroup" :aria-label="`${UNIT_LABELS[unit]} 来源`">
        <Button
          size="xs"
          role="radio"
          :aria-checked="choiceOf(unit) === 'default'"
          :variant="choiceOf(unit) === 'default' ? 'secondary' : 'outline'"
          :disabled="readOnly"
          :data-testid="`unit-${unit}-default`"
          @click="chooseDefault(unit)"
        >
          默认
        </Button>
        <Button
          size="xs"
          role="radio"
          :aria-checked="choiceOf(unit) === 'override'"
          :variant="choiceOf(unit) === 'override' ? 'secondary' : 'outline'"
          :disabled="readOnly"
          :data-testid="`unit-${unit}-override`"
          @click="chooseOverride(unit)"
        >
          覆盖
        </Button>
      </div>

      <div v-if="choiceOf(unit) === 'override'" class="mt-2">
        <UnitFields
          :unit="unit"
          :label="UNIT_LABELS[unit]"
          scope="override"
          :value="valueOf(unit)"
          :disabled="readOnly"
          @change="editor.setUnit(unit, { intent: 'set', value: $event })"
        />
      </div>
    </fieldset>

    <fieldset v-if="participationEditable" class="rounded-lg border border-border p-3">
      <legend class="px-1 text-xs font-medium">本操作参与状态</legend>
      <p class="mb-2 text-[11px] text-[var(--text-muted)]">
        排除只改变本操作的参与状态，不会删除独立值；恢复参与后继续使用原有设置。
      </p>
      <div class="flex flex-wrap gap-2" role="radiogroup" aria-label="本操作参与状态">
        <Button
          size="xs"
          role="radio"
          :aria-checked="participationState === 'participate'"
          :variant="participationState === 'participate' ? 'secondary' : 'outline'"
          :disabled="readOnly"
          data-testid="participation-include"
          @click="editor.setParticipation('participate')"
        >
          参与（{{ targetLabel }}）
        </Button>
        <Button
          size="xs"
          role="radio"
          :aria-checked="participationState === 'exclude'"
          :variant="participationState === 'exclude' ? 'secondary' : 'outline'"
          :disabled="readOnly"
          data-testid="participation-exclude"
          @click="editor.setParticipation('exclude')"
        >
          从本操作排除
        </Button>
      </div>
      <p v-if="effectivelyExcluded" class="mt-2 text-[11px] text-[var(--warning-ink)]">
        应用后这些文件夹将不参与本操作的计划生成。
      </p>
    </fieldset>
  </div>
</template>
