#!/bin/bash
set -euo pipefail

if [ -s /run/secrets/npm_token ]; then
  umask 077
  printf '//%s/:_authToken=%s\n' \
    "${NPM_REGISTRY_HOST:-npm.pkg.github.com}" "$(cat /run/secrets/npm_token)" > "$HOME/.npmrc"
fi
bun install --frozen-lockfile
exec "$@"
