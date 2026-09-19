import { expect, test, type Page } from '@playwright/test'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { readStackState } from './helpers/stack-state.ts'

/**
 * Returning to the overview keeps the list — behaviour, not a timing.
 *
 * The workbench's own navigation model says a return must preserve what the
 * user had: selection, filters and scroll position (spec N2). Keeping the
 * list mounted is how that is implemented, and this diagnostic proves the
 * consequence: after a round trip into a member's files and back, the checked
 * directory is still checked and the list is back where it was scrolled —
 * without a fresh page load or a lost selection.
 *
 * It also records the round trip's painted time and long tasks for the record
 * (e2e/.perf-results.json, gitignored), because a diagnostic that only asserts
 * a boolean would not show a regression that is still under the threshold.
 *
 * Skipped unless ONSEI_E2E=1 — opt-in diagnostics, not default CI.
 */

const e2eEnabled = process.env.ONSEI_E2E === '1'

interface PerfRound {
  painted: number
  long: number[]
}

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

/** A fixture library with enough directories to be worth virtualizing. */
function makeFixtureTree(count: number): string {
  const root = mkdtempSync(path.join(os.tmpdir(), 'onsei-perf-'))
  for (let i = 0; i < count; i++) {
    const dir = path.join(root, `album-${String(i).padStart(3, '0')}`)
    mkdirSync(dir, { recursive: true })
    writeFileSync(path.join(dir, 'track.flac'), 'perf fixture')
  }
  return root
}

test.describe('workbench return diagnostics', () => {
  test.skip(!e2eEnabled, 'e2e diagnostics run only with ONSEI_E2E=1')

  test('returning from a member keeps the overview list, with timings', async ({ page }) => {
    test.setTimeout(180_000)
    const { fixtureRoot } = readStackState()
    const root = makeFixtureTree(120)
    test.info().annotations.push({ type: 'fixture', description: `stack=${fixtureRoot} perf=${root}` })

    await page.goto('/worksets')
    const addButton = page.getByRole('button', { name: '添加媒体库' }).first()
    await addButton.click()
    await page.locator('#library-name').fill('Perf Library')
    await page.locator('#library-root').fill(root)
    await page.getByRole('button', { name: '保存' }).click()

    await page.getByRole('main').getByRole('link', { name: /Perf Library/ }).first().click()
    await expect(page).toHaveURL(/\/worksets\/[^/]+$/)
    await page.getByTestId('scan-button').click()
    await expect(page.getByText('扫描完成')).toBeVisible({ timeout: 60_000 })
    await expect(page.getByTestId('dir-list')).toBeVisible({ timeout: 30_000 })

    await installLongTaskObserver(page)

    // A selection and a scroll offset, both made before leaving: they are what
    // a return must bring back. The row the round trip starts from is one that
    // is visible at that offset — clicking a row Playwright has to scroll to
    // would move the list itself.
    await expect(page.locator('[data-testid="dir-list-spacer"] li').first()).toBeVisible({ timeout: 30_000 })
    const checked = page.locator('[data-testid="dir-list-spacer"] input[type="checkbox"]').first()
    await checked.check()
    await expect(page.getByText('已选择 1 个文件夹')).toBeVisible()
    const scrolledTo = await page
      .getByTestId('dir-list-scroller')
      .evaluate((node) => {
        node.scrollTop = 600
        return node.scrollTop
      })
    const visibleRow = page.locator('[data-testid="dir-list-spacer"] li').nth(12)

    const rounds: PerfRound[] = []
    for (let i = 0; i < 3; i++) {
      await visibleRow.click()
      // The member's address is its directory identity: the folder's name is
      // not in it (spec §9 N1′).
      await expect(page).toHaveURL(/\/f\/[0-9a-f]{32}$/)
      await expect(page.getByTestId('member-tree')).toBeVisible({ timeout: 30_000 })

      await installPerfWatch(page, '[data-testid="dir-list-spacer"]')
      // The way back is the overview crumb in the breadcrumb (the narrow
      // header's back control only exists below the rail breakpoint).
      await page.getByTestId('overview-breadcrumb').getByRole('link').last().click()
      await page.waitForFunction(() => (window as unknown as { __perf?: { painted: number } }).__perf?.painted)
      rounds.push(await readPerfRound(page))

      // The selection survived the trip, and the list is back where it was.
      await expect(page.getByText('已选择 1 个文件夹')).toBeVisible()
      const restored = await page.getByTestId('dir-list-scroller').evaluate((node) => node.scrollTop)
      expect(Math.abs(restored - scrolledTo)).toBeLessThan(5)
    }

    const timings = {
      spec: 'workbench-return',
      rounds,
      medianPainted: rounds
        .map((round) => round.painted)
        .sort((a, b) => a - b)[Math.floor(rounds.length / 2)],
      longTasks: rounds.flatMap((round) => round.long),
    }
    writeFileSync(
      path.join(process.cwd(), 'e2e', '.perf-results.json'),
      `${JSON.stringify(timings, null, 2)}\n`,
    )

    // A return that rebuilds the whole list would take a different order of
    // magnitude; this is a floor, not a target.
    expect(timings.medianPainted).toBeLessThan(1500)

    rmSync(root, { recursive: true, force: true })
  })
})
