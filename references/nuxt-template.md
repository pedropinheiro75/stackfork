# The Nuxt/Bun template

For a Nuxt app that has no app container, or no Compose at all, adapt [the starting point](../assets/nuxt.compose.yaml) rather than the general Compose-adaptation flow in `references/adapting-compose.md`. Do not force a non-Nuxt or dependency-only project into this template — adapt its own stack instead.

Copy `assets/Dockerfile`, `assets/entrypoint.sh`, `assets/nginx.conf` and `assets/registry.npmrc` into `.agent-dev/`. Set `DEV_CHECKOUT`, `DEV_RUNTIME_ENV` and `DEV_DB_NAME` in the generated runtime file. Match `DEV_DB_IMAGE`, `DEV_PGDATA_TARGET` and `DEV_BUN_VERSION` to the project; `DEV_APP_REPLICAS` defaults to 1 (set `DEV_APP_REPLICAS=2` to test multiple replicas — the template's nginx fronts them, since `stackfork` refuses a published port on a replicated service). Remove unused services. Add only the project's actual auth, mail and registry settings.

The generic template needs no private registry token; if the project requires one, explicitly add a Compose secret backed by `NPM_TOKEN` and grant it to the app (the entrypoint supports GitHub Packages tokens; see `references/adapting-compose.md` for the general registry-access flow). For a standalone generated Nuxt stack, `prepare` needs only this one `-f` file.

The entrypoint installs dependencies and runs the requested command; it does not modify host env/registry files, and it does not migrate or seed implicitly — run those explicitly per `references/adapting-compose.md`'s "Running it" section.

For a host-run/non-Nuxt app, keep its native commands and isolate its dependencies instead of containerizing it; containerization requires the correct frontend/backend build and environment wiring, which this template does not provide out of the box.
