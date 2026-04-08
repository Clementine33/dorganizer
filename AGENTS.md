# AGENTS.md

## Fast orientation
- `README.md` is currently empty; treat executable config as source of truth.
- Root `Taskfile.yml` is the primary command entrypoint for CI/release-quality tasks.
- This repo is a Windows-first desktop stack: Flutter UI + Go gRPC backend.

## Repo boundaries and real entrypoints
- Frontend app entry: `frontend/flutter_app/lib/main.dart`
  - Starts backend subprocess via `BackendProcess` (`lib/bootstrap/backend_process.dart`), waits for `ONSEI_BACKEND_READY ...`, then creates gRPC channel.
- Backend entry: `backend/go/cmd/onsei-organizer-backend/main.go`
  - Starts local gRPC server on `127.0.0.1:0`, prints handshake line consumed by Flutter, and serves until shutdown.
- gRPC contracts/codegen locations:
  - Proto (backend): `backend/go/api/proto/onsei/v1/service.proto`
  - Proto (frontend copy): `frontend/flutter_app/protos/onsei/v1/service.proto`
  - Generated Go stubs: `backend/go/internal/gen/onsei/v1/*.pb.go`
  - Generated Dart stubs: `frontend/flutter_app/lib/gen/onsei/v1/*.dart`

## Commands agents should use (verified)
- Root quality checks (same shape as CI):
  - `task test:go`
  - `task test:flutter`
  - `task analyze:flutter`
  - `task ci:quality` (runs the three above in that order)
- Backend-only (from repo root):
  - `task -d backend/go test`
  - `task -d backend/go test:e2e`
  - `task -d backend/go proto` (regenerates Go protobuf stubs)
- Frontend-only:
  - `task test:flutter`
  - `task analyze:flutter`
  - Focused Flutter test: `flutter test test/<path_to_test>.dart` (run in `frontend/flutter_app`)
- Focused Go test: `go test ./<pkg> -run <TestName>` (run in `backend/go`)

## Build/release facts that affect changes
- CI runs on `windows-latest` (`.github/workflows/ci.yml`).
- Release workflow triggers on tags matching `v*`, but enforces strict `vX.Y.Z` format before build (`.github/workflows/release.yml`).
- Release packaging is Task-based (`task release:windows-x64`) and includes:
  - Flutter Windows release artifacts
  - `backend/go/bin/onsei-organizer-backend.exe`
  - `config.json` copied from `config.json.template`

## Config/runtime gotchas
- Backend config is read from `<dataDir>/config.json` where `dataDir` is:
  - release/runtime: near executable layout
  - overrideable with `ONSEI_DATA_DIR`
- Flutter debug mode sets `ONSEI_DATA_DIR` to `frontend/flutter_app/.dev_data`.
- Missing `config.json` is tolerated for some paths (tools/execute defaults), but prune regex reads can fail if `prune.regex_pattern` is absent/empty.

## Codegen/proto workflow caveat
- There are two checked-in copies of `service.proto` (backend + frontend). Keep them in sync manually.
- Only Go protobuf regeneration is scripted in repo (`task -d backend/go proto`); Dart regen is not wired in Taskfiles/CI.
