import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { DraftResponse, PolicySlot, ResolvedPolicy } from '@/lib/api/types'
import PolicyEditor from './PolicyEditor.vue'

const slotPolicy: ResolvedPolicy = {
  schema_version: 1,
  classifier_tags: ['SEなし'],
  matched: {
    lossless: { codec: 'wav' },
    encoded: { codec: 'mp3', quality: { kind: 'bitrate', bitrate: 320 } },
  },
  unmatched: { encoded: { codec: 'mp3', quality: { kind: 'bitrate', bitrate: 192 } } },
}

const configuredSlots: PolicySlot[] = [
  { slot: 1, name: '默认', policy: slotPolicy },
  { slot: 2, name: '', policy: null },
  { slot: 3, name: '', policy: null },
]

const inlineDraft: DraftResponse = {
  workset_id: 'ws-a',
  version: 3,
  workflow_schema_version: 1,
  workflow: {
    schema_version: 1,
    steps: [{ step_type: 'reconcile_audio_outputs', policy: { kind: 'inline', policy: slotPolicy } }],
  },
  updated_at: '2026-08-30T00:00:00Z',
}

const emptyDraft: DraftResponse = {
  workset_id: 'ws-b',
  version: 1,
  workflow_schema_version: 1,
  workflow: {
    schema_version: 1,
    steps: [
      {
        step_type: 'reconcile_audio_outputs',
        policy: { kind: 'inline', policy: { schema_version: 1, classifier_tags: [], matched: {}, unmatched: {} } },
      },
    ],
  },
  updated_at: '2026-08-30T00:00:00Z',
}

function mountEditor(props: Record<string, unknown> = {}) {
  return mount(PolicyEditor, {
    props: {
      draft: inlineDraft,
      slots: configuredSlots,
      saving: false,
      generating: false,
      slotSaving: false,
      slotError: null,
      saveError: null,
      conflict: false,
      conflictMessage: null,
      dirty: false,
      readOnly: false,
      ...props,
    },
  })
}

describe('PolicyEditor', () => {
  it('renders exactly three ordered slot cards with editable configuration state', () => {
    const wrapper = mountEditor()
    const slotCards = wrapper.findAll('[data-testid^="policy-slot-"]')
    expect(slotCards.length).toBe(3)
    expect(wrapper.get('[data-testid="policy-slot-1"]').text()).toContain('默认')
    expect(wrapper.get('[data-testid="policy-slot-2"]').text()).toContain('未配置')
    // Empty slots cannot be applied.
    expect(wrapper.find('[data-testid="apply-slot-2"]').exists()).toBe(false)
  })

  it('applies a populated slot as an independent deep-cloned draft snapshot', async () => {
    const wrapper = mountEditor({ draft: emptyDraft })
    await wrapper.get('[data-testid="apply-slot-1"]').trigger('click')
    // Applying counts as a local edit needing a save.
    expect(wrapper.emitted('update:dirty')?.at(-1)).toEqual([true])

    await wrapper.get('[data-testid="save-draft"]').trigger('click')
    const payload = wrapper.emitted('save')?.[0]?.[0] as {
      workflow: { steps: { policy: { kind: string; policy: ResolvedPolicy } }[] }
    }
    expect(payload.workflow.steps[0]?.policy.kind).toBe('inline')
    const saved = payload.workflow.steps[0]?.policy.policy
    // Deep clone: same content, distinct object identity.
    expect(saved).toEqual(slotPolicy)
    expect(saved).not.toBe(slotPolicy)
    expect(saved.classifier_tags).toEqual(['SEなし'])
  })

  it('edits tags as literal text and emits them in the workflow payload', async () => {
    const wrapper = mountEditor({ draft: emptyDraft })
    const input = wrapper.get('[data-testid="tag-input"]')
    await input.setValue('無音效')
    await input.trigger('keydown', { key: 'Enter' })
    expect(wrapper.findAll('[data-testid^="remove-tag-"]').length).toBe(1)

    await wrapper.get('[data-testid="codec-matched-lossless"]').setValue('flac')
    await wrapper.get('[data-testid="codec-unmatched-encoded"]').setValue('mp3')

    await wrapper.get('[data-testid="save-draft"]').trigger('click')
    const payload = wrapper.emitted('save')?.[0]?.[0] as {
      workflow: { steps: { policy: { policy: ResolvedPolicy } }[] }
    }
    const saved = payload.workflow.steps[0]?.policy.policy
    expect(saved.classifier_tags).toEqual(['無音效'])
    expect(saved.matched.lossless?.codec).toBe('flac')
    expect(wrapper.emitted('add-library-tag')?.[0]).toEqual(['無音效'])
  })

  it('restores default tags from library and allows selecting suggestions', async () => {
    const wrapper = mountEditor({
      draft: emptyDraft,
      defaultTags: ['seなし', '反転'],
      customTags: [{ id: 10, tag: '效果音なし' }],
    })
    // Check suggestions render
    expect(wrapper.get('[data-testid="lib-tag-seなし"]').text()).toContain('seなし')
    expect(wrapper.get('[data-testid="lib-tag-效果音なし"]').text()).toContain('效果音なし')

    // Click single suggestion
    await wrapper.get('[data-testid="lib-tag-效果音なし"] span').trigger('click')
    expect(wrapper.findAll('[data-testid^="remove-tag-"]').length).toBe(1)
    expect(wrapper.text()).toContain('效果音なし')

    // Click restore default tags
    await wrapper.get('[data-testid="restore-default-tags"]').trigger('click')
    expect(wrapper.findAll('[data-testid^="remove-tag-"]').length).toBe(3)
    expect(wrapper.text()).toContain('seなし')
    expect(wrapper.text()).toContain('反転')
  })

  it('saves the current form back into a named slot', async () => {
    const wrapper = mountEditor()
    await wrapper.get('[data-testid="save-to-slot-3"]').trigger('click')
    await wrapper.get('[data-testid="slot-name-3"]').setValue('归档')
    await wrapper.get('[data-testid="confirm-save-slot"]').trigger('click')

    const payload = wrapper.emitted('save-slot')?.[0]?.[0] as { slot: number; name: string; policy: ResolvedPolicy }
    expect(payload.slot).toBe(3)
    expect(payload.name).toBe('归档')
    expect(payload.policy).toEqual(slotPolicy)
    expect(payload.policy).not.toBe(slotPolicy)
  })

  it('keeps every control disabled only for a genuinely read-only Workset', () => {
    const wrapper = mountEditor({ readOnly: true })

    expect(wrapper.get('[data-testid="codec-matched-lossless"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="save-and-generate"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="apply-slot-1"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="save-to-slot-1"]').attributes('disabled')).toBeDefined()
  })

  it('shows slot save errors from the backend', () => {
    const wrapper = mountEditor({ slotError: 'INVALID_POLICY: policy requires at least one non-empty classifier tag' })
    expect(wrapper.get('[data-testid="slot-error"]').text()).toContain('INVALID_POLICY')
  })

  it('disables save-and-generate while the policy is locally incomplete', async () => {
    const wrapper = mountEditor({ draft: emptyDraft })
    expect(wrapper.get('[data-testid="save-and-generate"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('配置不完整')

    // Completing the policy enables generation.
    await wrapper.get('[data-testid="tag-input"]').setValue('SEなし')
    await wrapper.get('[data-testid="tag-input"]').trigger('keydown', { key: 'Enter' })
    await wrapper.get('[data-testid="codec-matched-lossless"]').setValue('wav')
    await wrapper.get('[data-testid="codec-unmatched-encoded"]').setValue('mp3')
    expect(wrapper.get('[data-testid="save-and-generate"]').attributes('disabled')).toBeUndefined()
  })

  it('renders inline draft save failures', () => {
    const wrapper = mountEditor({ saveError: 'policy requires at least one non-empty classifier tag' })
    expect(wrapper.get('[data-testid="draft-save-error"]').text()).toContain('classifier tag')
  })
})
