import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'

/**
 * End-to-end execution of the current revision against the real stack (Go
 * backend + Vite + real ffmpeg). Everything runs on generated temporary media:
 * a short tone as the lossless source beside a below-target 128k mp3, which
 * the frozen plan rebuilds at the default 320k.
 *
 * Covered here (the M3 acceptance list):
 *   - soft deletion: the disk matches the report (Delete/ recovery, rebuilt
 *     bitrate) and a reload restores the finished session from the server;
 *   - hard deletion: replaced and obsolete media is gone, no Delete/ at all;
 *   - a component failure (drifted source after planning) preserves every
 *     source and old output byte-for-byte;
 *   - a running session cancels cooperatively and reports its unrun range.
 *
 * Runs after the sibling specs (alphabetical) on the shared stack; each test
 * owns its library, media root and workset, so no state crosses tests.
 */

const e2eEnabled = process.env.ONSEI_E2E === '1'

test.describe.configure({ timeout: 240_000 })

const fixtures: string[] = []

function makeFixtureRoot(prefix: string): string {
  const root = mkdtempSync(path.join(os.tmpdir(), `onsei-e2e-${prefix}-`))
  fixtures.push(root)
  return root
}

test.afterEach(() => {
  for (const root of fixtures.splice(0)) rmSync(root, { recursive: true, force: true })
})

function run(tool: string, args: string[]): string {
  return execFileSync(tool, args, { encoding: 'utf8' })
}

/** A real tone album: 00.wav source, a below-target 128k 00.mp3, optional aac. */
function encodeAlbum(root: string, name: string, opts: { m4a?: boolean } = {}): string {
  const dir = path.join(root, name)
  mkdirSync(dir, { recursive: true })
  const wav = path.join(dir, '00.wav')
  run('ffmpeg', ['-nostdin', '-v', 'error', '-y', '-f', 'lavfi', '-i', 'sine=frequency=440:duration=6', '-c:a', 'pcm_s16le', wav])
  run('ffmpeg', ['-nostdin', '-v', 'error', '-y', '-i', wav, '-b:a', '128k', path.join(dir, '00.mp3')])
  if (opts.m4a) {
    run('ffmpeg', ['-nostdin', '-v', 'error', '-y', '-i', wav, '-c:a', 'aac', '-b:a', '128k', path.join(dir, '00.m4a')])
  }
  return dir
}

function mp3Bitrate(file: string): number {
  // ffprobe takes no -nostdin here (unlike ffmpeg); the Go e2e probes the same way.
  const raw = run('ffprobe', ['-v', 'error', '-select_streams', 'a:0', '-show_entries', 'stream=bit_rate', '-of', 'json', file])
  const parsed = JSON.parse(raw) as { streams: Array<{ bit_rate: string }> }
  return Number(parsed.streams[0]?.bit_rate ?? 0)
}

async function addLibraryAndScan(page: Page, root: string, name: string): Promise<void> {
  await page.goto('/libraries')
  const emptyState = page.getByTestId('empty-add-library')
  if (await emptyState.isVisible().catch(() => false)) {
    await emptyState.click()
  } else {
    await page.getByRole('button', { name: '添加媒体库' }).click()
  }
  await page.locator('#library-name').fill(name)
  await page.locator('#library-root').fill(root)
  await page.getByRole('button', { name: '保存' }).click()
  await page.getByTestId('scan-button').click()
  await expect(page.getByText('扫描完成')).toBeVisible({ timeout: 60_000 })
}

/** Creates a workset over every scanned folder and opens its conversion list. */
async function createWorksetForAllFolders(page: Page): Promise<void> {
  await page.getByRole('checkbox', { name: '选择全部文件夹' }).check()
  await page.getByTestId('create-workset').click()
  await expect(page.getByTestId('create-workset-dialog')).toBeVisible()
  await page.getByTestId('confirm-create-workset').click()
  await expect(page).toHaveURL(/\/worksets\/ws-[\w.-]+\/?$/)
  await page.getByTestId('overview-operation').click()
  await expect(page).toHaveURL(/\/conversion$/)
}

async function generatePlan(page: Page): Promise<void> {
  await page.getByTestId('start-generation').click()
  await expect(page.getByTestId('operation-counts')).toBeVisible({ timeout: 60_000 })
}

/** Starts the run straight from the header — the mode is a settings choice. */
async function startExecution(page: Page): Promise<void> {
  await expect(page.getByTestId('start-execution')).toBeVisible({ timeout: 15_000 })
  await page.getByTestId('start-execution').click()
  // The run opens its own detail carrier instead of crowding the member list.
  await expect(page).toHaveURL(/\/conversion\/execution$/)
  await expect(page.getByTestId('execution-panel')).toBeVisible({ timeout: 15_000 })
}

/** Switches the operation's obsolete-audio handling to hard deletion. */
async function setHardDelete(page: Page): Promise<void> {
  await page.getByTestId('nav-conversion-settings').click()
  await expect(page.getByTestId('conversion-settings')).toBeVisible()
  await page.getByTestId('common-delete-mode-hard').check()
  await expect(page.getByTestId('common-hard-delete-warning')).toBeVisible()
  await page.getByTestId('apply-common').click()
  // The save clears the dirty state; leaving before that would trip the
  // unapplied-edit guard.
  await expect(page.getByTestId('apply-common')).toBeDisabled()
  await page.getByRole('link', { name: '← 转换列表' }).click()
  await expect(page).toHaveURL(/\/conversion$/)
}

test.describe('workset execution', () => {
  test.skip(!e2eEnabled, 'execution e2e runs only with ONSEI_E2E=1')

  test('soft deletion preserves recovery copies and a reload restores the finished session', async ({ page }) => {
    const root = makeFixtureRoot('exec-soft')
    const album = encodeAlbum(root, 'soft-album', { m4a: true })

    await addLibraryAndScan(page, root, 'E2E Exec Soft')
    await createWorksetForAllFolders(page)
    await generatePlan(page)
    await startExecution(page)

    await expect(page.getByTestId('execution-status')).toHaveText('已完成', { timeout: 90_000 })
    // The report lives in the detail carrier, not in the middle of the list.
    await expect(page.getByTestId('workbench-detail-inline').getByTestId('execution-panel')).toBeVisible()
    await expect(page.getByTestId('execution-panel')).toContainText('已写入')
    await expect(page.getByTestId('execution-panel')).toContainText('已清理到 Delete/')

    // Disk truth: the mp3 was rebuilt at the frozen target quality, the wav
    // source stays, and both the obsolete aac and the replaced mp3 are kept
    // under Delete/.
    expect(mp3Bitrate(path.join(album, '00.mp3'))).toBeGreaterThanOrEqual(319_000)
    expect(existsSync(path.join(album, '00.wav'))).toBe(true)
    expect(existsSync(path.join(album, '00.m4a'))).toBe(false)
    expect(existsSync(path.join(album, 'Delete', '00.m4a'))).toBe(true)
    expect(existsSync(path.join(album, 'Delete', '00.mp3'))).toBe(true)

    // A reload restores the terminal session from the server (no live stream
    // survives it) on the same report page, and the revision can never run
    // again.
    await page.reload()
    await expect(page).toHaveURL(/\/conversion\/execution$/)
    await expect(page.getByTestId('execution-status')).toHaveText('已完成', { timeout: 30_000 })
    const executeAgain = page.getByTestId('start-execution')
    await expect(executeAgain).toBeVisible()
    await expect(executeAgain).toBeDisabled()
    await expect(executeAgain).toHaveAttribute('title', /已执行/)
  })

  test('hard deletion removes the replaced and obsolete media without recovery copies', async ({ page }) => {
    const root = makeFixtureRoot('exec-hard')
    const album = encodeAlbum(root, 'hard-album', { m4a: true })

    await addLibraryAndScan(page, root, 'E2E Exec Hard')
    await createWorksetForAllFolders(page)
    // The obsolete-audio handling is a global setting; the plan freezes it.
    await setHardDelete(page)
    await generatePlan(page)
    await startExecution(page)

    await expect(page.getByTestId('execution-status')).toHaveText('已完成', { timeout: 90_000 })
    expect(mp3Bitrate(path.join(album, '00.mp3'))).toBeGreaterThanOrEqual(319_000)
    expect(existsSync(path.join(album, '00.wav'))).toBe(true)
    expect(existsSync(path.join(album, '00.m4a'))).toBe(false)
    expect(existsSync(path.join(album, 'Delete'))).toBe(false)
  })

  test('a source drifting after planning fails the run and preserves every file', async ({ page }) => {
    const root = makeFixtureRoot('exec-fail')
    const album = encodeAlbum(root, 'drift-album')

    await addLibraryAndScan(page, root, 'E2E Exec Drift')
    await createWorksetForAllFolders(page)
    await generatePlan(page)

    // The wav changes on disk after the plan was frozen and before the run: the
    // execution must refuse the component at precheck and leave everything.
    writeFileSync(path.join(album, '00.wav'), 'drifted-bytes', 'utf8')
    const mp3Before = statSync(path.join(album, '00.mp3')).size

    await startExecution(page)
    await expect(page.getByTestId('execution-status')).toHaveText('失败', { timeout: 90_000 })
    await expect(page.getByTestId('execution-error')).toBeVisible()

    expect(readFileSync(path.join(album, '00.wav'), 'utf8')).toBe('drifted-bytes')
    expect(statSync(path.join(album, '00.mp3')).size).toBe(mp3Before)
    expect(mp3Bitrate(path.join(album, '00.mp3'))).toBeLessThan(319_000)
    expect(existsSync(path.join(album, 'Delete'))).toBe(false)
  })

  test('canceling a running session stops it and reports the unrun range', async ({ page }) => {
    const root = makeFixtureRoot('exec-cancel')
    // Enough components that the run outlives the cancel click, so the stop is
    // a running cooperative cancel rather than a queued shortcut.
    const template = encodeAlbum(root, 'template')
    for (let index = 1; index <= 20; index++) {
      const dir = path.join(root, `cancel-${String(index).padStart(2, '0')}`)
      mkdirSync(dir, { recursive: true })
      cpSync(path.join(template, '00.wav'), path.join(dir, '00.wav'))
      cpSync(path.join(template, '00.mp3'), path.join(dir, '00.mp3'))
    }
    rmSync(template, { recursive: true, force: true })

    await addLibraryAndScan(page, root, 'E2E Exec Cancel')
    await createWorksetForAllFolders(page)
    await generatePlan(page)
    await startExecution(page)

    await page.getByTestId('execution-cancel').click()
    await expect(page.getByTestId('execution-status')).toHaveText('已取消', { timeout: 90_000 })
    await expect(page.getByTestId('execution-panel')).toContainText('已完成的组件结果保留')
    // The operations that never ran stay visible instead of being hidden or
    // counted as done.
    await expect(page.getByTestId('execution-remaining').first()).toBeVisible()
  })
})
