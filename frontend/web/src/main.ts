import { createPinia } from 'pinia'
import { createApp } from 'vue'
import App from './App.vue'
import { router } from './app/router'
import { ApiClient, apiClientKey } from './lib/api/client'
import { desktopAdapterKey } from './lib/desktop/desktop-adapter'
import { createDesktopAdapter } from './lib/desktop/web-adapter'
import './style.css'

async function bootstrap() {
  const desktopAdapter = createDesktopAdapter()
  const apiClient = new ApiClient(await desktopAdapter.resolveApi())
  const app = createApp(App)

  app.provide(desktopAdapterKey, desktopAdapter)
  app.provide(apiClientKey, apiClient)
  app.use(createPinia()).use(router).mount('#app')
}

void bootstrap()
