import { defineStore } from 'pinia'
import { ApiError } from '@/lib/api/client'
import type { ApiClientContract, PlanInfo, PlanResponse } from '@/lib/api/types'

// Monotonic tokens for list and detail loads: bumped on every request, so
// responses from superseded requests (fast library switching, quick plan
// navigation) are discarded instead of overwriting current state.
let plansRequestSeq = 0
let planDetailSeq = 0

function errorDetails(error: unknown): { code: string | null; message: string } {
  return {
    code: error instanceof ApiError ? error.code : null,
    message: error instanceof Error ? error.message : '发生未知错误',
  }
}

export const usePlansStore = defineStore('plans', {
  state: () => ({
    plans: [] as PlanInfo[],
    currentPlan: null as PlanResponse | null,
    // loading covers plan creation and listing; detailLoading tracks the
    // review-page fetch separately so one cannot clear the other early.
    loading: false,
    detailLoading: false,
    detailErrorCode: null as string | null,
    detailError: null as string | null,
    errorCode: null as string | null,
    error: null as string | null,
  }),
  actions: {
    async createForFolders(libraryId: string, folderIds: string[], client: ApiClientContract) {
      this.loading = true
      this.clearError()
      try {
        const plan = await client.createPlan({
          library_id: libraryId,
          folder_ids: [...folderIds],
          plan_type: 'slim',
          target_format: 'slim:mode1',
          prune_matched_excluded: false,
        })
        this.currentPlan = plan
        return plan
      } catch (error) {
        this.setError(error)
        throw error
      } finally {
        this.loading = false
      }
    },
    async createForFiles(libraryId: string, sourceFiles: string[], client: ApiClientContract) {
      this.loading = true
      this.clearError()
      try {
        const plan = await client.createPlan({
          library_id: libraryId,
          source_files: [...sourceFiles],
        })
        this.currentPlan = plan
        return plan
      } catch (error) {
        this.setError(error)
        throw error
      } finally {
        this.loading = false
      }
    },
    async loadPlans(libraryId: string | undefined, client: ApiClientContract) {
      const seq = ++plansRequestSeq
      this.loading = true
      this.clearError()
      try {
        const plans = await client.listPlans(libraryId)
        if (seq !== plansRequestSeq) return
        this.plans = plans
      } catch (error) {
        if (seq !== plansRequestSeq) return
        this.setError(error)
        throw error
      } finally {
        if (seq === plansRequestSeq) this.loading = false
      }
    },
    // loadPlan fetches one plan for the review page. Any in-memory currentPlan
    // for a different plan is cleared immediately so the page never renders
    // another plan's details while the fetch is in flight.
    async loadPlan(planID: string, client: ApiClientContract) {
      const seq = ++planDetailSeq
      this.detailLoading = true
      this.detailErrorCode = null
      this.detailError = null
      if (this.currentPlan && this.currentPlan.plan_id !== planID) this.currentPlan = null
      try {
        const plan = await client.getPlan(planID)
        if (seq !== planDetailSeq) return
        this.currentPlan = plan
      } catch (error) {
        if (seq !== planDetailSeq) return
        const details = errorDetails(error)
        this.detailErrorCode = details.code
        this.detailError = details.message
        throw error
      } finally {
        if (seq === planDetailSeq) this.detailLoading = false
      }
    },
    setError(error: unknown) {
      const details = errorDetails(error)
      this.errorCode = details.code
      this.error = details.message
    },
    clearError() {
      this.errorCode = null
      this.error = null
    },
  },
})