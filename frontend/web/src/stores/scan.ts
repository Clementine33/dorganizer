import { defineStore } from 'pinia'
import { markRaw } from 'vue'
import { ApiError } from '@/lib/api/client'
import type { ApiClientContract, ScanEvent } from '@/lib/api/types'

export type ScanStatus = 'idle' | 'scanning' | 'completed' | 'cancelled' | 'error'

// How the terminal state was reached. `event` means the backend confirmed it
// over SSE (folders/trees stay valid); `transport` means the stream ended or
// aborted without proof of what the backend committed (derived caches must be
// conservatively refreshed by the scan orchestration).
export type ScanTerminal = 'event' | 'transport'

export const useScanStore = defineStore('scan', {
  state: () => ({
    status: 'idle' as ScanStatus,
    terminal: null as ScanTerminal | null,
    libraryId: null as string | null,
    filesScanned: 0,
    dirsScanned: 0,
    scanId: null as string | null,
    rootPath: null as string | null,
    message: '',
    errorCode: null as string | null,
    errorMessage: null as string | null,
    controller: null as AbortController | null,
  }),
  actions: {
    async start(libraryId: string, client: ApiClientContract) {
      if (this.status === 'scanning') return
      const controller = markRaw(new AbortController())
      this.controller = controller
      this.status = 'scanning'
      this.libraryId = libraryId
      this.filesScanned = 0
      this.dirsScanned = 0
      this.scanId = null
      this.rootPath = null
      this.message = ''
      this.errorCode = null
      this.errorMessage = null
      this.terminal = null

      try {
        for await (const event of client.scanLibrary(libraryId, controller.signal)) {
          // A reset() or a newer scan superseded this request — never let
          // its events overwrite the current scan state.
          if (this.controller !== controller) return
          this.applyEvent(event)
        }
        if (this.controller !== controller) return
        if (this.status === 'scanning') {
          this.status = 'error'
          this.terminal = 'transport'
          this.errorCode = 'STREAM_ENDED'
          this.errorMessage = '扫描连接提前结束，请重试。'
        }
      } catch (error) {
        if (this.controller !== controller) return
        this.terminal = 'transport'
        if (controller.signal.aborted || (error instanceof DOMException && error.name === 'AbortError')) {
          this.status = 'cancelled'
          this.message = '扫描已取消'
        } else {
          this.status = 'error'
          this.errorCode = error instanceof ApiError ? error.code : 'STREAM_ERROR'
          this.errorMessage = error instanceof Error ? error.message : '扫描连接失败，请重试。'
        }
      } finally {
        if (this.controller === controller) this.controller = null
      }
    },
    applyEvent(event: ScanEvent) {
      const data = event.data
      if (event.type === 'started') {
        this.status = 'scanning'
        this.message = data.message ?? ''
      } else if (event.type === 'progress') {
        this.filesScanned = data.files_scanned ?? this.filesScanned
        this.dirsScanned = data.dirs_scanned ?? this.dirsScanned
      } else if (event.type === 'completed') {
        this.status = 'completed'
        this.terminal = 'event'
        this.filesScanned = data.files_scanned ?? this.filesScanned
        this.scanId = data.scan_id ?? null
        this.rootPath = data.root_path ?? null
      } else if (event.type === 'cancelled') {
        this.status = 'cancelled'
        this.terminal = 'event'
        this.message = data.message ?? '扫描已取消'
      } else if (event.type === 'error') {
        this.status = 'error'
        this.terminal = 'event'
        this.errorCode = data.code ?? 'SCAN_ERROR'
        this.errorMessage = data.message ?? '扫描失败，请重试。'
      }
    },
    cancel() {
      this.controller?.abort()
    },
    reset() {
      this.controller?.abort()
      this.$reset()
    },
  },
})
