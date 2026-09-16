import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { nextTick, reactive } from 'vue'
import type { OperationDraftDocument } from '@/lib/api/types'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import OverrideEditor from './OverrideEditor.vue'
import UnitSelect from './UnitSelect.vue'

const WAV_MP3 = {
  lossless: { codec: 'wav' },
  encoded: { codec: 'mp3', quality: { kind: 'bitrate', bitrate: 320 } },
}
const FLAC = { lossless: { codec: 'flac' } }

function draft(members: OperationDraftDocument['members'] = []): OperationDraftDocument {
  return reactive({
    schema_version: 1,
    mode: 'available_sources',
    classifier_tags: ['A'],
    matched: WAV_MP3,
    unmatched: WAV_MP3,
    members,
  }) as unknown as OperationDraftDocument
}

function mountEditor(
  harness: OperationDraftDocument,
  target: { kind: 'member'; memberId: string } | { kind: 'batch'; memberIds: string[] },
) {
  const store = useWorksetEditorStore()
  store.open({
    worksetId: 'ws-1',
    operation: 'conversion',
    target,
    baseVersion: 2,
    baseDocument: harness,
  })
  return mount(OverrideEditor, { props: { draft: harness, target, readOnly: false, participationEditable: true } })
}

function statusText(wrapper: VueWrapper, unit: string): string {
  return wrapper.get(`[data-testid="unit-${unit}"] [data-testid="unit-status"]`).text()
}

describe('OverrideEditor', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('offers exactly two choices per group', () => {
    const wrapper = mountEditor(draft(), { kind: 'member', memberId: 'm-1' })
    expect(wrapper.find('[data-testid="unit-mode-default"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="unit-mode-override"]').exists()).toBe(true)
    // No third "keep" action and no "restore inheritance" wording.
    expect(wrapper.findAll('[data-testid="unit-mode-set"]')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('恢复继承')
  })

  it('shows the current source and matches it to a choice', () => {
    const wrapper = mountEditor(draft([{ member_id: 'm-1', overrides: { matched: FLAC } }]), {
      kind: 'member',
      memberId: 'm-1',
    })
    // matched = 独立值, everything else = 默认.
    expect(wrapper.get('[data-testid="unit-matched"]').text()).toContain('独立值')
    expect(wrapper.get('[data-testid="unit-mode"]').text()).toContain('默认')
    // The source word never stands in for the value itself.
    expect(statusText(wrapper, 'matched')).toBe('当前：FLAC（与全局不同）')
    expect(statusText(wrapper, 'mode')).toBe('当前：可用源')
    expect(statusText(wrapper, 'classifier_tags')).toBe('当前：A')
    expect(statusText(wrapper, 'unmatched')).toBe('当前：WAV + MP3 320')
  })

  it('says out loud when an override merely spells the common value', () => {
    const wrapper = mountEditor(draft([{ member_id: 'm-1', overrides: { matched: WAV_MP3 } }]), {
      kind: 'member',
      memberId: 'm-1',
    })

    expect(statusText(wrapper, 'matched')).toBe('当前：WAV + MP3 320（与全局相同）')
  })

  it('states what an apply would write or restore', async () => {
    const wrapper = mountEditor(draft([{ member_id: 'm-1', overrides: { matched: FLAC } }]), {
      kind: 'member',
      memberId: 'm-1',
    })

    await wrapper.get('[data-testid="unit-matched-default"]').trigger('click')
    await nextTick()
    expect(statusText(wrapper, 'matched')).toBe('将回到全局值（WAV + MP3 320）')

    await wrapper.get('[data-testid="unit-matched-override"]').trigger('click')
    await nextTick()
    expect(statusText(wrapper, 'matched')).toBe('将写入 FLAC')
  })

  it('picking 默认 removes the override', async () => {
    const wrapper = mountEditor(draft([{ member_id: 'm-1', overrides: { matched: FLAC } }]), {
      kind: 'member',
      memberId: 'm-1',
    })
    await wrapper.get('[data-testid="unit-matched-default"]').trigger('click')
    await nextTick()

    expect(useWorksetEditorStore().session?.intent.units.matched).toEqual({ intent: 'inherit' })
    expect(useWorksetEditorStore().pendingDocument?.members).toEqual([])
  })

  it('picking 覆盖 seeds from the common value and reveals the fields', async () => {
    const wrapper = mountEditor(draft(), { kind: 'member', memberId: 'm-1' })
    await wrapper.get('[data-testid="unit-matched-override"]').trigger('click')
    await nextTick()

    expect(useWorksetEditorStore().session?.intent.units.matched).toEqual({ intent: 'set', value: WAV_MP3 })
    expect(wrapper.find('[data-testid="override-matched-lossless"]').exists()).toBe(true)
  })

  it('edits an override profile on a reactive draft without losing the other lane', async () => {
    const wrapper = mountEditor(draft(), { kind: 'member', memberId: 'm-1' })
    await wrapper.get('[data-testid="unit-matched-override"]').trigger('click')
    wrapper.findAllComponents(UnitSelect)
      .find(select => select.vm.$attrs['data-testid'] === 'override-matched-lossless')!.vm.$emit('change', 'flac')
    await nextTick()

    expect(useWorksetEditorStore().session?.intent.units.matched).toEqual({
      intent: 'set',
      value: { lossless: { codec: 'flac' }, encoded: { codec: 'mp3', quality: { kind: 'bitrate', bitrate: 320 } } },
    })
  })

  it('reports 多种值 for a batch with disagreeing members and leaves it unset', () => {
    const harness = draft([
      { member_id: 'm-1', overrides: { matched: FLAC } },
      { member_id: 'm-2' },
    ])
    const wrapper = mountEditor(harness, { kind: 'batch', memberIds: ['m-1', 'm-2'] })

    expect(wrapper.get('[data-testid="unit-matched"]').text()).toContain('多种值')
    expect(statusText(wrapper, 'matched')).toBe('当前：多种值')
    // Neither choice is pressed until the user decides.
    expect(wrapper.get('[data-testid="unit-matched-default"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-testid="unit-matched-override"]').attributes('aria-checked')).toBe('false')
    expect(useWorksetEditorStore().isDirty).toBe(false)
  })

  it('applies one choice to every member of the batch', async () => {
    const harness = draft([
      { member_id: 'm-1', overrides: { matched: FLAC } },
      { member_id: 'm-2' },
    ])
    const wrapper = mountEditor(harness, { kind: 'batch', memberIds: ['m-1', 'm-2'] })

    await wrapper.get('[data-testid="unit-unmatched-default"]').trigger('click')
    await nextTick()
    expect(useWorksetEditorStore().session?.intent.units.unmatched).toEqual({ intent: 'inherit' })

    await wrapper.get('[data-testid="unit-classifier_tags-override"]').trigger('click')
    await wrapper.get('[data-testid="override-classifier_tags"]').setValue('X, Y')
    await nextTick()

    const members = useWorksetEditorStore().pendingDocument?.members ?? []
    expect(members).toHaveLength(2)
    for (const record of members) {
      expect(record.overrides?.classifier_tags).toEqual(['X', 'Y'])
    }
    // The batch never touches the matched overrides it did not edit.
    expect(members.find((m) => m.member_id === 'm-1')?.overrides?.matched).toEqual(FLAC)
  })

  it('keeps overrides when the batch is excluded', async () => {
    const harness = draft([{ member_id: 'm-1', overrides: { matched: FLAC } }])
    const wrapper = mountEditor(harness, { kind: 'batch', memberIds: ['m-1'] })

    await wrapper.get('[data-testid="participation-exclude"]').trigger('click')
    await nextTick()

    const members = useWorksetEditorStore().pendingDocument?.members ?? []
    expect(members).toEqual([{ member_id: 'm-1', excluded: true, overrides: { matched: FLAC } }])
  })
})
