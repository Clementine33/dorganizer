import { defineStore } from 'pinia'
import { markRaw } from 'vue'

/**
 * UI-only workbench state: selection, filters, the batch name list and the
 * history selector. Never holds server data and never holds the edit session
 * content — that lives in the editor store so a carrier switch cannot lose it.
 *
 * Route and edit intent are the source of truth for what is being edited; the
 * selected-member count is NEVER used to infer an edit target (R05).
 */
export type MemberFilter = 'all' | 'change' | 'warn' | 'blocked' | 'excluded'

export const useWorksetUiStore = defineStore('workset-ui', {
  state: () => ({
    /** Checkbox set; batch actions act on this explicit selection only. */
    selectedMemberIds: markRaw(new Set<string>()),
    /** Last filtered-out selection count, shown while a filter hides rows. */
    hiddenSelectedCount: 0,
    filter: 'all' as MemberFilter,
    search: '' as string,
    /** Historical revision being reviewed; null = current draft/plan. */
    historyPlanId: null as string | null,
    /** Restored scroll offset of the list, per workset. */
    listScrollTop: 0,
    /** Frozen batch name list for the batch-edit route (E02). */
    batchMemberIds: [] as string[],
  }),
  getters: {
    selectionCount: (state) => state.selectedMemberIds.size,
    isHistorical: (state) => state.historyPlanId !== null,
  },
  actions: {
    toggleMember(id: string) {
      const next = new Set(this.selectedMemberIds)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      this.selectedMemberIds = markRaw(next)
    },
    toggleAllVisible(ids: string[]) {
      const all = ids.length > 0 && ids.every((id) => this.selectedMemberIds.has(id))
      const next = new Set(this.selectedMemberIds)
      for (const id of ids) {
        if (all) next.delete(id)
        else next.add(id)
      }
      this.selectedMemberIds = markRaw(next)
    },
    clearSelection() {
      this.selectedMemberIds = markRaw(new Set())
    },
    setFilter(filter: MemberFilter) {
      this.filter = filter
    },
    setSearch(search: string) {
      this.search = search
    },
    setHiddenSelectedCount(count: number) {
      this.hiddenSelectedCount = count
    },
    /** Copies the checked names: later list changes never alter the batch. */
    freezeBatchList() {
      this.batchMemberIds = [...this.selectedMemberIds]
    },
    clearBatchList() {
      this.batchMemberIds = []
    },
    selectRevision(planId: string | null) {
      this.historyPlanId = planId
    },
    /** Switching operation or workset never carries a name list across. */
    resetForOperation() {
      this.selectedMemberIds = markRaw(new Set())
      this.hiddenSelectedCount = 0
      this.filter = 'all'
      this.search = ''
      this.historyPlanId = null
      this.batchMemberIds = []
      this.listScrollTop = 0
    },
  },
})
