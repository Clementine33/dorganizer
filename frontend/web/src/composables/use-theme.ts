import { ref, watch, type Ref } from 'vue'

export type Theme = 'light' | 'dark' | 'system'

export const THEME_STORAGE_KEY = 'onsei-theme'

function systemPrefersDark(): boolean {
  return (
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-color-scheme: dark)').matches
  )
}

function readStored(): Theme {
  // Storage is best-effort: sandboxed or storage-blocked browsers throw on
  // access, and that must never abort application startup or a theme change.
  let stored: string | null = null
  try {
    stored = localStorage.getItem(THEME_STORAGE_KEY)
  } catch {
    stored = null
  }
  return stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'dark'
}

function apply(value: Theme): void {
  const dark = value === 'dark' || (value === 'system' && systemPrefersDark())
  document.documentElement.classList.toggle('dark', dark)
}

// One theme state for the whole application (N14): the desktop rail and the
// list pages' 更多 menu both render from it, so no entry can hold a stale value
// and react to a system-preference change with a preference the user already
// replaced. Created on first use, never per component instance.
let theme: Ref<Theme> | null = null
let media: MediaQueryList | null = null
let onSystemChange: (() => void) | null = null

/**
 * Theme plumbing for the workbench: class strategy on `<html>` (`.dark`),
 * preference persisted to localStorage under THEME_STORAGE_KEY. `system`
 * follows the OS preference and reacts to live changes while selected.
 */
export function useTheme() {
  if (!theme) {
    theme = ref(readStored())
    watch(theme, (value) => {
      // Persist first best-effort, then always apply so a write failure cannot
      // leave the visual theme stuck on the previous value.
      try {
        localStorage.setItem(THEME_STORAGE_KEY, value)
      } catch {
        /* storage unavailable; theme still applies below */
      }
      apply(value)
    })
    apply(theme.value)

    if (typeof window.matchMedia === 'function') {
      media = window.matchMedia('(prefers-color-scheme: dark)')
      onSystemChange = () => {
        if (theme?.value === 'system') apply('system')
      }
      media.addEventListener('change', onSystemChange)
    }
  }

  return {
    theme,
    dispose: () => {
      if (media && onSystemChange) media.removeEventListener('change', onSystemChange)
      media = null
      onSystemChange = null
    },
  }
}
