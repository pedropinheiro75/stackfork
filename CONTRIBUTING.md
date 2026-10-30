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

Maintainers can set `STACKFORK_PRIVATE_TERMS_FILE` to a file of private terms (one per line, kept outside the repo) to make `internal/privatecheck` fail if any of them appears in the tree; the test is skipped when it is unset.

## The macOS app

The app is a Swift package in `desktop/macos` (AppKit, WebKit, ServiceManagement and Foundation only; no dependency). It needs Xcode with Swift 6.2.

```sh
swift build --package-path desktop/macos
swift test --package-path desktop/macos
scripts/build-macos-app.sh 0.0.0-dev
```

The script writes `Stackfork.app`, the DMG and its `.sha256` file to `dist/macos-app/` (ignored by Git). Never use port 26607 or your own state directory in tests: run a built app with a temporary `HOME`.

The release workflow builds the DMG after GoReleaser. If that step fails, the CLI release and its cask are already published. On the release runner, run `scripts/build-macos-app.sh VERSION` and then `gh release upload vVERSION dist/macos-app/*.dmg dist/macos-app/*.sha256`.

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
