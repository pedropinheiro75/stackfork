<div align="center">

<img src="docs/logo.svg" width="120" height="120" alt="stackfork logo">

# stackfork

### Your branches. Your laptop. All running at once.

Run every app you build with AI, in as many versions as you like, all at once on your own computer, with none of them getting in each other's way.

**English** · [Português](README.pt-BR.md) · [Español](README.es.md)

[![CI](https://github.com/pedropinheiro75/stackfork/actions/workflows/ci.yml/badge.svg)](https://github.com/pedropinheiro75/stackfork/actions/workflows/ci.yml)
![Works with Claude Code · Codex · Copilot CLI](https://img.shields.io/badge/works%20with-Claude%20Code%20%C2%B7%20Codex%20%C2%B7%20Copilot%20CLI-f59e0b)
![Single binary, stdlib only](https://img.shields.io/badge/single%20binary-stdlib%20only-059669)
![macOS · Linux · Windows](https://img.shields.io/badge/platforms-macOS%20%C2%B7%20Linux%20%C2%B7%20Windows-6366f1)
[![License: MIT](https://img.shields.io/badge/license-MIT-db2777)](LICENSE)

[What it looks like](#what-it-looks-like) · [Install](#install) · [How you use it](#how-you-use-it) · [How it works](#how-it-works) · [Agents](#work-with-coding-agents) · [Safety](#safety) · [FAQ](#faq) · [Roadmap](#roadmap) · [Docs](#docs)

</div>

<img src="docs/dashboard.gif" alt="Dashboard demo: the Instances page with two container instances and one hybrid instance whose app runs on the host, the Project page with the startup workflow canvas and resource charts, the environment overrides dialog, and the Repos page">

**The problem.** You hand an AI agent a branch and keep working on another. Both run `docker compose up`. Same ports, same container names, same volumes, often the same database. One of you breaks the other, and the agent cannot tell.

**The fix.** stackfork learns how your project starts, once, and records the recipe in the repository: what runs in Docker, what runs on the host, what to install, migrate and seed, and which URL to open. From then on every worktree starts with one command, on its own ports, volumes, network and `.localhost` URL. Five agents and you can work on five branches of one project on one laptop, and nothing collides.

## What it looks like

<a href="docs/screenshots/instances-overview-dark.png">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/instances-overview-dark.png">
  <img src="docs/screenshots/instances-overview-light.png" alt="Instances page: acme-api, a hybrid instance whose app runs on the host with its PID, memory, CPU and a hot reload chip, and acme-web with two running checkouts, main and feature-invoices, each with its own URLs, database port and env overrides">
</picture>
</a>

**Overview.** Two projects, three running copies. `acme-api` runs its app on the host with hot reload and its database in Docker. `acme-web` runs `main` and `feature-invoices` side by side, each with its own URL, database port and env overrides.

<table>
<tr>
<td width="50%">
<a href="docs/screenshots/instances-hybrid-host-app-dark.png">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/instances-hybrid-host-app-dark.png">
  <img src="docs/screenshots/instances-hybrid-host-app-light.png" alt="Instance card of acme-api: the db service in Docker with its database port, connection URL, psql and exec shortcuts, and the app service running on the host with its PID, memory, CPU and a hot reload chip">
</picture>
</a>
</td>
<td width="50%">
<a href="docs/screenshots/project-resources-dark.png">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/project-resources-dark.png">
  <img src="docs/screenshots/project-resources-light.png" alt="Resources section of acme-web: memory and CPU charts for the main and feature-invoices checkouts, a disk bar per checkout and a table of every service">
</picture>
</a>
</td>
</tr>
<tr>
<td><b>Hybrid instance:</b> the database runs in Docker and the app runs on the host with hot reload, shown with its PID, memory and CPU.</td>
<td><b>Resources:</b> memory, CPU and disk for each checkout, so you see what every copy costs.</td>
</tr>
<tr>
<td width="50%">
<a href="docs/screenshots/project-workflow-dark.png">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/project-workflow-dark.png">
  <img src="docs/screenshots/project-workflow-light.png" alt="Project page: the startup workflow canvas of a hybrid project: secrets, prepare, compose up, the New data volume decision with the seed step, the Mode decision, and the host app that runs on this machine, each step with the state of the last run">
</picture>
</a>
</td>
<td width="50%">
<a href="docs/screenshots/environment-dialog-dark.png">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/environment-dialog-dark.png">
  <img src="docs/screenshots/environment-dialog-light.png" alt="Environment dialog for the app service on the feature-invoices worktree: three variable overrides with their project value and your value, a field to add another, and a Save and restart app button">
</picture>
</a>
</td>
</tr>
<tr>
<td><b>Startup workflow:</b> the recorded steps of a project, each with the result of its last run.</td>
<td><b>Environment:</b> change a variable for one worktree only, without touching the repository.</td>
</tr>
</table>

## Why developers keep it

| | |
|---|---|
| **Nothing new to maintain** | It reads the Compose file you already maintain and never edits it. If something cannot be isolated safely, it names the line. |
| **Hot reload and a debugger, still isolated** | Keep Postgres and Redis in containers and run the app on the host for hot reload and a debugger. Each branch still gets its own copy. |
| **Environments that follow your pull requests** | `worktree add` + `start-env`, or turn on live mode and a new branch starts by itself. Merge or close the pull request, and the environment goes away. GitOps, on your laptop. |
| **Set up once, for the whole team** | The startup plan is a committed file. Teammates and agents start the project the same way, every time. |
| **Agents run free, people hold the keys** | Agents start, test and stop environments on their own. Deleting data, trusting host commands and turning automation on wait for a person. |
| **Still running after a restart** | Host apps are health-checked and restarted. After a reboot, what was running comes back, and what lost its database stops instead of running against nothing. |
| **Every copy in one place** | A dashboard and a macOS menu bar app show every running copy, its URLs, memory and CPU, and why a start failed. |

## Install

You need Docker with the Compose plugin running. stackfork supports amd64 and arm64.

**macOS and Linux, with Homebrew**

```sh
brew trust pedropinheiro75/tap
brew install --cask pedropinheiro75/tap/stackfork
```

**macOS and Linux, with the install script**

```sh
curl -fsSL https://raw.githubusercontent.com/pedropinheiro75/stackfork/main/install.sh | sh
```

**Windows, in PowerShell**

```powershell
irm https://raw.githubusercontent.com/pedropinheiro75/stackfork/main/install.ps1 | iex
```

It needs Docker Desktop with the WSL 2 backend. Windows support is new; read [the Windows limits](SECURITY.md#windows).

<details>
<summary>Other ways to install</summary>

Download a binary from the [Releases page](https://github.com/pedropinheiro75/stackfork/releases).

Install with Go 1.26.10 or later:

```sh
go install github.com/pedropinheiro75/stackfork/cmd/stackfork@latest
```

Build from a clone, then put the binary on your `PATH`:

```sh
go build -o stackfork ./cmd/stackfork
```

</details>

Check the install:

```sh
stackfork version
```

The macOS menu bar app is the `.dmg` on the Releases page. See [docs/macos-app.md](docs/macos-app.md).

## How you use it

1. **Point stackfork at your code folder.** Run `stackfork dashboard --open`. In **Settings**, add the folder that holds all your projects, for example `~/code`.

<a href="docs/screenshots/settings-folders-dark.png">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/settings-folders-dark.png">
  <img src="docs/screenshots/settings-folders-light.png" alt="Settings page: the list of folders that stackfork scans for repositories, with a field to add another folder">
</picture>
</a>

2. **Click "Set up" and pick your agent.** Your agent reads the project and saves how it starts. This happens once, for you, your team and every agent. You can also ask your agent: "learn this project with stackfork". More in [learning.md](references/learning.md).

<a href="docs/screenshots/learn-with-agent-dark.png">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/learn-with-agent-dark.png">
  <img src="docs/screenshots/learn-with-agent-light.png" alt="Repos page: acme-docs is not set up yet; its Set up button opens a dialog where you pick Claude Code, Codex or Copilot CLI and see what the agent will check">
</picture>
</a>

3. **Ask your agent for an environment.** For example: "create a stackfork environment for the invoices feature". You get a full copy of the app for that branch. You can also press **Start** on the Repos page.

<a href="docs/screenshots/repos-dark.png">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/repos-dark.png">
  <img src="docs/screenshots/repos-light.png" alt="Repos page: the repositories found in the scanned folder, each with its status, worktrees and a Start button; acme-web is expanded and shows the Live switch, the learned startup steps (prepare, compose up, seed) and its two branches, main and feature-invoices, both running">
</picture>
</a>

4. **Open it.** Each environment has its own address. Open it in the browser and compare it side by side with `main`.

<a href="docs/screenshots/instances-two-checkouts-dark.png">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/instances-two-checkouts-dark.png">
  <img src="docs/screenshots/instances-two-checkouts-light.png" alt="Instances page: acme-web with two running checkouts, main and feature-invoices, each with its own address and database port">
</picture>
</a>

<details>
<summary>Prefer the terminal?</summary>

Every action also exists as a command. See the full walkthrough in [examples/node-postgres](examples/node-postgres/README.md) and every command in [references/commands.md](references/commands.md).

<img src="docs/demo.gif" alt="Terminal demo: stackfork start-env starts a project, a second worktree starts the same project, curl shows the two copies counting visits in separate databases, stackfork list shows both running, and down --volumes removes one">

</details>

## How it works

- **One recipe per project.** What your agent learned is a file in the repository. It says how the project starts, so nobody has to remember the steps.
- **One copy per branch.** stackfork starts the same project again for each branch, with different ports, a separate database and a separate network. It reads your Docker Compose file and never edits it.
- **One address per copy.** A small local proxy gives each copy a name ending in `.localhost`, so you open a readable URL and not a port number.
- **Git decides what runs.** A branch gets an environment, and a merged or closed pull request takes it away. stackfork watches every process it started and brings it back after a restart.
- **People keep the keys.** Agents can start, test and stop environments. Deleting data and trusting new commands on your machine wait for a person.

## What you get

- **Follows your pull requests.** Turn on live mode for a repository: a new branch starts its environment by itself, each push updates it, and when the pull request is merged or closed the environment is removed. Only a person can turn this on. [live](references/commands.md#live)
- **Docker, or your machine.** Keep the database in Docker and run the app on your machine for hot reload and a debugger. [host](references/commands.md#host)
- **Comes back after a restart.** After a reboot or a Docker restart, what was running starts again, without wiping or re-filling the database.
- **One setting for one branch.** Change an environment variable for a single environment without touching the repository. [env](references/commands.md#env)
- **Secrets stay in your keychain.** Passwords and tokens come from the OS keychain or 1Password. stackfork never prints them. [secret](references/commands.md#secret)
- **Explains failures.** Every start is recorded step by step with its logs, so you and your agent can see what broke. [worktrees.md](references/worktrees.md)
- **Says no when it is not safe.** If a project cannot be isolated safely, stackfork refuses and tells you which line to change. [adapting-compose.md](references/adapting-compose.md)

More screenshots in [docs/screenshots](docs/screenshots). Every command and flag: [references/commands.md](references/commands.md) or `stackfork help`.

## Work with coding agents

The repository ships an agent skill (`SKILL.md` and `references/`) that teaches the flow: reuse what was learned, prepare, start, verify, stop. Link a clone into your agent's skill directory:

```sh
mkdir -p ~/.claude/skills && ln -s "$(pwd)" ~/.claude/skills/stackfork   # Claude Code
mkdir -p ~/.codex/skills  && ln -s "$(pwd)" ~/.codex/skills/stackfork    # Codex
```

Then ask the agent in your own words: "prepare this project in a new worktree and test the login page". Codex needs the skill named: `Use $stackfork to prepare this checkout with an isolated development environment.`

Install the guard hooks so an agent cannot start a dev server on the host, run a raw `docker compose up` in a managed checkout, or delete data:

```sh
stackfork hooks install      # --tool claude|codex|copilot to choose one
stackfork hooks status
```

## Safety

stackfork isolates checkouts from each other. It is not a sandbox: it does not protect you from a malicious Compose file, and it adds nothing to the isolation Docker gives between containers and the host.

- The guard hooks block agents from `down --volumes`, `reset`, `prune --yes`, `plan --trust`, `live on` and the `secret` commands. It is a guardrail for cooperative agents, not a security boundary.
- Host commands run only after a person trusts the exact plan, per machine. The dashboard runs a data-deleting command only after a person confirms in a native dialog.
- `down` keeps volumes. `prune` is a dry run until `--yes`, touches only stackfork-labelled objects, and never runs `docker system prune`.
- Secret values come from your credential store, show as placeholders in the dashboard and logs, and are refused by `env set`.

### What is in the download

stackfork has no third-party code. All of it is in this repository.

| Part | Built from | Third-party code |
|---|---|---|
| CLI and dashboard server | Go standard library | None. `go.mod` lists no dependency |
| Dashboard page | HTML, CSS and JavaScript from this repository, built into the binary | None. It loads nothing from a CDN |
| macOS app | Swift with Apple frameworks | None. `Package.swift` lists no package |
| Bundled files | Geist Mono font (SIL OFL 1.1) and Simple Icons logos (CC0) | Static files only |

- **No telemetry and no update check.** The binary connects only to the loopback address of your own machine. Network traffic comes from the tools it runs for you: `git` talks to your remote and Docker pulls images.
- **What it runs.** `git` and `docker`, which you installed. For secrets, the credential store of your system (`security` on macOS, `secret-tool` on Linux, Credential Manager on Windows), and `op` only when you map a 1Password reference.
- **Images it pulls.** `caddy:2-alpine` for the local proxy and `alpine:3.20` for two checks. Both are official images from Docker Hub. Every other image belongs to your project.
- **How a release is built.** GitHub Actions builds each release from a signed tag. Every action is pinned to a commit, and Dependabot watches them. Each release has a `checksums.txt`, and the install scripts stop when the SHA-256 does not match.

Known limits: Apple has not notarized the macOS binaries yet ([#10](https://github.com/pedropinheiro75/stackfork/issues/10)), so the Homebrew cask clears the quarantine flag. A release has checksums but no signature or provenance attestation. The two image tags are not pinned by digest.

If you prefer not to trust a download, build it from the source:

```sh
go install github.com/pedropinheiro75/stackfork/cmd/stackfork@latest
```

To see what a binary was built from:

```sh
go version -m "$(command -v stackfork)"
```

[SECURITY.md](SECURITY.md) has the full boundary, the known limits and how to report a vulnerability.

## Why not just...

| Instead of stackfork | What stackfork does differently |
|---|---|
| `docker compose -p BRANCH` by hand | Derives the project name, ports, volumes and container names from the checkout, adds a `.localhost` URL, and also runs install, migrate, seed, host apps and cleanup. |
| devcontainers | Reads your Compose file as it is. It targets collisions between parallel copies, not one reproducible environment. |
| Dagger, container-use | A different layer: runs your existing stack, does not build or sandbox for you. |
| Worktree managers (Conductor and the like) | Complements them. They create the worktrees; stackfork makes each one's stack safe to run, and remembers how. |
| Dokploy, Coolify | They serve production. stackfork serves the laptop: many copies of one project, for people and agents. |

## FAQ

**Do I need to know Docker?**
You need Docker running and a project that already starts with Docker Compose. Your agent learns the rest once, and after that a new environment is one request or one click.

**Does it change my project?**
It adds one folder, `.stackfork/`, with the recipe. It never edits your Compose file. If a project cannot be isolated safely, stackfork refuses and names the line to change.

**Which AI agents does it work with?**
Claude Code and Codex have a ready skill. Claude Code, Codex and Copilot CLI have guard hooks. Any agent that can run a terminal command can use the CLI.

**How many copies can I run at once?**
As many as your computer's memory allows. Each copy is a full set of containers, so a heavy app costs more per branch. The dashboard shows memory and CPU per copy.

**Is it a sandbox?**
No. stackfork keeps your copies apart from each other, so two branches never share a port or a database. It is not a security boundary against untrusted code. Read the [safety model](#safety) and [SECURITY.md](SECURITY.md).

**Does anything leave my machine?**
There is no telemetry and no account. The dashboard listens only on `127.0.0.1`. stackfork reaches the network only through your own `git` and `gh` when live mode checks a branch or a pull request.

## Roadmap

What is planned next. Ideas and votes are welcome in [Discussions](https://github.com/pedropinheiro75/stackfork/discussions).

- **Share an environment over the internet**: give someone a link to one running branch on your laptop, behind an authentication layer so it is never open to everyone.
- **Traefik as the proxy**: replace the built-in proxy.
- **Load testing**: point a load-testing tool at any environment.
- **Anomaly alerts**: a warning when an app's CPU or memory behaves abnormally.
- **More stacks out of the box**: ready recipes for more languages and frameworks, in Docker with hot reload. JavaScript and TypeScript come first.
- **A database per branch, for real**: copy the data of `main` into a new environment, take a snapshot before a migration, and restore it with one click.
- **MCP server**: any agent finds start, logs, URLs and status as tools, with no skill to install.
- **Sleep what is idle**: stop an environment nobody opened for a while, and wake it on the next request.
- **Notifications**: a message on your system or in Slack when an environment is ready, fails or is removed.
- **Local HTTPS**: a trusted certificate for `*.localhost`, to test secure cookies, OAuth and service workers.
- **Open a teammate's pull request**: paste the pull request URL and get its environment running, ready to review.
- **Remote machine**: run the environments on a development server over SSH and open the URLs on your laptop.
- **Other container runtimes**: tested and documented support for OrbStack, Colima and Podman.
- **Pick repositories by hand**: add a single repository, or hide the ones you do not use.

## Docs

[references/](references/) · [SKILL.md](SKILL.md) · [SECURITY.md](SECURITY.md) · [CHANGELOG.md](CHANGELOG.md) · [CONTRIBUTING.md](CONTRIBUTING.md) · [LICENSE](LICENSE) (MIT)

**Give every branch, and every agent, its own place to run.** If stackfork saved you an afternoon of "port already in use", star the repository and tell the next person who runs two agents on one laptop.
