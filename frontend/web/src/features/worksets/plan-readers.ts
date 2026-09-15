import type {
  ComponentOutcome,
  DesiredProfile,
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

/** Whether one partition's targets are met by the planned inventory. */
export type PartitionStatus = 'satisfied' | 'unmet' | 'blocked' | 'absent'

export interface MemberParts {
  matched: PartitionStatus
  unmatched: PartitionStatus
}

/**
 * Per-partition status of one member's components. A partition with no
 * components has nothing asked of it, so it counts as satisfied rather than as
 * a failure — the relaxed conversion mode keeps unsatisfied stems on purpose
 * and still converts them.
 */
export function partitionStatus(components: ComponentOutcome[]): MemberParts {
  const status: MemberParts = { matched: 'absent', unmatched: 'absent' }
  for (const component of components) {
    const key: keyof MemberParts = component.partition === 'matched' ? 'matched' : 'unmatched'
    if (component.status === 'blocked') {
      status[key] = 'blocked'
      continue
    }
    if (componentHasUnmetTarget(component)) {
      if (status[key] !== 'blocked') status[key] = 'unmet'
    } else if (status[key] === 'absent') {
      status[key] = 'satisfied'
    }
  }
  return status
}

export interface MemberConclusion {
  tone: 'neutral' | 'success' | 'warning' | 'danger'
  /**
   * The one label the row shows. A partial result names the satisfied side
   * (无音效满足 / 有音效满足) and carries the warning tone, so the row never
   * says "partial" twice.
   */
  label: string
  /** Which partitions are short of their target, for the detail view. */
  detail: string
}

/**
 * One member's conclusion. The label reports how far the targets are met, so
 * an unmet target under the relaxed mode reads as a partial result instead of
 * an error that contradicts the plan being convertible.
 */
export function memberConclusion(input: {
  excluded: boolean
  hasRoot: boolean
  rootMissing: boolean
  hasOperations: boolean
  parts: MemberParts
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
  if (input.parts.matched === 'blocked' || input.parts.unmatched === 'blocked') {
    return { tone: 'danger', label: '阻塞', detail: '存在需要先处理的冲突或歧义。' }
  }
  const matchedUnmet = input.parts.matched === 'unmet'
  const unmatchedUnmet = input.parts.unmatched === 'unmet'
  if (matchedUnmet && unmatchedUnmet) {
    return { tone: 'warning', label: '目标未满足', detail: '无音效与有音效目标均未满足，现有文件按可用源保留。' }
  }
  if (matchedUnmet || unmatchedUnmet) {
    const satisfied = satisfiedLabel(input.parts)
    const unmetSide = matchedUnmet ? '无音效目标未满足' : '有音效目标未满足'
    return { tone: 'warning', label: satisfied, detail: `${satisfied}；${unmetSide}，按可用源保留。` }
  }
  if (!input.hasOperations) {
    return { tone: 'neutral', label: '无变化', detail: '目标已满足，无需改动。' }
  }
  return { tone: 'success', label: '全部满足', detail: '两个分类的目标都已满足。' }
}

/** Short label of the satisfied side, used where space is tight. */
export function satisfiedLabel(parts: MemberParts): string {
  const matchedOk = parts.matched !== 'unmet' && parts.matched !== 'blocked'
  const unmatchedOk = parts.unmatched !== 'unmet' && parts.unmatched !== 'blocked'
  if (matchedOk && !unmatchedOk) return '无音效满足'
  if (!matchedOk && unmatchedOk) return '有音效满足'
  return ''
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
