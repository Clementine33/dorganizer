import type { GenerationStatus, PlanningState, Workset } from '@/lib/api/types'

// Chinese display labels + badge tone classes for workset-domain enums.
// Badge tones: neutral (muted), info (sky), warn (amber), bad (red), ok (green).

export type BadgeTone = 'neutral' | 'info' | 'warn' | 'bad' | 'ok'

export const planningStateLabel: Record<PlanningState, string> = {
  unplanned: '待规划',
  planning: '规划中',
  planned: '已规划',
  needs_planning: '需重新规划',
  orphaned: '已孤立',
}

// Lookup with verbatim passthrough: the frontend never invents semantics for
// a value it does not recognize — unknown states render as themselves.
export function planningStateLabelOf(state: string): string {
  return planningStateLabel[state as PlanningState] ?? state
}

export function planningStateToneOf(state: string): BadgeTone {
  const tone = planningStateTone[state as PlanningState]
  return tone ?? 'neutral'
}

export const planningStateTone: Record<PlanningState, BadgeTone> = {
  unplanned: 'info',
  planning: 'info',
  planned: 'ok',
  needs_planning: 'warn',
  orphaned: 'neutral',
}

export const generationStatusLabel: Record<GenerationStatus, string> = {
  queued: '排队中',
  running: '生成中',
  completed: '已完成',
  failed: '失败',
  canceled: '已取消',
  interrupted: '已中断',
}

export const generationStatusTone: Record<GenerationStatus, BadgeTone> = {
  queued: 'neutral',
  running: 'info',
  completed: 'ok',
  failed: 'bad',
  canceled: 'neutral',
  interrupted: 'warn',
}

export function generationStatusLabelOf(status: string): string {
  return generationStatusLabel[status as GenerationStatus] ?? status
}

export function generationStatusToneOf(status: string): BadgeTone {
  const tone = generationStatusTone[status as GenerationStatus]
  return tone ?? 'neutral'
}

export function validationStateLabel(state: string): string {
  switch (state) {
    case 'valid':
      return '有效'
    case 'stale':
      return '已过期'
    case 'unavailable':
      return '不可用'
    default:
      return state
  }
}

export const summaryReasonLabel: Record<string, string> = {
  ACTIONABLE: '可执行',
  NO_MATCH: '无需变更',
  BLOCKED: '存在阻塞',
  PARTIAL: '部分阻塞',
}

export const memberStateLabel: Record<string, string> = {
  planned: '已规划',
  pending: '待规划',
  missing: '已缺失',
}

export const laneDecisionLabel: Record<string, string> = {
  KEEP: '保留',
  KEEP_ALL: '全部保留',
  REBUILD: '重建',
  REBUILD_ALL: '全部重建',
  BLOCKED: '阻塞',
}

export const resolutionLabel: Record<string, string> = {
  keep: '保留',
  delete: '删除',
  encode: '编码',
}

export const operationKindLabel: Record<string, string> = {
  encode: '编码',
  delete_obsolete: '删除冗余',
}

export const partitionLabel: Record<string, string> = {
  matched: '无音效',
  unmatched: '有音效',
}

export const toneClass: Record<BadgeTone, string> = {
  neutral: 'bg-muted text-muted-foreground',
  info: 'bg-sky-500/15 text-sky-600 dark:text-sky-400',
  warn: 'bg-amber-500/15 text-amber-600 dark:text-amber-400',
  bad: 'bg-destructive/15 text-destructive',
  ok: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400',
}

// Feed bucket of a workset, mirroring the backend's mutually exclusive,
// error-first classification (workset feed filter). Exported so the UI can
// color rows consistently with the server-side filter.
export function worksetError(v: Workset): boolean {
  if (v.planning_state === 'orphaned') return true
  if (v.current_revision) {
    if (v.current_revision.stale === true) return true
    if (v.current_revision.validation_state === 'stale' || v.current_revision.validation_state === 'unavailable') return true
    if (v.current_revision.blocked_count > 0) return true
  }
  if (v.latest_generation && (v.latest_generation.status === 'failed' || v.latest_generation.status === 'interrupted')) {
    return true
  }
  return false
}

export function worksetBucket(v: Workset): 'pending' | 'normal' | 'error' {
  if (worksetError(v)) return 'error'
  if (v.planning_state === 'unplanned' || v.planning_state === 'needs_planning' || v.planning_state === 'planning') {
    return 'pending'
  }
  return 'normal'
}

export function formatWorksetTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
