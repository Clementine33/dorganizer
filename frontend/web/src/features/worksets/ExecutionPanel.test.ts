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
  it('reports real counts while running, never a fabricated percentage', () => {
    const wrapper = mount(ExecutionPanel, { props: { view: view() } })

    expect(wrapper.get('[data-testid="execution-status"]').text()).toBe('执行中')
    expect(wrapper.get('[data-testid="execution-progress"]').text()).toContain('组件 1/2')
    expect(wrapper.get('[data-testid="execution-progress"]').text()).toContain('操作 1/3')
    expect(wrapper.text()).toContain('albumB')
  })

  it('keeps the partial facts of a failure: stage, error, unrun operations and preserved files', () => {
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
    expect(wrapper.get('[data-testid="execution-recovery"]').text()).toContain('Delete/00.m4a')
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
