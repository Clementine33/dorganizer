import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { RevisionDetailResponse, WorksetMember } from '@/lib/api/types'
import MemberReview from './MemberReview.vue'

const member: WorksetMember = {
  member_id: 'm1',
  folder_id: 'f1',
  folder_path: '/music/albumA',
  folder_name: 'albumA',
  rel_path: 'albumA',
}

/**
 * A frozen revision whose component concludes both: one file stays untouched
 * because the declared lossless target is already satisfied, another because
 * nothing could satisfy its target.
 */
const revision: RevisionDetailResponse = {
  plan_id: 'plan-1',
  revision_index: 1,
  created_at: '2026-09-18T00:00:00Z',
  root_path: '/music/albumA',
  snapshot_token: 'tok',
  status: 'ready',
  summary: { operation_count: 1, error_count: 0, total_count: 1, actionable_count: 1, summary_reason: '' },
  task: {
    kind: 'conversion',
    schema_version: 1,
    payload: {
      policy: {},
      policy_hash: '',
      classifier: {},
      summary: { component_count: 1, blocked_count: 0, operation_count: 1, error_count: 0, summary_reason: '' },
      components: [
        {
          component_id: 'comp-1',
          partition: 'matched',
          status: 'ok',
          lanes: [],
          operations: [
            {
              kind: 'encode',
              phase: 'materialize_outputs',
              component_id: 'comp-1',
              variant_stem: 'track',
              source_path: '/music/albumA/00.wav',
              target_path: '/music/albumA/00.mp3',
            },
          ],
          variant_decisions: [
            {
              stem: 'track',
              decisions: [
                { path: '/music/albumA/00.wav', resolution: 'keep', reason_code: 'KEEP_LOSSLESS_TARGET' },
                { path: '/music/albumA/01.mp3', resolution: 'keep', reason_code: 'UNMET_TARGET' },
                { path: '/music/albumA/legacy.m4a', resolution: 'delete', reason_code: 'OBSOLETE_ENCODED' },
              ],
            },
          ],
          projected_inventory: [],
          files: [],
        },
      ],
    },
  },
  counts: { members: 1, changed: 1, unmet_targets: 1, blocked: 0, unchanged: 0 },
  members: [
    {
      member_id: 'm1',
      member_name: 'albumA',
      folder_path: '/music/albumA',
      excluded: false,
      effective: { schema_version: 1 },
      sources: {},
    },
  ],
  roots: [
    {
      root_index: 0,
      root_path: '/music/albumA',
      root_status: 'ok',
      root_error_code: '',
      root_error_message: '',
      stale: false,
      inventory_fingerprint: 'fp',
      entry_count: 1,
    },
  ],
  component_roots: [{ step_index: 0, component_index: 0, component_id: 'comp-1', root_index: 0 }],
  execution: null,
}

describe('MemberReview', () => {
  it('shows the kept files of a component and why each was kept', () => {
    const wrapper = mount(MemberReview, { props: { member, revision, editable: false } })

    const kept = wrapper.get('[data-testid="component-kept"]').text()
    expect(kept).toContain('00.wav')
    expect(kept).toContain('已满足无损目标')
    expect(kept).toContain('01.mp3')
    expect(kept).toContain('目标未满足')
    // Deleted and materialized files are operations, never kept conclusions.
    expect(kept).not.toContain('legacy.m4a')
    expect(kept).not.toContain('00.wav →')
  })

  it('still lists the operations beside them', () => {
    const wrapper = mount(MemberReview, { props: { member, revision, editable: false } })

    expect(wrapper.text()).toContain('encode · /music/albumA/00.wav')
    expect(wrapper.text()).toContain('→ /music/albumA/00.mp3')
    expect(wrapper.text()).toContain('有变化')
  })
})
