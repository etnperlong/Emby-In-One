#!/usr/bin/env bash
set -euo pipefail
[[ $# -ge 2 && $# -le 3 ]] || { echo 'usage: docker-image-smoke.sh IMAGE VERSION [REVISION]' >&2; exit 2; }
image=$1 version=$2 revision=${3:-}
work=$(mktemp -d); name="eio-smoke-$$-$RANDOM"
as_root(){ if [[ $EUID == 0 ]]; then "$@"; else sudo "$@"; fi; }
cleanup(){ docker rm -f "$name" >/dev/null 2>&1 || true; as_root rm -rf "$work"; }
trap cleanup EXIT
platform=(); [[ -z "${EIO_PLATFORM:-}" ]] || platform=(--platform "$EIO_PLATFORM")
mkdir -p "$work/config" "$work/data"
password=$(head -c 24 /dev/urandom | base64)
printf 'server:\n  port: 8096\nadmin:\n  username: admin\n  password: "%s"\nupstream: []\nproxies: []\n' "$password" > "$work/config/config.yaml"
unset password
chmod 700 "$work/config" "$work/data"; chmod 600 "$work/config/config.yaml"
as_root chown -R 1000:1000 "$work/config" "$work/data"
[[ "$(docker run --rm "${platform[@]}" --entrypoint /app/emby-in-one "$image" --version)" == "$version" ]]
[[ "$(docker image inspect -f '{{.Config.User}}' "$image")" == 1000:1000 ]]
if [[ -n "$revision" ]]; then [[ "$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")" == "$revision" ]]; fi
docker run -d "${platform[@]}" --name "$name" --network none --user 1000:1000 -v "$work/config:/app/config" -v "$work/data:/app/data" "$image" >/dev/null
# distroless has no shell/wget/cat: probe with the binary, inspect state from the host.
healthy(){ docker exec "$name" /app/emby-in-one --healthcheck >/dev/null 2>&1; }
for ((i=0;i<60;i++)); do
  if healthy; then
    [[ "$(docker exec "$name" /app/emby-in-one --version)" == "$version" ]]
    # The app created its database as uid 1000, so the data mount is writable.
    as_root test -s "$work/data/mappings.db"
    docker restart "$name" >/dev/null
    for ((j=0;j<30;j++)); do healthy && break; sleep 2; done
    healthy
    as_root test -s "$work/data/mappings.db"
    printf 'Smoke passed image=%s version=%s platform=%s\n' "$image" "$version" "${EIO_PLATFORM:-native}"
    exit 0
  fi
  sleep 2
done
echo 'bounded image health smoke timed out' >&2
exit 1
