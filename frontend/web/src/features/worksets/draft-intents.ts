import type {
  DeleteMode,
  DraftMember,
  OperationDraftDocument,
  OverrideSet,
  OverrideUnit,
} from '@/lib/api/types'

/**
 * Editing intents over the sparse draft.
 *
 * Every edit names exactly one target and one intent per unit. The document is
 * rebuilt from the persisted base document plus those intents — the form never
 * becomes the saved document, so an untouched unit is never turned into a
 * member override and an explicit "inherit" really deletes the override.
 */

export type EditTarget =
  | { kind: 'common' }
  | { kind: 'batch'; memberIds: string[] }
  | { kind: 'member'; memberId: string }

/** keep = leave the stored state untouched; set = write an explicit value. */
export type UnitIntent<T> = { intent: 'keep' } | { intent: 'set'; value: T } | { intent: 'inherit' }

export type ParticipationIntent = 'keep' | 'participate' | 'exclude'

export interface EditIntent {
  units: Partial<Record<OverrideUnit, UnitIntent<unknown>>>
  /**
   * The obsolete-audio handling. It is a whole-operation choice, so it exists
   * only on the common target and is never a member override.
   */
  deleteMode?: UnitIntent<DeleteMode>
  participation: ParticipationIntent
}

export const EMPTY_INTENT: EditIntent = { units: {}, participation: 'keep' }

/** Where a unit's current value comes from for the edited target. */
export type UnitSource = 'common' | 'member' | 'mixed'

export interface UnitValue {
  /**
   * Defined when every member's EFFECTIVE value agrees — including the case
   * where some members reach that value through an explicit override. It is
   * undefined only when the target genuinely holds several values, which the
   * form shows as "多种值" and can never save as one business value.
   */
  value: unknown
  /** `common` = nobody overrides, `member` = everybody does, else `mixed`. */
  source: UnitSource
  /** How many members of the target explicitly override the unit. */
  overridden: number
  members: number
}

export const OVERRIDE_UNITS: OverrideUnit[] = ['mode', 'classifier_tags', 'matched', 'unmatched']

function commonValue(doc: OperationDraftDocument, unit: OverrideUnit): unknown {
  switch (unit) {
    case 'mode':
      return doc.mode ?? 'strict'
    case 'classifier_tags':
      return doc.classifier_tags ?? []
    case 'matched':
      return doc.matched
    case 'unmatched':
      return doc.unmatched
  }
}

function setCommon(doc: OperationDraftDocument, unit: OverrideUnit, value: unknown): void {
  switch (unit) {
    case 'mode':
      doc.mode = value as OperationDraftDocument['mode']
      return
    case 'classifier_tags':
      doc.classifier_tags = (value as string[]) ?? []
      return
    case 'matched':
      doc.matched = value as OperationDraftDocument['matched']
      return
    case 'unmatched':
      doc.unmatched = value as OperationDraftDocument['unmatched']
  }
}

function memberRecord(doc: OperationDraftDocument, memberId: string): DraftMember | undefined {
  return doc.members.find((m) => m.member_id === memberId)
}

function overrideValue(record: DraftMember | undefined, unit: OverrideUnit): { present: boolean; value: unknown } {
  const overrides = record?.overrides
  if (!overrides) return { present: false, value: undefined }
  switch (unit) {
    case 'mode':
      return overrides.mode === undefined
        ? { present: false, value: undefined }
        : { present: true, value: overrides.mode }
    case 'classifier_tags':
      return overrides.classifier_tags === undefined
        ? { present: false, value: undefined }
        : { present: true, value: overrides.classifier_tags }
    case 'matched':
      return overrides.matched === undefined
        ? { present: false, value: undefined }
        : { present: true, value: overrides.matched }
    case 'unmatched':
      return overrides.unmatched === undefined
        ? { present: false, value: undefined }
        : { present: true, value: overrides.unmatched }
  }
}

/** Structural equality of two unit values (a mode string, tags, a profile). */
export function sameUnitValue(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

/**
 * Whether two draft documents hold the same persisted content. Both sides come
 * from the server's canonical JSON (or from a save of it), so an exact string
 * compare is enough — and a content that merely was NOT changed by an unrelated
 * event (a generation publication advancing the version) compares equal.
 */
export function sameDocument(a: OperationDraftDocument, b: OperationDraftDocument): boolean {
  return sameUnitValue(a, b)
}

/**
 * Reads the current value and provenance of one unit for a target. A batch
 * whose members disagree reports `mixed`: a mixed value is a display state,
 * never something that can be saved.
 */
export function readUnit(doc: OperationDraftDocument, target: EditTarget, unit: OverrideUnit): UnitValue {
  if (target.kind === 'common') {
    return { value: commonValue(doc, unit), source: 'common', overridden: 0, members: 0 }
  }
  const memberIds = target.kind === 'member' ? [target.memberId] : target.memberIds
  const common = commonValue(doc, unit)
  let overridden = 0
  const effective: unknown[] = []
  for (const memberId of memberIds) {
    const { present, value } = overrideValue(memberRecord(doc, memberId), unit)
    if (present) {
      overridden++
      effective.push(value)
    } else {
      effective.push(common)
    }
  }
  const first = effective[0]
  const uniform = effective.every((v) => sameUnitValue(v, first))
  const source: UnitSource =
    overridden === 0 ? 'common' : overridden === memberIds.length && uniform ? 'member' : 'mixed'
  return {
    value: uniform ? first : undefined,
    source,
    overridden,
    members: memberIds.length,
  }
}

/** Reads participation of a member target: single member or batch summary. */
export function readParticipation(
  doc: OperationDraftDocument,
  target: EditTarget,
): 'participate' | 'exclude' | 'mixed' {
  if (target.kind === 'common') return 'participate'
  const memberIds = target.kind === 'member' ? [target.memberId] : target.memberIds
  let excluded = 0
  for (const memberId of memberIds) {
    if (memberRecord(doc, memberId)?.excluded) excluded++
  }
  if (excluded === 0) return 'participate'
  if (excluded === memberIds.length) return 'exclude'
  return 'mixed'
}

function setOverride(record: DraftMember, unit: OverrideUnit, value: unknown): void {
  const overrides: OverrideSet = record.overrides ?? {}
  switch (unit) {
    case 'mode':
      overrides.mode = value as OverrideSet['mode']
      break
    case 'classifier_tags':
      overrides.classifier_tags = value as string[]
      break
    case 'matched':
      overrides.matched = value as OverrideSet['matched']
      break
    case 'unmatched':
      overrides.unmatched = value as OverrideSet['unmatched']
      break
  }
  record.overrides = overrides
}

function clearOverride(record: DraftMember, unit: OverrideUnit): void {
  if (!record.overrides) return
  switch (unit) {
    case 'mode':
      delete record.overrides.mode
      break
    case 'classifier_tags':
      delete record.overrides.classifier_tags
      break
    case 'matched':
      delete record.overrides.matched
      break
    case 'unmatched':
      delete record.overrides.unmatched
      break
  }
  if (Object.keys(record.overrides).length === 0) delete record.overrides
}

function recordFor(doc: OperationDraftDocument, memberId: string): DraftMember {
  let record = memberRecord(doc, memberId)
  if (!record) {
    record = { member_id: memberId }
    doc.members.push(record)
  }
  return record
}

/** Drops records that state nothing: participation plus no override. */
function pruneMembers(doc: OperationDraftDocument): void {
  doc.members = doc.members.filter((m) => m.excluded === true || (m.overrides && Object.keys(m.overrides).length > 0))
}

/**
 * Builds the document to save from the persisted base plus the session's
 * intents. The base is never mutated; only units with a `set`/`inherit` intent
 * change, so this holds: editing one unit leaves every other value and every
 * other member's inheritance relationship byte-identical.
 */
export function applyIntent(
  base: OperationDraftDocument,
  target: EditTarget,
  intent: EditIntent,
): OperationDraftDocument {
  const doc: OperationDraftDocument = {
    ...base,
    classifier_tags: [...(base.classifier_tags ?? [])],
    members: base.members.map((m) => ({
      member_id: m.member_id,
      excluded: m.excluded,
      overrides: m.overrides ? { ...m.overrides } : undefined,
    })),
  }

  if (target.kind === 'common') {
    for (const unit of OVERRIDE_UNITS) {
      const unitIntent = intent.units[unit]
      // Common settings have no inheritance to restore: only `set` writes.
      if (unitIntent?.intent === 'set') setCommon(doc, unit, unitIntent.value)
    }
    if (intent.deleteMode?.intent === 'set') doc.delete_mode = intent.deleteMode.value
    pruneMembers(doc)
    return doc
  }

  const memberIds = target.kind === 'member' ? [target.memberId] : target.memberIds
  for (const memberId of memberIds) {
    if (intent.participation !== 'keep') {
      const record = recordFor(doc, memberId)
      if (intent.participation === 'exclude') record.excluded = true
      else delete record.excluded
    }
    for (const unit of OVERRIDE_UNITS) {
      const unitIntent = intent.units[unit]
      if (!unitIntent || unitIntent.intent === 'keep') continue
      if (unitIntent.intent === 'set') {
        setOverride(recordFor(doc, memberId), unit, unitIntent.value)
      } else {
        const record = memberRecord(doc, memberId)
        if (record) clearOverride(record, unit)
      }
    }
  }
  pruneMembers(doc)
  return doc
}

/** True when the intent would write anything at all. */
export function intentIsEmpty(intent: EditIntent): boolean {
  if (intent.participation !== 'keep') return false
  if (intent.deleteMode && intent.deleteMode.intent !== 'keep') return false
  return OVERRIDE_UNITS.every((unit) => {
    const unitIntent = intent.units[unit]
    return !unitIntent || unitIntent.intent === 'keep'
  })
}

/** How many units an intent sets or restores — the K in "N folders, K settings". */
export function intentUnitCount(intent: EditIntent): number {
  const deleteModeSet = intent.deleteMode && intent.deleteMode.intent !== 'keep' ? 1 : 0
  return (
    deleteModeSet +
    OVERRIDE_UNITS.filter((unit) => {
      const unitIntent = intent.units[unit]
      return unitIntent && unitIntent.intent !== 'keep'
    }).length
  )
}
