# Isolated development

Shared agent skill and Docker Compose helper for isolated development across projects, branches and Git worktrees. Works with any project’s Compose services, including dependency-only stacks and host-run apps. Includes an optional Node/Bun Nuxt template. Project- or company-specific recipes live in private profiles (`~/.config/stackfork/profiles/`), outside this repository.

The maintained source lives in this repository. Local Codex and Claude Code skill directories link to it.

- [Agent instructions](SKILL.md)
- [CLI source](cmd/stackfork)

Ask an agent: `Use $isolated-dev to prepare this project in a new worktree.`

## Install

```sh
go install github.com/pedropinheiro75/stackfork/cmd/stackfork@latest
# for now, from a local checkout:
go install ./cmd/stackfork
```

Make sure `stackfork` resolves on `PATH` (typically `$(go env GOPATH)/bin`, often `~/go/bin`).

## Reusing a prepared checkout

```sh
stackfork --root "$CHECKOUT" prepare
stackfork --root "$CHECKOUT" up
stackfork --root "$CHECKOUT" urls
```

`prepare` reuses the last source-file selection, or the standard `.agent-dev/` template paths on first use. Select a project's authorized registry config once with `--npmrc "$PROJECT_NPMRC"` before `prepare`; subsequent starts reuse its path. Normal startup does not reset or seed the database.

`where` returns the instance hostname before startup; `urls` uses it for HTTP endpoints, for example `api-worktree-<hash>.localhost:<port>`. Select a shorter unique name with `prepare --hostname api-571.localhost`. Agents must use that same hostname in browser-facing app configuration; DB endpoints remain on `127.0.0.1`. Existing app adapters need their callback/CORS/HMR settings checked when adopting it.

The Nuxt template includes real Node 22 and Bun, defaults to one app replica, and supports `DEV_APP_REPLICAS=2` for multi-replica testing.

## Dashboard

```sh
stackfork dashboard --open
```

A local web page listing every instance across every checkout, grouped by checkout: status, services, health and clickable endpoints, learned-knowledge status and plain-language warnings. It's local-only (binds `127.0.0.1`, checks the `Host` header, and a per-process token guards every action) and read-mostly: the only actions are `up`, `down` (volumes always kept), `restart SERVICE`, viewing logs, and starting/stopping/reloading the shared proxy. Anything destructive or input-heavy (`down --volumes`, `gc --apply`, `prepare --reassign-ports`, `clone-db`, `learn`) shows up as a command to copy, never a button. `stackfork list` / `list --json` show the same data from the terminal.

## Checks

```sh
go test ./...
```

Optional live checks create disposable Docker stacks and clean up their own resources:

```sh
STACKFORK_LIVE=1 go test ./...
```

Live tests use synthetic Compose fixtures in `testdata/`, including a synthetic Node streaming app for the template/proxy checks, and may download public images/packages. Neither establishes compatibility with any real application.

State lives under `~/.local/state/stackfork`. Legacy `idv-` Docker identities and their previous state path are still read, so existing stacks are never orphaned.
