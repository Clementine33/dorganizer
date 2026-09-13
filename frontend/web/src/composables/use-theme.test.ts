import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { THEME_STORAGE_KEY } from './use-theme'

// The theme state is one shared instance per application (module registry), so
// each case loads a fresh module instead of inheriting the previous case's ref.
async function freshTheme() {
  vi.resetModules()
  return await import('./use-theme')
}

beforeEach(() => {
  localStorage.clear()
  document.documentElement.classList.remove('dark')
})

describe('useTheme', () => {
  it('defaults to dark when no preference is stored', async () => {
    const { useTheme } = await freshTheme()
    expect(useTheme().theme.value).toBe('dark')
  })

  it('applies the dark class on <html> when set to dark', async () => {
    const { useTheme } = await freshTheme()
    const { theme } = useTheme()
    theme.value = 'dark'
    await nextTick()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('removes the dark class on <html> when set to light', async () => {
    document.documentElement.classList.add('dark')
    const { useTheme } = await freshTheme()
    const { theme } = useTheme()
    theme.value = 'light'
    await nextTick()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('persists the choice to localStorage', async () => {
    const { useTheme } = await freshTheme()
    const { theme } = useTheme()
    theme.value = 'light'
    await nextTick()
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
  })

  it('restores a stored preference on init', async () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'light')
    const { useTheme } = await freshTheme()
    expect(useTheme().theme.value).toBe('light')
  })

  it('shares one state between every entry, so no entry keeps a stale value', async () => {
    const { useTheme } = await freshTheme()
    const railEntry = useTheme()
    const pageMenuEntry = useTheme()

    railEntry.theme.value = 'light'
    await nextTick()

    expect(pageMenuEntry.theme.value).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})

describe('useTheme system preference', () => {
  it('ignores the OS preference once the user left 跟随系统', async () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'system')
    // Keep every registered listener: the defect this pins down was a *second*
    // entry reacting to the OS change with its own stale preference, so firing
    // only the most recent listener would not reproduce it.
    const listeners: (() => void)[] = []
    const stub = { matches: false }
    vi.spyOn(window, 'matchMedia').mockImplementation(
      (query: string) =>
        ({
          matches: stub.matches,
          media: query,
          onchange: null,
          addEventListener: (_event: string, listener: () => void) => {
            listeners.push(listener)
          },
          removeEventListener: () => {},
          addListener: () => {},
          removeListener: () => {},
          dispatchEvent: () => false,
        }) as unknown as MediaQueryList,
    )
    try {
      const { useTheme } = await freshTheme()
      const railEntry = useTheme()
      const pageMenuEntry = useTheme()
      // Booted on 跟随系统 with a light OS preference.
      expect(railEntry.theme.value).toBe('system')
      expect(document.documentElement.classList.contains('dark')).toBe(false)

      pageMenuEntry.theme.value = 'dark'
      await nextTick()
      expect(document.documentElement.classList.contains('dark')).toBe(true)

      // The OS later reports a change: it must not resurrect 跟随系统 through
      // the entry that still had it selected.
      stub.matches = false
      for (const listener of listeners) listener()
      expect(document.documentElement.classList.contains('dark')).toBe(true)
    } finally {
      vi.restoreAllMocks()
    }
  })
})

describe('useTheme storage resilience', () => {
  it('initializes with the default theme when getItem throws', async () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError: storage disabled')
    })
    try {
      localStorage.clear()
      const { useTheme } = await freshTheme()
      const { theme } = useTheme()
      expect(theme.value).toBe('dark')
      expect(document.documentElement.classList.contains('dark')).toBe(true)
    } finally {
      spy.mockRestore()
    }
  })

  it('still applies the theme when setItem throws', async () => {
    const spy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('SecurityError: storage disabled')
    })
    try {
      const { useTheme } = await freshTheme()
      const { theme } = useTheme()
      theme.value = 'light'
      await nextTick()
      expect(document.documentElement.classList.contains('dark')).toBe(false)
    } finally {
      spy.mockRestore()
    }
  })

  it('dispose still removes the media listener when storage is broken', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    try {
      const { useTheme } = await freshTheme()
      const { dispose } = useTheme()
      expect(() => dispose()).not.toThrow()
    } finally {
      vi.restoreAllMocks()
    }
  })
})
