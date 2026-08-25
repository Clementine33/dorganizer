import { ref, watch } from 'vue'

export type Theme = 'light' | 'dark' | 'system'

export const THEME_STORAGE_KEY = 'onsei-theme'

function systemPrefersDark(): boolean {
  return (
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-color-scheme: dark)').matches
  )
}

/**
 * Theme plumbing for the workbench: class strategy on `<html>` (`.dark`),
 * preference persisted to localStorage under THEME_STORAGE_KEY. `system`
 * follows the OS preference and reacts to live changes while selected.
 */
export function useTheme() {
  // Storage is best-effort: sandboxed or storage-blocked browsers throw on
  // access, and that must never abort application startup or a theme change.
  let stored: string | null = null
  try {
    stored = localStorage.getItem(THEME_STORAGE_KEY)
  } catch {
    stored = null
  }
  const theme = ref<Theme>(
    stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'dark',
  )

  function apply() {
    const dark = theme.value === 'dark' || (theme.value === 'system' && systemPrefersDark())
    document.documentElement.classList.toggle('dark', dark)
  }

  watch(theme, (value) => {
    // Persist first best-effort, then always apply so a write failure cannot
    // leave the visual theme stuck on the previous value.
    try {
      localStorage.setItem(THEME_STORAGE_KEY, value)
    } catch {
      /* storage unavailable; theme still applies below */
    }
    apply()
  })
  apply()

  let media: MediaQueryList | null = null
  let onSystemChange: (() => void) | null = null
  if (typeof window.matchMedia === 'function') {
    media = window.matchMedia('(prefers-color-scheme: dark)')
    onSystemChange = () => {
      if (theme.value === 'system') apply()
    }
    media.addEventListener('change', onSystemChange)
  }

  return {
    theme,
    dispose: () => {
      if (media && onSystemChange) media.removeEventListener('change', onSystemChange)
    },
  }
}