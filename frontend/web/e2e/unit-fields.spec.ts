import { expect, test, type Locator, type Page } from '@playwright/test'

test.skip(!process.env.ONSEI_E2E && !process.env.ONSEI_SELECT_TEST_URL, 'Needs a Vite server')
test.use({ baseURL: process.env.ONSEI_SELECT_TEST_URL ?? 'http://127.0.0.1:5173' })

async function expectAnchored(page: Page, trigger: Locator) {
  const menu = page.getByRole('listbox')
  await expect(menu).toBeVisible()
  await expect.poll(async () => {
    const anchor = await trigger.boundingBox()
    const popup = await menu.boundingBox()
    if (!anchor || !popup) return false
    const viewport = page.viewportSize()!
    const gap = Math.min(
      Math.abs(popup.y - anchor.y - anchor.height),
      Math.abs(anchor.y - popup.y - popup.height),
    )
    return Math.abs(popup.x - anchor.x) < 2 && gap <= 5
      && popup.x >= 0 && popup.y >= 0
      && popup.x + popup.width <= viewport.width
      && popup.y + popup.height <= viewport.height
  }).toBe(true)
}

for (const isMobile of [false, true]) {
  test.describe(isMobile ? 'mobile emulation' : 'desktop', () => {
    test.use({ isMobile, hasTouch: isMobile, deviceScaleFactor: isMobile ? 3 : 1 })

    test('conversion menus stay anchored when scrolled, resized and near the viewport edge', async ({ page }, testInfo) => {
      // Mount the real shared fields with production CSS inside a scrolling panel.
      await page.route('**/__unit-fields', route => route.fulfill({
        contentType: 'text/html',
        body: `<html><head><meta name="viewport" content="width=device-width, initial-scale=1"></head><body><div id="app"></div><script type="module">
          import { createApp, h, reactive } from '/node_modules/.vite/deps/vue.js';
          import UnitFields from '/src/features/worksets/UnitFields.vue';
          import '/src/style.css';
          const values = reactive({ mode: 'strict', matched: { lossless: { codec: 'flac' }, encoded: { codec: 'mp3' } } });
          createApp({ setup: () => () => h('div', {
            id: 'panel', style: 'height:100dvh;overflow:auto;padding:16px;transform:translateZ(0)'
          }, [h('div', { style: 'height:55vh' }),
            ...['mode', 'matched'].map(unit => h(UnitFields, {
              unit, label: unit, scope: 'test', value: values[unit],
              onChange: value => values[unit] = value
            })), h('div', { style: 'height:100vh' })]) }).mount('#app');
        </script></body></html>`,
      }))
      await page.setViewportSize({ width: 393, height: 852 })
      await page.goto('/__unit-fields')
      for (const field of ['mode', 'matched-lossless', 'matched-encoded']) {
        const trigger = page.getByTestId(`test-${field}`)
        await trigger.click()
        await expectAnchored(page, trigger)
        await page.locator('#panel').evaluate(el => { el.scrollTop += 35 })
        await expectAnchored(page, trigger)
        await page.setViewportSize({ width: 1440, height: 900 })
        await expectAnchored(page, trigger)
        await page.setViewportSize({ width: 320, height: 640 })
        await expectAnchored(page, trigger)
        await page.keyboard.press('Escape')
        await expect(trigger).toBeFocused()
        await page.setViewportSize({ width: 393, height: 852 })
      }

      const encoded = page.getByTestId('test-matched-encoded')
      if (isMobile) await encoded.tap()
      else await encoded.click()
      await page.getByRole('option', { name: '不需要', exact: true }).click()
      await expect(encoded).toHaveText(/不需要/)
      await expect(page.getByTestId('test-matched-bitrate')).toBeDisabled()
      await encoded.press('Enter')
      await expect(page.getByRole('option', { name: '不需要', exact: true })).toBeFocused()
      await page.keyboard.press('End')
      await expect(page.getByRole('option', { name: 'Opus', exact: true })).toBeFocused()
      await page.keyboard.press('Enter')
      await expect(encoded).toHaveText(/Opus/)
      await expect(page.getByTestId('test-matched-bitrate')).toBeEnabled()

      const lossless = page.getByTestId('test-matched-lossless')
      await lossless.click()
      await page.getByRole('option', { name: '不需要', exact: true }).click()
      await expect(lossless).toHaveText(/不需要/)
      await expect(encoded).toHaveText(/Opus/)

      await page.locator('#panel').evaluate(el => {
        (el.firstElementChild as HTMLElement).style.height = 'calc(100dvh - 90px)'
        el.scrollTop = 0
      })
      await encoded.click()
      await expectAnchored(page, encoded)
      await expect(page.getByRole('listbox')).toHaveAttribute('data-side', 'top')
      await page.screenshot({ path: testInfo.outputPath('mobile-menu.png') })
      await page.keyboard.press('Escape')
      const mode = page.getByTestId('test-mode')
      await mode.click()
      await page.getByRole('option', { name: '可用源（available_sources）', exact: true }).click()
      await expect(mode).toHaveText(/available_sources/)
      await page.setViewportSize({ width: 1440, height: 900 })
      await mode.click()
      await expectAnchored(page, mode)
      await page.screenshot({ path: testInfo.outputPath('desktop-menu.png') })
    })
  })
}
