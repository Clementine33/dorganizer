/**
 * Why a session stopped, in the user's words. The server's codes are stable
 * contract values (ADR 0007 §4); its English message is a fallback, not the
 * primary text the user reads.
 */
const SESSION_FAILURES: Record<string, string> = {
  SCAN_FAILED: '重新扫描文件夹失败：未做任何改动',
  INPUT_CHANGED: '文件夹输入已变化：未做任何改动，请重新生成计划版本后再执行',
  REVISION_LOAD_FAILED: '计划版本数据不可读',
  REQUEST_LOAD_FAILED: '会话数据不可读',
  MEMBERS_LOAD_FAILED: '成员数据不可读',
  WORKSET_LOAD_FAILED: '工作集数据不可读',
  DRAFT_LOAD_FAILED: '草稿数据不可读',
  PERSIST_FAILED: '写入计划版本失败',
}

export function sessionFailureText(code: string | undefined, message: string | undefined): string {
  const mapped = code ? SESSION_FAILURES[code] : undefined
  return mapped ?? message ?? code ?? '未知错误'
}
