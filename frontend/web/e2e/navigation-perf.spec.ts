import { expect, test, type Page } from '@playwright/test'
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path, { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

/**
 * Navigation latency diagnostic (FolderDetailPage -> LibrariesPage).
 *
 * User-reported symptom: returning from the folder tree to the libraries page
 * has a visible delay even though the Vue Query caches are warm — the library
 * list and folder list are NOT re-fetched. This spec turns that symptom into a
 * reproducible measurement and attributes it:
 *
 *   Phase A — small baseline against the real stack (2 folders, 4-file tree).
 *   Phase B — differential cases on three additional libraries, served by
 *             Playwright route interception:
 *               big-both   2500 folder rows + 12 000-file tree (801 visible rows)
 *               big-tree   real-small list (2 rows) + big tree     -> isolates
 *                          FolderTreeCard teardown cost
 *               big-list   2500 rows + tiny tree (3 visible rows)  -> isolates
 *                          FolderFlatList mount + layout cost
 *
 * Per round it records:
 *   - painted: ms from clicking `back-to-libraries` until the folder list's
 *     first row exists AND two animation frames have been produced (i.e. the
 *     new page has painted), measured inside the page via rAF, not polling;
 *   - long: durations of Long Tasks (>= 50 ms) that started inside that window
 *     (observed via PerformanceObserver, which Long Tasks require).
 *
 * It also counts folder/tree requests so the warm-cache invariant (zero
 * requests on repeated same-library round-trips) is asserted alongside the
 * timing data, matching the cache-hit behavior covered by
 * library-scan-plan.spec.ts.
 *
 * Timing results are written to e2e/.perf-results.json (gitignored).
 * Skipped unless ONSEI_E2E=1 — opt-in diagnostics, not default CI.
 *
 * Stack-state independence: every library here lives on its own temporary
 * on-disk root (created via mkdtemp), so this spec does not depend on an
 * empty data dir and can share one stack with library-scan-plan.spec.ts.
 * Ordering note: the smoke spec assumes an empty data dir at start, so this
 * diagnostic file must run after it (testDir alphabetical order guarantees
 * `library-scan-plan` < `navigation-perf`).
 */

const e2eEnabled = process.env.ONSEI_E2E === '1'

interface StackState {
  fixtureRoot: string
  httpPort: number
}

function readStackState(): StackState {
  const stateFile = process.env.ONSEI_E2E_STATE_FILE
    ? resolve(process.env.ONSEI_E2E_STATE_FILE)
    : fileURLToPath(new URL('./.stack-state.json', import.meta.url))
  return JSON.parse(readFileSync(stateFile, 'utf8')) as StackState
}

// ---- deterministic fixtures -------------------------------------------------

interface PerfRound {
  painted: number
  long: number[]
}

function makeFolders(count: number) {
  const folders = []
  for (let i = 0; i < count; i++) {
    const id = `perf-folder-${String(i).padStart(4, '0')}`
    folders.push({
      id,
      name: id,
      path: `/music/${id}`,
      relative_path: id,
      audio_file_count: 12,
    })
  }
  return folders
}

function makeTree(dirCount: number, filesPerDir: number) {
  const dirs = []
  for (let d = 0; d < dirCount; d++) {
    const dirPath = `/music/dir-${String(d).padStart(3, '0')}`
    const children = []
    for (let f = 0; f < filesPerDir; f++) {
      const name = `trk-${String(d).padStart(3, '0')}-${String(f).padStart(3, '0')}.flac`
      children.push({ name, path: `${dirPath}/${name}`, type: 'file', format: 'flac', bitrate: 1411, size: 10 * 1024 * 1024 })
    }
    dirs.push({ name: `dir-${String(d).padStart(3, '0')}`, path: dirPath, type: 'dir', children })
  }
  return { name: 'music', path: '/music', type: 'dir', children: dirs }
}

// ---- measurement seam -------------------------------------------------------

/** Long Tasks are only delivered to a PerformanceObserver, never into the entry buffer. */
async function installLongTaskObserver(page: Page): Promise<void> {
  await page.evaluate(() => {
    const win = window as unknown as { __longTasks: Array<{ start: number; duration: number }> }
    win.__longTasks = []
    try {
      new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) {
          win.__longTasks.push({ start: entry.startTime, duration: entry.duration })
        }
      }).observe({ entryTypes: ['longtask'] })
    } catch {
      // Long Tasks unsupported — timing data still works, long[] stays empty.
    }
  })
}

/** In-page rAF watcher: start mark now; "painted" when `selector` exists and two frames passed. */
async function installPerfWatch(page: Page, selector: string): Promise<void> {
  await page.evaluate((cssSelector) => {
    const win = window as unknown as {
      __perf: { start: number; painted: number; longBaseline: number }
      __longTasks: unknown[]
    }
    win.__perf = {
      start: performance.now(),
      painted: 0,
      longBaseline: win.__longTasks?.length ?? 0,
    }
    const tick = (): void => {
      if (win.__perf.painted > 0) return
      if (document.querySelector(cssSelector)) {
        requestAnimationFrame(() => {
          requestAnimationFrame(() => {
            win.__perf.painted = performance.now()
          })
        })
      } else {
        requestAnimationFrame(tick)
      }
    }
    requestAnimationFrame(tick)
  }, selector)
}

async function readPerfRound(page: Page): Promise<PerfRound> {
  return page.evaluate(() => {
    const perf = (window as unknown as { __perf: { start: number; painted: number; longBaseline: number } }).__perf
    const long = (window as unknown as { __longTasks: Array<{ start: number; duration: number }> }).__longTasks
    return {
      painted: perf.painted - perf.start,
      long: long.slice(perf.longBaseline).map((entry) => entry.duration),
    }
  })
}

function median(values: number[]): number {
  if (values.length === 0) return 0
  const sorted = [...values].sort((a, b) => a - b)
  const mid = Math.floor(sorted.length / 2)
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2
}

async function createLibrary(page: Page, name: string, rootPath: string): Promise<void> {
  await page.getByRole('button', { name: '添加媒体库' }).click()
  await page.locator('#library-name').fill(name)
  await page.locator('#library-root').fill(rootPath)
  await page.getByRole('button', { name: '保存' }).click()
  // The new library becomes active; its (empty, unscanned) folder list renders.
  await expect(page.getByRole('main').getByRole('heading', { name })).toBeVisible()
}

async function libraryIdByName(page: Page, name: string): Promise<string> {
  return page.evaluate((target) => {
    const select = document.querySelector<HTMLSelectElement>('select[aria-label="切换媒体库"]')
    const option = Array.from(select?.options ?? []).find((opt) => opt.textContent === target)
    return option?.value ?? ''
  }, name)
}

interface CaseShape {
  openButtonName: string
  firstRowSelector: string
  lastTreeRow: number
  rounds: number
}

async function runCase(
  page: Page,
  folderRequests: string[],
  libraryId: string,
  shape: CaseShape,
): Promise<{ painted: number[]; long: number[]; requests: number }> {
  await page.selectOption('select[aria-label="切换媒体库"]', libraryId)
  await expect(page.locator(shape.firstRowSelector).first()).toBeVisible({ timeout: 30_000 })
  const before = folderRequests.length
  const painted: number[] = []
  const long: number[] = []
  for (let i = 0; i < shape.rounds; i++) {
    await page.getByRole('button', { name: shape.openButtonName }).click()
    await expect(page.getByTestId('tree-row-0')).toBeVisible({ timeout: 30_000 })
    // First round fetches the (per-case) tree; later rounds must hit the cache.
    await expect(page.getByTestId(`tree-row-${shape.lastTreeRow}`)).toBeVisible({ timeout: 30_000 })
    await installPerfWatch(page, shape.firstRowSelector)
    await page.getByTestId('back-to-libraries').click()
    await page.waitForFunction(() => (window as unknown as { __perf?: { painted: number } }).__perf?.painted)
    const round = await readPerfRound(page)
    painted.push(round.painted)
    long.push(...round.long)
  }
  // Warm-cache invariant: the only request across the rounds is the first tree fetch.
  expect(folderRequests.length).toBe(before + 1)
  return { painted, long, requests: folderRequests.length - before }
}

test.describe('navigation latency diagnostics', () => {
  test.skip(!e2eEnabled, 'e2e diagnostics run only with ONSEI_E2E=1')

  test('measure and attribute warm-cache FolderDetailPage -> LibrariesPage return', async ({ page }) => {
    const { fixtureRoot } = readStackState()
    const folderRequests: string[] = []
    page.on('request', (request) => {
      if (/\/folders(\/|$)/.test(new URL(request.url()).pathname)) folderRequests.push(request.url())
    })

    const tmpRoots: string[] = []

    try {
      // ---- Phase A: small library on the real stack (scan materializes 2 folders) ----
      // Copy the launcher's fixture tree to our own temp root so this spec is
      // independent of the stack's data-dir state (the smoke spec may have
      // already created its own library at the original fixture root, and the
      // backend enforces unique root_path).
      const smallRoot = mkdtempSync(path.join(os.tmpdir(), 'onsei-e2e-small-'))
      tmpRoots.push(smallRoot)
      cpSync(fixtureRoot, path.join(smallRoot, 'music'), { recursive: true })
      await page.goto('/')
      await installLongTaskObserver(page)
      // Header add button works with either an empty or a populated data dir
      // (the empty-state button duplicates the name when there are no libraries).
      await page.getByRole('button', { name: '添加媒体库' }).first().click()
      await page.locator('#library-name').fill('Perf Small')
      await page.locator('#library-root').fill(path.join(smallRoot, 'music'))
      await page.getByRole('button', { name: '保存' }).click()
      await expect(page.getByTestId('scan-button')).toBeVisible()
      await page.getByTestId('scan-button').click()
      await expect(page.getByText('扫描完成')).toBeVisible()
      await expect(page.getByRole('checkbox', { name: '选择 albumA' })).toBeVisible()
      // Scan completion triggers a targeted folders refetch (active observer) —
      // let it settle, otherwise the snapshot taken inside runCase could race it.
      await page.waitForTimeout(1000)
      const smallLibraryId = await libraryIdByName(page, 'Perf Small')
      expect(smallLibraryId).not.toBe('')

      const small = await runCase(page, folderRequests, smallLibraryId, {
        openButtonName: '打开 albumA',
        firstRowSelector: '[aria-label="选择 albumA"]',
        lastTreeRow: 4, // albumA has 4 files -> rows 0..4
        rounds: 3,
      })
      // (runCase already asserted: exactly one albumA tree fetch beyond the settle count.)

      // ---- Phase B: differential cases on three additional libraries. ----
      // The backend enforces unique root_path (LIBRARY_EXISTS), so each
      // library needs its own on-disk root directory.
      const bigTreeFixture = makeTree(800, 15) // 12 000 files; 801 visible rows with root expanded
      const tinyTreeFixture = makeTree(2, 15) // 3 visible rows
      const bigListFixture = makeFolders(2500)
      const tinyListFixture = makeFolders(2)

      // Routes are installed BEFORE the libraries exist. Every creation
      // triggers exactly one folders fetch for the new library (the active
      // page's folder query after setActiveLibrary), so an order-based queue
      // dispatches the right payload per creation. Tree requests only happen
      // during runCase below, when the id -> fixture map is already populated.
      const treeById = new Map<string, unknown>()
      const foldersQueue = [bigListFixture, tinyListFixture, bigListFixture]
      await page.route(`**/api/v1/libraries/*/folders`, async (route) => {
        if (route.request().url().includes(`/libraries/${smallLibraryId}/`)) return route.fallback()
        const payload = foldersQueue.shift()
        if (!payload) throw new Error('unexpected folders request — queue misaligned')
        await route.fulfill({ json: { folders: payload } })
      })
      await page.route(`**/api/v1/libraries/*/folders/*/tree`, async (route) => {
        if (route.request().url().includes(`/libraries/${smallLibraryId}/`)) return route.fallback()
        const id = route.request().url().split('/libraries/')[1]?.split('/')[0] ?? ''
        const tree = treeById.get(id)
        if (!tree) throw new Error(`no tree fixture for library ${id}`)
        await route.fulfill({ json: { tree } })
      })

      for (const [name, dir] of [
        ['Perf Both', 'both'],
        ['Perf BigTree', 'tree'],
        ['Perf BigList', 'list'],
      ] as const) {
        const root = mkdtempSync(path.join(os.tmpdir(), `onsei-e2e-${dir}-`))
        tmpRoots.push(root)
        mkdirSync(path.join(root, 'music'), { recursive: true })
        await createLibrary(page, name, path.join(root, 'music'))
      }

      const bothId = await libraryIdByName(page, 'Perf Both')
      const bigTreeId = await libraryIdByName(page, 'Perf BigTree')
      const bigListId = await libraryIdByName(page, 'Perf BigList')
      expect(bothId).not.toBe('')
      expect(bigTreeId).not.toBe('')
      expect(bigListId).not.toBe('')
      treeById.set(bothId, bigTreeFixture)
      treeById.set(bigTreeId, bigTreeFixture)
      treeById.set(bigListId, tinyTreeFixture)

      const perfShape = {
        openButtonName: '打开 perf-folder-0000',
        firstRowSelector: '[data-testid="folder-checkbox-perf-folder-0000"]',
      }
      const both = await runCase(page, folderRequests, bothId, { ...perfShape, lastTreeRow: 800, rounds: 3 })
      const bigTree = await runCase(page, folderRequests, bigTreeId, { ...perfShape, lastTreeRow: 800, rounds: 2 })
      const bigList = await runCase(page, folderRequests, bigListId, { ...perfShape, lastTreeRow: 2, rounds: 2 })

      const summary = {
        small: { painted: small.painted, medianPainted: median(small.painted), long: small.long },
        'big-both': { painted: both.painted, medianPainted: median(both.painted), long: both.long },
        'big-tree': { painted: bigTree.painted, medianPainted: median(bigTree.painted), long: bigTree.long },
        'big-list': { painted: bigList.painted, medianPainted: median(bigList.painted), long: bigList.long },
        requestsPerCase: { both: both.requests, bigTree: bigTree.requests, bigList: bigList.requests },
      }
      writeFileSync(
        fileURLToPath(new URL('./.perf-results.json', import.meta.url)),
        JSON.stringify(summary, null, 2),
      )
      // eslint-disable-next-line no-console
      console.log(
        `[perf] small=${summary.small.medianPainted.toFixed(0)}ms · both=${summary['big-both'].medianPainted.toFixed(0)}ms · big-tree=${summary['big-tree'].medianPainted.toFixed(0)}ms · big-list=${summary['big-list'].medianPainted.toFixed(0)}ms`,
      )
      test.info().attach('navigation-perf-summary', {
        body: JSON.stringify(summary, null, 2),
        contentType: 'application/json',
      })

      // The diagnostic is only meaningful if the big cases clearly exceed the
      // small baseline — otherwise the loop cannot go red on this bottleneck.
      expect(summary['big-both'].medianPainted).toBeGreaterThan(summary.small.medianPainted * 1.5)
      // Both directions must be attributable: neither differential case may be
      // trivial next to the combined case (each contributes real cost).
      expect(median(bigTree.painted) + median(bigList.painted)).toBeGreaterThan(median(both.painted) * 0.5)
    } finally {
      for (const root of tmpRoots) rmSync(root, { recursive: true, force: true })
    }
  })
})