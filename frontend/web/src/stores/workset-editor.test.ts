import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import type { OperationDraftDocument } from '@/lib/api/types'
import { useWorksetEditorStore } from './workset-editor'

const base: OperationDraftDocument = {
  schema_version: 1,
  mode: 'available_sources',
  classifier_tags: ['A'],
  matched: { lossless: { codec: 'wav' } },
  unmatched: { encoded: { codec: 'mp3', quality: { kind: 'bitrate', bitrate: 320 } } },
  members: [],
}

const FLAC = { lossless: { codec: 'flac' } }

function openMember(store: ReturnType<typeof useWorksetEditorStore>, memberId: string): boolean {
  return store.open({
    worksetId: 'ws-1',
    operation: 'conversion',
    target: { kind: 'member', memberId },
    baseVersion: 3,
    baseDocument: base,
  })
}

describe('editor session', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('builds the pending document from base plus intents', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.setUnit('matched', { intent: 'set', value: FLAC })

    expect(store.pendingDocument?.members).toEqual([{ member_id: 'm-1', overrides: { matched: FLAC } }])
    expect(store.editedUnitCount).toBe(1)
    expect(store.isDirty).toBe(true)
  })

  it('does not mutate the persisted base document', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.setUnit('matched', { intent: 'set', value: FLAC })

    expect(store.session?.baseDocument.members).toEqual([])
    expect(base.members).toEqual([])
  })

  it('refuses a different target while edits are unapplied', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.setUnit('matched', { intent: 'set', value: FLAC })

    expect(openMember(store, 'm-2')).toBe(false)
    expect(store.session?.target).toEqual({ kind: 'member', memberId: 'm-1' })
    expect(store.conflictNotices).toBe(1)
  })

  it('allows re-opening the same target and switching after applying', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.setUnit('matched', { intent: 'set', value: FLAC })
    expect(openMember(store, 'm-1')).toBe(true)

    store.markApplied(store.pendingDocument ?? base, 4)
    expect(store.isDirty).toBe(false)
    expect(openMember(store, 'm-2')).toBe(true)
  })

  it('keeps the base version until an apply confirms the new one', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    expect(store.session?.baseVersion).toBe(3)
    store.markApplied(base, 4)
    expect(store.session?.baseVersion).toBe(4)
  })

  it('flags a conflict without dropping the content', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.setUnit('classifier_tags', { intent: 'set', value: [] })
    store.markStale()

    expect(store.session?.error).toContain('重新加载')
    expect(store.pendingDocument?.members).toEqual([{ member_id: 'm-1', overrides: { classifier_tags: [] } }])
  })

  it('clears the session on explicit discard only', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.setUnit('mode', { intent: 'inherit' })
    expect(store.session).not.toBeNull()
    store.close()
    expect(store.session).toBeNull()
  })
})
