/**
 * Plan status presentation. The backend echoes its own status strings;
 * this module only maps documented execution states to the agreed labels and
 * leaves every other value verbatim (frontend never invents semantics).
 */

export function mapPlanStatus(status: string): string {
  if (status === 'running') return 'in_progress'
  if (status === 'finished') return 'completed'
  if (status === 'stopped' || status === 'cancelled') return 'canceled'
  if (status === 'error') return 'failed'
  return status
}

export function planStatusLabel(status: string): string {
  switch (mapPlanStatus(status)) {
    case 'in_progress':
      return '进行中'
    case 'completed':
      return '已完成'
    case 'canceled':
      return '已取消'
    case 'failed':
      return '失败'
    case 'ready':
      return '就绪'
    case 'planned':
      return '已规划'
    default:
      return status
  }
}

export function formatPlanCreatedAt(iso: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  const pad = (n: number): string => String(n).padStart(2, '0')
  return `${date.getUTCFullYear()}-${pad(date.getUTCMonth() + 1)}-${pad(date.getUTCDate())} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())}`
}