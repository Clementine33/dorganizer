import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { RevisionDetailResponse, WorksetMember } from '@/lib/api/types'
import MemberReview from './MemberReview.vue'

const member: WorksetMember = {
  member_id: 'm1',
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
              variant_stem: '00',
              source_path: '/music/albumA/00.wav',
              target_path: '/music/albumA/00.mp3',
            },
            {
              kind: 'delete_obsolete',
              phase: 'remove_obsolete_audio',
              component_id: 'comp-1',
              variant_stem: '01',
              source_path: '/music/albumA/01.wav',
            },
          ],
          variant_decisions: [
            {
              stem: '00',
              decisions: [
                { path: '/music/albumA/00.mp3', resolution: 'encode', target_path: '/music/albumA/00.mp3' },
                { path: '/music/albumA/00.wav', resolution: 'keep', reason_code: 'KEEP_LOSSLESS_TARGET' },
              ],
            },
            {
              stem: '01',
              decisions: [
                { path: '/music/albumA/01.mp3', resolution: 'keep', reason_code: 'UNMET_TARGET' },
                { path: '/music/albumA/01.wav', resolution: 'delete', reason_code: 'OBSOLETE_LOSSLESS' },
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
  it("gives every file one row, in the plan's own words", () => {
    const wrapper = mount(MemberReview, { props: { member, revision, editable: false } })

    const rows = wrapper.findAll('[data-testid="component-decision"]').map((row) => row.text())
    expect(rows).toHaveLength(4)
    expect(rows[0]).toContain('生成')
    expect(rows[0]).toContain('00.mp3')
    expect(rows[1]).toContain('保留')
    expect(rows[1]).toContain('已满足无损目标')
    expect(rows[2]).toContain('保留')
    expect(rows[2]).toContain('目标未满足')
    expect(rows[3]).toContain('删除')
    expect(rows[3]).toContain('01.wav')
  })

  it('keeps the paths whole in the narrow column', () => {
    const wrapper = mount(MemberReview, { props: { member, revision, editable: false } })

    // The member folder is the card's header, so rows carry the member-relative
    // path unabridged instead of a clipped absolute one.
    const rows = wrapper.findAll('[data-testid="component-decision"]').map((row) => row.text())
    for (const row of rows) expect(row).not.toContain('/music/albumA/')
    expect(rows[0]).toContain('00.mp3')
    expect(rows[3]).toContain('01.wav')
  })

  it('does not repeat the same files as a second operations list', () => {
    const wrapper = mount(MemberReview, { props: { member, revision, editable: false } })

    const body = wrapper.text()
    expect(body).not.toContain('delete_obsolete')
    expect(body).not.toContain('→')
    expect(wrapper.findAll('[data-testid="component-kept"]')).toHaveLength(0)
    expect(body).toContain('有变化')
  })
})
