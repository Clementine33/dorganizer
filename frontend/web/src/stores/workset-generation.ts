import { defineStore } from 'pinia'
import { markRaw } from 'vue'
import { ApiError } from '@/lib/api/client'
import type { ApiClientContract, GenerationEvent } from '@/lib/api/types'

export type GenerationRunStatus =
  | 'idle'
  | 'streaming'
  | 'completed'
  | 'failed'
  | 'canceled'
  | 'interrupted'
  | 'error'

// How the terminal was reached. `event` means the backend confirmed the
// outcome over SSE (a revision may have been created); `transport` means the
// stream ended or aborted without a terminal event — the backend state is
// unknown and caches must be conservatively swept.
export type GenerationTerminal = 'event' | 'transport'

// Transient streaming state of the planning session the user is watching.
// This mirrors the scan store exception in AGENTS.md: a live process lifecycle
// (SSE), not a cacheable server resource. Cache coordination lives in
// use-workset-generation.
export const useWorksetGenerationStore = defineStore('workset-generation', {
  state: () => ({
    status: 'idle' as GenerationRunStatus,
    terminal: null as GenerationTerminal | null,
    worksetId: null as string | null,
    generationId: null as string | null,
    statusText: '' as string, // backend session status (queued/running/…)
    totalRoots: 0,
    completedRoots: 0,
    currentRoot: '' as string,
    errorCount: 0,
    revisionId: null as string | null,
    errorCode: null as string | null,
    errorMessage: null as string | null,
    // True once any SSE event was applied. A transport terminal without any
    // event means the stream never really started, so nothing committed and
    // caches must not be swept as if it had.
    receivedEvent: false,
    controller: null as AbortController | null,
  }),
  actions: {
    /**
     * Attaches the SSE stream for one session. Resolves to false when the
     * attach was refused because this singleton store is already streaming
     * another session — callers MUST NOT run terminal synchronization for a
     * refused attach (there is no terminal; sweeping here would feed the
     * detail-refetch → re-attach → sweep loop). Resolves true only for an
     * actually-attached stream that has now ended (event or transport).
     */
    async attach(worksetId: string, generationId: string, client: ApiClientContract): Promise<boolean> {
      if (this.status === 'streaming') return false
      const controller = markRaw(new AbortController())
      this.controller = controller
      this.status = 'streaming'
      this.worksetId = worksetId
      this.generationId = generationId
      this.terminal = null
      this.revisionId = null
      this.errorCode = null
      this.errorMessage = null
      this.receivedEvent = false

      try {
        for await (const event of client.streamGenerationEvents(worksetId, generationId, controller.signal)) {
          // A reset() or a newer session superseded this stream — never let
          // stale events overwrite the current state.
          if (this.controller !== controller) return true
          this.applyEvent(event as GenerationEvent)
          if (this.controller !== controller) return true
          if (this.status !== 'streaming') break // terminal event applied
        }
        if (this.controller !== controller) return true
        if (this.status === 'streaming') {
          // Stream ended without a terminal event: the backend outcome is
          // unknown (it may have completed just after the last event).
          this.status = 'error'
          this.terminal = 'transport'
          this.errorCode = 'STREAM_ENDED'
          this.errorMessage = '生成连接提前结束，请刷新查看结果。'
        }
      } catch (error) {
        if (this.controller !== controller) return true
        this.terminal = 'transport'
        if (controller.signal.aborted || (error instanceof DOMException && error.name === 'AbortError')) {
          this.status = 'error'
          this.errorCode = 'STREAM_ABORTED'
          this.errorMessage = '生成进度连接已断开。'
        } else {
          this.status = 'error'
          this.errorCode = error instanceof ApiError ? error.code : 'STREAM_ERROR'
          this.errorMessage = error instanceof Error ? error.message : '生成进度连接失败。'
        }
      } finally {
        if (this.controller === controller) this.controller = null
      }
      return true
    },
    applyEvent(event: GenerationEvent) {
      this.receivedEvent = true
      const data = event.data as Record<string, unknown>
      switch (event.type) {
        case 'session_snapshot': {
          this.statusText = String(data.status ?? '')
          this.totalRoots = Number(data.total_roots ?? this.totalRoots)
          this.completedRoots = Number(data.completed_roots ?? this.completedRoots)
          this.currentRoot = String(data.current_root ?? this.currentRoot)
          this.errorCount = Number(data.error_count ?? this.errorCount)
          break
        }
        case 'progress': {
          this.totalRoots = Number(data.total_roots ?? this.totalRoots)
          this.completedRoots = Number(data.completed_roots ?? this.completedRoots)
          this.currentRoot = String(data.current_root ?? this.currentRoot)
          this.errorCount = Number(data.error_count ?? this.errorCount)
          break
        }
        case 'completed': {
          this.status = 'completed'
          this.terminal = 'event'
          this.revisionId = String(data.revision_id ?? '')
          break
        }
        case 'failed': {
          this.status = 'failed'
          this.terminal = 'event'
          this.errorCode = String(data.error_code ?? 'GENERATION_FAILED')
          this.errorMessage = String(data.error_message ?? '生成失败')
          break
        }
        case 'canceled': {
          this.status = 'canceled'
          this.terminal = 'event'
          break
        }
        case 'interrupted': {
          this.status = 'interrupted'
          this.terminal = 'event'
          break
        }
        case 'error': {
          this.status = 'error'
          this.terminal = 'event'
          this.errorCode = String(data.code ?? 'INTERNAL')
          this.errorMessage = String(data.message ?? '生成事件流错误')
          break
        }
      }
    },
    /** Aborts the SSE stream only when it belongs to the given session. */
    cancel(worksetId: string, generationId: string) {
      if (this.worksetId === worksetId && this.generationId === generationId) {
        this.controller?.abort()
      }
    },
    reset() {
      this.controller?.abort()
      this.$reset()
    },
  },
})
