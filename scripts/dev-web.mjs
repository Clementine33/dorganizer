#!/usr/bin/env node
/**
 * dev-web.mjs — run the Go backend and the Vue/Vite web app together.
 *
 * Workflow:
 *   1. Spawn `go run ./cmd/onsei-organizer-backend` (backend/go module)
 *      with ONSEI_DATA_DIR=<repo>/.dev_data so dev data never touches the
 *      real library location.
 *   2. Read the backend's stdout handshake:
 *        ONSEI_BACKEND_READY port=<grpc> token=<token> version=<v> http_port=<http>
 *      and launch Vite with VITE_API_BASE=http://127.0.0.1:<http>/api/v1.
 *   3. On exit (SIGINT/SIGTERM, Vite exit, backend exit) tear down the
 *      backend first, cross-platform, using plain Node child processes —
 *      no shell scripting.
 *
 * The backend itself watches its parent and shuts down when we die, which
 * acts as a second safety net on top of the explicit kill below.
 */
import { spawn } from 'node:child_process'
import { copyFileSync, existsSync, mkdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const backendDir = path.join(repoRoot, 'backend', 'go')
const webDir = path.join(repoRoot, 'frontend', 'web')
const dataDir = path.join(repoRoot, '.dev_data')

const isWin = process.platform === 'win32'

mkdirSync(dataDir, { recursive: true })

// Seed the dev config from config.json.template when absent so the backend's
// classifier seed (prune.literal_tags) is available to new workset drafts.
// An existing .dev_data/config.json is never overwritten.
const devConfig = path.join(dataDir, 'config.json')
const configTemplate = path.join(repoRoot, 'config.json.template')
if (!existsSync(devConfig) && existsSync(configTemplate)) {
  copyFileSync(configTemplate, devConfig)
  console.log(`[dev-web] seeded ${devConfig} from config.json.template`)
}

const backend = spawn('go', ['run', './cmd/onsei-organizer-backend'], {
  cwd: backendDir,
  env: { ...process.env, ONSEI_DATA_DIR: dataDir },
  // We hold the backend's stdin open ourselves: the backend watches stdin
  // and shuts down on EOF (watchStdinEOF), so inheriting *our* stdin would
  // kill it instantly in any non-interactive environment (CI, IDE
  // integrated terminals, pipes, background jobs) where stdin is already at
  // EOF. An alive pipe of our own keeps it running everywhere and gives
  // teardown a graceful stop signal (close the pipe first, then fall back
  // to killing the process group).
  stdio: ['pipe', 'pipe', 'inherit'],
  // Own process group on POSIX so killing -pid takes the compiled binary
  // (a grandchild of `go run`) down with it.
  detached: !isWin,
})

let lineBuffer = ''
let vite = null
let teardownStarted = false
let teardownExitCode = 0

function launchVite(apiBase) {
  // Launch vite directly via its bin entry (node node_modules/vite/bin/vite.js)
  // instead of a pnpm shim: Windows pnpm installs may expose only pnpm.exe
  // (scoop shim, no pnpm.cmd), and even with a .cmd shim cmd.exe has
  // unreliable argument pass-through (the e2e launcher observed dropped
  // flags). A direct node spawn needs no shell and no shim.
  const viteBin = path.join(webDir, 'node_modules', 'vite', 'bin', 'vite.js')
  vite = spawn(process.execPath, [viteBin], {
    cwd: webDir,
    env: { ...process.env, VITE_API_BASE: apiBase },
    stdio: 'inherit',
  })
  vite.on('exit', (code) => {
    console.log(`[dev-web] Vite exited (code ${code}) — stopping backend`)
    teardown(code ?? 0)
  })
  vite.on('error', (err) => {
    console.error(`[dev-web] failed to launch Vite: ${err.message}`)
    teardown(1)
  })
}

function handleLine(line) {
  // ONSEI_BACKEND_READY port=51234 token=tok-1 version=v1 http_port=54321
  // (token may be empty in dev runs)
  const match = /^ONSEI_BACKEND_READY port=\d+ token=\S* version=\S+ http_port=(\d+)$/.exec(
    line.trim(),
  )
  if (!match) return
  const apiBase = `http://127.0.0.1:${match[1]}/api/v1`
  console.log(`[dev-web] backend ready (http_port=${match[1]})`)
  console.log(`[dev-web] VITE_API_BASE=${apiBase}`)
  launchVite(apiBase)
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
    // The backend exiting during teardown is expected; keep the exit code
    // we chose (e.g. 130/143 for SIGINT/SIGTERM) instead of the backend's.
    process.exit(teardownExitCode)
  } else {
    console.error(`[dev-web] backend exited unexpectedly (code ${code})`)
    // Route through teardown so Windows process-tree cleanup (taskkill /T)
    // still runs and Vite is not left holding port 5173.
    teardown(code ?? 1)
  }
})

backend.on('error', (err) => {
  console.error(`[dev-web] failed to start backend: ${err.message}`)
  process.exit(1)
})

function teardown(exitCode = 0) {
  if (teardownStarted) return
  teardownStarted = true
  teardownExitCode = exitCode

  // Stop Vite first (it is the foreground piece; the backend is the
  // long-lived daemon that must not outlive us). Vite is now a plain node
  // child, so a direct kill works on every platform.
  if (vite && vite.exitCode === null) {
    vite.kill()
  }

  const finish = () => process.exit(exitCode)
  if (backend.exitCode !== null) return finish()

  // Graceful stop: closing the pipe we hold sends the backend an EOF on
  // its stdin watcher, which cancels and shuts it down on its own.
  try {
    backend.stdin?.end()
  } catch {
    /* pipe may already be closed */
  }

  if (isWin) {
    // taskkill /T takes the whole tree (go run + compiled backend exe).
    const kill = spawn('taskkill', ['/pid', String(backend.pid), '/t', '/f'])
    kill.on('error', finish)
    kill.on('exit', finish)
  } else {
    try {
      process.kill(-backend.pid, 'SIGTERM')
    } catch {
      backend.kill('SIGTERM')
    }
    // Give the backend a moment to shut down (it also self-terminates when
    // its parent watcher sees us exit).
    setTimeout(finish, 250)
  }
}

process.on('SIGINT', () => teardown(130))
process.on('SIGTERM', () => teardown(143))

console.log(`[dev-web] starting backend (data dir: ${dataDir})`)