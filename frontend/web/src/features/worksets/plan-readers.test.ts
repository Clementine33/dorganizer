import { describe, expect, it } from 'vitest'
import type { ComponentOutcome } from '@/lib/api/types'
import {
  componentHasUnmetTarget,
  componentOperationCount,
  memberConclusion,
  operationsOf,
  partitionFacts,
  readComponent,
  type MemberFacts,
  type PartitionFacts,
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
  const base = { excluded: false, hasRoot: true, rootMissing: false }
  const side = (overrides: Partial<PartitionFacts> = {}): PartitionFacts => ({
    applicable: true,
    blocked: false,
    unmet: false,
    changes: false,
    ...overrides,
  })
  const facts = (matched: Partial<PartitionFacts> = {}, unmatched: Partial<PartitionFacts> = {}): MemberFacts => ({
    matched: side(matched),
    unmatched: side(unmatched),
  })

  it('names the partition that will change, not the one that is satisfied', () => {
    const matchedOnly = memberConclusion({ ...base, facts: facts({ changes: true }) })
    expect(matchedOnly.label).toBe('仅无音效转换')
    expect(matchedOnly.tone).toBe('success')
    expect(matchedOnly.detail).toContain('无音效：将转换')
    expect(matchedOnly.detail).toContain('有音效：无需改动')

    const unmatchedOnly = memberConclusion({ ...base, facts: facts({}, { changes: true }) })
    expect(unmatchedOnly.label).toBe('仅有音效转换')

    const both = memberConclusion({ ...base, facts: facts({ changes: true }, { changes: true }) })
    expect(both.label).toBe('全量转换')
  })

  it('keeps the changed label but reads as a warning while a target stays unmet', () => {
    const partial = memberConclusion({ ...base, facts: facts({ changes: true }, { unmet: true }) })

    expect(partial.label).toBe('仅无音效转换')
    expect(partial.tone).toBe('warning')
    expect(partial.detail).toContain('有音效：目标未满足，现有文件按可用源保留')
  })

  it('separates an unmet target from a folder with nothing to do', () => {
    const unmet = memberConclusion({ ...base, facts: facts({ unmet: true }) })
    expect(unmet.tone).toBe('warning')
    expect(unmet.label).toBe('目标未满足')
    expect(unmet.detail).toContain('无音效：目标未满足')

    const nothing = memberConclusion({ ...base, facts: facts() })
    expect(nothing.label).toBe('无需转换')
    expect(nothing.tone).toBe('neutral')

    // No component at all is 无适用, not 无变化: nothing was asked of it.
    const noFiles = memberConclusion({ ...base, facts: facts({ applicable: false }, { applicable: false }) })
    expect(noFiles.label).toBe('无适用')
    expect(noFiles.detail).toContain('无音效：无适用文件')
  })

  it('reports a blocked component above every other fact', () => {
    const blocked = memberConclusion({ ...base, facts: facts({ blocked: true, changes: true }) })

    expect(blocked.tone).toBe('danger')
    expect(blocked.label).toBe('阻塞')
  })
})

describe('partition facts', () => {
  function component(overrides: Partial<ComponentOutcome>): ComponentOutcome {
    return readComponent({ component_id: 'cmp', partition: 'matched', status: 'ok', ...overrides } as ComponentOutcome)
  }

  it('accumulates independent facts per partition', () => {
    const facts = partitionFacts([
      component({ operations: [{ kind: 'encode' }] as ComponentOutcome['operations'] }),
      component({
        component_id: 'cmp-2',
        status: 'blocked',
        variant_decisions: [{ stem: 'track', decisions: [{ path: '/a.mp3', resolution: 'keep', reason_code: 'UNMET_TARGET' }] }],
      }),
    ])

    expect(facts.matched).toEqual({ applicable: true, blocked: true, unmet: true, changes: true })
    // The partition with no component holds none of those facts.
    expect(facts.unmatched).toEqual({ applicable: false, blocked: false, unmet: false, changes: false })
  })
})
