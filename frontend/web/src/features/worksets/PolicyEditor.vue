<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { AlertTriangle, ChevronDown, ChevronRight, Plus, RotateCcw, Sparkles } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import type {
  DraftResponse,
  ResolvedPolicy,
  WorkflowPreset,
  WorkflowInput,
  PolicySourceWire,
  AudioOutputSpec,
} from '@/lib/api/types'

// Workflow composer: preset templates convert to editable inline policies;
// output controls are constrained to backend-valid codec/quality shapes;
// the classifier lives in an advanced section with free-form name/version.
// Schema v1 supports a single reconcile_audio_outputs step — the multi-step
// structure is reserved UI with the add-step entry visibly disabled.

const props = defineProps<{
  draft: DraftResponse | null
  presets: WorkflowPreset[]
  saving: boolean
  generating: boolean
  conflict: boolean
  conflictMessage: string | null
  dirty: boolean
}>()

const emit = defineEmits<{
  save: [{ workflow: WorkflowInput }]
  'save-and-generate': [{ workflow: WorkflowInput }]
  'load-server-version': []
  discard: []
  'update:dirty': [dirty: boolean]
}>()

// Local editable policy model. null until the draft loads.
const localPolicy = ref<ResolvedPolicy | null>(null)
const activePreset = ref<{ name: string; version: number } | null>(null)
const advancedOpen = ref(false)

// Seed the local model from the loaded draft (preset or inline).
watch(
  () => props.draft,
  (draft) => {
    if (!draft) return
    const step = draft.workflow.steps[0]
    if (!step) return
    if (step.policy.kind === 'preset') {
      const source = step.policy as { kind: 'preset'; name: string; version: number }
      activePreset.value = { name: source.name, version: source.version }
      const preset = props.presets.find((p) => p.name === source.name && p.version === source.version)
      // Resolve the preset's policy through the presets API so the form can
      // show it; until presets load, show an empty form.
      localPolicy.value = preset ? clonePolicy(preset.policy) : emptyPolicy()
    } else {
      activePreset.value = null
      localPolicy.value = normalizePolicy((step.policy as { kind: 'inline'; policy: ResolvedPolicy }).policy)
    }
  },
  { immediate: true },
)

function emptyPolicy(): ResolvedPolicy {
  return {
    schema_version: 1,
    classifier: { name: 'effect-direction', version: 1 },
    matched: {},
    unmatched: {},
  }
}

function clonePolicy(p: ResolvedPolicy): ResolvedPolicy {
  return JSON.parse(JSON.stringify(p)) as ResolvedPolicy
}

function normalizePolicy(p: ResolvedPolicy | undefined): ResolvedPolicy {
  const base = emptyPolicy()
  if (!p) return base
  return {
    schema_version: p.schema_version || 1,
    classifier: p.classifier ?? base.classifier,
    matched: p.matched ?? {},
    unmatched: p.unmatched ?? {},
  }
}

function markDirty() {
  emit('update:dirty', true)
}

function applyPreset(name: string, version: number) {
  if (props.dirty && !window.confirm('切换模板将覆盖当前未保存的自定义配置，继续？')) return
  const preset = props.presets.find((p) => p.name === name && p.version === version)
  if (!preset) return
  localPolicy.value = clonePolicy(preset.policy)
  activePreset.value = { name, version }
  emit('update:dirty', false)
}

function updateSpec(profile: 'matched' | 'unmatched', side: 'lossless' | 'encoded', spec: AudioOutputSpec | null) {
  if (!localPolicy.value) return
  if (spec === null) {
    delete localPolicy.value[profile][side]
  } else {
    localPolicy.value[profile][side] = spec
  }
  // Any direct edit leaves preset-template mode.
  activePreset.value = null
  markDirty()
}

const isCustom = computed(() => activePreset.value === null)

// Build the wire workflow from the local model.
function buildWorkflow(): WorkflowInput | null {
  if (!localPolicy.value) return null
  const policy: PolicySourceWire = isCustom.value
    ? { kind: 'inline', policy: localPolicy.value }
    : { kind: 'preset', name: activePreset.value!.name, version: activePreset.value!.version }
  return {
    schema_version: 1,
    steps: [{ step_type: 'reconcile_audio_outputs', policy }],
  }
}

function onSave() {
  const wf = buildWorkflow()
  if (wf) emit('save', { workflow: wf })
}

function onSaveAndGenerate() {
  const wf = buildWorkflow()
  if (wf) emit('save-and-generate', { workflow: wf })
}

const codecOptions = {
  lossless: [
    { value: '', label: '关闭' },
    { value: 'wav', label: 'WAV' },
    { value: 'flac', label: 'FLAC' },
  ],
  encoded: [
    { value: '', label: '关闭' },
    { value: 'mp3', label: 'MP3' },
    { value: 'aac', label: 'AAC/M4A' },
  ],
}

function specFor(profile: 'matched' | 'unmatched', side: 'lossless' | 'encoded'): AudioOutputSpec | null {
  return localPolicy.value?.[profile]?.[side] ?? null
}

function codecOf(profile: 'matched' | 'unmatched', side: 'lossless' | 'encoded'): string {
  return specFor(profile, side)?.codec ?? ''
}

function onCodecChange(profile: 'matched' | 'unmatched', side: 'lossless' | 'encoded', event: Event) {
  const value = (event.target as HTMLSelectElement).value
  if (value === '') {
    updateSpec(profile, side, null)
    return
  }
  const existing = specFor(profile, side)
  const next: AudioOutputSpec = { codec: value }
  if (side === 'encoded') {
    // Encoded outputs require a positive bitrate; default to the preset norm.
    next.quality = { kind: 'bitrate', bitrate: existing?.quality?.bitrate ?? 320 }
  }
  updateSpec(profile, side, next)
}

function onBitrateChange(profile: 'matched' | 'unmatched', side: 'lossless' | 'encoded', event: Event) {
  const raw = (event.target as HTMLInputElement).value
  const bitrate = Math.max(1, Math.floor(Number(raw) || 0))
  const existing = specFor(profile, side)
  if (!existing) return
  updateSpec(profile, side, { ...existing, quality: { kind: 'bitrate', bitrate } })
}
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col overflow-y-auto p-5" data-testid="policy-editor">
    <!-- Step structure (reserved multi-step UI; v1 supports one step) -->
    <div class="flex items-center gap-2">
      <span class="grid size-6 shrink-0 place-items-center rounded border border-border font-mono text-[10px]">1</span>
      <div class="min-w-0 flex-1 rounded-md border border-border bg-card px-3 py-2">
        <p class="font-heading text-xs font-semibold">reconcile_audio_outputs</p>
        <p class="text-[10px] text-muted-foreground">对每个专辑批次执行音频输出对账（wav/mp3/flac/aac）</p>
      </div>
      <Button variant="outline" size="sm" disabled aria-label="添加步骤（当前 schema v1 仅支持单个步骤）">
        <Plus class="size-3.5" />
        添加步骤
      </Button>
    </div>
    <p class="mt-1.5 pl-8 text-[10px] text-muted-foreground">
      当前 Workflow schema v1 仅支持上述单个步骤；后续版本将开放更多步骤类型与编排。
    </p>

    <!-- Preset templates -->
    <div class="mt-4">
      <p class="text-xs font-medium">策略模板</p>
      <div class="mt-1.5 flex flex-wrap gap-1.5">
        <button
          v-for="preset in presets"
          :key="`${preset.name}@${preset.version}`"
          type="button"
          class="flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-left text-[11px]"
          :class="
            activePreset?.name === preset.name && activePreset?.version === preset.version
              ? 'border-foreground/30 bg-foreground/10'
              : 'border-border hover:bg-accent'
          "
          :data-testid="`preset-${preset.name}`"
          @click="applyPreset(preset.name, preset.version)"
        >
          <Sparkles class="size-3 text-[var(--ring)]" />
          <span class="font-semibold">{{ preset.name }}@{{ preset.version }}</span>
        </button>
        <span
          v-if="isCustom"
          class="flex items-center gap-1.5 rounded-md border border-[var(--ring)]/40 bg-[var(--ring)]/10 px-2.5 py-1.5 text-[11px] font-semibold text-[var(--ring)]"
          data-testid="custom-policy-badge"
        >
          自定义策略
        </span>
      </div>
    </div>

    <!-- Output policy form -->
    <div v-if="localPolicy" class="mt-4 space-y-3" data-testid="policy-form">
      <div
        v-for="profile in (['matched', 'unmatched'] as const)"
        :key="profile"
        class="rounded-md border border-border bg-card/60 p-3"
      >
        <p class="text-xs font-semibold">
          {{ profile === 'matched' ? '有音效（matched）输出' : '无音效（unmatched）输出' }}
        </p>
        <div class="mt-2 grid grid-cols-2 gap-3">
          <div
            v-for="side in (['lossless', 'encoded'] as const)"
            :key="side"
            class="rounded border border-border bg-background/50 p-2.5"
          >
            <p class="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
              {{ side === 'lossless' ? '无损输出' : '有损输出' }}
            </p>
            <div class="mt-1.5 flex items-center gap-2">
              <select
                :value="codecOf(profile, side)"
                class="h-7 min-w-0 flex-1 rounded border border-input bg-background px-1.5 text-[11px]"
                :data-testid="`codec-${profile}-${side}`"
                :disabled="!isCustom"
                @change="onCodecChange(profile, side, $event)"
              >
                <option v-for="opt in codecOptions[side]" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
              </select>
              <div v-if="side === 'encoded' && specFor(profile, side)" class="flex items-center gap-1">
                <input
                  type="number"
                  min="1"
                  :value="specFor(profile, side)?.quality?.bitrate"
                  class="h-7 w-16 rounded border border-input bg-background px-1.5 text-[11px]"
                  :data-testid="`bitrate-${profile}-${side}`"
                  :disabled="!isCustom"
                  @change="onBitrateChange(profile, side, $event)"
                />
                <span class="text-[10px] text-muted-foreground">kbps</span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Classifier (advanced, free-form; backend validates support) -->
      <div class="rounded-md border border-border bg-card/60">
        <button
          type="button"
          class="flex w-full items-center gap-1.5 px-3 py-2 text-left"
          @click="advancedOpen = !advancedOpen"
        >
          <component :is="advancedOpen ? ChevronDown : ChevronRight" class="size-3.5" />
          <span class="text-xs font-semibold">高级 · 内容分类器</span>
        </button>
        <div v-if="advancedOpen" class="grid grid-cols-2 gap-3 px-3 pb-3">
          <label class="block">
            <span class="text-[10px] font-medium text-muted-foreground">分类器名称</span>
            <input
              v-model="localPolicy.classifier.name"
              type="text"
              class="mt-0.5 h-7 w-full rounded border border-input bg-background px-2 font-mono text-[11px]"
              data-testid="classifier-name"
              :disabled="!isCustom"
              @change="markDirty"
            />
          </label>
          <label class="block">
            <span class="text-[10px] font-medium text-muted-foreground">版本（正整数）</span>
            <input
              v-model.number="localPolicy.classifier.version"
              type="number"
              min="1"
              class="mt-0.5 h-7 w-full rounded border border-input bg-background px-2 font-mono text-[11px]"
              data-testid="classifier-version"
              :disabled="!isCustom"
              @change="markDirty"
            />
          </label>
        </div>
      </div>
    </div>

    <!-- Version conflict resolution -->
    <div
      v-if="conflict"
      class="mt-4 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2.5"
      data-testid="draft-conflict-banner"
    >
      <div class="flex items-start gap-2">
        <AlertTriangle class="mt-0.5 size-3.5 shrink-0 text-amber-600 dark:text-amber-400" />
        <div class="min-w-0 flex-1">
          <p class="text-xs font-semibold text-amber-600 dark:text-amber-400">配置已被其他操作更新</p>
          <p class="mt-0.5 text-[11px] leading-4 text-muted-foreground">
            {{ conflictMessage ?? '服务端已存在新版本。你的本地修改未丢失。' }}
          </p>
          <div class="mt-2 flex gap-2">
            <Button data-testid="load-server-draft" variant="outline" size="sm" @click="emit('load-server-version')">
              加载服务端版本（丢弃本地）
            </Button>
            <Button data-testid="discard-local-draft" variant="ghost" size="sm" @click="emit('discard')">
              <RotateCcw class="size-3" />
              放弃修改
            </Button>
          </div>
        </div>
      </div>
    </div>

    <!-- Actions -->
    <div class="sticky bottom-0 mt-4 flex items-center gap-2 border-t border-border bg-background pt-3">
      <p class="min-w-0 flex-1 truncate text-[10px] text-muted-foreground">
        {{ dirty ? '有未保存修改' : '配置已与服务器同步' }}
      </p>
      <Button data-testid="save-draft" variant="outline" size="sm" :disabled="saving || generating" @click="onSave">
        保存配置
      </Button>
      <Button
        data-testid="save-and-generate"
        size="sm"
        class="bg-[var(--brand)] text-white hover:bg-[var(--brand)]"
        :disabled="saving || generating"
        @click="onSaveAndGenerate"
      >
        {{ generating ? '生成中…' : '保存并生成新计划版本' }}
      </Button>
    </div>
  </div>
</template>
