#!/bin/bash
# Exercises this example's own README quick start for real: isolated
# Postgres, a host-run Vite dev server talking to it, routed through the
# shared proxy with `stackfork route add` (task 11.2's check - "documented
# steps work from a clean clone"). Run from anywhere; cleans up after
# itself, including the proxy only if this script is the one that started it.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
scratch="$(mktemp -d)"
state="$scratch/state"
legacy_state="$scratch/legacy-state"

sf() { stackfork --state-dir "$state" --legacy-state-dir "$legacy_state" "$@"; }

cd "$script_dir"
repo_root="$(git rev-parse --show-toplevel)"
composefile="examples/vite-api/docker-compose.yml"
hostname="vite-api-ci.localhost"

vite_pid=""
proxy_started_here=0
cleanup() {
  [ -n "$vite_pid" ] && kill "$vite_pid" 2>/dev/null || true
  (cd "$repo_root" && sf --root "$repo_root" route remove app) 2>/dev/null || true
  (cd "$repo_root" && sf --root "$repo_root" down --volumes) 2>/dev/null || true
  if [ "$proxy_started_here" = "1" ]; then
    sf proxy down 2>/dev/null || true
  fi
  rm -rf "$scratch"
}
trap cleanup EXIT

cd "$repo_root"
sf --root "$repo_root" prepare -f "$composefile" --hostname "$hostname"
sf --root "$repo_root" up --wait-timeout 90

db_port="$(sf --root "$repo_root" urls | grep '^db:' | sed -E 's#.*127\.0\.0\.1:([0-9]+).*#\1#')"
if [ -z "$db_port" ]; then
  echo "could not find the db's published port in urls output" >&2
  exit 1
fi

# Checked directly against Docker, not `stackfork proxy status`: that
# subcommand also reads a recorded port from --state-dir, which this
# scratch state dir was never used to start the proxy under, and errors -
# state-dir-independent is also the correct question here, since the
# proxy is one real container shared machine-wide regardless of which
# instance's --state-dir happens to be in play. This matters: `proxy up`
# recreates the shared container (breaking every other route on this
# machine, including any other agent session's) if it thinks the running
# one is on a different port, so this must never guess wrong.
already_running="$(docker inspect -f '{{.State.Running}}' stackfork-proxy 2>/dev/null || echo false)"
if [ "$already_running" = "true" ]; then
  proxy_started_here=0
else
  sf proxy up --port 80
  proxy_started_here=1
fi

cd "$script_dir"
npm install --no-audit --no-fund --silent

app_port=5179
DATABASE_URL="postgres://postgres:example@127.0.0.1:${db_port}/app" \
  npx vite --port "$app_port" --strictPort >"$scratch/vite.log" 2>&1 &
vite_pid=$!

ready=0
for _ in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:${app_port}/" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.5
done
if [ "$ready" != "1" ]; then
  echo "vite dev server never became ready:" >&2
  cat "$scratch/vite.log" >&2
  exit 1
fi

# The hard assertion: the host-run app is really talking to the isolated
# Postgres (this is always safe to check directly, regardless of what else
# is going on with the shared proxy on this machine).
d1="$(curl -sf "http://127.0.0.1:${app_port}/api/visits" | python3 -c 'import json,sys;print(json.load(sys.stdin)["visits"])')"
d2="$(curl -sf "http://127.0.0.1:${app_port}/api/visits" | python3 -c 'import json,sys;print(json.load(sys.stdin)["visits"])')"
if [ "$d2" != "$((d1 + 1))" ]; then
  echo "expected the count to increment on the app's own port, got $d1 then $d2" >&2
  exit 1
fi
echo "PASS: host-run vite-api reachable, real Postgres data ($d1 -> $d2)"

cd "$repo_root"
sf --root "$repo_root" route add app "$app_port"

# `stackfork urls` only ever reflects container-derived endpoints (docker
# compose ps), never a host-run route (that's dashboard-only rendering,
# internal/dashboard's App strip) - a route always lands on the instance's
# own hostname, set explicitly above via --hostname.
#
# Only asserted as a hard failure when this script started its own proxy:
# GatherRoutes/WriteCaddyfile/Reload are all scoped to --state-dir, and the
# *running container*'s mount was fixed at whichever state dir originally
# started it - reusing a pre-existing proxy (this machine, right now) means
# the real container is mounted to the real default state dir's Caddyfile,
# never this scratch one, so it can never actually see this route. That's
# correct, state-dir-respecting behaviour, not a bug to route around.
app_url="http://$hostname"
if [ "$proxy_started_here" = "1" ]; then
  p1="$(curl -sf "$app_url/api/visits" | python3 -c 'import json,sys;print(json.load(sys.stdin)["visits"])')"
  p2="$(curl -sf "$app_url/api/visits" | python3 -c 'import json,sys;print(json.load(sys.stdin)["visits"])')"
  if [ "$p2" != "$((p1 + 1))" ]; then
    echo "expected the count to increment through the proxy too, got $p1 then $p2" >&2
    exit 1
  fi
  echo "PASS: also reachable through the proxy at $app_url ($p1 -> $p2)"
else
  echo "NOTE: reusing a pre-existing proxy on this machine, so the proxy-routed URL ($app_url) can't be exercised safely here - verified via the app's own port instead. A fresh environment (no proxy running yet) exercises this path too."
fi
