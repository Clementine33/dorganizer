import { expect, test } from '@playwright/test'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'

/**
 * The workbench's addresses (spec §9): they carry identities, never names.
 *
 * The fixture is built from the names an address could never carry — Chinese,
 * an emoji, a percent sign, spaces. The browser flow then checks what the rules
 * promise: opening a folder puts an opaque id in the address instead of its
 * name, a deep link survives a reload, back and forward move between the list
 * and the folder, the list's own filter is the one parameter that rides along,
 * and the retired long addresses say they do not exist instead of guessing.
 *
 * Skipped unless ONSEI_E2E=1 — it needs the launched stack (real backend +
 * Vite) and a generated fixture tree.
 */

const e2eEnabled = process.env.ONSEI_E2E === '1'

const NAMES = ['专辑 A', '🎵 emoji', '100%_hits', 'space here']

function makeNamedTree(): string {
  const root = mkdtempSync(path.join(os.tmpdir(), 'onsei-names-'))
  for (const name of NAMES) {
    const dir = path.join(root, name)
    mkdirSync(dir, { recursive: true })
    writeFileSync(path.join(dir, '01.flac'), 'address fixture')
  }
  return root
}

test.describe('workbench addresses', () => {
  test.skip(!e2eEnabled, 'e2e runs only with ONSEI_E2E=1')

  test('carry ids instead of names, keep deep links, and retire the long addresses', async ({ page }) => {
    test.setTimeout(180_000)
    const root = makeNamedTree()
    const opened = NAMES[0]

    // A library of its own, on the fixture tree above.
    await page.goto('/worksets')
    await page.getByRole('button', { name: '添加媒体库' }).first().click()
    await page.locator('#library-name').fill('Addresses Library')
    await page.locator('#library-root').fill(root)
    await page.getByRole('button', { name: '保存' }).click()
    await page.getByRole('main').getByRole('link', { name: /Addresses Library/ }).first().click()

    // The workbench address is the library id, with no `libraries` segment.
    await expect(page).toHaveURL(/\/worksets\/[^/]+$/)
    await page.getByTestId('scan-button').click()
    await expect(page.getByText('扫描完成')).toBeVisible({ timeout: 60_000 })
    await expect(page.getByTestId('dir-list')).toContainText(opened)

    // Opening a folder: the address carries the directory's identity.
    await page.getByTestId(`dir-link-${opened}`).click()
    await expect(page.getByTestId('member-tree')).toBeVisible({ timeout: 30_000 })
    const address = new URL(page.url())
    expect(address.pathname).toMatch(/\/f\/[0-9a-f]{32}$/)
    expect(address.search).toBe('')
    expect(decodeURIComponent(address.pathname)).not.toContain(opened)
    await expect(page.getByTestId('member-tree')).toContainText('01.flac')

    // A deep link survives a reload: the same folder is opened again, read
    // back from the store, and the address is unchanged.
    const deepLink = page.url()
    await page.reload()
    await expect(page.getByTestId('member-tree')).toBeVisible({ timeout: 30_000 })
    await expect(page.getByTestId('member-tree')).toContainText('01.flac')
    expect(page.url()).toBe(deepLink)

    // Back and forward move between the list and the folder.
    await page.goBack()
    await expect(page.getByTestId('dir-list')).toBeVisible()
    await page.goForward()
    await expect(page.getByTestId('member-tree')).toBeVisible({ timeout: 30_000 })

    // Into the conversion record, then: a search term never enters the address,
    // while the list's own filter does — and nothing else rides along.
    await page.goBack()
    await expect(page.getByTestId('dir-list')).toBeVisible()
    for (const name of NAMES) await page.getByRole('checkbox', { name: `选择 ${name}` }).first().check()
    await page.getByTestId('enter-conversion').click()
    await expect(page.getByTestId('member-toolbar')).toBeVisible()

    await page.getByLabel('搜索文件夹').fill('专')
    await expect(page).toHaveURL(/\/conversion$/)
    expect(new URL(page.url()).search).toBe('')

    await page.getByRole('button', { name: '有变化' }).click()
    await expect(page).toHaveURL(/\/conversion\?filter=change$/)

    // The retired long address says it does not exist — it is not redirected to
    // a guessed target — and it offers the one entry the workbench has.
    const libraryID = new URL(deepLink).pathname.split('/')[2]
    await page.goto(`/worksets/libraries/${libraryID}/files?folder=${encodeURIComponent(opened)}`)
    await expect(page.getByTestId('not-found')).toBeVisible()
    await page.getByTestId('not-found-home').click()
    await expect(page.getByTestId('worksets-page')).toBeVisible()

    // Clean up: the library record this spec created, then the fixture on disk.
    page.on('dialog', (dialog) => void dialog.accept())
    await page.getByRole('button', { name: /^编辑 / }).first().click()
    await page.getByRole('button', { name: '删除媒体库' }).click()
    await expect(page.getByRole('button', { name: '删除媒体库' })).toBeHidden({ timeout: 15_000 })
    rmSync(root, { recursive: true, force: true })
  })
})
