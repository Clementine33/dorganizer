import type { DesktopAdapter } from './desktop-adapter'

export class WebDesktopAdapter implements DesktopAdapter {
  readonly platform = 'browser' as const

  async resolveApi(): Promise<{ baseUrl: string; token: string | null }> {
    return {
      baseUrl: import.meta.env.VITE_API_BASE || '/api/v1',
      // Only the developer server inlines ONSEI_TOKEN (see vite.config.ts);
      // never fall back to a VITE_* variable, which would ship the credential
      // in browser-exposed config and build output.
      token: import.meta.env.ONSEI_TOKEN || null,
    }
  }

  async pickFolder(): Promise<null> {
    return null
  }
}

export function createDesktopAdapter(): DesktopAdapter {
  return new WebDesktopAdapter()
}
