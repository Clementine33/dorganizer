#!/usr/bin/env node
/**
 * e2e/launch-stack.mjs — boot the real stack for the Playwright e2e smoke:
 * Go backend (fresh temp ONSEI_DATA_DIR + generated fixture tree) + Vite dev
 * server wired to the backend's handshake http_port.
 *
 * Reuses the exact backend-launch discipline from scripts/dev-web.mjs:
 *   - the backend's stdin is a pipe WE hold (never inherit), because the
 *     backend watches stdin and shuts down on EOF, which would kill it
 *     instantly in any non-interactive environment (CI, pipes, background);
 *   - the additive ONSEI_BACKEND_READY handshake is parsed for http_port and
 *     Vite is launched with VITE_API_BASE=http://127.0.0.1:<http_port>/api/v1;
 *   - teardown closes the held pipe first (graceful EOF shutdown), then
 *     falls back to killing the process group.
 *
 * Writes a tiny state file (default e2e/.stack-state.json, override with
 * ONSEI_E2E_STATE_FILE) that the Playwright spec reads to learn the fixture
 * root and http port.
 */
import { spawn } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const repoRoot = path.resolve(webDir, '..', '..')
const backendDir = path.join(repoRoot, 'backend', 'go')

const isWin = process.platform === 'win32'
const stateFile = process.env.ONSEI_E2E_STATE_FILE
  ? path.resolve(process.env.ONSEI_E2E_STATE_FILE)
  : path.join(path.dirname(fileURLToPath(import.meta.url)), '.stack-state.json')

// --- Fixture tree -----------------------------------------------------------
// Mirrors backend/go/tests/e2e/http_library_scan_plan_test.go: albumA holds
// lossy+lossless stems (slim:mode1 yields delete operations), albumB is
// audio-only so the folder index spans the root.
function createFixtureTree() {
  const dataDir = mkdtempSync(path.join(os.tmpdir(), 'onsei-e2e-'))
  const rootPath = path.join(dataDir, 'music')
  const files = [
    ['albumA', 'test1.mp3'],
    ['albumA', 'test1.flac'],
    ['albumA', 'test2.mp3'],
    ['albumA', 'test2.flac'],
    ['albumB', 'song1.mp3'],
    ['albumB', 'song2.mp3'],
  ]
  for (const [folder, file] of files) {
    const dir = path.join(rootPath, folder)
    mkdirSync(dir, { recursive: true })
    writeFileSync(path.join(dir, file), 'dummy audio')
  }
  return rootPath
}

const fixtureRoot = createFixtureTree()

// --- Backend ----------------------------------------------------------------
const dataDir = mkdtempSync(path.join(os.tmpdir(), 'onsei-e2e-data-'))
const backend = spawn('go', ['run', './cmd/onsei-organizer-backend'], {
  cwd: backendDir,
  env: { ...process.env, ONSEI_DATA_DIR: dataDir },
  // Held stdin pipe: never inherit (see header comment).
  stdio: ['pipe', 'pipe', 'inherit'],
  // Own process group on POSIX so killing -pid takes the compiled binary
  // (a grandchild of `go run`) down with it.
  detached: !isWin,
})

let lineBuffer = ''
let vite = null
let teardownStarted = false

function launchVite(httpPort) {
  const pnpmCmd = isWin ? 'pnpm.cmd' : 'pnpm'
  vite = spawn(pnpmCmd, ['exec', 'vite', '--', '--port', '5173', '--strictPort'], {
    cwd: webDir,
    env: { ...process.env, VITE_API_BASE: `http://127.0.0.1:${httpPort}/api/v1` },
    stdio: 'inherit',
    // On Windows .cmd shims need a shell; POSIX runs pnpm directly.
    shell: isWin,
  })
  vite.on('exit', (code) => {
    console.log(`[e2e] Vite exited (code ${code}) — tearing down`)
    teardown(code ?? 0)
  })
  vite.on('error', (err) => {
    console.error(`[e2e] failed to launch Vite: ${err.message}`)
    teardown(1)
  })
}

function handleLine(line) {
  // ONSEI_BACKEND_READY port=51234 token=tok-1 version=v1 http_port=54321
  const match = /^ONSEI_BACKEND_READY port=\d+ token=\S* version=\S+ http_port=(\d+)$/.exec(line.trim())
  if (!match) return
  const httpPort = Number(match[1])
  writeFileSync(stateFile, JSON.stringify({ fixtureRoot, httpPort }, null, 2))
  console.log(`[e2e] backend ready (http_port=${httpPort})`)
  console.log(`[e2e] fixture root: ${fixtureRoot}`)
  console.log(`[e2e] state file: ${stateFile}`)
  launchVite(httpPort)
}

backend.stdout.on('data', (chunk) => {
  lineBuffer += chunk.toString()
  let nl
  while ((nl = lineBuffer.indexOf('\n')) >= 0) {
    handleLine(lineBuffer.slice(0, nl))
    lineBuffer = lineBuffer.slice(nl + 1)
  }
})

backend.on('exit', (code) => {
  if (teardownStarted) {
    // Teardown is already in progress: finish() (rmSync cleanup then
    // process.exit) is scheduled and owns the exit. Force-exiting here
    // would skip the temp-dir cleanup.
    return
  }
  console.error(`[e2e] backend exited unexpectedly (code ${code})`)
  if (vite) vite.kill()
  process.exit(code ?? 1)
})

backend.on('error', (err) => {
  console.error(`[e2e] failed to start backend: ${err.message}`)
  process.exit(1)
})

function teardown(exitCode = 0) {
  if (teardownStarted) return
  teardownStarted = true
  console.error(`[e2e] teardown(${exitCode}) — stopping stack`)

  if (vite && vite.exitCode === null) vite.kill()

  const finish = () => {
    try {
      rmSync(path.dirname(fixtureRoot), { recursive: true, force: true })
      rmSync(dataDir, { recursive: true, force: true })
      rmSync(stateFile, { force: true })
      console.error('[e2e] teardown complete')
    } catch {
      /* best-effort cleanup */
    }
    process.exit(exitCode)
  }

  if (backend.exitCode !== null) return finish()

  // Graceful stop: EOF on the pipe we hold makes the backend shut down.
  try {
    backend.stdin?.end()
  } catch {
    /* pipe may already be closed */
  }

  if (isWin) {
    const kill = spawn('taskkill', ['/pid', String(backend.pid), '/t', '/f'])
    kill.on('error', finish)
    kill.on('exit', finish)
  } else {
    try {
      process.kill(-backend.pid, 'SIGTERM')
    } catch {
      backend.kill('SIGTERM')
    }
    setTimeout(finish, 250)
  }
}

process.on('SIGINT', () => teardown(130))
process.on('SIGTERM', () => teardown(143))

console.log(`[e2e] launching backend (data dir: ${dataDir})`)