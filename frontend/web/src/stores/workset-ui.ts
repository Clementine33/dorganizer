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
    // null = current revision; otherwise the selected historical revision's
    // plan_id (stable: loading earlier pages or generating a new revision
    // cannot shift the selection).
    historyPlanId: null as string | null,
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
    selectHistoryPlan(planId: string | null) {
      this.historyPlanId = planId
    },
    toggleBatchOpen(index: number) {
      const next = new Set(this.openBatchIndexes)
      if (next.has(index)) next.delete(index)
      else next.add(index)
      this.openBatchIndexes = markRaw(next)
    },
    openBatch(index: number) {
      if (this.openBatchIndexes.has(index)) return
      this.openBatchIndexes = markRaw(new Set([...this.openBatchIndexes, index]))
    },
    resetBatchSelection() {
      this.selectedBatchIndex = null
      this.selectedComponentId = null
      this.openBatchIndexes = markRaw(new Set())
      this.batchSearch = ''
      this.batchFilter = 'all'
      this.historyPlanId = null
    },
  },
})
