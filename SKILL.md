---
name: isolated-dev
description: Use when preparing, starting, testing, or cleaning up a local development environment in a Git worktree or branch with the stackfork CLI, especially when Docker Compose ports, databases, volumes, or app configuration could collide with another checkout.
---

# Isolated development (stackfork)

Use the project's actual runtime, package manager and startup flow through `stackfork`, a framework-independent Compose isolation helper (optional Nuxt/Bun template included). Adapt existing stacks first; do not force a non-Nuxt or dependency-only project into the Nuxt template.

## Reuse before rediscovering

Run `stackfork where` (lists prepared instances and configured profiles) and `stackfork status` first. If `.stackfork/knowledge.md` is `verified`, apply its recorded adapter and recipe directly — no scan, no questions (`references/learning.md`). Otherwise run `stackfork learn`, read `.stackfork/knowledge.md`, and run the learn session in `references/learning.md`. Do not repeat a full discovery, or rewrite a launcher, for a routine restart of an unchanged adapter.

## Follow profile context

For each profile `stackfork where` lists with `"context": true`, follow that profile's `context.md` first, before anything below — it may point at a private reference and template for this project instead of the generic one. Profiles are private and never part of this repository.

## Prepare, start, verify, stop

1. **Discover:** resolve the checkout with Git, read AGENTS.md/README, and adapt the project's real Compose stack (`references/adapting-compose.md`, or `references/nuxt-template.md` for a bare Nuxt app). Use a separate worktree per concurrent agent (`references/worktrees.md`).
2. **Prepare:** `stackfork --root "$CHECKOUT" prepare -f <compose files> --env-file <runtime env>`
3. **Start:** `stackfork --root "$CHECKOUT" up`
4. **URLs:** `stackfork --root "$CHECKOUT" urls` — report the named `<instance>.localhost` URL it prints, never a bare `localhost`/`127.0.0.1` (`references/hostnames.md`).
5. **Verify:** database identity and app readiness at minimum; login, HMR and a representative flow on first adoption.
6. **Stop:** `stackfork --root "$CHECKOUT" down` (keeps data).

## Refusals and deletion

Trust `stackfork prepare`'s refusals (fixed container names, external networks, Docker sockets, writable binds outside the checkout, fixed subnets) — never work around one. `down --volumes` deletes this instance's data and needs the user's own authorization for that specific deletion; never infer it from a request to restart. Never open or copy another project's real env files, registry credentials, certificates or private keys.

## Report

Report checkout, branch, Compose project, app/DB/auxiliary endpoints, and the checks actually run, without any credential values. Distinguish a validated Compose model and running dependencies from a working application — do not describe a dependency-only check as full application validation.
