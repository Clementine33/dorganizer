<script setup lang="ts">
import { computed } from 'vue'
import { cloneProfile } from '@/features/worksets/plan-readers'
import type { DesiredProfile, OverrideUnit } from '@/lib/api/types'
import UnitSelect from './UnitSelect.vue'

/**
 * The editable value of one setting group. Shared by the common settings form
 * and the member/batch override editor so the two entry points always offer the
 * same fields — only what they write differs (the common value vs an override).
 */
const props = defineProps<{
  unit: OverrideUnit
  /** Human name of the group, used in accessible names. */
  label: string
  value: unknown
  disabled?: boolean
  /** Distinguishes the two entry points in testids (common | override). */
  scope: string
}>()

const emit = defineEmits<{ change: [value: unknown] }>()

const MODES = [
  { value: 'available_sources', label: '可用源（available_sources）' },
  { value: 'strict', label: '严格（strict）' },
]

const LOSSLESS_CODECS = [
  { value: '', label: '不生成' },
  { value: 'wav', label: 'WAV' },
  { value: 'flac', label: 'FLAC' },
]
const ENCODED_CODECS = [
  { value: '', label: '不生成' },
  { value: 'mp3', label: 'MP3' },
  { value: 'aac', label: 'AAC' },
]

const profile = computed<DesiredProfile>(() => cloneProfile(props.value))
const tags = computed(() => ((props.value as string[] | undefined) ?? []).join(', '))

function setMode(mode: string) {
  emit('change', mode)
}

function setTags(raw: string) {
  emit(
    'change',
    raw
      .split(',')
      .map((tag) => tag.trim())
      .filter((tag) => tag.length > 0),
  )
}

function setCodec(lane: 'lossless' | 'encoded', codec: string) {
  const next = cloneProfile(props.value)
  if (codec === '') delete next[lane]
  else next[lane] = { ...(next[lane] ?? { codec: '' }), codec }
  emit('change', next)
}

function setBitrate(bitrate: number) {
  const next = cloneProfile(props.value)
  if (!next.encoded) return
  next.encoded = { ...next.encoded, quality: { kind: 'bitrate', bitrate } }
  emit('change', next)
}

const losslessCodec = computed(() => profile.value.lossless?.codec ?? '')
const encodedCodec = computed(() => profile.value.encoded?.codec ?? '')
const bitrate = computed(() => profile.value.encoded?.quality?.bitrate ?? 320)
</script>

<template>
  <div class="space-y-2">
    <UnitSelect
      v-if="unit === 'mode'"
      class="h-8 rounded-md border border-[var(--control-border)] bg-background px-2 text-xs"
      :value="(value as string) ?? 'strict'"
      :disabled="disabled"
      :aria-label="`${label} 转换模式`"
      :data-testid="`${scope}-${unit}`"
      :options="MODES"
      @change="setMode($event)"
    />

    <input
      v-else-if="unit === 'classifier_tags'"
      class="h-8 w-full rounded-md border border-[var(--control-border)] bg-background px-2 font-mono text-xs"
      :value="tags"
      :disabled="disabled"
      :aria-label="`${label} 分类标签`"
      :data-testid="`${scope}-${unit}`"
      placeholder="用逗号分隔；留空表示没有标签"
      @change="setTags(($event.target as HTMLInputElement).value)"
    />

    <div v-else class="flex flex-wrap items-center gap-3">
      <label class="flex items-center gap-1 text-[11px]">
        无损
        <UnitSelect
          class="h-7 rounded-md border border-[var(--control-border)] bg-background px-1.5 text-xs"
          :value="losslessCodec"
          :disabled="disabled"
          :aria-label="`${label} 无损目标`"
          :data-testid="`${scope}-${unit}-lossless`"
          :options="LOSSLESS_CODECS"
          @change="setCodec('lossless', $event)"
        />
      </label>
      <label class="flex items-center gap-1 text-[11px]">
        编码
        <UnitSelect
          class="h-7 rounded-md border border-[var(--control-border)] bg-background px-1.5 text-xs"
          :value="encodedCodec"
          :disabled="disabled"
          :aria-label="`${label} 编码目标`"
          :data-testid="`${scope}-${unit}-encoded`"
          :options="ENCODED_CODECS"
          @change="setCodec('encoded', $event)"
        />
      </label>
      <label class="flex items-center gap-1 text-[11px]">
        码率
        <input
          type="number"
          min="1"
          class="h-7 w-20 rounded-md border border-[var(--control-border)] bg-background px-1.5 font-mono text-xs"
          :value="bitrate"
          :disabled="disabled || encodedCodec === ''"
          :aria-label="`${label} 码率`"
          :data-testid="`${scope}-${unit}-bitrate`"
          @change="setBitrate(Number(($event.target as HTMLInputElement).value))"
        />
        kbps
      </label>
    </div>
  </div>
</template>
