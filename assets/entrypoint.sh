#!/bin/bash
set -euo pipefail

if [ -s /run/secrets/npm_token ]; then
  umask 077
  printf '@rrglobal:registry=https://npm.pkg.github.com\n//npm.pkg.github.com/:_authToken=%s\n' \
    "$(cat /run/secrets/npm_token)" > "$HOME/.npmrc"
fi
bun install --frozen-lockfile
exec "$@"
