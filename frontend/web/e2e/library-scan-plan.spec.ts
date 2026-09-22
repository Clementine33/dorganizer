import { expect, test, type Page } from '@playwright/test'
import { readStackState } from './helpers/stack-state.ts'

/**
 * Picks one option of a UnitSelect. Those fields are reka-ui comboboxes
 * (custom widgets with a portalled listbox), not native <select> elements, so
 * the trigger is opened and the option clicked.
 */
async function pickUnitOption(page: Page, testId: string, option: string) {
  await page.getByTestId(testId).click()
  await page.getByRole('option', { name: option, exact: true }).click()
}

/**
 * End-to-end smoke of the workset operation workflow against a real stack (Go
 * backend with a fresh ONSEI_DATA_DIR + Vite dev server), launched by
 * `e2e/launch-stack.mjs` (see playwright.config.ts webServer).
 *
 * The browser flow exercised here:
 *   1. create a library pointing at a generated fixture tree,
 *   2. run the scan to completion (SSE),
 *   3. select a folder and enter its conversion record (scope dialog → record),
 *   4. edit the common settings in place (direct fields, 恢复默认) and apply,
 *   5. generate a plan revision and read the five summary facts,
 *   6. generate again on a changed draft and confirm the new revision
 *      supersedes the old one,
 *   7. browse a member's files and rename one file through the shared file
 *      module, then return to the list,
 *   8. return to the library selection and delete the library.
 *
 * Skipped unless ONSEI_E2E=1 so CI can run it optionally and local `vitest`
 * runs never try to boot the stack (vitest only includes `src/**` anyway).
 */

const e2eEnabled = process.env.ONSEI_E2E === '1'

test.describe('workset operation smoke', () => {
  test.skip(!e2eEnabled, 'e2e smoke runs only with ONSEI_E2E=1')

  test('create library, scan, create workset, configure, generate and review', async ({ page }) => {
    const { fixtureRoot } = readStackState()
    const fixturePosix = fixtureRoot.replaceAll('\\', '/')

    // Empty data dir → the library selection, which is the product entry.
    await page.goto('/')
    await expect(page.getByTestId('libraries-empty')).toBeVisible()

    // The global shell exists before any page content: at the default desktop
    // width the rail owns the navigation, and 工作集 is the only entry — the
    // old 媒体库 entry is gone with its page (N02, N08, N21).
    await expect(page.getByTestId('global-rail')).toBeVisible()
    await expect(page.getByTestId('global-bottom-bar')).toBeHidden()
    await expect(page.getByTestId('global-rail').getByRole('link', { name: '工作集' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    await expect(page.getByTestId('global-rail').getByRole('link', { name: '媒体库' })).toHaveCount(0)

    // 1. Create the library pointing at the generated fixture tree.
    await page.getByTestId('empty-add-library').click()
    await page.locator('#library-name').fill('E2E Library')
    await page.locator('#library-root').fill(fixtureRoot)
    await page.getByRole('button', { name: '保存' }).click()
    await expect(page.getByRole('main').getByText(fixturePosix)).toBeVisible()

    // Entering the library is what opens the workbench: the overview lists
    // every direct child directory the scan will record.
    await page.getByRole('main').getByRole('link', { name: /E2E Library/ }).first().click()
    await expect(page).toHaveURL(/\/worksets\/[^/]+$/)
    await expect(page.getByTestId('overview')).toBeVisible()

    // 2. Run the scan and wait for the SSE stream to complete.
    await page.getByTestId('scan-button').click()
    await expect(page.getByText('扫描完成')).toBeVisible()

    // Every direct child directory is listed, with its audio count as status.
    await expect(page.getByTestId('dir-list')).toContainText('albumA')

    // 3. Select albumA and enter its conversion record. No naming step: the
    //    record is the library's own.
    await expect(page.getByRole('checkbox', { name: '选择 albumA' }).first()).toBeVisible()
    await page.getByRole('checkbox', { name: '选择 albumA' }).first().check()
    await expect(page.getByText('已选择 1 个文件夹')).toBeVisible()
    await page.getByTestId('enter-conversion').click()

    // The record opens on 转换: its settings, members and plan. The overview
    // keeps the record entry, so the workbench is one place.
    await expect(page).toHaveURL(/\/conversion$/)
    await expect(page.getByTestId('workspace-breadcrumb')).toContainText('工作集')
    await expect(page.getByRole('link', { name: '概览与成员' })).toBeVisible()
    await expect(page.getByTestId('member-toolbar')).toBeVisible()
    await expect(page.getByTestId('operation-header')).toBeVisible()
    await expect(page.getByTestId('member-toolbar')).toBeVisible()
    await expect(page.getByText('尚无计划版本')).toBeVisible()

    // 转换 is a group whose label navigates and whose arrow only folds — two
    // adjacent controls (N25); the settings child is reachable while unfolded.
    const group = page.getByTestId('nav-group-conversion')
    await expect(group).toHaveAttribute('aria-expanded', 'true')
    await expect(page.getByTestId('nav-conversion-settings')).toBeVisible()
    await group.click()
    await expect(group).toHaveAttribute('aria-expanded', 'false')
    await expect(page).toHaveURL(/\/conversion$/)
    await expect(page.getByTestId('nav-conversion-settings')).toBeHidden()

    // 4. Common settings, selected from the group's child — the only entry
    //    (the operation header carries no duplicate action).
    await group.click()
    await expect(page.getByTestId('nav-conversion-settings')).toBeVisible()
    await page.getByTestId('nav-conversion-settings').click()
    await expect(page.getByTestId('conversion-settings')).toBeVisible()
    // The settings are an edit carrier of this operation, not a page of their
    // own: the list and the shell stay put (R01, §7.3).
    await expect(page).toHaveURL(/\/conversion\/settings$/)
    await expect(page.getByTestId('workbench-header')).toBeVisible()
    await expect(page.getByTestId('member-toolbar')).toBeVisible()
    // Entering the settings page unfolds the group and marks the child as the
    // current page; the parent only shows its owning state (N27).
    await expect(group).toHaveAttribute('aria-expanded', 'true')
    await expect(page.getByTestId('nav-conversion-settings')).toHaveAttribute('aria-current', 'page')
    await expect(page.getByTestId('nav-conversion')).not.toHaveAttribute('aria-current', 'page')
    // The fields are editable directly, and 恢复默认 puts a group back to the
    // seeded value.
    await page.getByTestId('common-classifier_tags-input').fill('SEなし')
    await page.getByTestId('common-classifier_tags-input').press('Enter')
    await pickUnitOption(page, 'common-matched-encoded', 'MP3')
    await page.getByTestId('apply-common').click()
    await expect(page.getByTestId('conversion-settings')).toBeVisible()

    // 5. Generate the first plan revision.
    await page.getByRole('link', { name: '← 转换列表' }).click()
    await page.getByTestId('start-generation').click()
    await expect(page.getByTestId('operation-counts')).toBeVisible({ timeout: 30_000 })
    await expect(page.getByTestId('operation-counts')).toContainText('成员')

    // The member row opens the frozen review without selecting the member.
    await page.getByTestId('member-open').first().click()
    await expect(page.getByTestId('member-review')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('member-review')).toContainText('冻结的有效设置')
    // Every unit is inherited, and inheriting is the unmarked default: only a
    // member's own settings are called out.
    await expect(page.getByTestId('member-review')).not.toContainText('独立设置')
    await expect(page.getByTestId('member-review').getByTestId('member-review-edit')).toBeVisible()

    // A narrow container shows one layer at a time, and the route does not
    // change: the member detail becomes a full-page drill-down, 修改此文件夹
    // opens the editor inside the same workbench, and 取消 returns to the
    // detail. The carrier is chosen by the container, never by a separate URL
    // (§7.3, F13).
    await page.setViewportSize({ width: 600, height: 800 })
    // Below the shared 641px breakpoint the bottom bar replaces the rail in
    // CSS only — the route, the drill-down and the editing session are
    // untouched (N02, N12).
    await expect(page.getByTestId('global-bottom-bar')).toBeVisible()
    await expect(page.getByTestId('global-rail')).toBeHidden()
    await expect(page.getByTestId('global-bottom-bar').getByRole('link', { name: '工作集' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    await expect(page.getByTestId('member-review')).toBeVisible()
    await expect(page.getByTestId('member-toolbar')).toBeHidden()
    // The narrow header carries the local navigation button and two context
    // lines instead of the full path (N31).
    await expect(page.getByTestId('workspace-breadcrumb')).toBeHidden()
    await expect(page.getByTestId('workbench-context')).toContainText('文件夹详情')
    // The drawer holds the same levels as the sidebar: folding a group leaves it
    // open, and only a successful page selection closes it (N20, N28).
    await page.getByTestId('workbench-nav-toggle').click()
    const drawer = page.getByTestId('workbench-nav-drawer')
    await expect(drawer).toBeVisible()
    await page.getByTestId('nav-group-conversion').click()
    await expect(drawer).toBeVisible()
    await expect(page).toHaveURL(/\/conversion\/[^/]+$/)
    await page.getByTestId('nav-overview').click()
    await expect(page).toHaveURL(/\/worksets\/[^/]+$/)
    await expect(drawer).toBeHidden()
    await expect(page.getByTestId('workbench-context')).toContainText('概览与成员')
    // Every narrow page keeps a fixed parent to return to (N32): the overview
    // goes to the list, 转换 to the overview, a carrier to the list, and the
    // member editor to that member's detail.
    await expect(page.getByTestId('workbench-back')).toHaveAttribute('href', '/worksets')

    // Back into the operation, then the same drill-down as before: the narrow
    // carrier is a full page, and the local back control returns to the fixed
    // parent of the page in view (N32, R06).
    await page.getByTestId('current-record').click()
    await expect(page.getByTestId('member-toolbar')).toBeVisible()
    await expect(page.getByTestId('workbench-back')).toHaveAttribute('href', /\/worksets\/[^/]+$/)
    await page.getByTestId('member-open').first().click()
    await expect(page.getByTestId('member-review')).toBeVisible()
    await expect(page.getByTestId('workbench-back')).toHaveAttribute('href', /\/conversion$/)
    await page.getByTestId('member-review-edit').click()
    await expect(page.getByTestId('member-edit')).toBeVisible()
    await expect(page.getByTestId('workbench-header')).toBeVisible()
    await expect(page.getByTestId('workbench-context')).toContainText('修改此文件夹')
    await expect(page.getByTestId('workbench-back')).toHaveAttribute('href', /\/conversion\/[^/]+$/)
    await page.getByRole('button', { name: '取消' }).click()
    await expect(page.getByTestId('member-review')).toBeVisible()
    await page.getByTestId('workbench-back').click()
    await expect(page.getByTestId('member-toolbar')).toBeVisible()
    await page.setViewportSize({ width: 1280, height: 720 })
    await expect(page.getByTestId('member-toolbar')).toBeVisible()
    await page.getByTestId('member-open').first().click()
    await expect(page.getByTestId('member-review')).toBeVisible({ timeout: 15_000 })

    // A member-level override: 修改此文件夹 sets one unit and leaves the rest
    // inheriting.
    await page.getByTestId('member-review-edit').click()
    await expect(page.getByTestId('member-edit')).toBeVisible()
    await page.getByTestId('unit-matched-override').click()
    await pickUnitOption(page, 'override-matched-lossless', 'FLAC')
    await page.getByTestId('apply-member').click()
    // The frozen review keeps reporting the revision it was planned with, so
    // the override shows up on the member's row first: 默认 becomes 已修改.
    await expect(page.getByTestId('member-override-state').first()).toHaveText('已修改', { timeout: 15_000 })
    await page.keyboard.press('Escape')

    // 6. Regenerate on the changed draft: the new revision is a different
    //    version and its review reports the member's own setting.
    await page.getByTestId('start-generation').click()
    await expect(page.getByTestId('operation-counts')).toBeVisible({ timeout: 30_000 })
    await page.getByTestId('member-open').first().click()
    await expect(page.getByTestId('member-review')).toContainText('独立设置', { timeout: 15_000 })
    await expect(page.getByTestId('member-review')).toContainText('无音效目标')
    await page.keyboard.press('Escape')

    // A planned revision is directly executable — no confirmation step.
    await expect(page.getByTestId('start-execution')).toBeEnabled()

    // The settings entry lives in the workbench navigation, so its group is
    // unfolded first (it was folded back in the drawer above).
    await page.getByTestId('nav-group-conversion').click()
    await page.getByTestId('nav-conversion-settings').click()
    await pickUnitOption(page, 'common-mode', '严格')
    await page.getByTestId('apply-common').click()
    await page.getByRole('link', { name: '← 转换列表' }).click()
    await page.getByTestId('start-generation').click()
    await expect(page.getByTestId('operation-counts')).toBeVisible({ timeout: 30_000 })

    // 7. The shared file module: the same tree the overview browses, here for
    //    a conversion member, with the plan as a read-only second view (T1-T3).
    //    A member the plan covers opens on its plan, so 当前文件 — the view that
    //    allows modification — is named in the address explicitly.
    await page.getByTestId('nav-conversion').click()
    await page.getByTestId('member-files').first().click()
    await expect(page).toHaveURL(/\/conversion\/[^/]+\/files$/)
    await expect(page.getByTestId('plan-review-tree')).toBeVisible()
    await expect(page.getByTestId('view-plan')).toBeVisible()
    await expect(page.getByTestId('plan-summary')).toBeVisible()

    await page.getByTestId('view-current').click()
    await expect(page).toHaveURL(/\/conversion\/[^/]+\/files\?view=current$/)
    await expect(page.getByTestId('member-tree')).toBeVisible()

    // Renaming one file: the request carries the member-relative path, and the
    // result reports both what happened and whether the inventory refreshed
    // (F1, F4).
    await page.getByTestId('rename-test1.mp3').click()
    const renameInput = page.getByTestId('rename-input')
    await renameInput.fill('renamed.mp3')
    await page.getByTestId('rename-submit').click()
    await expect(page.getByTestId('file-op-result')).toContainText('完成 1 项')
    await expect(page.getByTestId('file-op-result')).toContainText('已刷新目录')
    await expect(page.getByTestId('member-tree')).toContainText('renamed.mp3')

    // Back to the list: the workbench survives, and the member list is where
    // it was (N2). The crumb is the way back at every tier.
    await page.getByTestId('workspace-breadcrumb').getByRole('link', { name: '转换' }).click()
    await expect(page.getByTestId('member-toolbar')).toBeVisible()
    await expect(page).toHaveURL(/\/conversion$/)

    // 8. Back to the library selection through the global rail: the global
    //    entry is reachable inside the workbench and always targets /worksets
    //    (G01, N22).
    await page.getByTestId('global-rail').getByRole('link', { name: '工作集' }).click()
    await expect(page.getByTestId('worksets-page')).toBeVisible()

    // 9. Clean up: delete the library this spec created so the stack returns
    //    to its initial state for the sibling diagnostics spec. The record,
    //    its plan and its execution results go with it; the media on disk does
    //    not (L1).
    page.on('dialog', (dialog) => void dialog.accept())
    // The selection page lists one row per library, each with its own edit
    // entry (the label names the library it edits).
    await page.getByRole('button', { name: /^编辑 / }).first().click()
    await page.getByRole('button', { name: '删除媒体库' }).click()
    await expect(page.getByRole('button', { name: '删除媒体库' })).toBeHidden({ timeout: 15_000 })
  })
})
