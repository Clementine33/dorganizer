// Minimal jsdom polyfills for the test environment.
// Vitest's jsdom population does not expose localStorage (jsdom implements
// it on its own realm and the global copy misses it), and Node's built-in
// experimental localStorage is unavailable without --localstorage-file.
// Provide a deterministic in-memory Storage for all tests.
const store = new Map<string, string>()

const memoryStorage: Storage = {
  get length() {
    return store.size
  },
  clear() {
    store.clear()
  },
  getItem(key: string) {
    return store.has(key) ? store.get(key)! : null
  },
  key(index: number) {
    return [...store.keys()][index] ?? null
  },
  removeItem(key: string) {
    store.delete(key)
  },
  setItem(key: string, value: string) {
    store.set(String(key), String(value))
  },
}

Object.defineProperty(globalThis, 'localStorage', {
  value: memoryStorage,
  configurable: true,
})

// jsdom does not implement window.matchMedia; provide a no-op stub so
// theme composables that consult the system preference can run in tests.
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }),
})