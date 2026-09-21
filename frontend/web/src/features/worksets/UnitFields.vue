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

// An absent lane is not "do not generate": the declared profile is the
// partition's final shape, so an undeclared output is removed where one exists.
const LOSSLESS_CODECS = [
  { value: '', label: '不需要' },
  { value: 'wav', label: 'WAV' },
  { value: 'flac', label: 'FLAC' },
]
const ENCODED_CODECS = [
  { value: '', label: '不需要' },
  { value: 'mp3', label: 'MP3' },
  { value: 'aac', label: 'AAC' },
  { value: 'opus', label: 'Opus' },
]

/**
 * The encoded lane's named shortcuts. A preset writes one codec and bitrate
 * pair — the same value the manual controls produce — so nothing new is stored
 * and any pair can still be composed by hand; a pair that matches a preset
 * simply lights it up.
 */
const PRESETS = [
  { value: 'opus-160', codec: 'opus', bitrate: 160, label: 'Opus 160' },
  { value: 'aac-256', codec: 'aac', bitrate: 256, label: 'AAC 256' },
  { value: 'mp3-320', codec: 'mp3', bitrate: 320, label: 'MP3 320' },
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
  if (codec === '') {
    delete next[lane]
  } else {
    const seeded = { ...(next[lane] ?? { codec: '' }), codec }
    // An encoded output is only valid with its bitrate; seeding the displayed
    // one keeps a fresh selection from being saved without quality.
    if (lane === 'encoded' && !seeded.quality) {
      seeded.quality = { kind: 'bitrate', bitrate: bitrate.value }
    }
    next[lane] = seeded
  }
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

/** Which preset the stored pair is, if any: the shortcut lights up, nothing more. */
const matchedPreset = computed(
  () =>
    PRESETS.find((preset) => preset.codec === encodedCodec.value && preset.bitrate === bitrate.value)?.value ?? null,
)

function applyPreset(preset: (typeof PRESETS)[number]) {
  const next = cloneProfile(props.value)
  next.encoded = { codec: preset.codec, quality: { kind: 'bitrate', bitrate: preset.bitrate } }
  emit('change', next)
}
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
      <!-- Shortcuts for the three pairs this app is run with; the manual codec
           and bitrate above stay the way to compose anything else. -->
      <span class="flex items-center gap-1" :data-testid="`${scope}-${unit}-presets`">
        <span class="text-[11px] text-[var(--text-muted)]">预设</span>
        <button
          v-for="preset in PRESETS"
          :key="preset.value"
          type="button"
          class="rounded-md border px-1.5 py-1 font-mono text-[11px] focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
          :class="
            matchedPreset === preset.value
              ? 'border-[var(--brand-border)] bg-[var(--brand-weak)] font-medium'
              : 'border-[var(--control-border)] hover:bg-muted'
          "
          :disabled="disabled"
          :aria-pressed="matchedPreset === preset.value"
          :title="`${preset.codec.toUpperCase()} ${preset.bitrate} kbps`"
          :data-testid="`${scope}-${unit}-preset-${preset.value}`"
          @click="applyPreset(preset)"
        >
          {{ preset.label }}
        </button>
      </span>
      <p v-if="encodedCodec === 'opus'" class="text-[11px] text-[var(--text-muted)]" data-testid="encoded-codec-hint">
        Opus 以 VBR 编码（码率为平均值，体积更小），采样率固定 48 kHz。
      </p>
    </div>
    <p
      v-if="(unit === 'matched' || unit === 'unmatched') && !profile.lossless && !profile.encoded"
      class="text-[11px] text-[var(--warning-ink)]"
      data-testid="empty-profile-warning"
    >
      两者都不需要：该分类下的音频将被移除，去向由「旧音频处理」决定。
    </p>
  </div>
</template>
