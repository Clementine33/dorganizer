import { expect, test } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

/**
 * End-to-end smoke of the whole web/gin library prototype workflow against a
 * real stack (Go backend with a fresh ONSEI_DATA_DIR + Vite dev server),
 * launched by `e2e/launch-stack.mjs` (see playwright.config.ts webServer).
 *
 * The full browser flow exercised here:
 *   1. create a library pointing at a generated fixture tree,
 *   2. run the scan to completion (SSE),
 *   3. verify the flat folder list populates,
 *   4. select a folder,
 *   5. generate one unified plan,
 *   6. verify the review page shows operations.
 *
 * Skipped unless ONSEI_E2E=1 so CI can run it optionally and local `vitest`
 * runs never try to boot the stack (vitest only includes `src/**` anyway).
 */

const e2eEnabled = process.env.ONSEI_E2E === '1'

interface StackState {
  fixtureRoot: string
  httpPort: number
}

function readStackState(): StackState {
  const stateFile = fileURLToPath(new URL('./.stack-state.json', import.meta.url))
  return JSON.parse(readFileSync(stateFile, 'utf8')) as StackState
}

test.describe('library scan → plan smoke', () => {
  test.skip(!e2eEnabled, 'e2e smoke runs only with ONSEI_E2E=1')

  test('create library, scan to completion, select folder, generate and review plan', async ({ page }) => {
    const { fixtureRoot } = readStackState()

    // Empty data dir → libraries empty state.
    await page.goto('/')
    await expect(page.getByTestId('empty-add-library')).toBeVisible()

    // 1. Create the library pointing at the generated fixture tree.
    await page.getByTestId('empty-add-library').click()
    await page.locator('#library-name').fill('E2E Library')
    await page.locator('#library-root').fill(fixtureRoot)
    await page.getByRole('button', { name: '保存' }).click()
    await expect(page.getByTestId('scan-button')).toBeVisible()
    // Library header shows the echoed root path (main content, not the sidebar brand).
    await expect(page.getByRole('main').getByText(fixtureRoot)).toBeVisible()

    // 2. Run the scan and wait for the SSE stream to complete.
    await page.getByTestId('scan-button').click()
    await expect(page.getByText('扫描完成')).toBeVisible()

    // 3. Flat folder list populates with the fixture folders.
    await expect(page.getByRole('checkbox', { name: '选择 albumA' })).toBeVisible()
    await expect(page.getByRole('checkbox', { name: '选择 albumB' })).toBeVisible()
    await expect(page.getByText('4 个音频文件')).toBeVisible()
    await expect(page.getByText('2 个音频文件')).toBeVisible()

    // 4. Select the albumA folder (lossy+lossless stems → slim deletes).
    await page.getByRole('checkbox', { name: '选择 albumA' }).check()
    await expect(page.getByText('已选择 1 个文件夹')).toBeVisible()

    // 5. Generate one unified plan for the selected folder.
    await page.getByTestId('generate-plan').click()
    // Plan ids look like plan-<timestamp>.<n> — the dot is part of the id.
    await page.waitForURL(/\/plans\/plan-[\w.-]+\/?$/)

    // 6. Review page shows the operations.
    await expect(page.getByText('计划审阅')).toBeVisible()
    await expect(page.getByText('计划操作')).toBeVisible()
    await expect(page.getByText('2 项')).toBeVisible()
    const groups = page.getByTestId('operation-group')
    await expect(groups).toHaveCount(1)
    // Operations are rendered expanded by default: two delete rows for the
    // lossless copies of test1/test2 (one unified plan for the folder).
    const rows = groups.locator('[data-testid="operation-row"]')
    await expect(rows).toHaveCount(2)
    await expect(rows.getByText('删除')).toHaveCount(2)
    const rowTexts = await rows.allTextContents()
    expect(rowTexts.some((text) => text.includes('test1.flac'))).toBe(true)
    expect(rowTexts.some((text) => text.includes('test2.flac'))).toBe(true)
  })
})