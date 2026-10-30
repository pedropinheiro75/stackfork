<div align="center">

<img src="docs/logo.svg" width="120" height="120" alt="stackfork logo">

# stackfork

### Every branch gets its own running stack. Every AI agent gets its own.

**A reproducible recipe for running your app locally, in Docker or on the host. Run as many branches of it as you need, at the same time, with every process supervised and Git opening and closing the environments.**

[![CI](https://github.com/pedropinheiro75/stackfork/actions/workflows/ci.yml/badge.svg)](https://github.com/pedropinheiro75/stackfork/actions/workflows/ci.yml)
![Works with Claude Code · Codex · Copilot CLI](https://img.shields.io/badge/works%20with-Claude%20Code%20%C2%B7%20Codex%20%C2%B7%20Copilot%20CLI-f59e0b)
![Single binary, stdlib only](https://img.shields.io/badge/single%20binary-stdlib%20only-059669)
![macOS · Linux](https://img.shields.io/badge/platforms-macOS%20%C2%B7%20Linux-6366f1)
[![License: MIT](https://img.shields.io/badge/license-MIT-db2777)](LICENSE)

[Install](#install) · [Quick start](#quick-start) · [What it does](#what-stackfork-does) · [Agents](#work-with-coding-agents) · [Safety](#safety-model)

</div>

<img src="docs/demo.gif" alt="Terminal demo: stackfork start-env starts a project, a second worktree starts the same project, curl shows the two copies counting visits in separate databases, stackfork list shows both running, and down --volumes removes one">

**The problem.** You give Claude Code a branch and keep working on another. Both run `docker compose up`. Same ports, same container names, same volumes, often the same database. One of you breaks the other, and the agent cannot tell.

**The fix.** stackfork learns how your project starts, once, and records the recipe in the repository: what runs in Docker, what runs on the host, what to install, migrate and seed, and which URL to open. From then on every worktree starts with one command, on its own ports, volumes, network and `.localhost` URL. Five agents and you can work on five branches of one project on one laptop, at the same time, and nothing collides. stackfork keeps watching every process it started, and Git decides which environments exist.

## Why developers keep it

| | |
|---|---|
| **Zero new config** | It reads the Compose file you already maintain and never edits it. If something cannot be isolated safely, it names the line. |
| **Docker, or not** | Keep Postgres and Redis in containers and run the app on the host for hot reload and a debugger. The recipe knows both, and each branch still gets its own copy. |
| **Git drives it** | `worktree add` + `start-env`, or turn on live mode and a new branch starts by itself. Merge or close the pull request, and the environment goes away. GitOps, on your laptop. |
| **Learn once, start forever** | The startup plan (install, build, migrate, seed, host apps, URLs) is a committed file. Teammates and agents start the project the same way, every time. |
| **Agents run free, people hold the keys** | Agents start, test and stop environments on their own. Deleting data, trusting host commands and turning automation on wait for a person. |
| **Every process is supervised** | Host apps are started, health-checked, restarted on a proxy change and reported when they stop answering. Reboot or Docker restart: what was running comes back, and what lost its database stops instead of running against nothing. |
| **You see everything** | A dashboard and a macOS menu bar app show every running copy, its URLs, its memory and CPU, what each start did and why it failed. |

<img src="docs/dashboard.gif" alt="Dashboard demo: the Instances page with two container instances and one hybrid instance whose app runs on the host, the Project page with the startup workflow canvas and resource charts, the environment overrides dialog, and the Repos page">

## Install

Supported platforms: macOS and Linux, amd64 and arm64. Windows builds and passes `go vet`, but it has not run on a Windows machine yet. Docker with the Compose plugin must be running. See [SECURITY.md](SECURITY.md) for the limits.

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

To check the install, run `stackfork version`.

### macOS app

`Stackfork.app` is a menu bar app for macOS 14 or later. It runs its own dashboard, lists your instances in the menu bar with **Start** and **Stop**, and opens the dashboard in a window. It needs no installed CLI. Press Control-Option-Command-S (`⌃⌥⌘S`) anywhere to show the window.

Until the first public release, build the DMG on your Mac. You need Xcode and Go.

1. From the root of a clone, run `scripts/build-macos-app.sh 0.2.0`. The script writes `dist/macos-app/stackfork-app_0.2.0_darwin_universal.dmg` and a `.sha256` file.
2. Open the DMG and drag `Stackfork.app` to Applications.
3. If macOS refuses to open the app, clear the quarantine attribute:

   ```sh
   xattr -dr com.apple.quarantine /Applications/Stackfork.app
   ```

4. Open Stackfork. The first menu row reads `Dashboard running`.

The app is signed ad hoc and is not notarized. Choose **Open at Login** in the menu to start it after you log in; this works only when the app is in `/Applications` or `~/Applications`. The app and a `stackfork dashboard` started in a terminal can run side by side. They share the same state, and only one of them watches your instances at a time.

## Quick start

These commands start two isolated copies of `examples/node-postgres`, a small Node server with Postgres. Each request counts a visit, so you can see that the two copies have separate data. The first run builds the app image.

```sh
git clone https://github.com/pedropinheiro75/stackfork && cd stackfork
stackfork learn && stackfork prepare -f examples/node-postgres/docker-compose.yml && stackfork up
git worktree add ../stackfork-2 && cd ../stackfork-2
stackfork prepare -f examples/node-postgres/docker-compose.yml && stackfork up
stackfork list && stackfork urls
```

`stackfork learn` comes first because `prepare` and `up` refuse a repository that was never learned. The second worktree reads what `learn` recorded in the main checkout.

`stackfork list` shows two instances in the `running` state, one per checkout. `stackfork urls` prints the app URL of the current checkout, for example `http://stackfork-2-ba629bc76a3b.localhost`. Run it in the first checkout for the other URL. Request each URL more than once: the `visits` count of one copy does not change the count of the other.

If the `.localhost` URLs do not answer, run `stackfork proxy up`. To clean up, run `stackfork down --volumes` in each checkout. Without `--volumes`, `down` keeps your data.

For your own project, replace the `-f` path with your Compose file. Then let an agent learn the project once, as described in [Learn a project](#learn-a-project), and start every worktree with `stackfork start-env`.

## What stackfork does

### Isolate every checkout

Each checkout and branch gets its own project name, ports, volumes, network and container names. Published ports become random host ports, and the shared proxy gives every instance a stable `.localhost` hostname, so URLs stay readable. stackfork refuses a stack that it cannot isolate safely (fixed container names, external networks, the Docker socket, writable binds outside the checkout) and tells you what to change. See [references/adapting-compose.md](references/adapting-compose.md).

### Learn a project

`stackfork learn` scans the Compose files, Dockerfiles, entrypoints, scripts and env names of a project, offline and without a model, and writes a draft `.stackfork/knowledge.json`. The agent that runs the learn session then records what the scan cannot know and confirms it with `learn verify`:

- the ordered **startup plan**: `prepare`, `compose-up`, install (`once: deps`, re-run when a lockfile changes), build, migrate (`once: schema`), seed (`once: data`, on a new database only), host apps and notes;
- the app's **entry URL** and named **links** (API, admin, mail inbox, Storybook);
- the **secrets** the project needs, by name only;
- **how code changes reach each app**: `hot` (live reload), `restart` or `rebuild`;
- whether the app's own entrypoint already migrates or seeds, so the plan does not do it twice.

The file is safe to commit. A worktree reads its own copy, then the main checkout's, then the newest verified copy in a sibling worktree. The dashboard has a **Learn with your agent** button that opens your terminal with the prompt. See [references/learning.md](references/learning.md).

### Start a branch with one command

```sh
stackfork worktree add REPO BRANCH   # creates the worktree
stackfork start-env                  # prepares and runs the plan
stackfork reset                      # throws the environment away and starts again
```

Every start is a recorded **run** with its steps, states and logs, with secret values redacted. `stackfork runs show` explains a failed start; the dashboard shows the same run on a card. `reset --from-main` reuses the main checkout's recipe. See [references/worktrees.md](references/worktrees.md) and [`runs`](references/commands.md#runs).

### Run the app on the host (hybrid mode)

The app can run on the host while its dependencies stay in Docker. A `host` step in the plan starts it detached, on a stable port, with the instance's environment (`DATABASE_URL`, `REDIS_URL`, `SMTP_HOST`, `<SERVICE>_URL`) and a `.localhost` route. Placeholders such as `{{port}}`, `{{url}}` and `{{api.url}}` expand at start, and follow the proxy when it runs. The dashboard shows each host app with its PID, memory and CPU; `up` restarts one that died by itself and leaves one you stopped. A host step named like a Compose service replaces it. See [`host`](references/commands.md#host).

### Let Git drive the environments (local GitOps)

The repository describes how the project starts, and the Git state decides what runs. stackfork reconciles the two while the dashboard runs:

1. **A branch becomes an environment.** `stackfork worktree add` plus `start-env`, or one click in the dashboard, and the branch runs on its own URL with its own database. An agent can do this for every pull request it works on.
2. **The environment follows the branch.** With `stackfork live on`, every start begins with a `sync` step that fetches the upstream, and the dashboard rebuilds what changed on each push: a changed Compose file, a new migration directory, a new lockfile.
3. **A closed pull request ends the environment.** When the branch is merged into the default branch, or its pull request is closed, branch cleanup removes the worktree, the instance and its volumes, and the local branch if you allow it. Default: it suggests the cleanup after a 24 hour grace period; `stackfork live cleanup auto` does it without asking.

The plan is a file in the repository, so a teammate's clone starts the same way, and a review of the plan is a code review. Only a person turns live mode or automatic cleanup on; the guard blocks agents from both. See [`live`](references/commands.md#live).

### Keep things running, or stop them together

- **Resume.** Each instance remembers whether you want it running. After a reboot or a Docker restart, the dashboard brings those instances back, in the order you last used them, without re-seeding. One watcher per machine does this, and `stackfork resume` does it from a terminal. Turn it off with `stackfork config set resume-on-start off`.
- **Stop cascade.** With `plan.stop_cascade` on and a `depends_on` list on host steps, losing a service (a stopped database) also stops the host apps and services that need it. A one-shot job that exited normally does not count as lost.

### Configure one instance without touching the repository

- **Env overrides.** `stackfork env set SERVICE NAME=VALUE` changes one variable of one service of one instance and recreates only that service. The dashboard lists every override and shows the project value next to yours. See [`env`](references/commands.md#env).
- **Secrets.** A project names the secrets it needs. Each person maps them on their machine to a `keychain:` item (macOS Keychain, Linux Secret Service, Windows Credential Manager) or a 1Password `op://` reference, for one repository or for a whole folder such as `~/clients/acme`. A start fails early, with the exact `secret map` command, when the Compose file uses a secret that nothing supplies. No `secret` command prints a value or takes one as an argument. See [`secret`](references/commands.md#secret).

### See everything in the dashboard

`stackfork dashboard --open` starts a local web page on `127.0.0.1`. The macOS app runs the same page.

| View | What it shows |
|---|---|
| **Instances** | Every instance grouped by project: state, health, endpoints, host apps with PID and memory, env overrides, the last run and its failure log. Start, Stop, Restart, Open App, Logs, Environment. |
| **Repos** | Every repository under your scan roots with its branches, worktrees and readiness. Start a ready project, create a worktree, turn live mode on, or learn a project with your agent. |
| **Project** | One repository on one page: its checkouts and their runs, the startup workflow as a canvas (steps, the Container/Hybrid decision, "New data volume?", host apps that start together), memory, CPU and disk charts per checkout, and the project's configuration. |
| **Clean up** | Docker disk use, what stackfork can free, and the prune targets. |
| **Settings** | The folders to scan, the Secrets table and resume on start. The proxy starts and stops from the header. |

<table>
  <tr>
    <td width="50%">
      <picture>
        <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/instances-overview-dark.png">
        <img src="docs/screenshots/instances-overview-light.png" alt="Instances page: acme-api, a hybrid instance whose app runs on the host with its PID, memory, CPU and a hot reload chip, and acme-web with two running checkouts, main and feature-invoices, each with its own URLs, database port and env overrides">
      </picture>
    </td>
    <td width="50%">
      <picture>
        <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/project-workflow-dark.png">
        <img src="docs/screenshots/project-workflow-light.png" alt="Project page: the startup workflow canvas of a hybrid project: secrets, prepare, compose up, the New data volume decision with the seed step, the Mode decision, and the host app that runs on this machine, each step with the state of the last run">
      </picture>
    </td>
  </tr>
  <tr>
    <td>
      <picture>
        <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/project-resources-dark.png">
        <img src="docs/screenshots/project-resources-light.png" alt="Project page: memory and CPU charts per checkout over the last hour, a disk bar per checkout with volumes and a share of the images, and a table of every service with its CPU and memory">
      </picture>
    </td>
    <td>
      <picture>
        <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/environment-dialog-dark.png">
        <img src="docs/screenshots/environment-dialog-light.png" alt="Environment dialog for one service: three overrides with the project value next to each, a reset button per row, a disclosure for the other variables, an add row, and a Save and restart button">
      </picture>
    </td>
  </tr>
</table>

More screenshots are in [docs/screenshots](docs/screenshots). For every command and flag, see [references/commands.md](references/commands.md) or run `stackfork help`.

## Work with coding agents

stackfork is the environment layer for Claude Code, Codex and Copilot CLI. The repository ships an agent skill (`SKILL.md` and `references/`) that teaches the flow: reuse what was learned, prepare, start, verify, stop. Link a checkout of this repository into your agent's skill directory, from the root of your clone:

```sh
mkdir -p ~/.claude/skills && ln -s "$(pwd)" ~/.claude/skills/stackfork   # Claude Code
mkdir -p ~/.codex/skills  && ln -s "$(pwd)" ~/.codex/skills/stackfork    # Codex
```

Then ask the agent in your own words, for example "prepare this project in a new worktree and test the login page". Claude Code selects the skill from the `description` in `SKILL.md`. Codex reads `agents/openai.yaml`, so use this prompt:

```text
Use $stackfork to prepare this checkout with an isolated development environment using stackfork.
```

Install the guard hooks so an agent cannot start a dev server on the host or run a raw `docker compose up` in a managed checkout:

```sh
stackfork hooks install      # add --tool claude|codex|copilot to choose one tool
stackfork hooks status
```

Codex runs only a hook that you trusted. After `hooks install --tool codex`, open `codex` once and approve the hook, or run `/hooks`.

## Safety model

stackfork isolates checkouts from each other. It is not a sandbox. It does not protect you from a malicious Compose file, and it adds no isolation between containers and the host beyond what Docker gives you.

- **Refusals.** stackfork refuses a stack that it cannot isolate safely and names the line to change.
- **Guard hooks.** The hook blocks host dev servers and raw `docker compose up` in a managed checkout. It also blocks agents from the commands that delete data or widen trust: `down --volumes`, `reset`, `prune --yes`, `plan --trust`, `live on`, `secret set|map|unmap|forget` and `env set --allow-secret`. It is a guardrail for cooperative agents, not a security boundary.
- **Trust for host steps.** A command that runs on the host (hybrid mode, host exec steps) runs only after a person runs `stackfork plan --trust` or clicks **Trust** in the dashboard, and only for the exact plan shown. Trust is stored per machine. A changed step needs a new trust.
- **Native confirmation.** The dashboard runs a data-deleting command only after a person clicks Run in a native macOS dialog. An agent can send the request, but it cannot answer the dialog.
- **Automatic actions are limited.** Resume only starts what you started before, never seeds, and skips a checkout that moved to another branch. The stop cascade only stops; it never removes anything.
- **Data kept by default.** `down` removes no volumes. `prune` is a dry run until you pass `--yes`, touches only Docker objects with stackfork labels, and never runs `docker system prune`.
- **Secrets.** Named secrets come from your OS credential store or 1Password. The dashboard and the logs show placeholders. `env set` refuses secret-like names.

Read [SECURITY.md](SECURITY.md) for the full boundary, the known limits and how to report a vulnerability.

## Why not just...

stackfork does not add an environment format. It isolates the Compose file that you already maintain, and it remembers how the project starts.

| Instead of stackfork | What it needs from you | What stackfork does differently |
|---|---|---|
| `docker compose -p BRANCH` by hand | A project name, free ports, a volume prefix and container names per branch, every time, for every agent | Does all of that from the checkout path, adds a stable `.localhost` URL per branch, and also runs the install, migrate and seed steps, the host apps and the cleanup. |
| devcontainers | A `devcontainer.json`, often a copy of your Compose setup | Reads your Compose file as it is. It targets collisions between parallel copies, not one reproducible environment. |
| Dagger and container-use | A pipeline or an agent sandbox that you define | Works at a different layer. It runs your existing stack and does not build or sandbox for you. |
| Conductor-style worktree managers | Nothing for Compose. They create and orchestrate worktrees and agents | Complements them. The manager creates the worktrees. stackfork makes each worktree's stack safe to run, and remembers how. |
| Dokploy, Coolify and other PaaS | A server and a deploy pipeline | Serves production. stackfork serves the laptop: many copies of one project, started the way the project needs, for people and agents. |

## Project files

If stackfork saved you an afternoon of "port already in use", star the repository and tell the next person who runs two agents on one laptop.

- [references/](references/): the command reference and the guides for learning, worktrees, hostnames and adapting Compose.
- [SKILL.md](SKILL.md): the agent skill.
- [SECURITY.md](SECURITY.md): the safety boundary, the known limits and how to report a vulnerability.
- [CHANGELOG.md](CHANGELOG.md): the history of changes.
- [CONTRIBUTING.md](CONTRIBUTING.md): how to build, test and contribute. `go test ./...` runs the offline tests; `STACKFORK_LIVE=1 go test ./...` adds the tests that use Docker.
- [LICENSE](LICENSE): MIT.
