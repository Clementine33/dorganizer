import type { FileOperationItemResult, FileOperationResult, RefreshState } from '@/lib/api/types'

/**
 * The words a direct file-management result is explained in.
 *
 * The backend reports a stable machine code per item and a refresh verdict for
 * the request as a whole; this module is the one place that turns those codes
 * into sentences, so a rename, a move and a batch delete never disagree about
 * what the same refusal means (ADR 0002 §1).
 */
const ITEM_CODE_TEXT: Record<string, string> = {
  PATH_INVALID: '路径不合法，已拒绝',
  OUTSIDE_MEMBER: '超出成员目录范围，已拒绝',
  MEMBER_ROOT: '不能修改成员根目录本身',
  SYMLINK: '符号链接不在此版本的修改范围内',
  SOURCE_MISSING: '源文件已不存在',
  TARGET_EXISTS: '目标已存在，不会覆盖',
  MOVE_INTO_SELF: '不能把目录移动到自己或其子目录里',
  INVALID_NAME: '新名称不合法：只能是一个名称，不能包含路径分隔符',
  INVALID_TARGET_DIR: '目标目录不存在或不是目录',
  PERMISSION_DENIED: '权限不足',
  FILE_LOCKED: '文件被其他程序占用',
  IO_ERROR: '文件系统操作失败',
  OPERATION_DENIED: '不支持的操作',
  COVERED_BY_PARENT: '已包含在所选父目录中，未重复处理',
}

const REQUEST_CODE_TEXT: Record<string, string> = {
  // Not only a scan, generation or execution: the backend answers BUSY while
  // its idle-time database maintenance holds the slot too, so the text names no
  // particular task.
  BUSY: '当前有其他任务或后台维护在进行，稍后再试',
  OPERATION_DENIED: '不支持的操作',
  MEMBER_MISSING: '该文件夹已不存在',
  MEMBER_PATH_INVALID: '该路径不是可管理的文件夹',
  FOLDER_PATH_INVALID: '路径不合法',
  REQUEST_REFUSED: '请求被拒绝，未执行任何修改',
}

export function itemStatusText(item: FileOperationItemResult): string {
  switch (item.status) {
    case 'ok':
      return item.recovered_path ? `已移入回收目录：${item.recovered_path}` : `已完成：${item.target ?? item.source}`
    case 'failed':
      return item.message ? `${item.message}（${item.code}）` : (ITEM_CODE_TEXT[item.code ?? ''] ?? '失败')
    case 'skipped':
      return ITEM_CODE_TEXT[item.code ?? ''] ?? '已跳过'
    default:
      return '未执行：前一项失败后停止'
  }
}

/** The refresh verdict, stated as its own fact: the writes already happened. */
export function refreshText(refresh: RefreshState): string {
  if (refresh.ok) return '已刷新目录'
  return `${refresh.message ?? '刷新失败'}（文件已修改，刷新失败）`
}

/** A request-level refusal (nothing ran) in the user's words. */
export function requestRefusalText(code: string, message: string): string {
  return `${REQUEST_CODE_TEXT[code] ?? message}（${code}）`
}

/** True when the request changed something on disk, whatever the refresh said. */
export function madeChanges(result: FileOperationResult): boolean {
  return result.succeeded > 0
}
