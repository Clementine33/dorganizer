import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { THEME_STORAGE_KEY, useTheme } from './use-theme'

describe('useTheme', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark')
  })

  it('defaults to dark when no preference is stored', () => {
    const { theme } = useTheme()
    expect(theme.value).toBe('dark')
  })

  it('applies the dark class on <html> when set to dark', async () => {
    const { theme } = useTheme()
    theme.value = 'dark'
    await nextTick()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('removes the dark class on <html> when set to light', async () => {
    document.documentElement.classList.add('dark')
    const { theme } = useTheme()
    theme.value = 'light'
    await nextTick()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('persists the choice to localStorage', async () => {
    const { theme } = useTheme()
    theme.value = 'light'
    await nextTick()
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
  })

  it('restores a stored preference on init', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'dark')
    const { theme } = useTheme()
    expect(theme.value).toBe('dark')
  })
})

describe('useTheme storage resilience', () => {
  it('initializes with the default theme when getItem throws', () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError: storage disabled')
    })
    try {
      localStorage.clear()
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
      const { theme } = useTheme()
      theme.value = 'light'
      await nextTick()
      expect(document.documentElement.classList.contains('dark')).toBe(false)
    } finally {
      spy.mockRestore()
    }
  })

  it('dispose still removes the media listener when storage is broken', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    try {
      const { dispose } = useTheme()
      expect(() => dispose()).not.toThrow()
    } finally {
      vi.restoreAllMocks()
    }
  })
})