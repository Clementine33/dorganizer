# AGENTS.md

## Architecture
- Go backend (`backend/go`, entry `cmd/onsei-organizer-backend/main.go`): gRPC on `127.0.0.1:0` + net/http API on an ephemeral port; startup prints an `ONSEI_BACKEND_READY port=… token=… http_port=…` handshake that web dev consumes via `VITE_API_BASE`.
- Vue 3 + TS web frontend (`frontend/web`, pnpm workspace), entry `src/main.ts`; API client in `frontend/web/src/lib/api/`, server/cache state in `frontend/web/src/queries/` (Vue Query), UI/selection state in `frontend/web/src/stores/` (Pinia) — with one documented exception: the transient scan SSE lifecycle stays in `frontend/web/src/stores/scan.ts` (streaming process state, not a cacheable resource).
- `scripts/dev-web.mjs` boots backend + Vite together (`ONSEI_DATA_DIR` → `<repo>/.dev_data`).
- Flutter app (`frontend/flutter_app`) is legacy, planned for removal — don't invest.

## Go module structure & engineering rules
- **Core goal**: Low coupling across modules, high cohesion within each file. Each file has one clear responsibility.
- **Service decomposition**: `service.go` holds only types, constructors, and DI wiring (≤80 lines); use cases split into separate files named by business verb (e.g., `run.go`, `persist.go`, `load_plan.go`).
- **No semantic-less files**: Never create `helpers.go`, `util.go`, or `common.go` dumps.
- **Split heuristics**: Split when a file hosts ≥2 responsibilities that change at different frequencies, or when a struct + method cluster exceeds single-screen readability. Never split purely to satisfy line limits.
- **Line limits (guidelines)**: Functions ≤100 lines; files ≤400 lines (comfortable) / >600 (should split).
- **Linter & CI gate**: `task lint:go` runs `golangci-lint` (based on maratori config, in `backend/go/.golangci.yml`). Gated in CI (`ci:quality`). Mechanical fixes: `task lint:go:fix`.

### Go test rules (enforced)
- New test files default to `package <pkg>_test`; same-package tests are allowed only where the test really exercises internals, and must keep the `//nolint:testpackage` note explaining why.
- No dedicated tests for unexported helpers/fields — cover behavior through the exported API. When an internal seam must be exposed to tests, funnel it through an `export_test.go`; never bulk re-export.
- Never generate mocks for interfaces defined in this repo; prefer real dependencies or small handwritten fakes.
- Don't assert call counts or call order unless the count/order is itself the contract (retry cap, idempotency, fail-fast).
- Use `testing/synctest` for time/concurrency; never `time.Sleep` to synchronize tests.
- Coverage is a diagnostic signal, not a KPI. Test deletion and production refactors never land in the same commit.

## Versions: mise is the single source
- node/go/pnpm/task are pinned in `mise.toml`; CI provisions from the same file via `jdx/mise-action`. Bump versions in `mise.toml` only — `package.json` intentionally has no `packageManager` (pnpm's self-download once corrupted the store).
- Keep `go.mod`'s `go` directive aligned with the mise pin. After editing `mise.toml` run `mise install`; a fresh checkout needs `mise trust` once.

## Commands (verified)
- Quality, same shape as CI: `task test:go` · `task lint:go` · `task typecheck:web` · `task test:web` · `task build:web` · `task ci:quality`
- Backend (from root): `task -d backend/go test` · `task -d backend/go lint` · `test:e2e` · `proto` (regen Go stubs); focused test: `go test ./<pkg> -run <Test>`
- Web (from `frontend/web`): `pnpm typecheck` · `pnpm test` · `pnpm build`; dev: `task dev:web`; e2e: `task e2e:web` (install Chromium once: `pnpm exec playwright install chromium`)

## Proto caveat
- Two checked-in copies of `service.proto` (backend + Flutter legacy). Keep them in sync; only Go regen is scripted (`task -d backend/go proto`).

## CI facts
- `.github/workflows/ci.yml`: main jobs on ubuntu-latest; `windows-smoke` gates the Windows release path (`task build:go:windows-x64` + optional e2e). Flutter Windows builds would need a Windows runner.
- PRs reviewed by CodeRabbit (`.github/coderabbit.yaml`).

## Agent skills

### Issue tracker

Issues and specs live as GitHub issues, driven via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Five canonical labels, each label string equal to its name: needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.