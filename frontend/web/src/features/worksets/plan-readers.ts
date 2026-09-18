import type {
  AudioOutputSpec,
  ComponentOutcome,
  DesiredProfile,
  FileDecision,
  OverrideUnit,
  RevisionDetailResponse,
  VariantDecision,
  PlanOperation,
} from '@/lib/api/types'

/**
 * Readers for plan snapshots and desired profiles.
 *
 * Plan components are persisted review data: a snapshot written before the
 * backend's never-null guarantee can still carry `null` where the current
 * contract promises arrays, and opening such a revision must not crash the
 * workbench. Normalizing once on read keeps every downstream render simple.
 */
/**
 * Normalized components of one revision's plan snapshot (the conversion task
 * payload inside the task envelope).
 */
export function revisionComponents(revision: RevisionDetailResponse): ComponentOutcome[] {
  return (revision.task?.payload?.components ?? []).map(readComponent)
}

export function readComponent(component: ComponentOutcome): ComponentOutcome {
  return {
    ...component,
    lanes: component.lanes ?? [],
    variant_decisions: (component.variant_decisions ?? []).map(readVariant),
    operations: component.operations ?? [],
    projected_inventory: component.projected_inventory ?? [],
    files: component.files ?? [],
  }
}

export function readVariant(variant: VariantDecision): VariantDecision {
  return { ...variant, decisions: variant.decisions ?? [] }
}

export function operationsOf(component: ComponentOutcome): PlanOperation[] {
  return component.operations ?? []
}

/** Plan facts of one component, as independent observations. */
export function componentHasUnmetTarget(component: ComponentOutcome): boolean {
  return (component.variant_decisions ?? []).some((variant) =>
    (variant.decisions ?? []).some((decision) => decision.reason_code === 'UNMET_TARGET'),
  )
}

export function componentOperationCount(component: ComponentOutcome): number {
  return (component.operations ?? []).length
}

/**
 * The files the plan left untouched: KEEP decisions are conclusions, never
 * executable operations. They are what a run shows as 保留文件 — a file stays
 * because it already satisfies the declared target, or because nothing could
 * satisfy it (UNMET_TARGET keeps the stem instead of a silently satisfied one).
 */
export function keptDecisions(component: ComponentOutcome): FileDecision[] {
  return (component.variant_decisions ?? [])
    .flatMap((variant) => variant.decisions ?? [])
    .filter((decision) => decision.resolution === 'keep')
}

/** Why a kept file was kept, in the plan's own short words. */
const KEEP_REASON_TEXT: Record<string, string> = {
  UNMET_TARGET: '目标未满足',
  KEEP_LOSSLESS_TARGET: '已满足无损目标',
  KEEP_ENCODED_SATISFIED: '已满足编码目标',
  SOURCE_AMBIGUOUS: '源不唯一',
}

export function keepReasonText(reasonCode: string | undefined): string {
  if (!reasonCode) return '原样保留'
  return KEEP_REASON_TEXT[reasonCode] ?? reasonCode
}

/** One partition's independent facts within a member's planned components. */
export interface PartitionFacts {
  /** A component exists for this partition: there are files to review at all. */
  applicable: boolean
  blocked: boolean
  unmet: boolean
  /** The plan carries executable operations for this partition. */
  changes: boolean
}

export interface MemberFacts {
  matched: PartitionFacts
  unmatched: PartitionFacts
}

export const PARTITION_TEXT: Record<keyof MemberFacts, string> = {
  matched: '无音效',
  unmatched: '有音效',
}

/**
 * Per-partition facts of one member's components. The three axes stay
 * independent: a partition can change and still carry an unmet target (the
 * relaxed mode converts what it can and keeps what it cannot).
 */
export function partitionFacts(components: ComponentOutcome[]): MemberFacts {
  const facts: MemberFacts = {
    matched: { applicable: false, blocked: false, unmet: false, changes: false },
    unmatched: { applicable: false, blocked: false, unmet: false, changes: false },
  }
  for (const component of components) {
    const side = facts[component.partition === 'matched' ? 'matched' : 'unmatched']
    side.applicable = true
    if (component.status === 'blocked') side.blocked = true
    if (componentHasUnmetTarget(component)) side.unmet = true
    if (componentOperationCount(component) > 0) side.changes = true
  }
  return facts
}

export interface MemberConclusion {
  tone: 'neutral' | 'success' | 'warning' | 'danger'
  /**
   * The one label the row shows, phrased as what will happen to this folder:
   * 仅无音效转换 / 仅有音效转换 / 全量转换, or why nothing will change.
   */
  label: string
  /** Both partitions' own facts, so neither side's state hides behind a word. */
  detail: string
}

/**
 * One member's conclusion. The label names who changes; the tone carries the
 * risk, so a folder that converts while a target stays unmet says so in the
 * label and still reads as a warning.
 */
export function memberConclusion(input: {
  excluded: boolean
  hasRoot: boolean
  rootMissing: boolean
  facts: MemberFacts
}): MemberConclusion {
  if (input.excluded) {
    return { tone: 'neutral', label: '已排除', detail: '本操作已排除该文件夹。' }
  }
  if (!input.hasRoot) {
    return { tone: 'neutral', label: '未参与', detail: '该文件夹在此版本中没有规划输入。' }
  }
  if (input.rootMissing) {
    return { tone: 'danger', label: '输入缺失', detail: '规划时未在扫描结果中找到该文件夹。' }
  }
  const sides = Object.values(input.facts)
  if (sides.some((side) => side.blocked)) {
    return { tone: 'danger', label: '阻塞', detail: '存在需要先处理的冲突或歧义。' }
  }
  const detail = detailText(input.facts)
  const changing = changedSides(input.facts)
  if (changing.length === 2) return { tone: toneFor(input.facts, 'success'), label: '全量转换', detail }
  if (changing.length === 1) {
    return { tone: toneFor(input.facts, 'success'), label: `仅${changing[0]}转换`, detail }
  }
  if (sides.some((side) => side.unmet)) {
    return { tone: 'warning', label: '目标未满足', detail }
  }
  if (!sides.some((side) => side.applicable)) {
    return { tone: 'neutral', label: '无适用', detail }
  }
  return { tone: 'neutral', label: '无需转换', detail }
}

/** Warning wins over success: an unmet target is the row's risk to read. */
function toneFor(facts: MemberFacts, base: MemberConclusion['tone']): MemberConclusion['tone'] {
  return Object.values(facts).some((side) => side.unmet) ? 'warning' : base
}

function changedSides(facts: MemberFacts): string[] {
  return (Object.keys(facts) as (keyof MemberFacts)[])
    .filter((key) => facts[key].changes)
    .map((key) => PARTITION_TEXT[key])
}

/** One line per partition: what it does, or why it does nothing. */
function detailText(facts: MemberFacts): string {
  return (Object.keys(facts) as (keyof MemberFacts)[])
    .map((key) => `${PARTITION_TEXT[key]}：${sideText(facts[key])}`)
    .join('；')
}

function sideText(side: PartitionFacts): string {
  if (side.blocked) return '阻塞'
  if (side.unmet) return side.changes ? '目标未满足，其余按可用源转换' : '目标未满足，现有文件按可用源保留'
  if (side.changes) return '将转换'
  return side.applicable ? '无需改动' : '无适用文件'
}

/**
 * Deep-copies a desired profile into plain objects. Vue props are reactive
 * proxies, which structuredClone refuses; the profile is plain JSON data, so
 * an explicit copy is both safe and clearer about what is carried over.
 */
export function cloneProfile(value: unknown): DesiredProfile {
  const src = (value ?? {}) as DesiredProfile
  const out: DesiredProfile = {}
  if (src.lossless) {
    out.lossless = {
      codec: src.lossless.codec,
      ...(src.lossless.quality ? { quality: { ...src.lossless.quality } } : {}),
    }
  }
  if (src.encoded) {
    out.encoded = {
      codec: src.encoded.codec,
      ...(src.encoded.quality ? { quality: { ...src.encoded.quality } } : {}),
    }
  }
  return out
}

/** One lane of a profile: "WAV", "MP3 320", or nothing when the lane is absent. */
function laneText(spec: AudioOutputSpec | undefined): string | null {
  if (!spec?.codec) return null
  const codec = spec.codec.toUpperCase()
  const bitrate = spec.quality?.bitrate
  return bitrate ? `${codec} ${bitrate}` : codec
}

/** A desired profile in one line: the exact output set the revision asks for. */
export function profileText(profile: DesiredProfile | undefined): string {
  if (!profile) return '未设置'
  const parts = [laneText(profile.lossless), laneText(profile.encoded)].filter(
    (part): part is string => part !== null,
  )
  // A profile that declares nothing is a declaration, not a gap: the partition
  // keeps no managed audio.
  return parts.length > 0 ? parts.join(' + ') : '不需要'
}

const MODE_TEXT: Record<string, string> = { strict: '严格', available_sources: '可用源' }

/**
 * One unit's value as the editors show it — the same rendering for a stored
 * value, a common value and a pending one, so "独立值" never has to stand in
 * for the value itself.
 */
export function unitValueText(unit: OverrideUnit, value: unknown): string {
  switch (unit) {
    case 'mode': {
      const mode = typeof value === 'string' ? value : ''
      return MODE_TEXT[mode] ?? (mode || '未设置')
    }
    case 'classifier_tags': {
      const tags = (value as string[] | undefined) ?? []
      return tags.length > 0 ? tags.join(', ') : '（空）'
    }
    default:
      return profileText(value as DesiredProfile | undefined)
  }
}
