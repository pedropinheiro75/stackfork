#!/bin/bash
# Builds the isolated demo world behind docs/demo.gif, docs/dashboard.gif and
# docs/screenshots/*. Run from the repository root:
#
#   DEMO_TMP=/private/tmp/sf-demo docs/screenshots/demo-world.sh build   # create + learn + verify
#   . $DEMO_TMP/env.sh                                                    # wrappers: stackfork, sfs
#   docs/screenshots/demo-world.sh start                                  # both projects running
#   docs/screenshots/demo-world.sh dashboard                              # dashboard on :8765
#   docs/screenshots/demo-world.sh reset                                  # stop + wipe instances (keeps trust)
#   docs/screenshots/demo-world.sh destroy                                # everything, proxy included
#
# Needs Go, Docker, node/npm (network for `npm ci`). Nothing outside DEMO_TMP
# is touched: own state dir, own config dir, own proxy container (sf-demo-proxy
# on port 8099). It never uses the real stackfork state, port 26607 or proxy.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
T="${DEMO_TMP:?set DEMO_TMP to a short, empty directory such as /private/tmp/sf-demo}"
PORT=8765

env_file() {
  cat >"$T/env.sh" <<EOF
export T=$T
export STACKFORK_CONFIG_DIR=\$T/config STACKFORK_PROXY_NAME=sf-demo-proxy
export PATH=\$T/bin:\$PATH
export GIT_AUTHOR_NAME=demo GIT_AUTHOR_EMAIL=demo@example.com GIT_COMMITTER_NAME=demo GIT_COMMITTER_EMAIL=demo@example.com
sfs() { command stackfork --state-dir \$T/state "\$@"; }
EOF
}

load() { . "$T/env.sh"; }

build() {
  mkdir -p "$T"/{bin,config,state,code}
  (cd "$repo" && go build -o "$T/bin/stackfork" ./cmd/stackfork)
  env_file; load
  sfs config set roots "$T/code" >/dev/null
  cd "$T/code"

  # acme-web: app in a container + Postgres (examples/node-postgres)
  mkdir acme-web
  cp "$repo"/examples/node-postgres/{Dockerfile,docker-compose.yml,package.json,server.js} acme-web/
  sed -i.bak 's#build: examples/node-postgres#build: .#' acme-web/docker-compose.yml && rm acme-web/*.bak
  (cd acme-web && git init -q -b main && git add -A && git commit -qm init)

  # acme-api: hybrid, Postgres in Compose + Vite on the host (examples/vite-api)
  mkdir acme-api
  cp "$repo"/examples/vite-api/{docker-compose.yml,index.html,package.json,package-lock.json,vite.config.js} acme-api/
  sed -i.bak 's#vite-api-example#acme-api#' acme-api/package.json && rm acme-api/*.bak
  printf 'node_modules\n' >acme-api/.gitignore
  (cd acme-api && npm ci --no-audit --no-fund --silent && git init -q -b main && git add -A && git commit -qm init)

  # acme-docs: a plain repo that is never learned (the "Set up" dialog frame)
  mkdir acme-docs
  printf '{\n  "name": "acme-docs",\n  "private": true,\n  "scripts": { "dev": "vite" }\n}\n' >acme-docs/package.json
  printf '<!doctype html>\n<title>Acme docs</title>\n<h1>Acme docs</h1>\n' >acme-docs/index.html
  printf '# Acme docs\n\nUser guides for Acme.\n' >acme-docs/README.md
  (cd acme-docs && git init -q -b main && git add -A && git commit -qm init)

  sfs proxy up --port 8099 >/dev/null

  # --- acme-web: learn, record the plan, verify as the person ---
  cd "$T/code/acme-web"
  sfs learn >/dev/null
  sfs learn record step --id prepare --kind prepare --args -f,docker-compose.yml >/dev/null
  sfs learn record step --id up --kind compose-up >/dev/null
  sfs learn record step --id seed --kind exec --service db --once data \
    --cmd "psql -U postgres app -c \"INSERT INTO visits (visited_at) SELECT now() - g * interval '7 minutes' FROM generate_series(1,120) g\"" >/dev/null
  sfs learn record step --remove prepare >/dev/null   # re-add before "up"
  sfs learn record step --id prepare --kind prepare --args -f,docker-compose.yml --before up >/dev/null
  sfs learn record entry --service app --port 3000 >/dev/null
  sfs prepare -f docker-compose.yml >/dev/null 2>&1
  sfs up >/dev/null 2>&1
  sfs learn record reload app rebuild >/dev/null
  sfs learn verify --check "database identity" --check "app readiness" \
    --confirm "yes, the plan starts the app and its database" >/dev/null
  git add -A && git commit -qm "Record the stackfork startup plan"

  # --- acme-api: hybrid plan with a host step, trusted by the person ---
  cd "$T/code/acme-api"
  sfs learn >/dev/null
  sfs learn record step --id up --kind compose-up >/dev/null
  sfs learn record step --id prepare --kind prepare --args -f,docker-compose.yml --before up >/dev/null
  sfs learn record step --id seed --kind exec --service db --once data \
    --cmd "psql -U postgres app -c \"CREATE TABLE IF NOT EXISTS visits (id SERIAL PRIMARY KEY, visited_at TIMESTAMPTZ NOT NULL DEFAULT now()); INSERT INTO visits (visited_at) SELECT now() - g * interval '5 minutes' FROM generate_series(1,48) g\"" >/dev/null
  sfs learn record step --id app --kind host --name app --modes hybrid --depends-on db \
    --cmd "npx vite --port {{port}} --strictPort" >/dev/null
  (cd "$T/code" && sfs config set-mode acme-api hybrid >/dev/null)
  sfs plan --mode hybrid --trust >/dev/null          # the person trusts the host step
  sfs prepare -f docker-compose.yml >/dev/null 2>&1
  sfs up >/dev/null 2>&1
  sfs learn record entry --service app >/dev/null
  sfs learn record reload app hot >/dev/null
  sfs learn verify --check "database identity" --check "app readiness" \
    --confirm "yes, the plan runs the app on the host against the isolated database" >/dev/null
  git add -A && git commit -qm "Record the stackfork startup plan"

  reset
  echo "demo world ready in $T (instances stopped, proxy up on :8099)"
}

reset() {
  load
  for r in "$T"/code/acme-api "$T"/code/acme-web "$T"/code/acme-web/.worktrees/*; do
    [ -d "$r" ] && (cd "$r" && sfs host rm app >/dev/null 2>&1; sfs down --volumes >/dev/null 2>&1) || true
  done
  # the seed marker lives in the instance state: drop it so the next start seeds again
  for d in "$T"/state/sf-*; do [ -d "$d" ] && rm -rf "$d"; done
  for w in "$T"/code/acme-web/.worktrees/*; do
    [ -d "$w" ] && git -C "$T/code/acme-web" worktree remove --force "$w"
  done
  git -C "$T/code/acme-web" branch -D feature-invoices >/dev/null 2>&1 || true
  return 0
}

start() {
  load
  (cd "$T/code/acme-web" && sfs start-env >/dev/null 2>&1)
  (cd "$T/code/acme-web" && sfs worktree add . feature-invoices --from main >/dev/null \
    && cd .worktrees/feature-invoices && sfs start-env >/dev/null 2>&1)
  # per-instance env overrides for the feature worktree (the "N env overrides" chips)
  (cd "$T/code/acme-web/.worktrees/feature-invoices" && for kv in FEATURE_INVOICES=on LOG_LEVEL=debug INVOICE_CURRENCY=EUR; do sfs env set app "$kv" >/dev/null 2>&1; done)
  (cd "$T/code/acme-api" && sfs start-env --mode hybrid >/dev/null 2>&1)
  sfs list
}

dashboard() {
  load
  (cd "$T" && nohup "$T/bin/stackfork" --state-dir "$T/state" dashboard --port $PORT >"$T/dashboard.log" 2>&1 &
   echo $! >"$T/dashboard.pid")
  sleep 2
  echo "dashboard: http://127.0.0.1:$PORT  (pid $(cat "$T/dashboard.pid"); stop it with: kill \$(cat $T/dashboard.pid))"
}

destroy() {
  load
  [ -f "$T/dashboard.pid" ] && kill "$(cat "$T/dashboard.pid")" 2>/dev/null || true
  reset
  sfs proxy down >/dev/null 2>&1 || true
  docker images --format '{{.Repository}}:{{.Tag}}' | grep -E '^sf-(acme|feature)' | xargs -r docker rmi >/dev/null 2>&1 || true
  rm -rf "$T"
}

"${1:?usage: build|start|dashboard|reset|destroy}"
