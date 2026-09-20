import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { apiClientKey } from '@/lib/api/client'
import { apiStub } from '@/test/api-stub'
import { installTestQueryPlugin } from '@/test/query-client'
import PlanReviewTree from './PlanReviewTree.vue'
import type { ComponentOutcome, RevisionDetailResponse, Workset } from '@/lib/api/types'

/**
 * The plan's own view of a member (spec T3).
 *
 * It renders the frozen conclusions — kept, deleted, generated — and marks a
 * planned output as pending because that file does not exist yet. It reads no
 * directory listing: a plan review built from the latest scan would be a
 * different promise than the one the plan makes.
 */

const workset: Workset = {
  workset_id: 'ws-1',
  title: 'Archive',
  version: 1,
  library: { library_id: 'lib-1', name: 'Archive', root_path: '/music' },
  members: [
    { member_id: 'm-1', folder_path: '/music/albumA', folder_name: 'albumA', rel_path: 'albumA', dir_id: 'dir-albumA' },
  ],
  operations: [],
  updated_at: '',
  created_at: '',
}

function component(): ComponentOutcome {
  return {
    component_id: 'c-1',
    partition: 'matched',
    status: 'ok',
    lanes: [],
    variant_decisions: [
      {
        stem: 'track1',
        decisions: [
          // Real codes: a mapped keep reason, and a delete reason the review is
          // deliberately silent about.
          { path: '/music/albumA/keep.flac', resolution: 'keep', reason_code: 'KEEP_ENCODED_SATISFIED' },
          { path: '/music/albumA/obsolete.mp3', resolution: 'delete', reason_code: 'OBSOLETE_ENCODED' },
          { path: '/music/albumA/track1.wav', resolution: 'encode', target_path: '/music/albumA/track1.wav' },
        ],
      },
    ],
    operations: [
      {
        kind: 'encode',
        phase: 'execute',
        component_id: 'c-1',
        variant_stem: 'track1',
        source_path: '/music/albumA/track1.flac',
        target_path: '/music/albumA/track1.wav',
      },
    ],
    projected_inventory: ['/music/albumA/track1.wav'],
    files: [],
  }
}

const revision: RevisionDetailResponse = {
  plan_id: 'plan-1',
  revision_index: 1,
  created_at: '',
  root_path: '/music',
  snapshot_token: 'snap',
  status: 'ready',
  summary: { operation_count: 1, error_count: 0, total_count: 1, actionable_count: 1, summary_reason: 'ACTIONABLE' },
  task: {
    kind: 'conversion',
    schema_version: 1,
    payload: {
      policy: {},
      policy_hash: 'ph',
      classifier: {},
      summary: { component_count: 1, blocked_count: 0, operation_count: 1, error_count: 0, summary_reason: 'ACTIONABLE' },
      components: [component()],
    },
  },
  counts: { members: 1, changed: 1, unmet_targets: 0, blocked: 0, unchanged: 0 },
  members: [],
  roots: [
    {
      root_index: 0,
      root_path: '/music/albumA',
      root_status: 'ok',
      root_error_code: '',
      root_error_message: '',
      stale: false,
      inventory_fingerprint: 'fp',
      entry_count: 3,
    },
  ],
  component_roots: [{ step_index: 0, component_index: 0, component_id: 'c-1', root_index: 0 }],
  execution: null,
}

async function mountTree(
  overrides: {
    memberPath?: string
    revision?: Partial<RevisionDetailResponse>
    operation?: Record<string, unknown>
  } = {},
) {
  const api = apiStub({
    getCurrentRecord: vi.fn().mockResolvedValue({ workset }),
    getWorkset: vi.fn().mockResolvedValue(workset),
    getOperation: vi.fn().mockResolvedValue(
      overrides.operation ?? {
        workset_id: 'ws-1',
        operation_type: 'conversion',
        version: 2,
        planning_state: 'planned',
        current_revision: { plan_id: 'plan-1', revision_index: 1, counts: revision.counts },
        active_generation: null,
        latest_generation: null,
        active_execution: null,
        latest_execution: null,
      },
    ),
    getOperationDraft: vi.fn().mockResolvedValue({ version: 2, document: { members: [] } }),
    getRevision: vi.fn().mockResolvedValue({ ...revision, ...overrides.revision }),
  } as never)
  const wrapper = mount(PlanReviewTree, {
    props: { worksetId: 'ws-1', memberPath: overrides.memberPath ?? 'albumA' },
    global: { plugins: [createPinia(), installTestQueryPlugin()], provide: { [apiClientKey as symbol]: api } },
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  localStorage.clear()
})

describe('plan review tree', () => {
  it('renders the frozen decisions of the plan', async () => {
    const wrapper = await mountTree()

    const rows = wrapper.findAll('[role="treeitem"] li, [data-resolution]')
    const text = wrapper.text()
    expect(text).toContain('keep.flac')
    expect(text).toContain('保留')
    expect(text).toContain('obsolete.mp3')
    expect(text).toContain('删除')
    expect(text).toContain('track1.wav')
    expect(text).toContain('生成')
    expect(rows.length).toBeGreaterThan(0)

    // The plan's code is read with the member review's own rule: a kept file
    // says why in the plan's words, and a code the map does not cover is never
    // spilled into the tree as SCREAMING_SNAKE.
    expect(text).toContain('已满足编码目标')
    expect(text).not.toContain('KEEP_ENCODED_SATISFIED')
    expect(text).not.toContain('OBSOLETE_ENCODED')
  })

  it('marks a planned output as pending, because it is not on disk yet', async () => {
    const wrapper = await mountTree()

    const pending = wrapper.findAll('[data-pending="true"]')
    expect(pending.length).toBe(1)
    expect(pending[0].text()).toContain('track1.wav')
    expect(pending[0].text()).toContain('待生成')
  })

  it('offers no way to manage files: the plan review is read-only', async () => {
    const wrapper = await mountTree()

    expect(wrapper.find('[data-testid="file-toolbar"]').exists()).toBe(false)
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="row-actions"]').exists()).toBe(false)
  })

  it('summarises what the plan does with this folder before the rows', async () => {
    const wrapper = await mountTree()

    // The same reader the conversion list's row uses, so the row that opened
    // this page and the page itself never disagree.
    const summary = wrapper.get('[data-testid="plan-summary"]').text()
    expect(summary).toContain('仅无音效转换')
    expect(summary).toContain('无音效：将转换')

    const tally = wrapper.get('[data-testid="plan-tally"]').text()
    expect(tally).toContain('保留 1')
    expect(tally).toContain('删除 1')
    expect(tally).toContain('生成 1')
    expect(tally).toContain('1 项待生成')
  })

  it('says why a folder the plan does not cover has nothing to list', async () => {
    const wrapper = await mountTree({ memberPath: 'albumB' })

    expect(wrapper.get('[data-testid="plan-summary"]').text()).toContain('未参与')
    expect(wrapper.get('[data-testid="plan-empty"]').text()).toContain('没有列出这个文件夹的文件')
    expect(wrapper.find('[data-pending="true"]').exists()).toBe(false)
  })

  it('keeps the frozen plan on screen and says it needs regenerating', async () => {
    const wrapper = await mountTree({
      operation: {
        workset_id: 'ws-1',
        operation_type: 'conversion',
        version: 3,
        planning_state: 'needs_planning',
        current_revision: { plan_id: 'plan-1', revision_index: 1, validation_state: 'stale' },
        active_generation: null,
        latest_generation: null,
        active_execution: null,
        latest_execution: null,
      },
    })

    expect(wrapper.get('[data-testid="plan-needs-regeneration"]').text()).toContain('需重新生成')
    // The proposal itself is untouched: a stale plan is still the plan.
    expect(wrapper.get('[data-testid="plan-tally"]').text()).toContain('保留 1')
  })

  it('says so when the record has no plan yet', async () => {
    const api = apiStub({
      getCurrentRecord: vi.fn().mockResolvedValue({ workset }),
      getWorkset: vi.fn().mockResolvedValue(workset),
      getOperation: vi.fn().mockResolvedValue({
        workset_id: 'ws-1',
        operation_type: 'conversion',
        version: 1,
        planning_state: 'unplanned',
        current_revision: null,
        active_generation: null,
        latest_generation: null,
        active_execution: null,
        latest_execution: null,
      }),
      getOperationDraft: vi.fn().mockResolvedValue({ version: 1, document: { members: [] } }),
    } as never)
    const wrapper = mount(PlanReviewTree, {
      props: { worksetId: 'ws-1', memberPath: 'albumA' },
      global: { plugins: [createPinia(), installTestQueryPlugin()], provide: { [apiClientKey as symbol]: api } },
    })
    await flushPromises()

    expect(wrapper.text()).toContain('还没有当前计划')
  })
})
