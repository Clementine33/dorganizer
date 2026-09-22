import { defineStore } from 'pinia'
import { markRaw } from 'vue'

/**
 * UI-only workbench state: selection, the search term and the batch name list.
 * The list's filter is not here: it is the one page parameter an address may
 * carry, so a filtered view can be linked (ADR 0003 §5). Never
 * holds server data and never holds the edit session content — that lives in
 * the editor store so a carrier switch cannot lose it.
 *
 * There is no historical-revision selector: a record keeps one plan, the
 * current one (ADR 0001 §2).
 *
 * Route and edit intent are the source of truth for what is being edited; the
 * selected-member count is NEVER used to infer an edit target.
 */
export const useWorksetUiStore = defineStore('workset-ui', {
  state: () => ({
    /** Checkbox set; batch actions act on this explicit selection only. */
    selectedMemberIds: markRaw(new Set<string>()),
    /** Last filtered-out selection count, shown while a filter hides rows. */
    hiddenSelectedCount: 0,
    search: '' as string,
    /** Restored scroll offset of the list, per workset. */
    listScrollTop: 0,
    /** Frozen batch name list for the batch-edit route. */
    batchMemberIds: [] as string[],
  }),
  getters: {
    selectionCount: (state) => state.selectedMemberIds.size,
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
    /** Switching operation or workset never carries a name list across. */
    resetForOperation() {
      this.selectedMemberIds = markRaw(new Set())
      this.hiddenSelectedCount = 0
      this.search = ''
      this.batchMemberIds = []
      this.listScrollTop = 0
    },
  },
})
