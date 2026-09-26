#!/bin/bash
set -euo pipefail

# Optional private module host, mirroring the Nuxt template's registry
# secret: only written when both are set, never printed or baked into the
# image (it comes from a Docker secret, resolved at up/run time).
if [ -n "${GOPRIVATE:-}" ] && [ -s /run/secrets/go_token ]; then
  umask 077
  printf 'machine %s login x-access-token password %s\n' \
    "${GO_REGISTRY_HOST:?GO_REGISTRY_HOST is required with GOPRIVATE}" "$(cat /run/secrets/go_token)" > "$HOME/.netrc"
fi

go mod download
exec "$@"
