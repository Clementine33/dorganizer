import { expect, test } from '@playwright/test'
import { readStackState } from './helpers/stack-state.ts'

/**
 * End-to-end smoke of the workset workflow against a real stack (Go backend
 * with a fresh ONSEI_DATA_DIR + Vite dev server), launched by
 * `e2e/launch-stack.mjs` (see playwright.config.ts webServer).
 *
 * The full browser flow exercised here:
 *   1. create a library pointing at a generated fixture tree,
 *   2. run the scan to completion (SSE),
 *   3. verify the flat folder list populates,
 *   4. select a folder and create a workset (title dialog → deep link),
 *   5. save-and-generate a first revision on the seeded balanced draft,
 *   6. verify the review stage shows the batch + component outcome,
 *   7. switch to the configure stage, save-and-generate again → v2,
 *   8. back to the feed: both worksets visible with their states.
 *
 * Skipped unless ONSEI_E2E=1 so CI can run it optionally and local `vitest`
 * runs never try to boot the stack (vitest only includes `src/**` anyway).
 */

const e2eEnabled = process.env.ONSEI_E2E === '1'

test.describe('workset workbench smoke', () => {
  test.skip(!e2eEnabled, 'e2e smoke runs only with ONSEI_E2E=1')

  test('create library, scan, create workset, generate revisions, review batches', async ({ page }) => {
    const { fixtureRoot } = readStackState()
    const fixturePosix = fixtureRoot.replaceAll('\\', '/')

    // Empty data dir → libraries empty state.
    await page.goto('/')
    await expect(page.getByTestId('empty-add-library')).toBeVisible()

    // 1. Create the library pointing at the generated fixture tree.
    await page.getByTestId('empty-add-library').click()
    await page.locator('#library-name').fill('E2E Library')
    await page.locator('#library-root').fill(fixtureRoot)
    await page.getByRole('button', { name: '保存' }).click()
    await expect(page.getByTestId('scan-button')).toBeVisible()
    // Library header echoes the root path (backend normalizes backslashes to
    // forward slashes, so match the normalized form).
    await expect(page.getByRole('main').getByText(fixturePosix)).toBeVisible()

    // 2. Run the scan and wait for the SSE stream to complete.
    await page.getByTestId('scan-button').click()
    await expect(page.getByText('扫描完成')).toBeVisible()

    // 3. Flat folder list populates with the fixture folders.
    const albumA = page.getByRole('checkbox', { name: '选择 albumA' })
    await expect(albumA).toBeVisible()
    await expect(page.getByRole('checkbox', { name: '选择 albumB' })).toBeVisible()

    // 4. Select albumA and open the create-workset dialog.
    await albumA.check()
    await expect(page.getByText('已选择 1 个文件夹')).toBeVisible()
    await page.getByTestId('create-workset').click()
    await expect(page.getByTestId('create-workset-dialog')).toBeVisible()
    await expect(page.getByTestId('workset-folder-review')).toContainText('albumA')
    // Default title is prefilled; confirm as-is.
    await page.getByTestId('confirm-create-workset').click()
    await expect(page).toHaveURL(/\/worksets\/ws-[\w.-]+\/?$/)

    // 5. Workbench opens in the configure stage on the seeded balanced preset.
    await expect(page.getByTestId('policy-editor')).toBeVisible()
    await expect(page.getByTestId('preset-balanced')).toBeVisible()
    await page.getByTestId('save-and-generate').click()

    // 6. The single-root fixture generates within seconds, so the transient
    //    progress bar may never paint. Wait for the terminal: the review
    //    stage unlocks once current_revision exists.
    await expect(page.getByTestId('stage-review')).toBeEnabled({ timeout: 30_000 })
    await page.getByTestId('stage-review').click()
    await expect(page.getByTestId('revision-review')).toBeVisible()
    // Expand the albumA batch card to reveal its component chips.
    await page.getByTestId('batch-0').getByRole('button').first().click()
    await expect(page.getByTestId('album-batch-list')).toContainText('albumA')
    await expect(page.getByTestId('album-batch-list').locator('[data-testid^="batch-component-"]').first()).toBeVisible()

    // 7. A second save-and-generate produces revision v2 (metadata row count
    //    grows; the immutable review is replaced by the new current revision).
    await page.getByTestId('stage-configure').click()
    await expect(page.getByTestId('policy-editor')).toBeVisible()
    await page.getByTestId('save-and-generate').click()
    await expect(page.getByTestId('stage-review')).toBeEnabled({ timeout: 30_000 })

    // 8. Back to the feed: the workset is planned with a current revision.
    await page.getByRole('link', { name: '工作集' }).click()
    await expect(page.getByTestId('workset-feed')).toContainText('已规划')
  })
})
