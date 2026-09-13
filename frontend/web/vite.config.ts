import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'
import VueDevTools from 'vite-plugin-vue-devtools'


// https://vite.dev/config/
export default defineConfig(({ command }) => ({
  plugins: [vue(), tailwindcss(),VueDevTools(),],
  define:
    command === 'serve'
      ? {
          // Vite only exposes VITE_* by default; opt this one local bearer
          // token in explicitly so WebDesktopAdapter can read the backend's
          // ONSEI_TOKEN during development. Build output never embeds it.
          'import.meta.env.ONSEI_TOKEN': JSON.stringify(process.env.ONSEI_TOKEN ?? ''),
        }
      : {},
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
}))