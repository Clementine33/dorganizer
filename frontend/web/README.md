# onsei-organizer-web

Vue 3 + Vite + TypeScript web client for the Onsei Organizer music-library
workbench. Part of the root pnpm workspace (`pnpm-workspace.yaml`).

Stack: Vue 3.5, Vite 8, TypeScript 6, Tailwind CSS v4, shadcn-vue (local
component registry under `src/components/ui`), Pinia, vue-router, Vitest +
Vue Test Utils.

## Commands (run from repo root or `frontend/web`)

- `pnpm -C frontend/web dev` — Vite dev server alone
- `pnpm dev:web` (repo root) — `node scripts/dev-web.mjs`: starts the Go
  backend, parses `ONSEI_BACKEND_READY` for `http_port`, and launches Vite
  with `VITE_API_BASE=http://127.0.0.1:<http_port>/api/v1`
- `pnpm -C frontend/web typecheck` — `vue-tsc -b`
- `pnpm -C frontend/web test` — Vitest
- `pnpm -C frontend/web build` — typecheck + production build

## Conventions

- Exact dependency versions only (no floating `latest`).
- Utility/theme code lives in `src/lib` / `src/composables` / `src/stores`;
  new UI primitives go through the shadcn-vue CLI (`components.json`).
- Theme: class strategy on `<html>` (`.dark`), preference in
  `localStorage['onsei-theme']` (`light | dark | system`); see
  `src/composables/use-theme.ts` and the pre-paint script in `index.html`.