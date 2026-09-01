import { vi } from 'vitest'
import type { ApiClientContract } from '@/lib/api/types'

// Full-contract ApiClient stub factory. Every method is a vi.fn() that
// resolves to a sensible neutral default; tests override per method via
// `overrides`. Keeping the factory in one place means a new ApiClientContract
// method only needs to be added here instead of in every test file.
export function apiStub(overrides: Partial<ApiClientContract> = {}): ApiClientContract {
  const base = {
    getHealth: vi.fn().mockResolvedValue({ ok: true, version: 'test' }),
    listLibraries: vi.fn().mockResolvedValue([]),
    getLibrary: vi.fn(),
    createLibrary: vi.fn(),
    updateLibrary: vi.fn(),
    deleteLibrary: vi.fn().mockResolvedValue(undefined),
    scanLibrary: vi.fn(),
    listFolders: vi.fn().mockResolvedValue([]),
    getFolderTree: vi.fn(),
    listPolicySlots: vi.fn().mockResolvedValue([
      { slot: 1, name: '', policy: null },
      { slot: 2, name: '', policy: null },
      { slot: 3, name: '', policy: null },
    ]),
    savePolicySlot: vi.fn(),
    listClassifierTags: vi.fn().mockResolvedValue({ default_tags: [], custom_tags: [] }),
    addClassifierTag: vi.fn(),
    deleteClassifierTag: vi.fn().mockResolvedValue(undefined),
    createWorkset: vi.fn(),
    listWorksets: vi.fn().mockResolvedValue({ worksets: [], next_cursor: undefined }),
    getWorkset: vi.fn(),
    getWorksetDraft: vi.fn(),
    saveWorksetDraft: vi.fn(),
    startGeneration: vi.fn(),
    getGeneration: vi.fn(),
    cancelGeneration: vi.fn(),
    streamGenerationEvents: vi.fn(),
    listRevisions: vi.fn().mockResolvedValue([]),
    getRevision: vi.fn(),
  } as ApiClientContract
  return Object.assign(base, overrides)
}
