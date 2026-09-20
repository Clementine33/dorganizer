import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { ExecutionView } from '@/lib/api/types'
import ExecutionPanel from './ExecutionPanel.vue'

function view(overrides: Partial<ExecutionView> = {}): ExecutionView {
  return {
    execution_id: 'exec-1',
    workset_id: 'ws-1',
    operation_type: 'conversion',
    plan_id: 'plan-1',
    status: 'running',
    options: { delete_mode: 'soft' },
    total_components: 2,
    completed_components: 1,
    total_operations: 3,
    completed_operations: 1,
    current_root: '/music/albumB',
    current_component_id: 'comp-b',
    current_phase: 'component',
    components: [],
    error_code: '',
    error_message: '',
    started_at: '',
    finished_at: '',
    created_at: '',
    ...overrides,
  }
}

describe('ExecutionPanel', () => {
  it("groups the partitions of one folder into that folder's own card", () => {
    const wrapper = mount(ExecutionPanel, {
      props: {
        members: [
          {
            member_id: 'm-1',
            folder_path: '/music/albumA',
            folder_name: '专辑 A',
            rel_path: '专辑 A',
            dir_id: 'd-1',
          },
        ],
        view: view({
          current_root: '/music/albumA',
          current_component_id: 'comp-a2',
          components: [
            {
              component_index: 1,
              component_id: 'comp-a1',
              root_path: '/music/albumA',
              partition: 'matched',
              status: 'succeeded',
              operations: 1,
              completed_operations: 1,
              committed: ['/music/albumA/00.mp3'],
              removed: [],
              remaining: [],
              recovery: [],
              inventory_synced: true,
            },
            {
              component_index: 2,
              component_id: 'comp-a2',
              root_path: '/music/albumA',
              partition: 'unmatched',
              status: 'pending',
              operations: 1,
              completed_operations: 0,
              committed: [],
              removed: [],
              remaining: ['encode:/music/albumA/01.mp3'],
              recovery: [],
              inventory_synced: true,
            },
          ],
        }),
      },
    })

    // One card for what the user selected — however many partitions it has —
    // named the way the record names it.
    const card = wrapper.get('[data-testid="execution-folder"]')
    expect(wrapper.findAll('[data-testid="execution-folder"]')).toHaveLength(1)
    expect(card.get('[data-testid="execution-folder-name"]').text()).toBe('专辑 A')
    expect(card.findAll('[data-testid="execution-component"]')).toHaveLength(2)
    expect(card.text()).toContain('无音效')
    expect(card.text()).toContain('有音效')

    // The card reports the folder's own state and counts, and says it is the
    // one the run is inside right now.
    expect(card.get('[data-testid="execution-folder-status"]').text()).toBe('执行中')
    const counts = card.get('[data-testid="execution-folder-counts"]').text()
    expect(counts).toContain('组件 1/2')
    expect(counts).toContain('操作 1/2')
  })

  it('gives each folder its own verdict when a run stops partway', () => {
    const wrapper = mount(ExecutionPanel, {
      props: {
        view: view({
          status: 'canceled',
          current_component_id: '',
          components: [
            {
              component_index: 1,
              component_id: 'comp-a',
              root_path: '/music/albumA',
              partition: 'matched',
              status: 'succeeded',
              operations: 1,
              completed_operations: 1,
              committed: ['/music/albumA/00.mp3'],
              removed: [],
              remaining: [],
              recovery: [],
              inventory_synced: true,
            },
            {
              component_index: 2,
              component_id: 'comp-b',
              root_path: '/music/albumB',
              partition: 'matched',
              status: 'pending',
              operations: 1,
              completed_operations: 0,
              committed: [],
              removed: [],
              remaining: ['encode:/music/albumB/00.mp3'],
              recovery: [],
              inventory_synced: true,
            },
          ],
        }),
      },
    })

    const cards = wrapper.findAll('[data-testid="execution-folder"]')
    expect(cards).toHaveLength(2)
    expect(cards[0]!.attributes('data-folder-status')).toBe('succeeded')
    expect(cards[1]!.attributes('data-folder-status')).toBe('pending')
    expect(cards[0]!.text()).toContain('已完成')
    expect(cards[1]!.text()).toContain('未执行')
  })

  it('states a session-level failure in the user\'s words', () => {
    const wrapper = mount(ExecutionPanel, {
      props: {
        view: view({
          status: 'failed',
          error_code: 'INPUT_CHANGED',
          error_message: 'the scanned inventory no longer matches the revision\'s recorded inputs; regenerate the plan',
        }),
      },
    })

    expect(wrapper.get('[data-testid="execution-error"]').text()).toContain('文件夹输入已变化')
    expect(wrapper.get('[data-testid="execution-error"]').text()).not.toContain('regenerate the plan')
  })

  it('reports real counts while running, never a fabricated percentage', () => {
    const wrapper = mount(ExecutionPanel, { props: { view: view() } })

    expect(wrapper.get('[data-testid="execution-status"]').text()).toBe('执行中')
    expect(wrapper.get('[data-testid="execution-progress"]').text()).toContain('组件 1/2')
    expect(wrapper.get('[data-testid="execution-progress"]').text()).toContain('操作 1/3')
    expect(wrapper.text()).toContain('albumB')
  })

  it('keeps the partial facts of a failure: stage, error and unrun operations', () => {
    const wrapper = mount(ExecutionPanel, {
      props: {
        view: view({
          status: 'failed',
          error_code: 'COMPONENT_ENCODE_FAILED',
          error_message: 'ffmpeg failed',
          components: [
            {
              component_index: 1,
              component_id: 'comp-b',
              root_path: '/music/albumB',
              partition: 'matched',
              status: 'failed',
              stage: 'materialize',
              operations: 2,
              completed_operations: 0,
              committed: [],
              removed: [],
              remaining: ['encode:/music/albumB/00.wav'],
              recovery: ['/music/albumB/Delete/00.m4a'],
              error_code: 'COMPONENT_ENCODE_FAILED',
              error_message: 'ffmpeg failed',
              inventory_synced: false,
              inventory_sync_error: 'database is locked',
            },
            {
              component_index: 2,
              component_id: 'comp-c',
              root_path: '/music/albumC',
              partition: 'matched',
              status: 'pending',
              operations: 1,
              completed_operations: 0,
              committed: [],
              removed: [],
              remaining: ['encode:/music/albumC/00.wav'],
              recovery: [],
              inventory_synced: true,
            },
          ],
        }),
      },
    })

    expect(wrapper.get('[data-testid="execution-status"]').text()).toBe('失败')
    expect(wrapper.get('[data-testid="execution-error"]').text()).toContain('ffmpeg failed')
    expect(wrapper.text()).toContain('停在生成输出')
    expect(wrapper.get('[data-testid="execution-remaining"]').text()).toContain('00.wav')
    expect(wrapper.get('[data-testid="execution-inventory-warning"]').text()).toContain('未完全同步')
    // The component that never ran is visible as such, not hidden or counted
    // as done.
    const components = wrapper.findAll('[data-testid="execution-component"]')
    expect(components).toHaveLength(2)
    expect(components[1]!.text()).toContain('未执行')
  })

  it('names the soft-delete destination and the hard-delete consequence', () => {
    const soft = mount(ExecutionPanel, {
      props: {
        view: view({
          status: 'succeeded',
          components: [
            {
              component_index: 1,
              component_id: 'comp-a',
              root_path: '/music/albumA',
              partition: 'matched',
              status: 'succeeded',
              operations: 1,
              completed_operations: 1,
              committed: ['/music/albumA/00.mp3'],
              removed: ['/music/albumA/00.m4a'],
              remaining: [],
              recovery: ['/music/albumA/Delete/00.m4a'],
              inventory_synced: true,
            },
          ],
        }),
      },
    })
    expect(soft.text()).toContain('已清理到 Delete/')

    const hard = mount(ExecutionPanel, {
      props: {
        view: view({
          options: { delete_mode: 'hard' },
          status: 'succeeded',
          components: [
            {
              component_index: 1,
              component_id: 'comp-a',
              root_path: '/music/albumA',
              partition: 'matched',
              status: 'succeeded',
              operations: 1,
              completed_operations: 1,
              committed: ['/music/albumA/00.mp3'],
              removed: ['/music/albumA/00.m4a'],
              remaining: [],
              recovery: [],
              inventory_synced: true,
            },
          ],
        }),
      },
    })
    expect(hard.text()).toContain('已硬删除')
    expect(hard.text()).toContain('硬删除')
  })

  it("shows the plan's kept files, not where a run moved files", () => {
    const wrapper = mount(ExecutionPanel, {
      props: {
        kept: {
          'comp-a': [
            { path: '/music/albumA/disc1/00.wav', resolution: 'keep', reason_code: 'UNMET_TARGET' },
            { path: '/music/albumA/01.mp3', resolution: 'keep', reason_code: 'KEEP_ENCODED_SATISFIED' },
          ],
        },
        view: view({
          status: 'succeeded',
          components: [
            {
              component_index: 1,
              component_id: 'comp-a',
              root_path: '/music/albumA',
              partition: 'matched',
              status: 'succeeded',
              operations: 1,
              completed_operations: 1,
              committed: ['/music/albumA/00.mp3'],
              removed: ['/music/albumA/00.m4a'],
              remaining: [],
              recovery: ['/music/albumA/Delete/00.m4a'],
              inventory_synced: true,
            },
          ],
        }),
      },
    })

    const kept = wrapper.get('[data-testid="execution-kept"]').text()
    expect(kept).toContain('disc1/00.wav')
    expect(kept).not.toContain('/music/albumA/')
    expect(wrapper.get('[title="/music/albumA/disc1/00.wav"]').text()).toBe('disc1/00.wav')
    expect(wrapper.get('details').attributes('open')).toBeUndefined()
    expect(wrapper.get('summary').text()).toContain('保留 2')
    expect(kept).toContain('目标未满足')
    expect(kept).toContain('已满足编码目标')
    // The run's own bookkeeping stays out of the panel.
    expect(wrapper.find('[data-testid="execution-recovery"]').exists()).toBe(false)
    expect(wrapper.find('[title="/music/albumA/Delete/00.m4a"]').exists()).toBe(false)
  })

  it('explains a canceled run without claiming the whole revision happened', () => {
    const wrapper = mount(ExecutionPanel, {
      props: {
        view: view({
          status: 'canceled',
          components: [
            {
              component_index: 2,
              component_id: 'comp-b',
              root_path: '/music/albumB',
              partition: 'matched',
              status: 'canceled',
              operations: 2,
              completed_operations: 1,
              committed: ['/music/albumB/00.mp3'],
              removed: [],
              remaining: ['remove:/music/albumB/00.m4a'],
              recovery: [],
              inventory_synced: true,
            },
          ],
        }),
      },
    })

    expect(wrapper.get('[data-testid="execution-status"]').text()).toBe('已取消')
    expect(wrapper.text()).toContain('已完成的组件结果保留')
    expect(wrapper.get('[data-testid="execution-remaining"]').text()).toContain('00.m4a')
  })
})
