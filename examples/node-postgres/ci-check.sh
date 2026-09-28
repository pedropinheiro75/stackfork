#!/bin/bash
# Exercises this example's own README quick start for real: two worktrees
# of the same checkout running side by side via the stackfork CLI, with
# independently counting Postgres data (task 11.1's check). Run from
# anywhere; it builds its own scratch state and cleans up after itself.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
scratch="$(mktemp -d)"
state="$scratch/state"
legacy_state="$scratch/legacy-state"
trap 'rm -rf "$scratch"' EXIT

sf() { stackfork --state-dir "$state" --legacy-state-dir "$legacy_state" "$@"; }

cd "$script_dir"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
  echo "not inside a Git checkout; the example needs one for worktree add" >&2
  exit 1
}
repo1="$(git rev-parse --show-toplevel)"
branch="$(git rev-parse --abbrev-ref HEAD)"
repo2="$scratch/worktree-2"
git -C "$repo1" worktree add -q --detach "$repo2" "$branch"

composefile="examples/node-postgres/docker-compose.yml"

port_of() { docker port "$1" 3000/tcp | head -1 | sed 's/.*://'; }
visits() { curl -sf "http://127.0.0.1:$1/"; }

cd "$repo1" && sf --root "$repo1" prepare -f "$composefile" --hostname node-postgres-ci-1.localhost && sf --root "$repo1" up --build --wait-timeout 120
cd "$repo2" && sf --root "$repo2" prepare -f "$composefile" --hostname node-postgres-ci-2.localhost && sf --root "$repo2" up --build --wait-timeout 120

p1="$(sf --root "$repo1" where | python3 -c 'import json,sys;print(json.load(sys.stdin)["project"])')"
p2="$(sf --root "$repo2" where | python3 -c 'import json,sys;print(json.load(sys.stdin)["project"])')"

port1="$(port_of "${p1}-app-1")"
port2="$(port_of "${p2}-app-1")"

v1a="$(visits "$port1" | python3 -c 'import json,sys;print(json.load(sys.stdin)["visits"])')"
v2a="$(visits "$port2" | python3 -c 'import json,sys;print(json.load(sys.stdin)["visits"])')"
v1b="$(visits "$port1" | python3 -c 'import json,sys;print(json.load(sys.stdin)["visits"])')"

if [ "$v1b" != "$((v1a + 1))" ]; then
  echo "expected instance 1's own count to increment independently, got $v1a then $v1b" >&2
  exit 1
fi
if [ "$v2a" -gt 1 ]; then
  echo "expected instance 2 to start from its own empty database, got $v2a" >&2
  exit 1
fi

echo "PASS: two worktrees of node-postgres ran side by side with independent ports and data"

cd "$repo1" && sf --root "$repo1" down --volumes
cd "$repo2" && sf --root "$repo2" down --volumes
git -C "$repo1" worktree remove --force "$repo2"
