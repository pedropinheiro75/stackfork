# stackfork

**Run the same project twice — for two AI agents, or an agent and you — without them stepping on each other.**

The moment you (or a coding agent) open a second Git worktree of a project and run `docker compose up`, it collides with the first: same ports, same container names, same volumes, sometimes the same database. Today the fix is manual — hand-edit `.env`, guess free ports, remember which worktree owns which container — and it breaks the moment a teammate's agent does the same thing next to yours. stackfork takes the Compose stack you already have and isolates it per checkout-and-branch: its own ports, volumes, network, container names — so a human and an agent, or five agents, can each have a fully running copy of the same project at the same time, on the same machine, without coordinating.

<!-- TODO: 20s demo GIF (openspec/changes/prepare-open-source-release task 12.2) -->

## Install

```sh
go install github.com/pedropinheiro75/stackfork/cmd/stackfork@latest
```

Make sure `stackfork` resolves on `PATH` (typically `$(go env GOPATH)/bin`, often `~/go/bin`).

From a local checkout instead:

```sh
go install ./cmd/stackfork
```

## Quick start

Five commands, on a project you already have checked out with a Compose file:

```sh
git worktree add ../myproject-feature feature-branch   # a second copy of the same repo
cd ../myproject-feature
stackfork prepare                                       # isolate this checkout+branch's copy of the stack
stackfork up                                             # start it
stackfork urls                                           # e.g. myproject-feature-a1b2c3.localhost:54211
```

Run `stackfork list` from either checkout to see both instances — and your original checkout's stack, still running untouched on its own ports. No `.env` surgery, no port bookkeeping, no `docker compose down` in the other window first.

`prepare` reuses the same source-file selection on later runs. It renders your project's actual `docker-compose.yml` (or whichever files you pass with `-f`) into an isolated snapshot: fresh container/volume/network names keyed on checkout path + branch, ports reassigned off Docker's collision, secrets never printed or stored. Nothing about your Compose file itself changes.

## How it's different

stackfork isn't a new environment format and it doesn't run your project *for* you — it isolates the Compose stack you already maintain, as-is:

- **vs. devcontainers** — a devcontainer describes a dev environment from scratch, usually duplicating what your Compose file already says. stackfork reads that Compose file directly; there's nothing new to write or keep in sync.
- **vs. Dagger / container-use** — those are programmable container pipelines, closer to CI/build tooling than "run my existing dev stack a second time safely." stackfork does one narrower thing: collision isolation for a Compose-based dev stack.
- **vs. Conductor-style agent/worktree managers** — those orchestrate the worktrees and the agents; they generally assume you've already solved (or ignored) the "two Docker stacks colliding" problem. stackfork is the piece that solves that specific problem, and composes fine alongside them.

## Dashboard

```sh
stackfork dashboard --open
```

A local, read-mostly web page listing every instance across every checkout it knows about: status, services, health, clickable app/DB/mail endpoints (labelled by role, not just raw ports), learned-knowledge status, plain-language warnings. Local-only (binds `127.0.0.1`, checks the `Host` header, a per-process token guards every action) — the only actions it can take are `up`, `down` (volumes always kept), per-service `restart`/`start`/`stop`, viewing logs, and the shared proxy's up/down/reload. Destructive or input-heavy operations (`down --volumes`, `prepare --reassign-ports`, `learn`) show up as an exact command to copy, never a button. `stackfork list` / `list --json` show the same data from a terminal.

## Safety model

stackfork's job is **collision isolation between checkouts** — making sure parallel worktrees/branches of the same project don't collide on ports, volumes, networks, container names, databases or cookies, and refusing what it can't isolate safely (fixed container names, external networks, the Docker socket, writable binds outside the checkout, fixed subnets) rather than working around it silently.

It is **not** a sandbox. It doesn't protect against a malicious or untrusted Compose file, doesn't isolate containers from the host beyond what Docker itself provides, and assumes the project you're isolating is one you already trust enough to run its Compose stack directly. See [SECURITY.md](SECURITY.md) for the full boundary, including the one endpoint (`/api/copy`, dashboard) that ever returns a real credential and exactly when.

## Agent skill install

The skill this repo ships (`SKILL.md` plus `references/`) teaches an agent the same discover → prepare → up → verify → down flow this README uses, plus how to adapt a project's real Compose stack instead of forcing a template on it. Point your agent's skill directory at this checkout:

```sh
# Claude Code
mkdir -p ~/.claude/skills && ln -s "$(pwd)" ~/.claude/skills/isolated-dev

# Codex
mkdir -p ~/.codex/skills && ln -s "$(pwd)" ~/.codex/skills/isolated-dev
```

Project- or company-specific recipes (a client's known gotchas, a shared registry token source, a house Nuxt template) stay out of this repo entirely — they live in your own private profile directory (`~/.config/stackfork/profiles/*`, or `$STACKFORK_PROFILE_DIRS`), which `stackfork where` reports back to the agent when one applies.

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
