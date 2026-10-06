# Command reference

A compact cheat sheet, one section per subcommand. `stackfork help` (or bare `stackfork`) prints the same list; `stackfork <command> --help` prints that command's own flags. Every flag named here is checked against the real `flag.FlagSet` by `TestCommandsMdMatchesRealFlagSets` (`internal/cli/commands_md_test.go`), so it can't silently drift.

Global flags, accepted anywhere (before or after the command): `--root DIR`, `--state-dir DIR`, `--legacy-state-dir DIR`, `--npmrc PATH`, `--secret NAME=REF`, `--profile-dir DIR`, `--instance NAME`. `--instance NAME` (up, down, restart, start, stop, status, urls, logs, exec, run, env, open, host) addresses any prepared instance from any directory; `stackfork list` shows the names. A relative `-f`/`--env-file` resolves against `--root` (the checkout), not the directory you happen to run `stackfork` from.

### prepare
Isolate this checkout+branch's copy of the Compose stack.
Flags: `-f`/`--file`, `--env-file`, `--hostname`, `--reassign-ports`, `--random-ports`
```sh
stackfork --root .worktrees/pr630 prepare -f .agent-dev/nuxt.compose.yaml --env-file .agent-dev/runtime.env
```

### up
Start the prepared stack.
Flags: `--build`, `--profile`, `--service`, `--wait-timeout`
```sh
stackfork up --build --wait-timeout 120
```

### down
Stop it (data kept unless `--volumes`).
Flags: `--volumes`, `--instance`
```sh
stackfork down --volumes
```

### status
Machine-readable (JSON lines) state of every service. An unhealthy or exited service gets an optional `hint` field and a `try stackfork restart <service>` line on stderr; a down proxy with routes gets a warning on stderr.
```sh
stackfork status
```

### urls
This instance's URLs.
```sh
stackfork urls
```

### where
Resolve the current checkout+branch to its instance (project, state dir, hostname, configured profiles).
```sh
stackfork where
```

### list
Every instance across every checkout.
Flags: `--json`
```sh
stackfork list --json
```

### run
Run a one-off command in a fresh container (no dependency conflict with anything already running).
```sh
stackfork run app npm test
```

### exec
Run a command in an already-running container.
```sh
stackfork exec db psql -U postgres app
```

### restart / start / stop
Restart, start or stop one service.
```sh
stackfork restart app
```

### logs
Recent (or following) logs for a service, secrets redacted.
Flags: `--tail`, `--follow`
```sh
stackfork logs app --tail 200 --follow
```

### learn
Scan a project and record/verify what you learned (`.stackfork/knowledge.json`).
Sub-forms: `learn` (scan), `learn record answer <question> <answer>`, `learn record gotcha <problem> <fix>`, `learn record recipe --prepare ... --up ... [--migrate ...] [--seed-once ...] [--login ...]`, `learn verify --check ... --confirm ...`
```sh
stackfork learn
stackfork learn verify --check "database identity" --check "app readiness" --confirm "yes, it works"
```

### env
Print this instance's env vars, for a host-run app - `eval "$(stackfork env)"`.
Flags: `--format` (`shell`, the default, or `dotenv`), `--with-passwords`
```sh
eval "$(stackfork env)"
stackfork env --format dotenv > .env.local
```

### open
Open this instance's primary app URL in the browser (the portless one when the proxy routes it); `--print` only prints it. Add a SERVICE to open that service's URL instead.
Flags: `--print`
```sh
stackfork open --print
stackfork open mailpit --instance sf-310-parsed-report-8d0cca37ec66
```

### guard
Pre-tool-use hook handler for Claude Code, Codex and Copilot CLI: reads the tool's hook JSON on stdin and blocks (exit 2, or a Copilot deny on stdout) only a dev server (`bun|npm|pnpm|yarn dev|start|serve`, `nuxt dev`, `vite`, `next dev`, `nodemon`) or a raw `docker compose up/start` started directly inside a stackfork-managed checkout. Everything else, and anything it cannot parse, is allowed. Install it with `stackfork hooks install`.
Flags: `--tool` (`claude`, the default, `codex` or `copilot`)
```sh
echo '{"cwd":"/repo","tool_name":"Bash","tool_input":{"command":"bun run dev"}}' | stackfork guard
```

### host
The sanctioned way to run an app on the host in a managed checkout: `stackfork host NAME [--port N] -- COMMAND...`. The instance must be running. It gives COMMAND a stable free port (kept in the manifest), the same environment as `env --with-passwords` plus `PORT`, `NUXT_PORT`, `NITRO_PORT` and `HOST`, and a proxy route `NAME` for as long as it runs; signals are forwarded and the route is removed on exit. The guard allows it.
Flags: `--port`
```sh
stackfork host app -- bun run dev
stackfork host app --port 3000 --instance sf-310-parsed-report-8d0cca37ec66 -- bun run dev
```

### hooks
Install, remove or report the `guard` pre-tool-use hook in each agent tool's own config: `~/.claude/settings.json` (`$CLAUDE_CONFIG_DIR`), `~/.codex/hooks.json` (`$CODEX_HOME`) and `~/.copilot/settings.json` (`$COPILOT_HOME`; not `config.json`, which Copilot CLI rewrites at the end of a session and drops hooks from. A hook an older version put there is removed). Existing hooks are preserved, repeated runs change nothing, and a changed file is first copied to `<file>.stackfork-bak-<timestamp>`. Codex only runs a hook the user has trusted: after installing, open `codex` once and approve it (or run `/hooks`); stackfork never writes that approval, and `hooks status` says `installed but untrusted` until a matching `[hooks.state."…"]` entry exists in Codex's `config.toml`.
Flags: `--tool` (repeatable: `claude`, `codex`, `copilot`; default all)
```sh
stackfork hooks status
stackfork hooks install --tool claude
```

### clone-db
Seed this instance's database from another instance's volume or a local dump.
Flags: `--from`, `--from-dump`, `--service`, `--database`, `--volume`, `--replace`, `--allow-running`
```sh
stackfork clone-db --from sf-myapp-main-ab2267a
stackfork clone-db --from-dump backup.sql --replace
```

### prune
Remove whole instances and their Docker resources: containers, networks, stackfork-built images (`<project>-<service>:dev`), state, proxy routes, and volumes unless `--keep-volumes`. **Dry run by default**: a table of what would go (project, state, checkout, branch, legacy, volumes, images, last activity) plus a best-effort disk estimate. `--orphaned` = checkout/branch gone (never a running one); `--stopped` = no running container; `--all` = everything, running ones stopped first, plus the proxy container. No scope flag: the dry run shows all three groups. `--older-than 7d|48h|2w` filters by last activity (newest of manifest/state mtime and container finish time; running = now). Deleting needs `--yes` or an interactive `y/N` (typing `prune` when running instances or `--all` are involved); without a TTY and `--yes` it prints the dry run and exits 1. Only objects labelled `io.stackfork.root` (or the legacy `idv` equivalent), or exactly matching a known instance's compose project, are touched; never `docker system prune`. Covers legacy `idv-` instances. Agents may run the dry run only; the guard blocks `--yes` and `--all`.
Flags: `--orphaned`, `--stopped`, `--all`, `--older-than`, `--keep-volumes`, `--yes`, `--json`
```sh
stackfork prune                       # dry run, all groups
stackfork prune --stopped --older-than 7d
stackfork prune --orphaned --keep-volumes --yes
```

### gc
Alias of `prune --orphaned --keep-volumes`: instances whose checkout or branch is gone. Dry-run by default; `--apply` removes without a prompt; `--volumes` (needs `--yes`) also deletes their data.
Flags: `--apply`, `--volumes`, `--yes`
```sh
stackfork gc --apply --volumes --yes
```

### proxy
The shared portless-URL proxy. It restarts with Docker (`--restart unless-stopped`); `proxy up` on a stopped one just starts it. `status`, `urls`, `doctor` and the dashboard warn when routes exist but it is down.
Sub-forms: `proxy up [--port N]`, `proxy down`, `proxy status`, `proxy reload`
```sh
stackfork proxy up --port 80
```

### route
Register a host-run app's port with the proxy.
Sub-forms: `route add <name> <port>`, `route remove <name>`, `route list`
```sh
stackfork route add app 5173
```

### dashboard
A local web page (127.0.0.1-only) listing every instance.
Flags: `--port`, `--open`
```sh
stackfork dashboard --open
```

### doctor
Check the local environment the proxy/tool depend on (Docker context, Compose version, `*.localhost` resolution, proxy port, `host.docker.internal`).
```sh
stackfork doctor
```

### version
Print the version.
```sh
stackfork version
```
