# Contributing

## Requirements

- Go >= 1.24 (`go.mod` pins the minimum toolchain).
- Docker with Compose v2, only for the live tests.

## Standard library only

New CLI code is Go standard library only. `flag.FlagSet` per subcommand,
`encoding/json` for manifests and Compose output. Adding a third-party
dependency needs a written reason in the PR description: what problem the
standard library doesn't solve, and why the dependency is worth the
maintenance and supply-chain cost. `go.sum` should stay absent or empty.

## Running the tests

Offline (no Docker, no network beyond what `go test` itself needs):

```sh
go test ./...
```

Live, exercising real Docker Compose stacks (creates and tears down its own
disposable resources only):

```sh
STACKFORK_LIVE=1 go test ./...
```

Live tests use the synthetic fixtures in `testdata/`, never a real project
checkout, so they pass on a clean machine.

## Invariants (do not weaken)

These come from `openspec/config.yaml` and hold for every change:

- **Refuse, don't guess.** Anything that cannot be isolated safely (external
  networks, the Docker socket, host namespaces, writable binds outside the
  checkout, fixed subnets) is rejected with a clear error, never silently
  passed through or worked around.
- **Identity = resolved checkout path + branch.** Never the directory
  basename alone.
- **Never print rendered Compose config or credentials.** Compose errors are
  suppressed because they can contain interpolated secrets. State files are
  written `0600`/`0700`.
- **Never delete data without an explicit flag** (`down --volumes`, `gc`
  confirmation).
- **Existing running stacks created under legacy `idv-` identities must stay
  manageable.** Legacy identities are read, never rewritten or migrated.

## Before opening a PR

- `go vet ./...` and `go test ./...` pass.
- If you touched anything under `.github/workflows/`, run `actionlint`
  locally; workflows are not exercised on GitHub as part of review.
- If you touched packaging, run `goreleaser check` and
  `goreleaser release --snapshot --clean` locally.
