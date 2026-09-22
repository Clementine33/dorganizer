import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import { apiClientKey } from '@/lib/api/client'
import type { ApiClientContract, Operation, OperationDraftDocument, RevisionDetailResponse, Workset } from '@/lib/api/types'
import { apiStub } from '@/test/api-stub'
import { installTestQueryPlugin } from '@/test/query-client'
import { useWorksetEditorStore } from '@/stores/workset-editor'
import MemberEditPage from './MemberEditPage.vue'

/**
 * Entering one member's editor is a decision when another target still holds
 * unapplied edits: the page must ask, never silently bounce the user back
 * to the list (the reported "修改此文件夹 does nothing").
 */
const workset: Workset = {
  workset_id: 'ws-1',
  title: 'T',
  version: 5,
  library: { library_id: 'lib-a', name: 'Lib', root_path: '/music' },
  members: [
    { member_id: 'm-1', folder_path: '/music/a', folder_name: 'a', rel_path: 'a', dir_id: 'd-a' },
    { member_id: 'm-2', folder_path: '/music/b', folder_name: 'b', rel_path: 'b', dir_id: 'd-b' },
  ],
  operations: [],
  updated_at: '',
  created_at: '',
}

const operation: Operation = {
  workset_id: 'ws-1',
  operation_type: 'conversion',
  version: 5,
  planning_state: 'planned',
  current_revision: null,
  active_generation: null,
  latest_generation: null,
  active_execution: null,
  latest_execution: null,
}

const revision = {
  plan_id: 'plan-1',
  revision_index: 1,
  created_at: '',
  root_path: '/music',
  snapshot_token: 'tok',
  status: 'ready',
  summary: { operation_count: 0, error_count: 0, total_count: 0, actionable_count: 0, summary_reason: '' },
  task: { kind: 'conversion', schema_version: 1, payload: { policy: {}, policy_hash: '', classifier: {}, summary: { component_count: 0, blocked_count: 0, operation_count: 0, error_count: 0, summary_reason: '' }, components: [] } },
  counts: { members: 2, changed: 0, unmet_targets: 0, blocked: 0, unchanged: 0 },
  members: [],
  roots: [],
  component_roots: [],
  execution: null,
} as unknown as RevisionDetailResponse

const document: OperationDraftDocument = {
  schema_version: 1,
  mode: 'strict',
  classifier_tags: ['A'],
  matched: {},
  unmatched: {},
  members: [],
}

function pageApi(): ApiClientContract {
  return apiStub({
    getCurrentRecord: vi.fn().mockResolvedValue({ workset }),
    getWorkset: vi.fn().mockResolvedValue(workset),
    getOperation: vi.fn().mockResolvedValue(operation),
    getOperationDraft: vi.fn().mockResolvedValue({
      workset_id: 'ws-1',
      operation_type: 'conversion',
      version: 5,
      schema_version: 1,
      updated_at: '',
      document,
    }),
    getRevision: vi.fn().mockResolvedValue(revision),
    listClassifierTags: vi.fn().mockResolvedValue({ default_tags: [], custom_tags: [] }),
  } as never)
}

function seedPendingCommonEdit(pinia: ReturnType<typeof createPinia>): void {
  const editor = useWorksetEditorStore(pinia)
  editor.open({
    worksetId: 'ws-1',
    operation: 'conversion',
    target: { kind: 'common' },
    baseVersion: 5,
    baseDocument: document,
  })
  editor.setUnit('mode', { intent: 'set', value: 'available_sources' })
}

async function mountEditMember(seedPending: boolean): Promise<{ wrapper: VueWrapper; router: Router }> {
  const pinia = createPinia()
  if (seedPending) seedPendingCommonEdit(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/worksets/:libraryId/conversion', name: 'conversion', component: { template: '<div />' } },
      { path: '/worksets/:libraryId/conversion/settings', name: 'conversion-settings', component: { template: '<div />' } },
      { path: '/worksets/:libraryId/conversion/batch-edit', name: 'conversion-batch-edit', component: { template: '<div />' } },
      { path: '/worksets/:libraryId/conversion/:memberId/edit', name: 'conversion-member-edit', component: MemberEditPage },
      { path: '/worksets/:libraryId/conversion/:memberId', name: 'conversion-member', component: { template: '<div />' } },
    ],
  })
  await router.push('/worksets/lib-a/conversion/m-1/edit')
  await router.isReady()
  const wrapper = mount(MemberEditPage, {
    global: { plugins: [pinia, router, installTestQueryPlugin()], provide: { [apiClientKey as symbol]: pageApi() } },
  })
  await flushPromises()
  return { wrapper, router }
}

afterEach(() => vi.restoreAllMocks())

describe('MemberEditPage pending-edit conflict', () => {
  it('opens this member without asking when nothing else is pending', async () => {
    const confirm = vi.spyOn(window, 'confirm')
    const { router } = await mountEditMember(false)

    expect(confirm).not.toHaveBeenCalled()
    expect(useWorksetEditorStore().session?.target).toEqual({ kind: 'member', memberId: 'm-1' })
    expect(router.currentRoute.value.name).toBe('conversion-member-edit')
  })

  it('drops the other edit and opens this member when the user accepts', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const { router } = await mountEditMember(true)

    expect(confirm).toHaveBeenCalledOnce()
    expect(useWorksetEditorStore().session?.target).toEqual({ kind: 'member', memberId: 'm-1' })
    expect(router.currentRoute.value.name).toBe('conversion-member-edit')
  })

  it('keeps the other edit and returns to the list only when the user declines', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const { router } = await mountEditMember(true)

    expect(confirm).toHaveBeenCalledOnce()
    expect(useWorksetEditorStore().session?.target).toEqual({ kind: 'common' })
    expect(router.currentRoute.value.name).toBe('conversion')
  })
})