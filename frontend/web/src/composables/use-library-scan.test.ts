import { describe, expect, it } from 'vitest'
import type { Folder, Library, PlanInfo, PlanResponse, TreeNode } from '@/lib/api/types'
import { queryKeys } from '@/queries/query-keys'
import { createTestQueryClient } from '@/test/query-client'
import { syncAfterScan } from './use-library-scan'

const library: Library = {
  id: 'lib-a',
  name: 'Archive',
  root_path: 'D:\\Music',
  created_at: '',
  updated_at: '',
  last_scan_at: null,
  last_scan_status: '',
  last_scan_error: '',
}

const folders: Folder[] = [
  { id: 'folder-a', name: 'Alpha', path: 'D:\\Music\\Alpha', relative_path: 'Alpha', audio_file_count: 1 },
]

const tree: TreeNode = {
  name: 'Alpha',
  path: 'D:\\Music\\Alpha',
  type: 'dir',
  format: '',
  bitrate: null,
  children: [],
}

const planInfo: PlanInfo = {
  plan_id: 'plan-1',
  root_path: 'D:\\Music',
  plan_type: 'slim',
  status: 'planned',
  created_at: '2026-08-22T00:00:00Z',
}

const planResponse: PlanResponse = {
  plan_id: 'plan-1',
  snapshot_token: 'snap-1',
  root_path: 'D:\\Music',
  summary: {
    operation_count: 0,
    error_count: 0,
    total_count: 0,
    actionable_count: 0,
    summary_reason: 'ACTIONABLE',
  },
  operations: [],
  errors: [],
  successful_folders: [],
}

function seedLibraryDomain(client: ReturnType<typeof createTestQueryClient>) {
  client.setQueryData(queryKeys.libraries.list(), [library])
  client.setQueryData(queryKeys.libraries.folders('lib-a', 'd:/music'), folders)
  client.setQueryData(queryKeys.libraries.tree('lib-a', 'd:/music', 'folder-a'), tree)
}

function seedPlans(client: ReturnType<typeof createTestQueryClient>) {
  client.setQueryData(queryKeys.plans.list('lib-a', 100), [planInfo])
  client.setQueryData(queryKeys.plans.detail('plan-1'), planResponse)
}

function plansUntouched(client: ReturnType<typeof createTestQueryClient>): boolean {
  return (
    client.getQueryData(queryKeys.plans.list('lib-a', 100)) !== undefined &&
    client.getQueryData(queryKeys.plans.detail('plan-1')) !== undefined
  )
}

describe('syncAfterScan terminal cache matrix', () => {
  it('completed refreshes library metadata + derived caches and never touches plan keys', async () => {
    const client = createTestQueryClient()
    seedLibraryDomain(client)
    seedPlans(client)
    const foldersKey = queryKeys.libraries.folders('lib-a', 'd:/music')
    const treeKey = queryKeys.libraries.tree('lib-a', 'd:/music', 'folder-a')

    await syncAfterScan(client, 'lib-a', 'completed', 'event')

    // Inactive derived entries are dropped…
    expect(client.getQueryData(foldersKey)).toBeUndefined()
    expect(client.getQueryData(treeKey)).toBeUndefined()
    // …the library list is invalidated for metadata refresh…
    expect(client.getQueryState(queryKeys.libraries.list())?.isInvalidated).toBe(true)
    // …and plans are completely untouched.
    expect(plansUntouched(client)).toBe(true)
  })

  it('confirmed cancel/error over SSE only refreshes library metadata', async () => {
    const client = createTestQueryClient()
    seedLibraryDomain(client)
    seedPlans(client)
    const foldersKey = queryKeys.libraries.folders('lib-a', 'd:/music')
    const treeKey = queryKeys.libraries.tree('lib-a', 'd:/music', 'folder-a')

    await syncAfterScan(client, 'lib-a', 'cancelled', 'event')

    expect(client.getQueryData(foldersKey)).toEqual(folders)
    expect(client.getQueryData(treeKey)).toEqual(tree)
    expect(client.getQueryState(queryKeys.libraries.list())?.isInvalidated).toBe(true)
    expect(plansUntouched(client)).toBe(true)
  })

  it('transport failure conservatively refreshes derived caches but never plans', async () => {
    const client = createTestQueryClient()
    seedLibraryDomain(client)
    seedPlans(client)
    const foldersKey = queryKeys.libraries.folders('lib-a', 'd:/music')
    const treeKey = queryKeys.libraries.tree('lib-a', 'd:/music', 'folder-a')

    await syncAfterScan(client, 'lib-a', 'error', 'transport')

    expect(client.getQueryData(foldersKey)).toBeUndefined()
    expect(client.getQueryData(treeKey)).toBeUndefined()
    expect(client.getQueryState(queryKeys.libraries.list())?.isInvalidated).toBe(true)
    expect(plansUntouched(client)).toBe(true)
  })

  it('transport failure before the first SSE event skips the derived refresh', async () => {
    const client = createTestQueryClient()
    seedLibraryDomain(client)
    seedPlans(client)
    const foldersKey = queryKeys.libraries.folders('lib-a', 'd:/music')
    const treeKey = queryKeys.libraries.tree('lib-a', 'd:/music', 'folder-a')

    // The POST /scans request was rejected before any event arrived: the
    // scan never started, so folders/trees cannot have changed.
    await syncAfterScan(client, 'lib-a', 'error', 'transport', false)

    expect(client.getQueryData(foldersKey)).toEqual(folders)
    expect(client.getQueryData(treeKey)).toEqual(tree)
    expect(client.getQueryState(queryKeys.libraries.list())?.isInvalidated).toBe(true)
    expect(plansUntouched(client)).toBe(true)
  })

  it('user cancel is metadata-only even when the stream had started', async () => {
    const client = createTestQueryClient()
    seedLibraryDomain(client)
    seedPlans(client)
    const foldersKey = queryKeys.libraries.folders('lib-a', 'd:/music')
    const treeKey = queryKeys.libraries.tree('lib-a', 'd:/music', 'folder-a')

    // Cancel aborts the stream (transport terminal) after a 'started' event:
    // materialized folders are replaced in one transaction only on completion,
    // so nothing changed — the derived caches must not be dropped or refetched.
    await syncAfterScan(client, 'lib-a', 'cancelled', 'transport', true)

    expect(client.getQueryData(foldersKey)).toEqual(folders)
    expect(client.getQueryData(treeKey)).toEqual(tree)
    expect(client.getQueryState(queryKeys.libraries.list())?.isInvalidated).toBe(true)
    expect(plansUntouched(client)).toBe(true)
  })
})