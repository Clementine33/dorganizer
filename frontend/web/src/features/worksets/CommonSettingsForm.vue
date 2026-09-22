<script setup lang="ts">
import { computed } from 'vue'
import { Button } from '@/components/ui/button'
import { OVERRIDE_UNITS } from '@/features/worksets/draft-intents'
import { defaultDraftValues } from '@/features/worksets/draft-defaults'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import type { DeleteMode, OperationDraftDocument, OverrideUnit } from '@/lib/api/types'
import UnitFields from './UnitFields.vue'

/**
 * Common conversion settings: the four groups are edited directly, because a
 * common value has nothing to inherit from — the three-intent model belongs to
 * the member and batch entry points. 恢复默认 puts a group back to the value a
 * new operation is seeded with. 旧音频处理 is a fifth, whole-operation choice:
 * it is never a member override and freezes into every revision the draft
 * produces.
 */
const props = defineProps<{
  draft: OperationDraftDocument
  /** Library-level default tags, used by 恢复默认. */
  defaultTags: string[]
}>()

const editor = useWorksetEditorStore()

const UNIT_LABELS: Record<OverrideUnit, { label: string; hint: string }> = {
  mode: { label: '转换模式', hint: '严格模式要求最终格式完全符合设置；可用源模式在没有合格无损源时保留原轨。' },
  classifier_tags: { label: '分类标签', hint: '按字面匹配判定“无音效”,不区分大小写；留空表示不做分类。' },
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

/** The obsolete-audio handling: pending edit, else the persisted choice. */
const deleteModeValue = computed<DeleteMode>({
  get: () => {
    const pending = editor.session?.intent.deleteMode
    if (pending?.intent === 'set') return pending.value
    return props.draft.delete_mode ?? 'soft'
  },
  set: (value) => editor.setDeleteMode(value),
})
const deleteModeDirty = computed(() => deleteModeValue.value !== defaults.value.delete_mode)

function restoreDeleteMode() {
  editor.setDeleteMode(defaults.value.delete_mode)
}

function restoreAll() {
  for (const unit of OVERRIDE_UNITS) restoreDefault(unit)
  restoreDeleteMode()
}

const dirtyUnits = computed(() => OVERRIDE_UNITS.filter((unit) => editor.session?.intent.units[unit]?.intent === 'set'))
</script>

<template>
  <div class="space-y-3" data-testid="common-settings-form">
    <div class="flex flex-wrap items-center gap-2">
      <Button
        v-if="dirtyUnits.length > 0 || deleteModeDirty"
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

    <fieldset class="rounded-lg border border-border p-3" data-testid="common-group-delete_mode">
      <legend class="flex items-center gap-2 px-1 text-xs font-medium">
        旧音频处理
        <span v-if="deleteModeDirty" class="font-normal text-[var(--brand-ink)]">已修改</span>
      </legend>
      <p class="mb-2 text-[11px] text-[var(--text-muted)]">
        执行时如何处置被替换和不再需要的旧音频；该设置随草稿冻结进每个计划版本。
      </p>
      <div class="space-y-1.5">
        <label
          class="flex cursor-pointer items-start gap-2 rounded-md border border-border p-2 has-[:checked]:border-[var(--brand-border)] has-[:checked]:bg-[var(--brand-weak)]"
        >
          <input
            v-model="deleteModeValue"
            type="radio"
            name="common-delete-mode"
            value="soft"
            class="mt-0.5"
            :disabled="editor.session?.applying ?? false"
            data-testid="common-delete-mode-soft"
          />
          <span>
            <span class="font-medium">软删除（默认）</span>
            <span class="block text-[11px] text-[var(--text-muted)]">旧文件移动到库根目录下的 Delete/（保留各文件夹的原相对路径），可手动恢复。</span>
          </span>
        </label>
        <label
          class="flex cursor-pointer items-start gap-2 rounded-md border border-border p-2 has-[:checked]:border-[var(--danger-border)] has-[:checked]:bg-[var(--danger-weak)]"
        >
          <input
            v-model="deleteModeValue"
            type="radio"
            name="common-delete-mode"
            value="hard"
            class="mt-0.5"
            :disabled="editor.session?.applying ?? false"
            data-testid="common-delete-mode-hard"
          />
          <span>
            <span class="font-medium">硬删除</span>
            <span class="block text-[11px] text-[var(--text-muted)]">旧文件直接从磁盘移除，本应用无法恢复。</span>
          </span>
        </label>
      </div>
      <p
        v-if="deleteModeValue === 'hard'"
        class="mt-1.5 text-[11px] text-[var(--danger-ink)]"
        role="alert"
        data-testid="common-hard-delete-warning"
      >
        硬删除不可恢复：被替换和被清理的旧音频文件将被永久移除。
      </p>
      <div class="mt-2">
        <Button
          size="xs"
          variant="ghost"
          :disabled="!deleteModeDirty"
          data-testid="common-delete_mode-restore"
          @click="restoreDeleteMode"
        >
          恢复默认
        </Button>
      </div>
    </fieldset>
  </div>
</template>
