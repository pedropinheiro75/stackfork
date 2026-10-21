<div align="center">

<img src="docs/logo.svg" width="120" height="120" alt="stackfork logo">

# stackfork

**Run the same project twice — for two AI agents, or an agent and you — without them stepping on each other.** 🧑‍🤝‍🧑🤖

[![CI](https://github.com/pedropinheiro75/stackfork/actions/workflows/ci.yml/badge.svg)](https://github.com/pedropinheiro75/stackfork/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/pedropinheiro75/stackfork.svg)](https://pkg.go.dev/github.com/pedropinheiro75/stackfork)
![Go >= 1.24](https://img.shields.io/badge/go-%3E%3D1.24-2563eb)
![Standard library only](https://img.shields.io/badge/dependencies-stdlib%20only-059669)
[![License: MIT](https://img.shields.io/badge/license-MIT-db2777)](LICENSE)

</div>

You open a second Git worktree of a project and run `docker compose up`. It collides with the first one: same ports, same container names, same volumes, sometimes the same database. This happens each time a coding agent works next to you, or next to another agent.

**stackfork takes the Compose stack you already have and isolates it for each checkout and branch.** Each copy gets its own ports, volumes, network and container names. A person and an agent, or five agents, can run the same project at the same time on one machine without coordination.

<img src="docs/demo.gif" alt="Terminal demo: two Git worktrees of one project each start with stackfork up, then stackfork list shows both instances running, and both app URLs answer in the browser">

## Install

Supported platforms: macOS and Linux (amd64 and arm64). Windows is built and vetted, but it has not run on a Windows machine yet. See [SECURITY.md](SECURITY.md) for its limits.

Choose one method:

- Go 1.24 or later:

  ```sh
  go install github.com/pedropinheiro75/stackfork/cmd/stackfork@latest
  ```

  Make sure `$(go env GOPATH)/bin` is on your `PATH`.

- Release binary: download the archive for your system from the [releases page](https://github.com/pedropinheiro75/stackfork/releases), unpack it, and put `stackfork` on your `PATH`.

A Homebrew tap is coming with the first release.

To check the install, run `stackfork version`. Docker with the Compose plugin must be running.

## Quick start

These five steps start two isolated copies of one Compose project. The project is the `examples/node-postgres` example: a small Node server and Postgres. Each request counts a visit, so you can see that the two copies have separate data. The first run builds the app image.

1. Clone the repository and go into it:

   ```sh
   git clone https://github.com/pedropinheiro75/stackfork && cd stackfork
   ```

2. Prepare and start the first copy:

   ```sh
   stackfork prepare -f examples/node-postgres/docker-compose.yml && stackfork up
   ```

3. Create a second worktree of the same repository and go into it:

   ```sh
   git worktree add ../stackfork-2 && cd ../stackfork-2
   ```

4. Prepare and start the second copy. Use the same command as in step 2:

   ```sh
   stackfork prepare -f examples/node-postgres/docker-compose.yml && stackfork up
   ```

5. Show both instances and the URL of this one:

   ```sh
   stackfork list
   stackfork urls
   ```

`stackfork list` shows two instances in the `running` state, one for each checkout. `stackfork urls` prints the app URL, for example `http://stackfork-2-ba629bc76a3b.localhost`. Run `stackfork urls` in the first checkout to get the other URL. Request each URL more than once. The `visits` count of one copy does not change the count of the other.

If the `.localhost` URLs do not answer, for example after a Docker restart, run `stackfork proxy up`.

To clean up, run `stackfork down --volumes` in each checkout. Without `--volumes`, `down` keeps your data.

The example is a normal Compose project that stackfork does not change. For your own project, replace the `-f` path with your Compose file. `stackfork prepare` renders the file into an isolated snapshot and does not edit it.

## How it differs from other tools

stackfork does not add an environment format. It isolates the Compose file that you already maintain.

| Tool | What it needs from you | What stackfork does differently |
|---|---|---|
| devcontainers | A `devcontainer.json`, often a copy of your Compose setup | Reads your Compose file as it is. It targets collisions between parallel copies, not one reproducible environment. |
| Dagger and container-use | A pipeline or an agent sandbox that you define | Works at a different layer. It runs your existing stack and does not build or sandbox for you. |
| Conductor-style worktree managers | Nothing for Compose. They create and orchestrate worktrees and agents | Complements them. The manager creates the worktrees. stackfork makes each worktree's stack safe to run. |

## Safety model

stackfork isolates checkouts from each other. It is not a sandbox. It does not protect you from a malicious Compose file, and it adds no isolation between containers and the host beyond what Docker gives you.

- **Refusals.** stackfork refuses a stack that it cannot isolate safely. Examples are fixed container names, external networks, the Docker socket, writable binds outside the checkout and fixed subnets.
- **Guard hooks.** `stackfork hooks install` adds a pre-tool-use hook to Claude Code, Codex and Copilot CLI. The hook blocks a host dev server or a raw `docker compose up` inside a managed checkout, and it allows everything else. It is a guardrail for cooperative agents, not a security boundary.
- **Trust for host steps.** A project can record a command to run on the host (hybrid mode). That command runs only after a person runs `stackfork plan --trust` or clicks **Trust** in the dashboard. Trust is stored for each machine. The guard blocks agents from trusting a plan.
- **Native confirmation.** The dashboard runs a data-deleting command (`down --volumes`, `prune`, `gc`, worktree removal) only after a person clicks Run in a native macOS dialog. An agent can send the request, but it cannot answer the dialog.
- **Environment reset.** `stackfork reset` deletes one worktree's volumes and rebuilds its environment from the code in the worktree; it asks first and never touches your code.
- **Data kept by default.** `stackfork down` removes no volumes. `stackfork prune` is a dry run until you pass `--yes`. It touches only Docker objects that carry stackfork labels, and it never runs `docker system prune`.
- **Secrets.** stackfork prints no secret value. The dashboard shows a `<password>` placeholder. It returns a real password only when the value is written literally in your Compose file. Named secrets (tokens, API keys) come from your OS keychain or 1Password: see [Secrets](#secrets).

Read [SECURITY.md](SECURITY.md) for the full boundary, the known limits and how to report a vulnerability.

## Runs

Each Start (`start-env`, the dashboard's Start button, `up` and `seed`) is a run. stackfork records the steps of a run, their states and output under the instance's `runs/` directory and keeps the last 20 runs. After a failed start, run `stackfork runs show` to see which step failed, and `stackfork runs show --step <step>` for its log (secret values are redacted). While a start is in progress, the dashboard's Repos list shows its plan as it runs, and each instance card shows the last run.

## Agent skill

The repository ships an agent skill (`SKILL.md` and `references/`). It teaches an agent the flow: discover, prepare, up, verify, down. Link a checkout of this repository into your agent's skill directory. Run these commands from the root of your clone.

For Claude Code:

```sh
mkdir -p ~/.claude/skills && ln -s "$(pwd)" ~/.claude/skills/stackfork
```

For Codex:

```sh
mkdir -p ~/.codex/skills && ln -s "$(pwd)" ~/.codex/skills/stackfork
```

Then ask the agent in your own words, for example "prepare this project in a new worktree". Claude Code selects the skill from the `description` in `SKILL.md`. Codex reads `agents/openai.yaml`, so use this prompt:

```text
Use $stackfork to prepare this checkout with an isolated development environment using stackfork.
```

To block agents that start a dev server on the host, install the guard hooks:

```sh
stackfork hooks install      # add --tool to choose one tool
stackfork hooks status
```

Codex runs only a hook that you trusted. After `hooks install --tool codex`, open `codex` once and approve the hook, or run `/hooks`.

## Secrets

A project can name the secrets it needs (`learn record secret NPM_TOKEN --hint "..."`). Each person decides where the value comes from on their machine. To move a token from your shell to the keychain:

```sh
printenv NPM_TOKEN | stackfork secret set acme-npm-token     # stores it; the value is never an argument
stackfork secret map . NPM_TOKEN=keychain:acme-npm-token      # this repo's NPM_TOKEN now comes from the keychain
stackfork secret check                                        # resolves | missing | locked | unavailable | error
```

`keychain:` uses the macOS Keychain, the Linux Secret Service (`secret-tool`) or the Windows Credential Manager (target `stackfork/ITEM`, at most 2560 bytes). `op://VAULT/ITEM/FIELD` reads from 1Password with `op`. Every run starts with a `secrets` step that fails before any Docker command and lists each problem with the command that fixes it. A Start from the dashboard does not see your shell, so use the keychain or 1Password for those values. The dashboard's Repos panel has a Secrets table with a hidden-answer **Set value…** dialog.

## Dashboard

```sh
stackfork dashboard --open
```

The dashboard is a local web page. It lists every instance in every checkout that stackfork knows about, with status, health and clickable endpoints. It binds to `127.0.0.1`, checks the `Host` header and uses a token for each process. `stackfork list --json` gives the same data in a terminal.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/dashboard-overview-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="docs/screenshots/dashboard-overview-light.png">
  <img src="docs/screenshots/dashboard-overview-light.png" alt="Dashboard overview: three running instances (two container-based, one host-run) and one stopped, each with its app/DB/mail endpoints and branch/checkout shown">
</picture>

Each service row expands to show the exact command for a destructive action and one-click copy of connection strings:

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/dashboard-service-detail-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="docs/screenshots/dashboard-service-detail-light.png">
  <img src="docs/screenshots/dashboard-service-detail-light.png" alt="One instance expanded, showing its down command to copy and each service's connection URL/psql/exec copy actions">
</picture>

The **Repos** tab finds the repositories under the folders that you add with `stackfork config add-root PATH`. It shows branches, worktrees and readiness, and it starts a ready project with one click. Nothing is scanned until you add a folder.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/repos-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="docs/screenshots/repos-light.png">
  <img src="docs/screenshots/repos-light.png" alt="Repos tab: repositories grouped by folder with readiness badges, worktrees, branches without a worktree and Start buttons">
</picture>

## Learn and plan: container or hybrid mode

`stackfork learn` scans a project and records how to start it in `.stackfork/knowledge.json`. You or your agent check the result with `stackfork learn verify`. The file is safe to commit, and later worktrees reuse it. The record is a startup plan: an ordered list of steps. Run `stackfork plan` to see the steps and whether this machine trusts them.

The plan has two modes:

- **container** (default): every service, including the app, runs in Docker.
- **hybrid**: the app runs on the host as a `host` step. The app service and the services that depend on it are dropped from Compose. The other services stay in containers.

To use hybrid mode:

1. Run `stackfork config set mode hybrid`. For one repository, run `stackfork config set-mode REPO hybrid`.
2. Run `stackfork plan --mode hybrid` and read the host steps.
3. Run `stackfork plan --trust`, or click **Trust** in the dashboard.
4. Run `stackfork start-env`.

Manage a host app with `stackfork host stop|restart|logs NAME`. `stackfork down` stops it first. For the full command list, see [references/commands.md](references/commands.md) and `stackfork --help`.

## Project files

- [SECURITY.md](SECURITY.md): the safety boundary, known limits and how to report a vulnerability.
- [CHANGELOG.md](CHANGELOG.md): the history of changes.
- [CONTRIBUTING.md](CONTRIBUTING.md): how to build, test and contribute. Run `go test ./...` to run the offline tests.
- [LICENSE](LICENSE): MIT.
