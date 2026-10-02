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
Machine-readable (JSON lines) state of every service.
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

### clone-db
Seed this instance's database from another instance's volume or a local dump.
Flags: `--from`, `--from-dump`, `--service`, `--database`, `--volume`, `--replace`, `--allow-running`
```sh
stackfork clone-db --from sf-myapp-main-ab2267a
stackfork clone-db --from-dump backup.sql --replace
```

### gc
Remove state for checkouts/branches that no longer exist. Dry-run by default.
Flags: `--apply`, `--volumes`, `--yes`
```sh
stackfork gc --apply --volumes --yes
```

### proxy
The shared portless-URL proxy.
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
