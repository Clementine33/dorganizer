import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ApiClientContract, ScanEvent } from '@/lib/api/types'
import { useScanStore } from './scan'

async function* events(items: ScanEvent[]): AsyncGenerator<ScanEvent> {
  for (const item of items) yield item
}

function apiWithScan(scanLibrary: ApiClientContract['scanLibrary']): ApiClientContract {
  return {
    getHealth: vi.fn(),
    listLibraries: vi.fn(),
    getLibrary: vi.fn(),
    createLibrary: vi.fn(),
    updateLibrary: vi.fn(),
    deleteLibrary: vi.fn(),
    scanLibrary,
    listFolders: vi.fn(),
    getFolderTree: vi.fn(),
    getPlan: vi.fn(),
    createPlan: vi.fn(),
    listPlans: vi.fn(),
  }
}

describe('scan store', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('tracks start and progress before marking folders for refresh on completion', async () => {
    const api = apiWithScan(
      vi.fn(() =>
        events([
          { type: 'started', data: { stage: 'scan', message: 'Scanning /music' } },
          { type: 'progress', data: { stage: 'scan', files_scanned: 8, dirs_scanned: 2 } },
          {
            type: 'completed',
            data: { stage: 'scan', scan_id: 'scan-1', root_path: '/music', files_scanned: 8 },
          },
        ]),
      ),
    )
    const store = useScanStore()

    await store.start('lib-a', api)

    expect(store.status).toBe('completed')
    expect(store.filesScanned).toBe(8)
    expect(store.dirsScanned).toBe(2)
    expect(store.scanId).toBe('scan-1')
    expect(store.rootPath).toBe('/music')
    expect(store.foldersRefreshNeeded).toBe(true)
  })

  it('aborts an active scan and settles as cancelled', async () => {
    const scanLibrary = vi.fn((_id: string, signal: AbortSignal) => ({
      async *[Symbol.asyncIterator]() {
        yield { type: 'started', data: { stage: 'scan' } } as ScanEvent
        if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
        await new Promise<void>((_resolve, reject) => {
          signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), {
            once: true,
          })
        })
      },
    }))
    const store = useScanStore()

    const pending = store.start('lib-a', apiWithScan(scanLibrary))
    await vi.waitFor(() => expect(store.status).toBe('scanning'))
    store.cancel()
    await pending

    expect(store.status).toBe('cancelled')
    expect(scanLibrary.mock.calls[0]?.[1].aborted).toBe(true)
  })

  it('records the backend code and message from an error event', async () => {
    const api = apiWithScan(
      vi.fn(() =>
        events([
          {
            type: 'error',
            data: { stage: 'scan', code: 'SCAN_FAILED', message: 'Unreadable folder' },
          },
        ]),
      ),
    )
    const store = useScanStore()

    await store.start('lib-a', api)

    expect(store.status).toBe('error')
    expect(store.errorCode).toBe('SCAN_FAILED')
    expect(store.errorMessage).toBe('Unreadable folder')
    expect(store.foldersRefreshNeeded).toBe(false)
  })
})
