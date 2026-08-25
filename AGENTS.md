# AGENTS.md

## Fast orientation
- `README.md` is currently empty; treat executable config as source of truth.
- Root `Taskfile.yml` is the primary command entrypoint for CI/release-quality tasks.
- Stack: Go backend (gRPC + net/http HTTP API) with a Vue 3 + TypeScript web frontend (`frontend/web`, pnpm workspace).
- The legacy Flutter app (`frontend/flutter_app`) still exists in the tree but is planned for removal — don't invest in it.

## Repo boundaries and real entrypoints
- Frontend (web) entry: `frontend/web/src/main.ts`
  - `scripts/dev-web.mjs` boots the Go backend and Vite together; the backend prints an `ONSEI_BACKEND_READY port=<grpc> token=... version=... http_port=<http>` handshake that the web app consumes via `VITE_API_BASE`.
- Backend entry: `backend/go/cmd/onsei-organizer-backend/main.go`
  - Starts a local gRPC server on `127.0.0.1:0` plus a net/http HTTP API on an ephemeral port; the handshake line carries `http_port` for clients.
- Web API client + SSE live in `frontend/web/src/lib/api/`, state in `frontend/web/src/stores/`.
- gRPC contracts/codegen locations:
  - Proto (backend): `backend/go/api/proto/onsei/v1/service.proto`
  - Proto (frontend copy, legacy until Flutter is removed): `frontend/flutter_app/protos/onsei/v1/service.proto`
  - Generated Go stubs: `backend/go/internal/gen/onsei/v1/*.pb.go`

## Commands agents should use (verified)
- Root quality checks (same shape as CI):
  - `task test:go`
  - `task typecheck:web`
  - `task test:web`
  - `task build:web`
  - `task ci:quality` (runs Go + Flutter + Web checks in that order; Flutter steps only matter until removal)
- Backend-only (from repo root):
  - `task -d backend/go test`
  - `task -d backend/go test:e2e`
  - `task -d backend/go proto` (regenerates Go protobuf stubs)
  - Focused Go test: `go test ./<pkg> -run <TestName>` (run in `backend/go`)
- Web-only (from `frontend/web`):
  - `pnpm typecheck` / `pnpm test` / `pnpm build`
  - `task dev:web` (repo root) to run backend + Vite dev together
  - e2e: `task e2e:web` (Playwright; boots backend + Vite — install Chromium once via `pnpm exec playwright install chromium`)

## Build/CI facts that affect changes
- CI runs on `windows-latest` (`.github/workflows/ci.yml`): `go-test` + `web-quality` jobs.
  - pnpm cache requires `pnpm/action-setup@v4` to run before `setup-node`.
- PRs are reviewed by CodeRabbit (`.github/coderabbit.yaml`).

## Config/runtime gotchas
- Backend config is read from `<dataDir>/config.json` where `dataDir` is:
  - release/runtime: near executable layout
  - overrideable with `ONSEI_DATA_DIR`
- `scripts/dev-web.mjs` sets `ONSEI_DATA_DIR` to `<repo>/.dev_data` so dev data never touches real data.
- Missing `config.json` is tolerated for some paths (tools/execute defaults), but prune regex reads can fail if `prune.regex_pattern` is absent/empty.

## Codegen/proto workflow caveat
- Two checked-in copies of `service.proto` (backend + Flutter copy). Keep them in sync while the Flutter app exists; the Dart copy goes away with Flutter.
- Only Go protobuf regeneration is scripted in repo (`task -d backend/go proto`); Dart regen is not wired in Taskfiles/CI.