# Adapting a project's Compose stack

Detail for step 1 of `SKILL.md` ("adapt"): making a checkout-local adapter safe to run through `stackfork`.

## Discover before adapting

Resolve the repository and checkout with Git and read its AGENTS.md or README. Inspect the current Compose files, entrypoint, package scripts, runtime configuration, migrations, and seed/reset/test launchers. Preserve required dependencies, the database's major version, the runtime and the package manager. Trace host-facing URLs (browser, host-run processes) separately from container-facing URLs (service DNS, internal ports).

## The adapter

For an existing full stack, make a checkout-local adapter (extra Compose file(s) plus a private env file) rather than editing the project's tracked Compose files. Keep it in `.agent-dev/` (see `references/worktrees.md` for why this directory is git-excluded, not committed — unlike `.stackfork/`, see `references/learning.md`).

Provision one private `.agent-dev/runtime.env` explicitly (mode 0600). Any Compose `env_file` must point to this file; replace inherited lists using `!override`, not normal list merging. Do not render the developer's real `.env` just to discover keys — the helper disables implicit Compose env loading and never prints rendered configuration. The snapshot (`compose.json` under the state directory) may still contain values explicitly supplied to it, so keep it private.

Before starting, inspect the source adapter and executable startup code for:

- **Endpoints.** Database/Redis/mail/PDF/storage endpoints belonging to this instance. Containers use service DNS and internal ports; host processes/browser URLs use the actual published ports. Mask a source-mounted `.env` with the generated runtime file so the app framework cannot silently load the developer's DB URL. Handle `.env.local`, `.env.development`, `.env.test` and package/playground env files too if the app loads them. For a host-run or hybrid app (the frontend/backend runs natively, dependencies are isolated), don't hand-assemble these from `where`/`urls` — `eval "$(stackfork --root "$CHECKOUT" env)"` exports `DATABASE_URL`/`REDIS_URL`/`MONGODB_URL`, `SMTP_HOST`/`SMTP_PORT` and one `<SERVICE>_URL` per HTTP service directly into the shell that starts it, safe for any value it resolves to; add `--with-passwords` only when the app genuinely needs the real credential (a literal Compose value only, never a secret).
- **Hardcoded names and shared resources.** Container-name references, external networks, absolute/shared writable directories, custom storage drivers, build tags and mounted Docker sockets. `stackfork prepare` refuses or rewrites these (see the tool's own refusal messages); do not work around a refusal by loosening the adapter. Replace shared persistence with fresh project-scoped named volumes; do not mount/copy existing database files into a fresh server.
- **Destructive seeds/resets.** Scripts that invoke their own `docker compose` or `docker exec` with hardcoded names. Route these through `stackfork run`/`exec` or adapt the launcher. Do not use host-wide reset/prune commands, or scripts targeting the main checkout's shared DB.
- **Ports, cookies, auth.** Dev/test server ports, browser base URLs, HMR, PDF callbacks, CORS, cookies. Ports do not isolate cookies; use the per-instance hostname (`references/hostnames.md`) and host-only cookies, plus a distinct browser context when needed. Clear inherited real SMTP/cloud endpoints; send test email to a local catcher.

## Registry access

For registry access, an existing `NPM_TOKEN` takes precedence. Otherwise select the project's authorized registry configuration once with `--npmrc PATH`; `stackfork` remembers the path and reads the token for `up`/`run`, supplying it to the Compose secret through the process environment — it never prints or stores the token itself. It supports a literal token or a single `${VARIABLE}` reference. Never select another project's credentials. For other registries, adapt the project's own authentication flow; for private packages generally, use an explicit Compose secret rather than a token build argument.

## Running it

Select the actual source filenames/services; do not assume the database service is called `db`. `stackfork prepare` creates a private canonical snapshot under the state directory; it does not start Docker. It replaces host bindings with Docker-allocated loopback ports, namespaces volumes/networks/build images, removes fixed container names, and rejects unsafe shared-resource configurations — this is an isolation check, not a sandbox for arbitrary application code or external service URLs.

`up --build` starts all non-profile services; use `--profile NAME` explicitly when needed. `run SERVICE COMMAND...` runs a one-off container with no dependencies; start required dependencies first. `exec SERVICE COMMAND...` executes in an existing container. These commands always target the snapshot, never the source files directly. Re-run `prepare` after changing the source Compose files or the selected env file; never edit the generated snapshot directly.

Run migrations explicitly after database readiness, using the project's real script. Seed a **new, verified instance DB once** when the task requires initial data: `clone-db --from <instance>` copies another prepared instance's data volume; `clone-db --from-dump FILE` restores a local `.sql`/`.sql.gz`/Postgres custom-format dump instead (a teammate's export, a production snapshot) — both refuse a non-empty target unless `--replace`, and `--from-dump` needs the instance already `up` (it restores through the live database, not a stopped volume). Ordinary app start/restart must not seed/reset. No persistent marker may suppress future migrations.
