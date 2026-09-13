import { describe, expect, it } from 'vitest'
import type { ComponentOutcome } from '@/lib/api/types'
import {
  componentHasUnmetTarget,
  componentOperationCount,
  memberConclusion,
  operationsOf,
  readComponent,
} from './plan-readers'

/** A snapshot as an older backend wrote it: empty collections serialized as null. */
const legacyComponent = {
  component_id: 'cmp-legacy',
  partition: 'matched',
  status: 'ok',
  lanes: null,
  variant_decisions: null,
  operations: null,
  projected_inventory: null,
  files: null,
} as unknown as ComponentOutcome

describe('plan snapshot readers', () => {
  it('normalizes a legacy snapshot whose collections are null', () => {
    const component = readComponent(legacyComponent)
    expect(component.lanes).toEqual([])
    expect(component.variant_decisions).toEqual([])
    expect(component.operations).toEqual([])
    expect(component.projected_inventory).toEqual([])
    expect(component.files).toEqual([])
    expect(operationsOf(component)).toEqual([])
    expect(componentOperationCount(component)).toBe(0)
    expect(componentHasUnmetTarget(component)).toBe(false)
  })

  it('normalizes a nested variant with no decisions', () => {
    const component = readComponent({
      ...legacyComponent,
      variant_decisions: [{ stem: 'track' }],
    } as unknown as ComponentOutcome)
    expect(component.variant_decisions[0].decisions).toEqual([])
  })

  it('keeps the real content untouched', () => {
    const component = readComponent({
      component_id: 'cmp-1',
      partition: 'unmatched',
      status: 'ok',
      lanes: [{ lane: 'encoded', decision: 'REBUILD' }],
      variant_decisions: [
        {
          stem: 'track',
          decisions: [{ path: '/music/track.mp3', resolution: 'keep', reason_code: 'UNMET_TARGET' }],
        },
      ],
      operations: [{ kind: 'encode', phase: 'materialize_outputs', component_id: 'cmp-1', variant_stem: 'track', source_path: '/music/track.flac' }],
      projected_inventory: ['/music/track.mp3'],
      files: [{ path: '/music/track.mp3', size: 1, mtime: 2 }],
    } as unknown as ComponentOutcome)

    expect(componentOperationCount(component)).toBe(1)
    expect(componentHasUnmetTarget(component)).toBe(true)
    expect(component.lanes).toHaveLength(1)
  })
})

describe('member conclusion', () => {
  const base = { excluded: false, hasRoot: true, rootMissing: false, hasOperations: true }

  it('names the satisfied side once instead of labelling a partial result twice', () => {
    const partial = memberConclusion({ ...base, parts: { matched: 'unmet', unmatched: 'satisfied' } })
    expect(partial.label).toBe('有音效满足')
    expect(partial.tone).toBe('warning')
    expect(partial.detail).toContain('无音效目标未满足')

    const mirror = memberConclusion({ ...base, parts: { matched: 'satisfied', unmatched: 'unmet' } })
    expect(mirror.label).toBe('无音效满足')
    expect(mirror.tone).toBe('warning')
  })

  it('keeps full and empty results as their own labels', () => {
    expect(memberConclusion({ ...base, parts: { matched: 'satisfied', unmatched: 'satisfied' } })).toEqual({
      tone: 'success',
      label: '全部满足',
      detail: '两个分类的目标都已满足。',
    })
    expect(memberConclusion({ ...base, hasOperations: false, parts: { matched: 'satisfied', unmatched: 'satisfied' } }).label).toBe(
      '无变化',
    )
    expect(memberConclusion({ ...base, parts: { matched: 'unmet', unmatched: 'unmet' } }).label).toBe('目标未满足')
  })
})
