import { defineStore } from 'pinia'

/**
 * Workbench navigation UI state (N26): which groups are folded, keyed by the
 * stable group id.
 *
 * Session scope — it never enters the URL, a query cache or the backend, and no
 * action resets it, so a preference survives a route change and a desktop/
 * mobile form change while still being gone after a reload. It is deliberately
 * separate from the shell's sidebar `expanded` and the drawer's open state.
 */
export const useWorkbenchNavStore = defineStore('workbench-nav', {
  state: () => ({
    collapsedGroups: {} as Record<string, boolean>,
  }),
  actions: {
    isGroupCollapsed(id: string): boolean {
      return this.collapsedGroups[id] === true
    },
    setGroupCollapsed(id: string, collapsed: boolean): void {
      this.collapsedGroups[id] = collapsed
    },
  },
})
