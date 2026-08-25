import { beforeEach, describe, expect, it } from 'vitest'
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