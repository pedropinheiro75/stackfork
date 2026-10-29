# Launch plan

How stackfork gets its first thousand users. Internal, not shipped with the skill.

## Positioning

One sentence: **stackfork gives every branch, and every AI agent, its own running copy of your Compose project, from the Compose file you already have.**

The pain in the user's words: "I run Claude Code on a branch and keep working on another, and `docker compose up` breaks one of us."

Three taglines to test (headline of the repo, the Show HN title, the first line of the tweet):

1. Every branch gets its own running stack. Every AI agent gets its own.
2. Run five branches of one project on one laptop. Nothing collides.
3. GitOps for your laptop: a branch is an environment, a closed PR removes it.

Who it is for, in order: people who run coding agents in parallel (Claude Code, Codex, Copilot CLI, Conductor users); teams with a `docker-compose.yml` and a seed script; platform engineers who tried devcontainers for this and gave up.

Proof points to repeat everywhere: zero config format, a single binary with no dependencies, the plan is a committed file, the guard keeps agents from deleting data, the demo GIF is real.

## Prerequisites before the repository goes public

1. Public repository with the rewritten README, the demo GIF and the dashboard GIF.
2. Releases: tagged `v0.2.0`, binaries for macOS and Linux (amd64 and arm64) built by CI, the DMG attached, SHA256 files.
3. Install in one line: `brew install --cask pedropinheiro75/tap/stackfork` (publish the tap) and `go install ...@latest`.
4. A notarized macOS app, or a clear "ad hoc signed" note with the `xattr` command (already in the README).
5. A 60 second video for X and YouTube: the problem (two terminals collide), `learn`, `start-env` in two worktrees, the dashboard, a PR closes and the environment disappears.
6. An issue template and a `good first issue` label with five real issues.
7. A `docs/` site is not needed. The README and `references/` are enough for launch.

## Channels, in order

Week 1: Show HN and X on the same morning (Tuesday to Thursday, 9:00 US Eastern).

- **Show HN** title: "Show HN: Stackfork – one running stack per branch (and per AI agent), from your Compose file". The first comment is the author's: what it is, what it is not (not a sandbox), the demo, and a question for the thread ("how do you keep two agents from sharing a database today?").
- **X thread**: one line per feature with a screenshot each, the GIF first, the install line last. Tag the agent ecosystems (Claude Code, Codex, Conductor) without asking for a retweet.
- **Reddit**: r/ClaudeAI, r/docker, r/devops, r/selfhosted. One honest post each, written for that community, never cross-posted verbatim.

Week 2: lists and integrations.

- Pull requests to the awesome lists: awesome-claude-code, awesome-docker, awesome-compose, awesome-devtools.
- A skill entry in the Claude Code and Codex skill catalogs; the `SKILL.md` already exists.
- A post in the Conductor and container-use communities: stackfork is the environment layer under their worktrees.

Week 3 and after: content that answers a search.

- "Running two Claude Code agents on one Compose project" (the problem post).
- "Hybrid mode: Postgres in Docker, the app on the host, per branch".
- "A branch is an environment: GitOps on a laptop".
- One short video per feature, 20 to 40 seconds, from the dashboard GIF material.

## What to measure

Stars and clones per day; `stackfork version` has no telemetry and gets none. GitHub release downloads by asset. Issues opened by people who are not the author. Mentions on X and HN. The only goal for month one: 50 people who say it fixed their collision problem.

## Rules

- No fake social proof, no "used by" logos before they are true.
- Never show a client project name, path or hostname in any image or video.
- Every claim in a post must be true in the released version on that day.
