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

Supported platforms: macOS and Linux (amd64 and arm64). Windows is built and vetted, but it has not run on a Windows machine yet. See [SECURITY.md](SECURITY.md) for its limits. Docker with the Compose plugin must be running.

Choose one method:

- Go 1.24 or later:

  ```sh
  go install github.com/pedropinheiro75/stackfork/cmd/stackfork@latest
  ```

  Make sure `$(go env GOPATH)/bin` is on your `PATH`.

- Build from a clone:

  ```sh
  git clone https://github.com/pedropinheiro75/stackfork && cd stackfork
  go build -o stackfork ./cmd/stackfork
  ```

  Put the `stackfork` binary on your `PATH`.

- Homebrew (macOS and Linux), after the first public release. The cask is not published yet:

  ```sh
  brew install --cask pedropinheiro75/tap/stackfork
  ```

To check the install, run `stackfork version`.

### macOS app

`Stackfork.app` is a menu bar app for macOS 14 or later. It runs its own dashboard, lists your instances in the menu bar with **Start** and **Stop**, and opens the dashboard in a window. It needs no installed CLI.

Until the first public release, build the DMG on your Mac. You need Xcode and Go.

1. From the root of a clone, run `scripts/build-macos-app.sh 0.1.0`. The script writes `dist/macos-app/stackfork-app_0.1.0_darwin_universal.dmg` and a `.sha256` file.
2. Open the DMG and drag `Stackfork.app` to Applications.
3. If macOS refuses to open the app, clear the quarantine attribute:

   ```sh
   xattr -dr com.apple.quarantine /Applications/Stackfork.app
   ```

4. Open Stackfork. The first menu row reads `Dashboard running`.

The app is signed ad hoc and is not notarized. Choose **Open at Login** in the menu to start it after you log in; this works only when the app is in `/Applications` or `~/Applications`. Press Control-Option-Command-S (`⌃⌥⌘S`) anywhere to show the dashboard window.

The app never uses or stops a `stackfork dashboard` that runs in a terminal. Both can run side by side, but live mode runs in only one of them. They share the same state directories, so update both together.

## Quick start

These five commands start two isolated copies of the `examples/node-postgres` project: a small Node server and Postgres. Each request counts a visit, so you can see that the two copies have separate data. The first run builds the app image.

```sh
git clone https://github.com/pedropinheiro75/stackfork && cd stackfork
stackfork learn && stackfork prepare -f examples/node-postgres/docker-compose.yml && stackfork up
git worktree add ../stackfork-2 && cd ../stackfork-2
stackfork prepare -f examples/node-postgres/docker-compose.yml && stackfork up
stackfork list && stackfork urls
```

`stackfork learn` comes first because `prepare` and `up` refuse a repository that was never learned. The second worktree reads what `learn` recorded in the main checkout.

`stackfork list` shows two instances in the `running` state, one for each checkout. `stackfork urls` prints the app URL of this checkout, for example `http://stackfork-2-ba629bc76a3b.localhost`. Run it in the first checkout for the other URL. Request each URL more than once. The `visits` count of one copy does not change the count of the other.

If the `.localhost` URLs do not answer, for example after a Docker restart, run `stackfork proxy up`. To clean up, run `stackfork down --volumes` in each checkout. Without `--volumes`, `down` keeps your data.

For your own project, replace the `-f` path with your Compose file. `stackfork prepare` renders the file into an isolated snapshot and never edits it.

## Feature tour

Each item links to the details.

- **Learn once, reuse everywhere.** `stackfork learn` scans a project. You or your agent record an ordered startup plan, the app's entry URL and named links in `.stackfork/knowledge.json`, then confirm it with `learn verify`. The file is safe to commit. Later worktrees reuse it, and a worktree can borrow verified knowledge from a sibling worktree. `stackfork plan` shows the steps, and a derived plan for a project that has none. See [references/learning.md](references/learning.md).
- **One command per branch.** `stackfork worktree add REPO BRANCH` creates a worktree. `stackfork start-env` prepares and starts it. `stackfork reset` deletes its environment, volumes included, and starts it again from the code in the worktree. `reset --from-main` reuses the main checkout's Compose recipe. See [references/worktrees.md](references/worktrees.md).
- **Runs.** Each start is a recorded run with steps and logs. After a failed start, run `stackfork runs show`, and add `--step STEP` for that step's log. Secret values are redacted. See [`runs`](references/commands.md#runs).
- **Hybrid mode and host apps.** The app can run on the host while its dependencies stay in Docker. `stackfork host start NAME -- COMMAND` runs it detached, on a stable port, with this instance's environment and a `.localhost` route. `host ls` shows PID, memory and CPU, and `up` restarts a host app that stopped by itself. A person must trust host steps first with `stackfork plan --trust`. Set `stop_cascade` in the project's plan and give host steps a `depends_on` list, and stopping or losing a service (a removed database) also stops the host apps that need it. See [`host`](references/commands.md#host).
- **Per-instance environment overrides.** `stackfork env set SERVICE NAME=VALUE` changes one variable of one service of one instance, outside the repository, and recreates only that service. See [`env`](references/commands.md#env).
- **Secrets.** A project names the secrets it needs. Each person maps them on their machine to a `keychain:` item (macOS Keychain, Linux Secret Service or Windows Credential Manager) or to a 1Password `op://` reference, for one repository or for a whole folder such as `~/clients/acme`. No `secret` command prints a value or takes one as an argument. See [`secret`](references/commands.md#secret).
- **Live mode.** `stackfork live on` makes the running instances of a repository follow their upstream while the dashboard runs, and rebuild what changed. When a worktree's branch is merged or its pull request is closed, live mode can offer to clean the worktree up. See [`live`](references/commands.md#live).
- **Dashboard.** `stackfork dashboard --open` starts a local web page with four views. **Instances** are grouped by project, with status, health, endpoints and host apps. **Repos** shows branches, worktrees and readiness, and starts a ready project with one click. **Clean up** shows Docker disk use, frees space and prunes instances. **Settings** holds the folders to scan (add one with `stackfork config add-root PATH`; nothing is scanned before that) and the **Secrets** table. It binds to `127.0.0.1`.
- **Agent guard.** `stackfork hooks install` blocks a host dev server or a raw `docker compose up` in a managed checkout, for Claude Code, Codex and Copilot CLI.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/dashboard-overview-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="docs/screenshots/dashboard-overview-light.png">
  <img src="docs/screenshots/dashboard-overview-light.png" alt="Dashboard overview: three running instances (two container-based, one host-run) and one stopped, each with its app/DB/mail endpoints and branch/checkout shown">
</picture>

For every command and flag, see [references/commands.md](references/commands.md) or run `stackfork help`.

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
- **Guard hooks.** The hook blocks host dev servers and raw `docker compose up` in a managed checkout. It also blocks agents from commands that delete data or widen trust: `down --volumes`, `reset`, `prune --yes`, `plan --trust`, `live on`, `secret set|map|unmap` and `env set --allow-secret`. It is a guardrail for cooperative agents, not a security boundary.
- **Trust for host steps.** A project can record a command to run on the host (hybrid mode). That command runs only after a person runs `stackfork plan --trust` or clicks **Trust** in the dashboard. Trust is stored for each machine.
- **Native confirmation.** The dashboard runs a data-deleting command only after a person clicks Run in a native macOS dialog. An agent can send the request, but it cannot answer the dialog.
- **Data kept by default.** `stackfork down` removes no volumes. `stackfork prune` is a dry run until you pass `--yes`. It touches only Docker objects that carry stackfork labels, and it never runs `docker system prune`.
- **Secrets.** The dashboard shows a `<password>` placeholder. It returns a real password only when the value is written literally in your Compose file. Named secrets come from your OS credential store or 1Password, and `env set` refuses secret-like names.

Read [SECURITY.md](SECURITY.md) for the full boundary, the known limits and how to report a vulnerability.

## Agent skill

The repository ships an agent skill (`SKILL.md` and `references/`). It teaches an agent the flow: reuse what was learned, prepare, up, verify, down. Link a checkout of this repository into your agent's skill directory. Run these commands from the root of your clone.

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

To block agents that start a dev server on the host, install the guard hooks and check them:

```sh
stackfork hooks install      # add --tool claude|codex|copilot to choose one tool
stackfork hooks status
```

Codex runs only a hook that you trusted. After `hooks install --tool codex`, open `codex` once and approve the hook, or run `/hooks`.

## Project files

- [references/](references/): the command reference and guides for learning, worktrees, hostnames and adapting Compose.
- [SECURITY.md](SECURITY.md): the safety boundary, known limits and how to report a vulnerability.
- [CHANGELOG.md](CHANGELOG.md): the history of changes.
- [CONTRIBUTING.md](CONTRIBUTING.md): how to build, test and contribute. Run `go test ./...` to run the offline tests.
- [LICENSE](LICENSE): MIT.
