# AGENTS.md

## Architecture
- Go backend (`backend/go`, entry `cmd/onsei-organizer-backend/main.go`): gRPC on `127.0.0.1:0` + net/http API on an ephemeral port; startup prints an `ONSEI_BACKEND_READY port=… token=… http_port=…` handshake that web dev consumes via `VITE_API_BASE`.
- Vue 3 + TS web frontend (`frontend/web`, pnpm workspace), entry `src/main.ts`; API client in `frontend/web/src/lib/api/`, server/cache state in `frontend/web/src/queries/` (Vue Query), UI/selection state in `frontend/web/src/stores/` (Pinia) — with one documented exception: the transient scan SSE lifecycle stays in `frontend/web/src/stores/scan.ts` (streaming process state, not a cacheable resource).
- `scripts/dev-web.mjs` boots backend + Vite together (`ONSEI_DATA_DIR` → `<repo>/.dev_data`).
- Flutter app (`frontend/flutter_app`) is legacy, planned for removal — don't invest.

## Versions: mise is the single source
- node/go/pnpm/task are pinned in `mise.toml`; CI provisions from the same file via `jdx/mise-action`. Bump versions in `mise.toml` only — `package.json` intentionally has no `packageManager` (pnpm's self-download once corrupted the store).
- Keep `go.mod`'s `go` directive aligned with the mise pin. After editing `mise.toml` run `mise install`; a fresh checkout needs `mise trust` once.

## Commands (verified)
- Quality, same shape as CI: `task test:go` · `task typecheck:web` · `task test:web` · `task build:web` · `task ci:quality`
- Backend (from root): `task -d backend/go test` · `test:e2e` · `proto` (regen Go stubs); focused test: `go test ./<pkg> -run <Test>`
- Web (from `frontend/web`): `pnpm typecheck` · `pnpm test` · `pnpm build`; dev: `task dev:web`; e2e: `task e2e:web` (install Chromium once: `pnpm exec playwright install chromium`)

## Proto caveat
- Two checked-in copies of `service.proto` (backend + Flutter legacy). Keep them in sync; only Go regen is scripted (`task -d backend/go proto`).

## CI facts
- `.github/workflows/ci.yml`: main jobs on ubuntu-latest; `windows-smoke` gates the Windows release path (`task build:go:windows-x64` + optional e2e). Flutter Windows builds would need a Windows runner.
- PRs reviewed by CodeRabbit (`.github/coderabbit.yaml`).