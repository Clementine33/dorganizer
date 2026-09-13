import { ApiError } from './client'

export function errorDetails(error: unknown): { code: string | null; message: string } {
  if (error instanceof ApiError) return { code: error.code, message: error.message }
  if (error instanceof Error) return { code: null, message: error.message }
  return { code: null, message: '发生未知错误' }
}