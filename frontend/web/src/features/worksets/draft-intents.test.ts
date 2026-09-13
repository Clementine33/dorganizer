import { describe, expect, it } from 'vitest'
import type { OperationDraftDocument } from '@/lib/api/types'
import {
  applyIntent,
  EMPTY_INTENT,
  intentIsEmpty,
  intentUnitCount,
  readParticipation,
  readUnit,
  type EditIntent,
} from './draft-intents'

const WAV = { lossless: { codec: 'wav' } }
const FLAC = { lossless: { codec: 'flac' } }

const base: OperationDraftDocument = {
  schema_version: 1,
  mode: 'available_sources',
  classifier_tags: ['A'],
  matched: WAV,
  unmatched: { encoded: { codec: 'mp3', quality: { kind: 'bitrate', bitrate: 320 } } },
  members: [
    { member_id: 'm-yi', overrides: { matched: FLAC } },
    { member_id: 'm-bing', overrides: { classifier_tags: ['A'] } },
  ],
}

function intent(units: EditIntent['units'], participation: EditIntent['participation'] = 'keep'): EditIntent {
  return { units, participation }
}

describe('readUnit', () => {
  it('reports common provenance for a member without an override', () => {
    expect(readUnit(base, { kind: 'member', memberId: 'm-jia' }, 'matched')).toMatchObject({
      source: 'common',
      value: WAV,
    })
  })

  it('reports member provenance for an explicit override', () => {
    expect(readUnit(base, { kind: 'member', memberId: 'm-yi' }, 'matched')).toMatchObject({
      source: 'member',
      value: FLAC,
    })
  })

  it('keeps equal values with different provenance apart', () => {
    // 丙 explicitly repeats the common tag list: same value, member source.
    expect(readUnit(base, { kind: 'member', memberId: 'm-bing' }, 'classifier_tags')).toMatchObject({
      source: 'member',
      value: ['A'],
    })
    expect(readUnit(base, { kind: 'member', memberId: 'm-jia' }, 'classifier_tags')).toMatchObject({
      source: 'common',
      value: ['A'],
    })
  })

  it('reports mixed without a value when a batch holds several effective values', () => {
    // 乙 overrides matched=FLAC while 甲 inherits WAV.
    const mixed = readUnit(base, { kind: 'batch', memberIds: ['m-yi', 'm-jia'] }, 'matched')
    expect(mixed.source).toBe('mixed')
    expect(mixed.value).toBeUndefined()
    expect(mixed.overridden).toBe(1)
    expect(mixed.members).toBe(2)
  })

  it('reports the shared value but mixed provenance when only some members override', () => {
    const batch = readUnit(base, { kind: 'batch', memberIds: ['m-bing', 'm-jia'] }, 'classifier_tags')
    expect(batch.source).toBe('mixed')
    expect(batch.value).toEqual(['A'])
    expect(batch.overridden).toBe(1)
  })

  it('reports common for a batch where none overrides', () => {
    expect(readUnit(base, { kind: 'batch', memberIds: ['m-jia', 'm-bing'] }, 'mode')).toMatchObject({
      source: 'common',
      value: 'available_sources',
    })
  })
})

describe('applyIntent', () => {
  it('never materializes untouched units', () => {
    const next = applyIntent(base, { kind: 'member', memberId: 'm-jia' }, intent({ matched: { intent: 'set', value: FLAC } }))
    const record = next.members.find((m) => m.member_id === 'm-jia')
    expect(record?.overrides).toEqual({ matched: FLAC })
    expect(next.members.find((m) => m.member_id === 'm-yi')?.overrides).toEqual({ matched: FLAC })
    expect(next.classifier_tags).toEqual(['A'])
    expect(next.mode).toBe('available_sources')
  })

  it('edits only the named unit of a batch and leaves the rest alone', () => {
    const next = applyIntent(
      base,
      { kind: 'batch', memberIds: ['m-yi', 'm-jia'] },
      intent({ unmatched: { intent: 'set', value: WAV } }),
    )
    // 乙's matched=FLAC override survives the batch edit.
    expect(next.members.find((m) => m.member_id === 'm-yi')?.overrides).toEqual({ matched: FLAC, unmatched: WAV })
    expect(next.members.find((m) => m.member_id === 'm-jia')?.overrides).toEqual({ unmatched: WAV })
    // Untouched units stay inherited for both.
    expect(next.members.find((m) => m.member_id === 'm-jia')?.overrides?.classifier_tags).toBeUndefined()
  })

  it('stores an explicit empty tag list instead of inheriting', () => {
    const next = applyIntent(base, { kind: 'member', memberId: 'm-jia' }, intent({ classifier_tags: { intent: 'set', value: [] } }))
    expect(next.members.find((m) => m.member_id === 'm-jia')?.overrides?.classifier_tags).toEqual([])
  })

  it('restores inheritance by deleting the override', () => {
    const next = applyIntent(base, { kind: 'member', memberId: 'm-bing' }, intent({ classifier_tags: { intent: 'inherit' } }))
    expect(next.members).toHaveLength(1)
    expect(next.members[0].member_id).toBe('m-yi')
  })

  it('keeps overrides when a member is excluded', () => {
    const next = applyIntent(base, { kind: 'member', memberId: 'm-yi' }, intent({}, 'exclude'))
    expect(next.members.find((m) => m.member_id === 'm-yi')).toMatchObject({
      excluded: true,
      overrides: { matched: FLAC },
    })
    expect(readParticipation(next, { kind: 'member', memberId: 'm-yi' })).toBe('exclude')
  })

  it('writes common settings without touching member records', () => {
    const next = applyIntent(base, { kind: 'common' }, intent({ classifier_tags: { intent: 'set', value: ['B'] } }))
    expect(next.classifier_tags).toEqual(['B'])
    expect(next.members.find((m) => m.member_id === 'm-bing')?.overrides?.classifier_tags).toEqual(['A'])
  })

  it('does not mutate the base document', () => {
    const copy = structuredClone(base)
    applyIntent(base, { kind: 'member', memberId: 'm-jia' }, intent({ matched: { intent: 'set', value: FLAC } }))
    expect(base).toEqual(copy)
  })

  it('drops member records that state nothing', () => {
    const withEmpty: OperationDraftDocument = {
      ...base,
      members: [...base.members, { member_id: 'm-jia' }],
    }
    const next = applyIntent(withEmpty, { kind: 'common' }, EMPTY_INTENT)
    expect(next.members.map((m) => m.member_id)).toEqual(['m-yi', 'm-bing'])
  })
})

describe('intent accounting', () => {
  it('reports an empty intent so the apply button can be disabled', () => {
    expect(intentIsEmpty(EMPTY_INTENT)).toBe(true)
    expect(intentIsEmpty(intent({ matched: { intent: 'keep' } }))).toBe(true)
    expect(intentIsEmpty(intent({ matched: { intent: 'inherit' } }))).toBe(false)
    expect(intentIsEmpty(intent({}, 'exclude'))).toBe(false)
  })

  it('counts the edited units for the submit label', () => {
    expect(intentUnitCount(intent({ matched: { intent: 'set', value: FLAC }, mode: { intent: 'inherit' } }))).toBe(2)
    expect(intentUnitCount(EMPTY_INTENT)).toBe(0)
  })
})
