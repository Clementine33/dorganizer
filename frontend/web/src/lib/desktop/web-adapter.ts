import type { DesktopAdapter } from './desktop-adapter'

export class WebDesktopAdapter implements DesktopAdapter {
  readonly platform = 'browser' as const

  async resolveApi(): Promise<{ baseUrl: string; token: string | null }> {
    return {
      baseUrl: import.meta.env.VITE_API_BASE || '/api/v1',
      token: import.meta.env.ONSEI_TOKEN || import.meta.env.VITE_ONSEI_TOKEN || null,
    }
  }

  async pickFolder(): Promise<null> {
    return null
  }
}

export function createDesktopAdapter(): DesktopAdapter {
  return new WebDesktopAdapter()
}
