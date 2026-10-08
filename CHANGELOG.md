# Changelog

All notable changes to this project are documented here. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Repo browser (`add-repo-browser`): `stackfork config` (scan roots, excludes, max
  depth in `config.json`), `stackfork repos [--json] [--rescan] [--prompt REPO BRANCH]`
  (repos under the roots with branches, worktrees, instance state and a
  `ready`/`draft`/`unknown` readiness, local git only, mtime-cached),
  `stackfork worktree add REPO BRANCH [--from BASE]` (`<repo>/.worktrees/<branch>`,
  local git exclude, allowlisted adapter-file copy with a credential denylist) and
  `stackfork start-env [CHECKOUT]` (prepare when needed, then up). The dashboard
  gets a Repos tab (search, ready-only, Start, "Set up with your agent", per-repo
  Fetch, copy-only worktree removal), a Settings panel for the roots
  (`POST /api/config`, server-side validation) and `GET /api/repos`
  (byte-identical to `repos --json`); new POST actions `start`, `worktree-add`
  and `fetch` only accept repos and worktrees the scan found under a root.
- `stackfork prune [--orphaned|--stopped|--all] [--older-than DURATION] [--keep-volumes] [--yes] [--json]`
  (`add-prune`): dry run by default (table, groups, disk estimate), typed
  confirmation for running instances or `--all`, exit 1 without a TTY and
  `--yes`. Removes containers, networks, built images, state, routes and
  volumes (unless `--keep-volumes`) of exactly the matching instances, legacy
  `idv-` included; never by name prefix, never `docker system prune`. `gc` is
  now an alias of `prune --orphaned --keep-volumes`. The guard blocks
  `prune --yes`, `prune --all` and `gc --apply` for agents; the dashboard has a
  read-only "Clean up" panel in the header menu (`GET /api/cleanup`).
- The proxy container starts with `--restart unless-stopped` (an existing one
  is updated on the next `proxy up`), and `proxy up` starts a stopped proxy
  instead of recreating it. `status`, `urls`, `doctor` and the dashboard warn
  when routes exist but the proxy is down.
- Agent guard (`add-agent-guard`): `stackfork guard [--tool claude|codex|copilot]`
  blocks a dev server (`bun|npm|pnpm|yarn dev|start|serve`, `nuxt dev`, `vite`,
  `next dev`, `nodemon`) or a raw `docker compose up/start` started directly in
  a stackfork-managed checkout, and allows everything else;
  `stackfork hooks install|uninstall|status` wires it into Claude Code, Codex
  and Copilot CLI (idempotent, existing hooks kept, a backup first).
- `stackfork host NAME [--port N] -- COMMAND...`: the sanctioned host-run path
  (stable port, `env --with-passwords` plus `PORT`/`NUXT_PORT`/`NITRO_PORT`/`HOST`,
  a proxy route while it runs, signals forwarded).
- Global `--instance NAME` on up, down, restart, start, stop, status, urls,
  logs, exec, run, env, open and host; a checkout with no stack now lists this
  repository's instances in other worktrees, and `restart <instance-name>` says
  it is an instance, not a service.
- `stackfork open [SERVICE] [--print]`: the instance's primary app URL.
- `status`, `list` and the dashboard hint `stackfork restart <service>` for an
  unhealthy or exited service (`status` adds an optional `hint` to the JSON row).

- `assets/go/`: an optional Go web app template (mirrors the Nuxt one -
  app + Postgres, `go mod download` entrypoint, optional private-module
  netrc via a Compose secret). `assets/nuxt.compose.yaml` and friends moved
  to `assets/nuxt/` alongside it, so a template's assets live under its own
  runtime rather than flat at the top level. See `references/go-template.md`.
- `stackfork list` / `list --json`: every instance across every checkout,
  from a new `internal/inventory` package shared with the dashboard, so the
  two can never disagree.
- `stackfork dashboard [--port N] [--open]`: a local, read-mostly web page
  (embedded assets, no Node toolchain) showing the same inventory, grouped
  by checkout, with clickable HTTP endpoints, learned-knowledge status and
  warnings. Safe actions only (`up`, `down` with volumes always kept,
  `restart SERVICE`, log viewing, proxy up/down/reload), behind a
  127.0.0.1-only bind, a `Host` allowlist, and a per-process action token.
  Destructive or input-heavy commands (`down --volumes`, `gc --apply`,
  `prepare --reassign-ports`, `clone-db`, `learn`) show up as a command to
  copy, never a button.
- `stackfork restart SERVICE` and `stackfork logs [SERVICE] [--tail N]
  [--follow]`, and a new `internal/redact` package that replaces resolved
  secret values in log output with `[redacted:<name>]`.
- `stackfork env [--format shell|dotenv] [--with-passwords]`: the current
  instance's `DATABASE_URL`/`REDIS_URL`/`MONGODB_URL`, `SMTP_HOST`/
  `SMTP_PORT`, and one `<SERVICE>_URL` per HTTP service (the portless proxy
  URL when routed), for a host-run or hybrid app - `eval "$(stackfork
  env)"`. Reuses the dashboard's own env-block/connection-URL logic; a real
  password only ever appears with `--with-passwords`, and only for a
  literal Compose value, never `--secret`/`env_file`/`${VAR}`.
- `stackfork clone-db --from-dump FILE [--service NAME] [--database NAME]
  [--replace]`: seeds this instance's own running database from a local
  `.sql`, `.sql.gz`, or Postgres custom-format dump, streamed directly into
  the container (never a temp copy on disk). Postgres supports all three
  forms; MySQL/MariaDB gets plain SQL.

### Rewritten in Go

The CLI (`scripts/compose.py`, Python standard library) has been ported to
Go: module `github.com/pedropinheiro75/stackfork`, standard library only,
building a single static binary. Behaviour was ported for parity first
(golden tests comparing the Python and Go `prepare` output on shared
`testdata/` fixtures), then the Python CLI and its tests
(`scripts/compose.py`, `scripts/test_compose.py`, `scripts/test_template.py`)
were deleted once the Go binary reached parity. Tests run offline with
`go test ./...`; `STACKFORK_LIVE=1 go test ./...` adds the Docker-backed
tests.

### Renamed to stackfork, with legacy compatibility

The tool is renamed from `isolated-dev`/`isolated-dev` to `stackfork`
(prefix `sf`, label namespace `io.stackfork`, state directory
`~/.local/state/stackfork`). Existing `idv-`-prefixed instances are not
migrated or renamed: `where`, `status`, `urls`, `list` and `down` still read
`~/.local/state/isolated-dev/` and recognise `idv-` instance names and
`io.isolated-dev.root` labels, so stacks created before the rename stay
manageable without data loss. `RR_DEV_HOSTNAME` is still exported alongside
the new `DEV_HOSTNAME` for existing adapters, but is deprecated in favor of
`DEV_HOSTNAME`.

### Private profiles

Client- and company-specific material (recipes, references, assets) no
longer lives in this repository. It moves to a *profile*: a directory the
user owns outside the repo, selected with `--profile-dir` or
`STACKFORK_PROFILE_DIRS`, or picked up automatically from
`~/.config/stackfork/profiles/`. The CLI never reads profile content; the
agent skill reads the matching reference and follows the profile's
`context.md` if present.

### Synthetic fixtures replace real checkouts in tests

The live and parity test suites now run against synthetic multi-service
Compose fixtures under `testdata/` instead of a local, private checkout, so
`--live`/`STACKFORK_LIVE=1` passes on a machine with no prior project
checkouts.

### Removed

- The Python CLI (`scripts/compose.py`) and its tests (`scripts/test_compose.py`,
  `scripts/test_template.py`). All CLI code is now Go.

### Deprecated

- `RR_DEV_HOSTNAME` environment variable: still set for backward
  compatibility, superseded by `DEV_HOSTNAME`.
