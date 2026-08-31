<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { AlertTriangle, Download, Plus, RotateCcw, Upload, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import type {
  DraftResponse,
  ResolvedPolicy,
  PolicySlot,
  WorkflowInput,
  AudioOutputSpec,
} from '@/lib/api/types'

// Workflow composer: the three global policy slots act as reusable templates.
// "应用" copies a slot policy into the current draft as an inline snapshot;
// "存入槽位" overwrites a slot from the current form. All workset drafts are
// inline policies — slot edits never alter saved drafts or revisions.
// Classifier is a literal tag set (one tag per row); matching is
// case-insensitive substring against the root-relative path.
// Schema v1 supports a single reconcile_audio_outputs step — the multi-step
// structure is reserved UI with the add-step entry visibly disabled.

const props = defineProps<{
  draft: DraftResponse | null
  slots: PolicySlot[]
  saving: boolean
  generating: boolean
  /** Slot save mutation state. */
  slotSaving: boolean
  slotError: string | null
  conflict: boolean
  conflictMessage: string | null
  dirty: boolean
  /** Orphaned worksets / active generations must not accept edits. */
  readOnly: boolean
}>()

const emit = defineEmits<{
  save: [{ workflow: WorkflowInput }]
  'save-and-generate': [{ workflow: WorkflowInput }]
  'save-slot': [{ slot: number; name: string; policy: ResolvedPolicy }]
  'load-server-version': []
  discard: []
  'update:dirty': [dirty: boolean]
}>()

// Local editable policy model. null until the draft loads.
const localPolicy = ref<ResolvedPolicy | null>(null)

// Slot save form state: which slot receives the current form, with its name.
const slotTarget = ref<number | null>(null)
const slotName = ref('')

// Seed the local model from the loaded inline draft. The draft prop changes
// identity on every background refetch (generation terminals, save cache
// sync); re-seeding on those would clobber a just-made edit (dirty is still
// false at that instant). Seed once per draft *version*: the only time the
// form must adopt server state is an actual server-side draft replacement.
const seededVersion = ref<number | undefined>(undefined)

watch(
  [() => props.draft?.workset_id, () => props.draft?.version],
  ([, version]) => {
    const draft = props.draft
    if (!draft) return
    if (localPolicy.value !== null && version === seededVersion.value) return
    // While dirty, a re-seed would clobber the user's unsaved edits with the
    // server draft; leave the local model alone until they settle.
    if (props.dirty && localPolicy.value !== null) return
    localPolicy.value = seedFromDraft(draft)
    seededVersion.value = version
  },
  { immediate: true },
)

function emptyPolicy(): ResolvedPolicy {
  return {
    schema_version: 1,
    classifier_tags: [],
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
    classifier_tags: Array.isArray(p.classifier_tags) ? [...p.classifier_tags] : [],
    matched: p.matched ?? {},
    unmatched: p.unmatched ?? {},
  }
}

// Seed the local editable copy from the server draft. The draft object comes
// from the vue-query cache, whose objects are readonly reactive proxies —
// seeding without a deep clone aliases the proxy and silently drops every
// later form mutation.
function seedFromDraft(draft: DraftResponse): ResolvedPolicy {
  const step = draft.workflow.steps[0]
  if (!step) return emptyPolicy()
  if (step.policy.kind === 'inline') {
    return clonePolicy(normalizePolicy(step.policy.policy))
  }
  return emptyPolicy()
}

function markDirty() {
  emit('update:dirty', true)
}

// Snapshot-on-apply: the slot policy is deep-cloned into the draft; later slot
// edits cannot reach this draft.
function applySlot(slot: PolicySlot) {
  if (!slot.policy) return
  if (props.dirty && !window.confirm('应用槽位策略将覆盖当前未保存的修改，继续？')) return
  localPolicy.value = clonePolicy(slot.policy)
  emit('update:dirty', true)
}

function beginSaveSlot(index: number) {
  if (slotTarget.value === index) {
    slotTarget.value = null
    return
  }
  slotTarget.value = index
  slotName.value = props.slots[index]?.name ?? ''
}

function confirmSaveSlot() {
  if (
    slotTarget.value === null ||
    !localPolicy.value ||
    !localComplete.value ||
    !slotName.value.trim()
  ) {
    return
  }
  emit('save-slot', { slot: slotTarget.value + 1, name: slotName.value.trim(), policy: clonePolicy(localPolicy.value) })
  slotTarget.value = null
}

function updateSpec(profile: 'matched' | 'unmatched', side: 'lossless' | 'encoded', spec: AudioOutputSpec | null) {
  if (!localPolicy.value) return
  if (spec === null) {
    delete localPolicy.value[profile][side]
  } else {
    localPolicy.value[profile][side] = spec
  }
  markDirty()
}

// Tag editor helpers. Tags are literal text — no regex validation anywhere;
// the backend normalizes (trims/dedupes) and is the sole authority.
const tagInput = ref('')

function addTag() {
  if (!localPolicy.value) return
  const tag = tagInput.value.trim()
  if (!tag) return
  tagInput.value = ''
  if (!localPolicy.value.classifier_tags) localPolicy.value.classifier_tags = []
  // Local duplicate guard (case-insensitive) keeps the chips clean; the
  // backend dedupes authoritatively on save.
  if (localPolicy.value.classifier_tags.some((t) => t.toLowerCase() === tag.toLowerCase())) return
  localPolicy.value.classifier_tags.push(tag)
  markDirty()
}

function removeTag(index: number) {
  if (!localPolicy.value) return
  localPolicy.value.classifier_tags?.splice(index, 1)
  markDirty()
}

function onTagKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter') {
    event.preventDefault()
    addTag()
  }
}

// Locally obvious incompleteness: at least one tag and one output per profile.
// The backend remains the authority for full validation.
const localComplete = computed(() => {
  const p = localPolicy.value
  if (!p) return false
  if (!p.classifier_tags || p.classifier_tags.length === 0) return false
  const hasOutput = (profile: 'matched' | 'unmatched') =>
    Boolean(p[profile]?.lossless || p[profile]?.encoded)
  return hasOutput('matched') && hasOutput('unmatched')
})

// Build the wire workflow from the local model — always an inline snapshot.
function buildWorkflow(): WorkflowInput | null {
  if (!localPolicy.value) return null
  return {
    schema_version: 1,
    steps: [{ step_type: 'reconcile_audio_outputs', policy: { kind: 'inline', policy: localPolicy.value } }],
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
    // Encoded outputs require a positive bitrate; default to the common norm.
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

    <!-- Content classifier: literal tags -->
    <div v-if="localPolicy" class="mt-4 space-y-3" data-testid="policy-form">
      <div class="rounded-md border border-border bg-card/60 p-3" data-testid="tag-editor">
        <p class="text-xs font-semibold">内容分类标签</p>
        <p class="mt-0.5 text-[10px] leading-4 text-muted-foreground">
          命中任一标签（不区分大小写，匹配专辑内相对路径的子串）的文件归入「无音效」分区，其余归入「有音效」。标签按普通文本处理，无需正则语法。
        </p>
        <div v-if="localPolicy.classifier_tags?.length" class="mt-2 flex flex-wrap gap-1.5" data-testid="tag-chips">
          <span
            v-for="(tag, index) in localPolicy.classifier_tags"
            :key="`${tag}-${index}`"
            class="flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 font-mono text-[10px]"
          >
            {{ tag }}
            <button
              type="button"
              class="text-muted-foreground hover:text-destructive"
              :aria-label="`删除标签 ${tag}`"
              :data-testid="`remove-tag-${index}`"
              :disabled="readOnly"
              @click="removeTag(index)"
            >
              <X class="size-3" />
            </button>
          </span>
        </div>
        <div class="mt-2 flex items-center gap-2">
          <input
            v-model="tagInput"
            type="text"
            placeholder="输入标签后回车添加，例如 SEなし"
            class="h-7 min-w-0 flex-1 rounded border border-input bg-background px-2 font-mono text-[11px]"
            data-testid="tag-input"
            :disabled="readOnly"
            @keydown="onTagKeydown"
          />
          <Button variant="outline" size="sm" data-testid="add-tag" :disabled="readOnly || !tagInput.trim()" @click="addTag">
            添加
          </Button>
        </div>
      </div>

      <!-- Output policy form -->
      <div
        v-for="profile in (['matched', 'unmatched'] as const)"
        :key="profile"
        class="rounded-md border border-border bg-card/60 p-3"
      >
        <p class="text-xs font-semibold">
          {{ profile === 'matched' ? '无音效（matched）输出' : '有音效（unmatched）输出' }}
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
                :disabled="readOnly"
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
                  :disabled="readOnly"
                  @change="onBitrateChange(profile, side, $event)"
                />
                <span class="text-[10px] text-muted-foreground">kbps</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Global policy slots -->
    <div class="mt-4">
      <p class="text-xs font-medium">全局策略槽位</p>
      <p class="mt-0.5 text-[10px] text-muted-foreground">
        槽位是可复用的模板：应用会把槽位当前策略复制为本工作集配置的独立快照，之后修改槽位不影响已保存的工作集或历史版本。
      </p>
      <div class="mt-1.5 space-y-1.5" data-testid="policy-slots">
        <div
          v-for="(slot, index) in slots"
          :key="slot.slot"
          class="rounded-md border px-3 py-2"
          :class="slotTarget === index ? 'border-[var(--ring)]/60 bg-[var(--ring)]/5' : 'border-border bg-card/60'"
          :data-testid="`policy-slot-${slot.slot}`"
        >
          <div class="flex min-w-0 items-center gap-2">
            <span class="grid size-5 shrink-0 place-items-center rounded bg-foreground/10 font-mono text-[9px] font-semibold">{{ slot.slot }}</span>
            <span class="min-w-0 flex-1 truncate text-[11px] font-semibold">
              {{ slot.name || `槽位 ${slot.slot}（空）` }}
            </span>
            <span v-if="!slot.policy" class="shrink-0 text-[10px] text-muted-foreground">未配置</span>
            <Button
              v-if="slot.policy"
              variant="outline"
              size="sm"
              class="h-6 px-2 text-[10px]"
              :data-testid="`apply-slot-${slot.slot}`"
              :disabled="readOnly"
              @click="applySlot(slot)"
            >
              <Download class="size-3" />
              应用
            </Button>
            <Button
              variant="outline"
              size="sm"
              class="h-6 px-2 text-[10px]"
              :data-testid="`save-to-slot-${slot.slot}`"
              :disabled="readOnly || !localComplete"
              @click="beginSaveSlot(index)"
            >
              <Upload class="size-3" />
              存入槽位
            </Button>
          </div>
          <div v-if="slotTarget === index" class="mt-2 flex items-center gap-2" :data-testid="`slot-save-form-${slot.slot}`">
            <input
              v-model="slotName"
              type="text"
              placeholder="槽位名称"
              class="h-7 min-w-0 flex-1 rounded border border-input bg-background px-2 text-[11px]"
              :data-testid="`slot-name-${slot.slot}`"
            />
            <Button size="sm" class="h-7 px-2 text-[10px]" data-testid="confirm-save-slot" :disabled="!slotName.trim()" @click="confirmSaveSlot">
              保存
            </Button>
            <Button variant="ghost" size="sm" class="h-7 px-2 text-[10px]" @click="slotTarget = null">取消</Button>
          </div>
        </div>
      </div>
      <p v-if="slotError" class="mt-1.5 text-[10px] text-destructive" data-testid="slot-error">{{ slotError }}</p>
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
      <Button data-testid="save-draft" variant="outline" size="sm" :disabled="saving || generating || readOnly" @click="onSave">
        保存配置
      </Button>
      <Button
        data-testid="save-and-generate"
        size="sm"
        class="bg-[var(--brand)] text-white hover:bg-[var(--brand)]"
        :disabled="saving || generating || readOnly"
        @click="onSaveAndGenerate"
      >
        {{ generating ? '生成中…' : '保存并生成新计划版本' }}
      </Button>
    </div>
  </div>
</template>
