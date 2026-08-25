import { defineStore } from 'pinia'
import { ApiError } from '@/lib/api/client'
import type { ApiClientContract, PlanInfo, PlanResponse } from '@/lib/api/types'

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
    loading: false,
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
      this.loading = true
      this.clearError()
      try {
        this.plans = await client.listPlans(libraryId)
      } catch (error) {
        this.setError(error)
        throw error
      } finally {
        this.loading = false
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
