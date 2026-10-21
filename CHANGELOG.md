# Changelog

All notable changes to this project are documented here. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Secret vault (`add-secret-vault`): `keychain:ITEM` (macOS Keychain, Linux Secret Service, Windows Credential Manager target `stackfork/ITEM`, at most 2560 bytes) and `op://VAULT/ITEM/FIELD` (1Password) references, a machine `secrets` map in `config.json` and secret names in `knowledge.json` (`learn record secret`). `secret ls|check|set|map|unmap` never print or take a value. Every run starts with a `secrets` step that fails before Docker with each problem and its fix. Values go only to the Compose child (`os.Setenv` is gone) and are redacted in logs. The `--secret` flag now accepts only `env:` and `npmrc:` references; `keychain:` and `op://` come from `secret map`. `run` and `exec` redact their output with the values known for the instance. The guard blocks agents from `secret set|map|unmap`, store reads of stackfork items and mapped `op://` reads. The dashboard Repos panel has a Secrets table with Resolves, Missing, Locked, Not checked, Shell only, Unavailable and Error chips, **Check**, and a hidden-answer **Set value…** dialog. Windows code is built and vetted, not run on a Windows machine.
- Every start begins with a `sync` step: it fetches the checkout's upstream, fast-forwards a clean checkout with no local commits, and otherwise touches nothing and warns how many commits it is behind and how to update. A failed fetch is a note, never a failed start.
- `worktree add` for a branch that is already checked out names that checkout and the `start-env` command for it.
- Plan runs (`add-plan-runs`): each `start-env`, dashboard Start, `up` and `seed` is recorded as a run with its steps, states and step logs under the instance's `runs/` directory, the last 20 kept. `stackfork runs` and `runs show [ID] [--step STEP]` read them. The dashboard's Repos list shows the live plan of a start, each instance card shows its last run, and the log drawer reads any step of a run. A recorded `up` and `start-env` merge Compose's stderr into stdout in the step log (and the terminal output), so the two streams are no longer separate.
- Startup plan (`add-learn-plan`): `learn` records an ordered plan of steps (prepare, compose-up,
  exec, host, note) instead of a flat recipe. Check it with `stackfork plan [--mode] [--json]`.
  Hybrid mode (`config set mode hybrid`, `config set-mode REPO hybrid`, `start-env --mode`) runs
  your app on the host and the rest in containers. Host steps run only after you trust them with
  `stackfork plan --trust` or the dashboard's Trust button. `host start|stop|restart|logs NAME`
  runs an app detached and supervised, and `down` stops it first.
- Windows support (`add-learn-plan` slices 2 and 3): host steps, instance locks, the dashboard's
  trust and confirm dialogs, the Learn terminal and the agent guard (Windows paths, `cmd /c` and
  `powershell -Command`) are built for native Windows, and state and config live under
  `%LOCALAPPDATA%`. `env --format powershell` prints PowerShell lines. It is built and vetted but
  has not run on a Windows machine; the limits are in SECURITY.md.
- `doctor` reports a `host apps` warning for a host app whose supervisor is gone but whose port
  still answers.
- A new worktree of a repo that another worktree already runs can be started: `start-env` (and
  `worktree add`) rebuild the recipe from that sibling's prepared instance, copying its untracked
  adapter files and runtime env with checkout paths rewritten (never `.env`, keys or tokens), and
  `ready` now means a worktree is startable. A failed Start shows on the repo row with the CLI command to retry.
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

### Changed

- The seed is a step of its run: `seed.log` is no longer written, and the seed's output is the seed step's log. A failed seed now stops `start-env`.

### Fixed

- A stopped or exited host app stays as an exited row with Start; `down` forgets it.
- The card's Start on a hybrid instance runs the startup plan, not a plain `compose up`.

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
