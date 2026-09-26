# The Go template

For a Go web app that has no app container, or no Compose at all, adapt [the starting point](../assets/go/compose.yaml) rather than the general Compose-adaptation flow in `references/adapting-compose.md`. Do not force a non-Go or dependency-only project into this template - adapt its own stack instead. It assumes the app serves HTTP (the healthcheck hits its root path); a non-HTTP Go program (a worker, a CLI) doesn't fit this template either.

Copy `assets/go/Dockerfile` and `assets/go/entrypoint.sh` into `.agent-dev/`. Set `DEV_CHECKOUT`, `DEV_RUNTIME_ENV` and `DEV_DB_NAME` in the generated runtime file. Match `DEV_DB_IMAGE`, `DEV_PGDATA_TARGET`, `DEV_GO_VERSION` and `DEV_SERVER_PORT` to the project (Go has no fixed dev-server port convention the way Nuxt does; read it off the project's own `main.go`/config). Replace `command: [go, run, .]` with the project's own hot-reload invocation (`air`, `reflex`, ...) if it already uses one - this template's default has none. Remove the `db` service for a project with no database.

The generic template needs no private module host; if the project pulls from a private Go module proxy or a private GitHub org, set `DEV_GOPRIVATE` (a `GOPRIVATE`-style pattern, e.g. `github.com/org/*`) and `DEV_GO_REGISTRY_HOST`, and add a Compose secret named `go_token` backed by the project's authorized token, granted to the app service - the entrypoint writes it to `~/.netrc` for that host only when both are set. For a standalone generated Go stack, `prepare` needs only this one `-f` file.

The entrypoint runs `go mod download` then the requested command; it does not modify host env/module-cache files, and it does not migrate or seed implicitly - run those explicitly per `references/adapting-compose.md`'s "Running it" section, with the project's own migration tool.

For a host-run/non-web Go program, keep its native commands and isolate its dependencies instead of containerizing it; containerization requires the correct build and environment wiring, which this template does not provide out of the box.
