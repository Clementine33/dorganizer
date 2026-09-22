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

  it('does not count an intent that lands on the persisted value as an edit', () => {
    const store = useWorksetEditorStore()
    store.open({
      worksetId: 'ws-1',
      operation: 'conversion',
      target: { kind: 'common' },
      baseVersion: 3,
      baseDocument: base,
    })

    // Re-picking the value that is already saved records an intent, but it
    // changes nothing — it must not read as dirty (C09) or block a switch.
    store.setUnit('mode', { intent: 'set', value: base.mode })
    store.setUnit('matched', { intent: 'set', value: base.matched })

    expect(store.isDirty).toBe(false)
    expect(openMember(store, 'm-1')).toBe(true)
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

  it('flags a conflict without dropping the content, and leaves the form usable', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.setUnit('classifier_tags', { intent: 'set', value: [] })
    store.startApplying()
    store.markStale()

    expect(store.session?.error).toContain('放弃本地修改')
    expect(store.session?.applying).toBe(false)
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

describe('server draft reconciliation', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  const scope = { worksetId: 'ws-1', operation: 'conversion' } as const

  it('re-bases the version when a generation advanced it without touching the draft', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.setUnit('matched', { intent: 'set', value: FLAC })

    store.syncWithServer(scope, { version: 6, document: base })

    expect(store.session?.baseVersion).toBe(6)
    expect(store.isDirty).toBe(true)
    expect(store.pendingDocument?.members).toEqual([{ member_id: 'm-1', overrides: { matched: FLAC } }])
  })

  it('adopts a genuinely moved document while nothing is unapplied', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')

    store.syncWithServer(scope, { version: 6, document: { ...base, classifier_tags: ['B'] } })

    expect(store.session?.baseVersion).toBe(6)
    expect(store.session?.baseDocument.classifier_tags).toEqual(['B'])
  })

  it('keeps pending edits against a moved document, for the save to report', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.setUnit('matched', { intent: 'set', value: FLAC })

    store.syncWithServer(scope, { version: 6, document: { ...base, classifier_tags: ['B'] } })

    expect(store.session?.baseVersion).toBe(3)
    expect(store.session?.baseDocument.classifier_tags).toEqual(['A'])
  })

  it('ignores another workset or operation', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')

    store.syncWithServer({ worksetId: 'ws-9', operation: 'conversion' }, { version: 6, document: base })

    expect(store.session?.baseVersion).toBe(3)
  })

  it('retires a stale banner once the server copy moved', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.markStale()

    store.syncWithServer(scope, { version: 6, document: base })

    expect(store.session?.error).toBeNull()
  })
})

describe('refused save', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('names the cause: a busy operation is a wait, not a reload', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.startApplying()
    store.failSave({ code: 'GENERATION_IN_PROGRESS', message: 'cancel or wait for the active generation' })

    expect(store.session?.applying).toBe(false)
    expect(store.session?.error).toContain('正在生成')
    expect(store.session?.error).not.toContain('放弃本地修改')
  })

  it('falls back to the server message and never loses the edits', () => {
    const store = useWorksetEditorStore()
    openMember(store, 'm-1')
    store.setUnit('mode', { intent: 'set', value: 'strict' })
    store.failSave({ message: 'invalid JSON payload' })

    expect(store.session?.error).toBe('invalid JSON payload')
    expect(store.isDirty).toBe(true)
  })
})
