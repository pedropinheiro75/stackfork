# Changelog

All notable changes to this project are documented here. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

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
