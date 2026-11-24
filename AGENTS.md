# AGENTS.md

## Source Of Truth
- Fork of `ArizeSky/Emby-In-One` (remote `upstream`), published as `etnperlong/Emby-In-One` (remote `origin`).
- Shipped app is the Go Emby aggregation proxy: entrypoint `cmd/emby-in-one/main.go`, app wiring `internal/backend/server.go`, route table `internal/backend/routes.go`.
- The `/web` browser client is served from `public/web/`. `web-src/` holds the maintained player modules that are vendored into it; `tests/web-*.test.cjs` load them and compare them with the vendored chunks.
- `legacy/` is the old Node/Express implementation with its own `package.json`; touch it only when a change explicitly targets legacy Node.

## Fork Deltas
Carried on top of upstream; keep them through every sync:
- SQLite is pure Go (`zombiezen.com/go/sqlite` over modernc) in `internal/backend/sqlite_cgo.go`. Builds use `CGO_ENABLED=0`; there is no `third_party/sqlite`. modernc has no linux/mips or mipsle port.
- Docker runtime is `gcr.io/distroless/static-debian13`, which has no shell, `wget`, `id` or `cat`. The image runs as `USER 1000:1000`, its healthcheck is `/app/emby-in-one --healthcheck`, and `/app/config` + `/app/data` are created in the builder and copied with `--chown=1000:1000`. `scripts/docker-image-smoke.sh` probes with the binary and inspects state from the host.
- Builder image is `golang:1.26-trixie`; OCI labels live in `Dockerfile`.
- Images publish from `.github/workflows/publish-image.yml` on any pushed tag (linux/amd64, linux/arm64). Upstream's `release.yml` (amd64/arm64 binaries here) only runs when `github.repository == 'ArizeSky/Emby-In-One'`; `docker.yml` is upstream's manual validate/publish pipeline.

## Syncing Upstream
- Upstream restarted history at V1.4.4 (root `c73d812`); the old line is `upstream/legacy-v1.4.3-main`. This fork joins both with a `-s ours` merge (`merge: join legacy V1.4.3 history`), so sync with `git merge upstream/main`. A plain `git rebase upstream/main` replays the whole legacy line.
- Upstream tags are fetched into `refs/upstream-tags/*` (`remote.upstream.tagOpt --no-tags`), so `git push --tags` carries only the fork's own tags. Reference them as `refs/upstream-tags/V1.4.4-rc5`.
- `sqlite_cgo.go` conflicts: take upstream's locking contract and keep zombiezen calls under `callMu` (see SQLite below).

## Commands
- Full Go suite as CI runs it: `go test -parallel 1 -timeout 15m ./...`.
- Focused tests: `go test ./internal/backend -run TestName -count=1`; CLI/distribution: `go test ./cmd/emby-in-one -run TestName -count=1`.
- Race checks: `go test -race` on the touched package; CI shards the backend with `scripts/go-race-shard.py`.
- gofmt is a CI gate: `gofmt -l cmd internal` must print nothing.
- Frontend and script tests: `npm test` (`node --test tests/*.test.cjs`, no install needed).
- Panel stylesheet: `npm run build:panel` regenerates `public/vendor/tailwind.css` from `assets/panel.css`; CI fails when the committed copy is stale.
- Production binary: `CGO_ENABLED=0 go build -ldflags="-s -w -X main.Version=dev" -o /tmp/emby-in-one ./cmd/emby-in-one`.
- Local run needs `config/config.yaml` plus `data/`: `mkdir -p config data`, create config from the README sample, then `go run ./cmd/emby-in-one`.

## Current Test Baseline
- As of 2026-11-24 on `join/v1.4.4-rc5`: `go vet ./...`, `go test -parallel 1 ./...` and `npm test` (1211 tests) all pass.

## Architecture Notes
- SQLite: every store shares one connection. Reads go through `withReadLock`, single writes through `writeParams`/`withWriteLock`, multi-statement writes through `withWriteTx`. `mu` (RWMutex) gives that isolation; `callMu` serializes each zombiezen call because readers share `mu`. Lock order is store mutex → `mu` → `callMu`.
- Config loading is a custom YAML subset in `internal/backend/config.go`, not a general YAML library. Saves are atomic and render strings as single-quoted scalars.
- Config lookup prefers `/app/config/config.yaml`, then `config/config.yaml`, `config.yaml`, and parent variants. Default data dir is `/app/data` if present, else `data`.
- Runtime data under `data/`: `mappings.db`, `tokens.json`, `captured-headers.json`, `emby-in-one.log`.
- `public/embed.go` embeds `admin.html`, `admin.js`, `admin-activity.js`, `admin-theme.js`, `vendor/` and `web/`. New panel or web assets must be listed there, and every build context must include `public/`.
- `routes.go` uses Go `http.ServeMux` method/path patterns and wraps handlers with body limit, logging, `/emby` prefix compatibility, and CORS middleware.

## Behavior Gotchas
- Admin API CORS is same-origin only; Emby client routes intentionally return permissive CORS.
- Built-in client identity schemes (none/Infuse) are gone. Passthrough upstreams defer login until a real client identity has been captured.
- Behind a reverse proxy, `server.trustProxy: true` is required or login rate limiting treats clients as the proxy IP.
- The admin panel is plain `public/admin.html` + JS with every asset self-hosted under `public/vendor/` (same-origin CSP); the Tailwind regeneration above is its only build step.

## Packaging Gotchas
- The runtime Dockerfile exists three times: `Dockerfile`, `install.sh` (`write_runtime_dockerfile_to`) and `emby-in-one-cli.sh` (`download_and_install_docker`). Change them together; `cmd/emby-in-one/distribution_test.go` and `tests/docker-deployment.test.cjs` enforce the shared shape.
- The builder copies `go.mod`, `go.sum`, `cmd/`, `internal/` and `public/`.
- Version strings live in `package.json`, `emby-in-one-cli.sh` (`VERSION=`), `public/web/web.html`, `Update.md` and the README headers.
- Installers (`release-install.sh`, `emby-in-one-cli.sh`) download from `GITHUB_REPO="ArizeSky/Emby-In-One"`, i.e. upstream releases.
- Shell script tests enforce LF line endings, no UTF-8 BOM, and the exact `SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"` assignment in install scripts.
