import { defineStore } from 'pinia'
import type { DeleteMode, OperationDraftDocument, OperationType, OverrideUnit } from '@/lib/api/types'
import {
  applyIntent,
  EMPTY_INTENT,
  intentIsEmpty,
  intentUnitCount,
  sameDocument,
  type EditIntent,
  type EditTarget,
} from '@/features/worksets/draft-intents'

/**
 * The workbench's single active edit session (E02-E04).
 *
 * The session owns everything that must survive collapsing the editor,
 * returning to the list, or switching carriers at a container breakpoint:
 * the target, the frozen batch name list, the base document and operation
 * version it was opened against, and the field intents that have not been
 * applied yet. It deliberately holds no server state — the persisted draft
 * lives in Vue Query, and applying an intent builds a new document from the
 * base without mutating either.
 */
export interface EditSession {
  worksetId: string
  operation: OperationType
  target: EditTarget
  /** Operation version the session was opened against (the If-Match basis). */
  baseVersion: number
  /** Persisted document the session started from; intents apply to this. */
  baseDocument: OperationDraftDocument
  intent: EditIntent
  /** Set when the session began from a historical (read-only) revision. */
  readOnly: boolean
  applying: boolean
  error: string | null
}

export const useWorksetEditorStore = defineStore('workset-editor', {
  state: () => ({
    session: null as EditSession | null,
    /** How many distinct targets asked to open while one session was active. */
    conflictNotices: 0,
  }),
  getters: {
    active: (state) => state.session,
    isDirty: (state) => (state.session ? !intentIsEmpty(state.session.intent) : false),
    /** The document a save would send: base + intents, never the live draft. */
    pendingDocument(state): OperationDraftDocument | null {
      if (!state.session) return null
      return applyIntent(state.session.baseDocument, state.session.target, state.session.intent)
    },
    editedUnitCount(): number {
      if (!this.session) return 0
      return intentUnitCount(this.session.intent)
    },
  },
  actions: {
    /**
     * Opens a session. Returns false when another target already has
     * unapplied edits — the caller must ask the user to discard or continue
     * rather than silently switching the edit target (E04).
     */
    open(input: {
      worksetId: string
      operation: OperationType
      target: EditTarget
      baseVersion: number
      baseDocument: OperationDraftDocument
      readOnly?: boolean
    }): boolean {
      if (this.session && !intentIsEmpty(this.session.intent) && !sameTarget(this.session.target, input.target)) {
        this.conflictNotices++
        return false
      }
      this.session = {
        worksetId: input.worksetId,
        operation: input.operation,
        target: input.target,
        baseVersion: input.baseVersion,
        baseDocument: input.baseDocument,
        intent: { ...EMPTY_INTENT, units: {} },
        readOnly: input.readOnly ?? false,
        applying: false,
        error: null,
      }
      return true
    },
    setUnit(unit: OverrideUnit, next: EditIntent['units'][OverrideUnit]) {
      if (!this.session) return
      this.session.intent = {
        ...this.session.intent,
        units: { ...this.session.intent.units, [unit]: next },
      }
    },
    /** The obsolete-audio handling: a whole-operation choice, common only. */
    setDeleteMode(value: DeleteMode) {
      if (!this.session) return
      this.session.intent = { ...this.session.intent, deleteMode: { intent: 'set', value } }
    },
    setParticipation(participation: EditIntent['participation']) {
      if (!this.session) return
      this.session.intent = { ...this.session.intent, participation }
    },
    startApplying() {
      if (!this.session) return
      this.session.applying = true
      this.session.error = null
    },
    /** Successful save: the base becomes the saved document, intents clear. */
    markApplied(document?: OperationDraftDocument, version?: number) {
      if (!this.session) return
      this.session.intent = { ...EMPTY_INTENT, units: {} }
      this.session.applying = false
      if (document) this.session.baseDocument = document
      if (version !== undefined) this.session.baseVersion = version
    },
    /** A refused save is answered by its cause, never one blanket banner. */
    failSave(error: { code?: string; message?: string }) {
      if (!this.session) return
      this.session.applying = false
      this.session.error = saveFailureMessage(error)
    },
    /**
     * The server copy moved. A version that advanced without the document
     * changing — a generation publication — is not a conflict: re-base silently
     * so the next save echoes the current If-Match. A genuinely moved document
     * is adopted only while nothing is unapplied; a dirty session keeps its
     * edits, and the save reports the conflict.
     */
    syncWithServer(
      input: { worksetId: string; operation: OperationType },
      server: { version: number; document: OperationDraftDocument },
    ) {
      const session = this.session
      if (!session || session.worksetId !== input.worksetId || session.operation !== input.operation) return
      const versionMoved = server.version !== session.baseVersion
      const documentMoved = !sameDocument(server.document, session.baseDocument)
      if (!versionMoved && !documentMoved) return
      if (documentMoved && !intentIsEmpty(session.intent)) return
      session.baseVersion = server.version
      if (documentMoved) session.baseDocument = server.document
      // A moved server copy retires both a stale-conflict banner and the
      // "wait for the generation" advice: neither cause is true any more.
      session.error = null
    },
    /** Discard: only an explicit user action drops unapplied edits (E08). */
    close() {
      this.session = null
    },
    /** Server data moved under a dirty session: keep content, flag conflict. */
    markStale() {
      if (!this.session) return
      this.session.applying = false
      this.session.error = '服务端草稿已更新：请放弃本地修改后重新进入以加载服务器版本'
    },
  },
})

/** A busy operation is a wait, not a reload: the code decides the text. */
const BUSY_SAVE_MESSAGES: Record<string, string> = {
  GENERATION_IN_PROGRESS: '正在生成计划版本：请等待生成结束或取消后再应用设置',
  EXECUTION_IN_PROGRESS: '正在执行：请等待执行结束或取消后再应用设置',
}

function saveFailureMessage(error: { code?: string; message?: string }): string {
  const mapped = error.code ? BUSY_SAVE_MESSAGES[error.code] : undefined
  return mapped ?? error.message ?? '保存草稿失败'
}

function sameTarget(a: EditTarget, b: EditTarget): boolean {
  if (a.kind !== b.kind) return false
  if (a.kind === 'member' && b.kind === 'member') return a.memberId === b.memberId
  if (a.kind === 'batch' && b.kind === 'batch') {
    return a.memberIds.length === b.memberIds.length && a.memberIds.every((id, i) => id === b.memberIds[i])
  }
  return true
}
