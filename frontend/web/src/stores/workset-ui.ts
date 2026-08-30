import { defineStore } from 'pinia'
import { markRaw } from 'vue'

// UI-only selection state for the workset workbench (never server data).
// openBatchIndexes is markRaw'd because it is a mutable Set, not reactive
// state — components replace it wholesale on change.
export const useWorksetUiStore = defineStore('workset-ui', {
  state: () => ({
    selectedWorksetId: null as string | null,
    selectedBatchIndex: null as number | null,
    selectedComponentId: null as string | null,
    openBatchIndexes: markRaw(new Set<number>()),
    batchSearch: '' as string,
    batchFilter: 'all' as 'all' | 'change' | 'blocked' | 'pending',
    // 0 = current revision; >0 = index into the workset's revision list
    // (metadata-only historical read-back).
    historyIndex: 0,
  }),
  actions: {
    selectWorkset(id: string | null) {
      if (this.selectedWorksetId === id) return
      this.selectedWorksetId = id
      this.resetBatchSelection()
    },
    selectBatch(index: number | null) {
      this.selectedBatchIndex = index
      this.selectedComponentId = null
    },
    selectComponent(componentId: string | null) {
      this.selectedComponentId = componentId
    },
    selectHistory(index: number) {
      this.historyIndex = index
    },
    toggleBatchOpen(index: number) {
      const next = new Set(this.openBatchIndexes)
      if (next.has(index)) next.delete(index)
      else next.add(index)
      this.openBatchIndexes = markRaw(next)
    },
    resetBatchSelection() {
      this.selectedBatchIndex = null
      this.selectedComponentId = null
      this.openBatchIndexes = markRaw(new Set())
      this.batchSearch = ''
      this.batchFilter = 'all'
      this.historyIndex = 0
    },
  },
})
