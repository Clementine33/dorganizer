import { afterEach, describe, expect, it, vi } from 'vitest'

describe('Playwright stack configuration', () => {
  afterEach(() => {
    vi.unstubAllEnvs()
    vi.resetModules()
  })

  it('does not start a web server when the e2e smoke is disabled', async () => {
    vi.stubEnv('ONSEI_E2E', '')
    vi.resetModules()

    const { default: config } = await import('../playwright.config')

    expect(config.webServer).toBeUndefined()
  })

  it('starts the real stack when the e2e smoke is enabled', async () => {
    vi.stubEnv('ONSEI_E2E', '1')
    vi.resetModules()

    const { default: config } = await import('../playwright.config')

    expect(config.webServer).toMatchObject({
      command: 'node e2e/launch-stack.mjs',
      url: 'http://127.0.0.1:5173',
    })
  })
})
