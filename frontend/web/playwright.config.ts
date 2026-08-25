import { defineConfig, devices } from '@playwright/test'

/**
 * Playwright e2e for the web/gin library prototype.
 *
 * The spec (e2e/library-scan-plan.spec.ts) is skipped unless ONSEI_E2E=1, so
 * this config is a no-op in ordinary `pnpm test` runs and only boots the full
 * stack (Go backend on a fresh temp data dir + Vite) on demand:
 *
 *   ONSEI_E2E=1 pnpm exec playwright test
 *
 * The stack is started by `node e2e/launch-stack.mjs`, which spawns the real
 * backend binary via `go run`, parses the additive ONSEI_BACKEND_READY
 * http_port handshake, and launches Vite pointed at it (see the launcher's
 * header for the held-stdin rationale). Playwright waits for the Vite
 * dev-server URL; the backend port is dynamic and never needs to be known
 * up front.
 */
const e2eEnabled = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env?.ONSEI_E2E === '1'

export default defineConfig({
  testDir: './e2e',
  // The stack is a single shared state machine — one spec, one worker.
  workers: 1,
  fullyParallel: false,
  timeout: 120_000,
  expect: { timeout: 15_000 },
  reporter: [['list']],
  use: {
    baseURL: 'http://127.0.0.1:5173',
    trace: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: e2eEnabled
    ? {
        command: 'node e2e/launch-stack.mjs',
        url: 'http://127.0.0.1:5173',
        reuseExistingServer: false,
        timeout: 180_000,
        // The launcher must be killed by SIGTERM so it can run its graceful
        // teardown (held-stdin EOF, process-group SIGTERM) and clean temp dirs.
        gracefulShutdown: { signal: 'SIGTERM', timeout: 30_000 },
      }
    : undefined,
})