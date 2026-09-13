<script setup lang="ts">
import { computed } from 'vue'
import { Button } from '@/components/ui/button'
import { OVERRIDE_UNITS } from '@/features/worksets/draft-intents'
import { defaultDraftValues } from '@/features/worksets/draft-defaults'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import type { OperationDraftDocument, OverrideUnit } from '@/lib/api/types'
import UnitFields from './UnitFields.vue'

/**
 * Common conversion settings: the four groups are edited directly, because a
 * common value has nothing to inherit from — the three-intent model belongs to
 * the member and batch entry points. 恢复默认 puts a group back to the value a
 * new operation is seeded with.
 */
const props = defineProps<{
  draft: OperationDraftDocument
  /** Library-level default tags, used by 恢复默认. */
  defaultTags: string[]
}>()

const editor = useWorksetEditorStore()

const UNIT_LABELS: Record<OverrideUnit, { label: string; hint: string }> = {
  mode: { label: '转换模式', hint: '严格保留组件一致性；可用源在没有合格无损源时保留原轨。' },
  classifier_tags: { label: '分类标签', hint: '按字面匹配判定“无音效”；留空表示不做分类。' },
  matched: { label: '无音效目标', hint: '匹配标签的文件夹要保留或生成的目标。' },
  unmatched: { label: '有音效目标', hint: '未匹配标签的文件夹要保留或生成的目标。' },
}

const defaults = computed(() => defaultDraftValues(props.defaultTags))

/** The values the form shows: pending edits win over the persisted draft. */
function valueOf(unit: OverrideUnit): unknown {
  const pending = editor.session?.intent.units[unit]
  if (pending?.intent === 'set') return pending.value
  switch (unit) {
    case 'mode':
      return props.draft.mode ?? 'available_sources'
    case 'classifier_tags':
      return props.draft.classifier_tags ?? []
    case 'matched':
      return props.draft.matched
    case 'unmatched':
      return props.draft.unmatched
  }
}

function setValue(unit: OverrideUnit, value: unknown) {
  editor.setUnit(unit, { intent: 'set', value })
}

function restoreDefault(unit: OverrideUnit) {
  editor.setUnit(unit, { intent: 'set', value: defaults.value[unit] })
}

/** True when a group currently differs from the value a new draft is seeded with. */
function isDefault(unit: OverrideUnit): boolean {
  return JSON.stringify(valueOf(unit) ?? null) === JSON.stringify(defaults.value[unit] ?? null)
}

function restoreAll() {
  for (const unit of OVERRIDE_UNITS) restoreDefault(unit)
}

const dirtyUnits = computed(() => OVERRIDE_UNITS.filter((unit) => editor.session?.intent.units[unit]?.intent === 'set'))
</script>

<template>
  <div class="space-y-3" data-testid="common-settings-form">
    <div class="flex flex-wrap items-center gap-2">
      <p class="text-[11px] text-[var(--text-muted)]">
        全局设置是所有继承它的文件夹的基准；此处修改只写全局值，不会给任何文件夹写入独立设置。
      </p>
      <Button
        v-if="dirtyUnits.length > 0"
        size="xs"
        variant="ghost"
        class="ml-auto"
        data-testid="restore-all-defaults"
        @click="restoreAll"
      >
        全部恢复默认
      </Button>
    </div>

    <fieldset
      v-for="unit in OVERRIDE_UNITS"
      :key="unit"
      class="rounded-lg border border-border p-3"
      :data-testid="`common-group-${unit}`"
    >
      <legend class="flex items-center gap-2 px-1 text-xs font-medium">
        {{ UNIT_LABELS[unit].label }}
        <span v-if="!isDefault(unit)" class="font-normal text-[var(--brand-ink)]">已修改</span>
      </legend>
      <p class="mb-2 text-[11px] text-[var(--text-muted)]">{{ UNIT_LABELS[unit].hint }}</p>

      <UnitFields
        :unit="unit"
        :label="UNIT_LABELS[unit].label"
        scope="common"
        :value="valueOf(unit)"
        :disabled="editor.session?.applying ?? false"
        @change="setValue(unit, $event)"
      />

      <div class="mt-2">
        <Button
          size="xs"
          variant="ghost"
          :disabled="isDefault(unit)"
          :data-testid="`common-${unit}-restore`"
          @click="restoreDefault(unit)"
        >
          恢复默认
        </Button>
      </div>
    </fieldset>
  </div>
</template>
