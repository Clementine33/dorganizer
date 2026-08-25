import type { InjectionKey } from 'vue'
import { inject } from 'vue'

export interface DesktopAdapter {
  platform: 'browser' | 'tauri'
  resolveApi(): Promise<{ baseUrl: string; token: string | null }>
  pickFolder(): Promise<string | null>
}

export const desktopAdapterKey: InjectionKey<DesktopAdapter> = Symbol('desktop-adapter')

export function useDesktopAdapter(): DesktopAdapter {
  const adapter = inject(desktopAdapterKey)
  if (!adapter) throw new Error('DesktopAdapter was not provided')
  return adapter
}
