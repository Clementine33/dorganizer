import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export interface StackState {
  fixtureRoot: string
  httpPort: number
}

// Same file launch-stack.mjs writes: override with ONSEI_E2E_STATE_FILE
// (resolved the same way), defaulting to the in-repo .stack-state.json.
export function readStackState(): StackState {
  const stateFile = process.env.ONSEI_E2E_STATE_FILE
    ? resolve(process.env.ONSEI_E2E_STATE_FILE)
    : fileURLToPath(new URL('../.stack-state.json', import.meta.url))
  return JSON.parse(readFileSync(stateFile, 'utf8')) as StackState
}