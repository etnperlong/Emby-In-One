# AGENTS.md

## Source Of Truth
- Current shipped app is the Go Emby aggregation proxy: entrypoint `cmd/emby-in-one/main.go`, app wiring in `internal/backend/server.go`, route table in `internal/backend/routes.go`.
- `src/` and `tests/` are the older Node/Express implementation and tests; `package.json` is not the production build path. Touch them only when the change explicitly targets legacy Node code.
- Do not use `.planning/` as current architecture guidance; it can describe stale Node-primary assumptions.

## Commands
- Full Go suite: `go test ./...`.
- Focused Go backend test: `go test ./internal/backend -run TestName -count=1`.
- Focused Go CLI/distribution test: `go test ./cmd/emby-in-one -run TestName -count=1`.
- Build the production binary: `CGO_ENABLED=0 go build -ldflags="-s -w -X main.Version=dev" -o /tmp/emby-in-one ./cmd/emby-in-one`.
- Local run needs `config/config.yaml` plus `data/`: `mkdir -p config data`, create config from the README sample, then `go run ./cmd/emby-in-one`.
- Legacy Node tests use the built-in runner: `npm test -- tests/utils/stream-proxy.test.js` or `npm test`.

## Current Test Baseline
- As of 2026-05-29, `go test ./...` fails at `TestConfigSaveUsesAtomicReplace` in `internal/backend/auth_hardening_test.go`; re-check before attributing that failure to your changes.
- As of 2026-05-29, `npm test -- tests/utils/cors-policy.test.js` fails its admin CORS header assertion; re-check before treating Node tests as a clean baseline.

## Architecture Notes
- SQLite uses the pure Go `zombiezen.com/go/sqlite` dependency through `internal/backend/sqlite_cgo.go`, so production builds support `CGO_ENABLED=0` and do not need a native compiler.
- Config loading is a custom YAML subset in `internal/backend/config.go`, not a general YAML library. Save paths are atomic and render strings as single-quoted scalars.
- Config lookup prefers `/app/config/config.yaml`, then `config/config.yaml`, `config.yaml`, and parent variants. Default data dir is `/app/data` if present, else `data`.
- Runtime data files under `data/` include `mappings.db`, `tokens.json`, `captured-headers.json`, and `emby-in-one.log`.
- `public/embed.go` embeds only `public/admin.html` and `public/admin.js`; keep those filenames and include `public/` in any build context.
- `routes.go` uses Go `http.ServeMux` method/path patterns and wraps handlers with body limit, logging, `/emby` prefix compatibility, and CORS middleware.

## Behavior Gotchas
- Admin API CORS is same-origin only; Emby client routes intentionally return permissive CORS.
- Passthrough upstreams intentionally skip startup/reconnect login until a real admin client identity has been captured. Do not reintroduce Infuse fallback login for passthrough without captured headers.
- Behind a reverse proxy, `server.trustProxy: true` is required or login rate limiting treats clients as the proxy IP.
- The admin UI is plain `public/admin.html` plus `public/admin.js` using CDN assets; there is no frontend build step.

## Packaging Gotchas
- Docker/source deployment is Go-only. `Dockerfile`, `install.sh`, and `emby-in-one-cli.sh` contain duplicated Dockerfile/Compose snippets; keep them in sync when changing build layout or version args.
- The builder must copy `go.mod`, `go.sum`, `cmd/`, `internal/`, and `public/`; omitting `public/` breaks the Go embed import.
- Version strings are duplicated in README files, `docker-compose.yml`, `install.sh`, `emby-in-one-cli.sh`, and `Update.md`.
- Shell script tests enforce LF line endings, no UTF-8 BOM, and the exact valid `SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"` assignment in install scripts.
