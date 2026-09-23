---
name: isolated-dev
description: Use when preparing, starting, testing, or cleaning up a ClientCo local development environment in a Git worktree or branch, especially when Docker Compose ports, databases, volumes, or app configuration could collide with another checkout.
---

# ClientCo isolated development

Use the project's actual startup flow. APP-A is the reference for a full Nuxt development stack; it is not a universal replacement for every Compose file. This skill includes a deterministic Compose helper and a APP-A-derived Nuxt starting point. It does not migrate all repositories automatically.

## Reuse before setup

For an existing `.agent-dev/` setup, use `where` and `status` first. Reuse its source adapter, credentials reference and prepared state. Run `prepare` only when configuration changes, then `up` and `urls`. Do not repeat the portfolio survey, create another check worktree, or rewrite a launcher for every restart. Do the discovery below for a new project adapter or changed startup requirements.

## Discover and choose

1. Resolve the repository and checkout with Git, read its AGENTS.md and brain note under `~/code/notes/Agency/ClientCo/`, then inspect the startup/configuration files cited there. Search memory-plugin for that project and fetch relevant observations if available. Report non-ok/stale briefs. Do not open another project's real env files, registry credentials, certificates or private keys.
2. Read this project's row in [the inventory and adaptation guide](references/clientco.md). Inspect current Compose, entrypoint, package scripts, runtime configuration, migrations, seed/reset and test launchers. Preserve required dependencies, DB major version, runtime and package manager. Trace host-facing URLs separately from container-facing URLs.
3. For concurrent agents, use separate worktrees. Derive identity from the full checkout path AND branch, never basename alone. Stop the old stack **before** switching branches in the same folder: running bind mounts immediately see changed files. A branch-specific Docker name cannot isolate shared source files.
4. When the requested app includes uncommitted work, carry the requested tracked changes and relevant untracked source files into the new worktree. Preserve binaries, workspace packages and patches; exclude ignored credentials, dependencies and build output. Verify the original checkout is unchanged. A worktree created from HEAD alone does not contain that work.
5. Keep generated development files in the checkout's `.agent-dev/`, excluded using Git's local exclude file; do not overwrite tracked Compose files or the developer's `.env`. New worktrees do not contain ignored files. Start configuration from examples and fresh local values; obtain registry access through the approved credential source for this project. Never copy another project's credentials.

## Prepare a runnable source configuration

For an existing full stack, make a checkout-local adapter. For a Nuxt app missing an app container or Compose entirely, adapt [the Nuxt starting point](assets/nuxt.compose.yaml), copying its Dockerfile, entrypoint.sh, nginx.conf and registry.npmrc into `.agent-dev/`. Follow the project-specific steps in the guide. For a host-run/non-Nuxt app, keep its native commands and isolate its dependencies; containerization requires the correct frontend/backend build and environment wiring.

Provision one private `.agent-dev/runtime.env` explicitly (mode 0600). Any Compose `env_file` must point to this file; replace inherited lists using `!override`, not normal list merging. Do not render the developer's real `.env` just to discover keys. The helper disables implicit Compose env loading and never prints rendered configuration. The snapshot may still contain values explicitly supplied to it, so keep it private.

Before starting, inspect the source adapter and executable startup code for:

- Database/Redis/mail/PDF/storage endpoints belonging to this instance. Containers use service DNS and internal ports; host processes/browser URLs use the actual published ports. Mask a source-mounted `.env` with the generated runtime file so Bun/Nuxt/Drizzle cannot silently load the developer's DB URL. Handle `.env.local`, `.env.development`, `.env.test` and package/playground env files too if those are loaded by this app.
- Hardcoded container-name references, external networks, absolute/shared writable directories, custom storage drivers, build tags and mounted Docker sockets. Replace shared persistence with fresh project-scoped named volumes; do not mount/copy existing database files into a fresh server.
- Destructive seeds/resets and scripts that invoke their own `docker compose` or `docker exec` with hardcoded names. Route these through the helper or adapt the launcher. Do not use APP-A `make reset`, global prune, or scripts targeting the main checkout's shared DB.
- Dev/test server ports, browser base URLs, HMR, PDF callbacks, CORS, cookies and auth. Ports do not isolate cookies; use the helper's per-instance hostname and host-only cookies, plus a distinct browser context when needed. Clear inherited real SMTP/cloud endpoints; send test email to the local catcher.

## Browser hostname convention

Every browser-facing app must use `<instance>.localhost:<published-port>`, including host-run/non-Nuxt apps. Do not report bare `localhost` or `127.0.0.1` as the app URL. Run `where` before adapting the app to obtain the deterministic `hostname`: the checkout name plus its path/branch hash. For a shorter project/issue name, select `prepare --hostname eup-274.localhost` (or `app-b-571.localhost`) once; it is remembered. Explicit names must be unique across concurrent instances; default names already incorporate checkout/branch identity.

`prepare` supplies `RR_DEV_HOSTNAME` for Compose interpolation; the Nuxt template also exposes it in the app environment. This variable is a hostname, not a URL or server bind address. Keep servers bound to `0.0.0.0` and container-to-container URLs on service DNS. Adapt the project's actual browser API origin, CORS/allowed hosts, auth callbacks and HMR settings to this hostname and the published ports. For host-run apps use the same hostname returned by `where` and their actual listening ports. Do not assume setting `RR_DEV_HOSTNAME` automatically configures a framework. Keep cookies host-only, never scoped to `.localhost`.

After startup, obtain Docker endpoints from `urls` and verify login/HMR using the named URL in a browser. Do not silently fall back to a bare loopback URL if DNS, host validation or callbacks fail; fix the adapter or report the blocker. Existing adapters need these settings checked before adopting the named URL; changing helper output alone does not migrate application configuration. Do not restart another agent's active stack just to adopt this convention.

## Helper

Requires macOS/Linux, Python 3.9+ and Docker Compose with `config --no-env-resolution` (tested on v5.1.2). Use the installed skill directory for `SKILL`:

```sh
SKILL="$HOME/.codex/skills/isolated-dev"
python3 "$SKILL/scripts/compose.py" --root "$CHECKOUT" prepare \
  -f "$CHECKOUT/docker-compose.yml" \
  -f "$CHECKOUT/.agent-dev/adapter.yaml" \
  --env-file "$CHECKOUT/.agent-dev/runtime.env"
python3 "$SKILL/scripts/compose.py" --root "$CHECKOUT" up --service db
python3 "$SKILL/scripts/compose.py" --root "$CHECKOUT" status
```

After the first explicit selection, `prepare` remembers the ordered source files and env path. For a new standard template setup it defaults to `.agent-dev/nuxt.compose.yaml` and `.agent-dev/runtime.env`; it never defaults to the developer's `.env`.

For registry access, an existing `NPM_TOKEN` takes precedence. Otherwise explicitly select the authorized registry configuration for this same project once:

```sh
python3 "$SKILL/scripts/compose.py" --root "$CHECKOUT" --npmrc "$PROJECT_NPMRC" prepare
```

The helper remembers the file path and reads the GitHub token for `up`/`run`, supplying it to the Compose secret through the process environment. It supports a literal token or a single `${VARIABLE}` reference. It does not print or store that token in the manifest. Never select another project's credentials. No checkout-specific `manage.py` is needed. Prefer the template's secret mechanism over legacy token build arguments.

Select actual source filenames/services; APP-C/APP-B use `postgres`, not `db`. A standalone generated Nuxt stack needs only its one `-f` file. `prepare` creates a private canonical snapshot under `~/.local/state/isolated-dev/`; it does not start Docker. It replaces host bindings with Docker-allocated loopback ports, namespaces volumes/networks/build images, removes fixed container names, and rejects unsafe shared resource configurations. This is an isolation check, not a sandbox for arbitrary application code or external service URLs.

Use `where` to get the identity/state path and other prepared branch instances for this checkout. `status` returns machine-readable state/ports; `urls` prints browser and dependency endpoints without credentials. HTTP schemes are inferred from common target ports; custom ports/protocols still need project-specific interpretation. `up --build` starts all non-profile services; use `--profile NAME` explicitly when needed. `run SERVICE COMMAND...` runs a one-off container with no dependencies; start required dependencies first. `exec SERVICE COMMAND...` executes in an existing container. These commands always target the snapshot. Re-run `prepare` after changing source Compose or the selected env file; never edit the generated snapshot directly.

Run migrations explicitly after database readiness, using this project's real script. Seed a **new, verified instance DB once** when the task requires initial data. Ordinary app start/restart must not seed/reset. The Nuxt entrypoint installs dependencies and runs the requested command; it does not modify host env/registry files or migrate/seed implicitly. No persistent marker may suppress future migrations.

For cleanup, use `down` (retains data). If someone already switched branches, `where` lists the old identity and `down --instance NAME` can stop that old stack from the same checkout. `up`/`run` refuse while another managed branch stack is running from that folder; this cannot undo source changes an already-running bind mount has seen. `down --volumes` deletes this instance's data and needs the user's existing authorization for that deletion. Never infer deletion authorization from a request to restart. Stop before removing its worktree.

## Verify and report

Run `python3 "$SKILL/scripts/test_compose.py"` after changing the helper. `--live` additionally tests six disposable Postgres instances using the current APP-A/APP-C/APP-B Compose definitions and removes only its scratch resources. `python3 "$SKILL/scripts/test_template.py"` builds and runs two template stacks with a real-Node streamed-download fixture, checks proxy routing/env isolation/teardown, and cleans its containers, volumes and image tags; it requires public image/package downloads. It does not test the private ClientCo apps.

For first adoption or changes to isolation wiring, verify DB identity, migrations, login, HMR and a representative app-specific flow (for EUP, download the spreadsheet through nginx and validate the response). Also verify two independent instances and scoped teardown. For routine restarts of an unchanged adapter, check its expected DB identity and app readiness; reuse the recorded isolation checks. Do not repeat scratch-stack tests for every application task. Check helper snapshots/source files for accidental secrets before sharing them; report endpoint addresses without credentials.

Report checkout, branch, Compose project, app/DB/auxiliary endpoints, checks actually run, and missing credentials or app-specific blockers. Distinguish a validated Compose model, running dependencies and a working application. Do not describe dependency-only checks as full application validation.
