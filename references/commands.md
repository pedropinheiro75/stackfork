# Command reference

A compact cheat sheet, one section per subcommand. `stackfork help` (or bare `stackfork`) prints the same list; `stackfork <command> --help` prints that command's own flags. Every flag named here is checked against the real `flag.FlagSet` by `TestCommandsMdMatchesRealFlagSets` (`internal/cli/commands_md_test.go`), so it can't silently drift.

Global flags, accepted anywhere (before or after the command): `--root DIR`, `--state-dir DIR`, `--legacy-state-dir DIR`, `--npmrc PATH`, `--secret NAME=REF` (REF is `env:VAR` or `npmrc:PATH#HOST` only; `keychain:` and `op://` come from `secret map`), `--profile-dir DIR`, `--instance NAME`. `--instance NAME` (up, down, restart, start, stop, status, urls, logs, exec, run, env, open, host) addresses any prepared instance from any directory; `stackfork list` shows the names. A relative `-f`/`--env-file` resolves against `--root` (the checkout), not the directory you happen to run `stackfork` from.

### prepare
Isolate this checkout+branch's copy of the Compose stack.
Flags: `-f`/`--file`, `--env-file`, `--force`, `--hostname`, `--reassign-ports`, `--random-ports`
`prepare` and `up` refuse a checkout whose repository was never learned (no knowledge file, adapter, instance or profile reference): `<repo> has not been learned yet: run stackfork learn first`. A draft (learn ran, not verified) is allowed, and `up` on an existing instance is never blocked. `--force` overrides the refusal with a warning (people only: the guard blocks it for agents, who run `stackfork learn`).
```sh
stackfork --root .worktrees/pr630 prepare -f .agent-dev/nuxt.compose.yaml --env-file .agent-dev/runtime.env
```

Recipe gap: in a linked worktree, `prepare` compares its Compose files with the files the main checkout's last prepare used. When one is missing, it prints one `warning:` line on stderr that names both lists and the fix (`stackfork --root WORKTREE reset --from-main`) and goes on; the exit code stays 0. `start-env` and `reset` repeat the line in their `prepare` step log, the reset confirmation shows it, and the dashboard shows it on the worktree's card. A worktree with extra files and nothing missing has no gap, and a main checkout that was never prepared gives nothing to compare with.

Dependency caches across restarts: `prepare` turns an anonymous volume on a dependency or build-cache folder (`node_modules`, `.nuxt`, `.next`, `.output`, `vendor`, `.venv`, `target`, ...) into a named volume of that instance (`<project>_<service>_<path>`). A `down` then `up` reuses it, so an install inside the container only fetches what changed instead of everything. `down --volumes` and `prune` remove it like any other volume. Run `prepare` again for an existing instance to pick this up.

### up
Start the prepared stack.
Flags: `--build`, `--force`, `--mode`, `--profile`, `--service`, `--wait-timeout`
If Compose fails with `endpoint with name X already exists in network N` (Docker keeps these after a crash or a full disk) and N is one of this project's networks and X is not a running container, `up` force-disconnects X, prints a note and retries once.
`--mode container|hybrid` names the plan mode that decides whether this `up` runs the one-time seed (default: the configured mode); `start-env` sets it.
```sh
stackfork up --build --wait-timeout 120
```

### down
Stop it (data kept unless `--volumes`).
Flags: `--volumes`, `--forget`, `--instance`
`--forget` (needs `--volumes`, else exit 2) removes the instance's state directory after a successful `down --volumes`, so it leaves the list; the next Start prepares it again. The dashboard's "Remove instance and data" runs it. The guard blocks `--forget` for agents. Every `down` also force-disconnects leftover endpoints of this project's networks (label `com.docker.compose.project`) whose container is gone, and removes the network.
```sh
stackfork down --volumes
```

### status
Machine-readable (JSON lines) state of every service. An unhealthy or exited service gets an optional `hint` field and a `try stackfork restart <service>` line on stderr; a down proxy with routes gets a warning on stderr.
```sh
stackfork status
```

### urls
This instance's URLs. The recorded entry (`App: ...`) and the named links come first, then the endpoint lines; an entry that is only guessed is marked `guessed` with the `learn record entry` command, and a recorded target the stack lacks is a warning on stderr.
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
Restart, start or stop one or more services.
```sh
stackfork restart app
stackfork restart app db
```

### logs
Recent (or following) logs for a service, secrets redacted.
Flags: `--tail`, `--follow`
```sh
stackfork logs app --tail 200 --follow
```

### learn
Scan a project and record/verify what you learned (`.stackfork/knowledge.json`).
Sub-forms: `learn` (scan), `learn record answer <question> <answer>`, `learn record gotcha <problem> <fix>`, `learn record recipe --prepare ... --up ... [--migrate ...] [--seed-once ...|none] [--seed-service ...] [--login ...]` (an alias that writes steps and marks the plan recorded; a seed step the scan proposed stays in the recorded plan, so it will run, until you record `--seed-once none`), `learn record step --id ID --kind K [--cmd ...] [--service ...] [--once data|schema|none] [--on host|container] [--modes a,b] [--exclude a,b] [--name ...] [--dir ...] [--port-env A,B] [--port N] [--args A,B] [--text ...] [--before ID]` or `--remove ID`, `learn record entry --service S --port N [--path P]` or `--none` or `--remove` (the app's entry: a Compose service and its container port; checked against the prepared instance), `learn record link NAME (--path P | --service S --port N [--path P])` or `--remove NAME` (a named quick link: a path on the entry, or another service of the stack; a path is relative and starts with one `/`), `learn record secret NAME [--hint TEXT]` or `--remove NAME` (declares a secret NAME the project needs, with a hint for the person; names only, never a reference or a value, which stay in the machine config), `learn record live-watch PATH --action rebuild|prepare|migrate [--service S]` or `--remove PATH` (a live-mode watch override; `rebuild` needs `--service`; PATH is relative to the checkout), `learn record live-ignore PATH` or `--remove PATH` (stop watching a default path), `learn record fact migration-dir PATH` or `--remove PATH` (a migration directory the scan missed), `learn verify --check ... --confirm ...` (needs a prepared instance for the checkout, a recorded entry or `none`, and, with an entry, a running instance: the entry and each link are probed once; it also writes the plan's `prepare` step from the instance's recipe; see `references/learning.md`)
```sh
stackfork learn
stackfork learn record entry --service nginx --port 80 --path /app
stackfork learn record link Email --service smtp4dev --port 80
stackfork learn record secret NPM_TOKEN --hint "GitHub Packages read token"
stackfork learn verify --check "database identity" --check "app readiness" --confirm "yes, it works"
```

### plan
Print the startup plan (`.stackfork/knowledge.json`; the checkout's own, else the main checkout's, else the newest verified one of another worktree, noted on stderr) for a mode: each step with its state (proposed, recorded, verified), the steps skipped in this mode, and whether this machine trusts the host steps. `--trust` records that trust (host and prepare steps) without prompting; agents are blocked from running it. Trust is needed only when the mode runs a host step; its hash covers all host and prepare steps (a new step field needs a new trust) and it is stored once for each main checkout, so worktrees with different host steps invalidate each other. The dashboard's Trust button shows every field in a native dialog. Host steps print `port: unknown` when no port was detected. When the checkout has no recorded plan steps (no `knowledge.json`, or a plan without steps), `plan` prints the derived plan instead of failing: the steps a `start-env` would run, from the same code, each with its source (`implicit`, `adapter .stackfork/dev.compose.yaml`, `recipe of PROJECT`, `auto-detected: ...`), under the label `derived — not recorded; run learn to record it`. A start that would be refused prints `start-env would refuse this checkout: REASON` instead of steps. Showing it changes nothing: it writes no file and runs no plan step, no Docker and no host plan command; only read-only git queries run. The refusal it reports is the one `start-env` prints, which `start-env --force` can override for a project that only needs learn. `plan` exits 0 for a derived plan (before, a checkout without `knowledge.json` exited with an error); a `knowledge.json` that cannot be read is still an error. `--json` adds `"derived": true`, `prepare_from`, `refusal`, `gap` and a `source` on each step.
Flags: `--mode`, `--json`, `--trust`
```sh
stackfork plan --mode hybrid
stackfork plan --json
stackfork plan --trust
```

### env
Print this instance's env vars, for a host-run app - `eval "$(stackfork env)"`.
`--format powershell` prints `$env:NAME = '…'` lines for PowerShell, with each value single-quoted.
Flags: `--format` (`shell`, the default, `dotenv` or `powershell`), `--with-passwords`
```sh
eval "$(stackfork env)"
stackfork env --format dotenv > .env.local
```

Env overrides (per instance, never in the repository): `env set SERVICE NAME=VALUE`, `env unset SERVICE NAME`, `env overrides [SERVICE] [--json]` and `env overrides --clear [SERVICE]`. SERVICE is a Compose service of the prepared snapshot or a host app; any other name fails and lists the valid ones. Overrides live in `env-overrides.json` (mode 0600) in the instance's state directory and win over the project's `env_file` and `environment` values. For a container, `Execute` passes a derived second Compose file, `compose.env-overrides.json`, after the snapshot, so the snapshot and its `config_sha256` never change; for a host app they are added after the instance's environment block. `set`, `unset` and `--clear` then apply the change as a run of command `env` with one step per service: a running container is recreated alone (`up -d --no-deps --wait SERVICE`), a stopped container is recreated without starting it (`--no-start`), a running host app is restarted, and a service with no container or a host app that is not running keeps the override for its next `up` or start. When another run (an `up`, say) uses the instance, the command waits up to 2 minutes; then it exits non-zero with `stored, not applied yet` and the value stays stored. `--no-restart` stores the change and applies nothing; `--clear` takes it too. The file survives `up`, `down` and `reset` (`reset` prints `keeping N env overrides`; `reset --clear-env` drops them) and goes with `down --volumes --forget`. A secret-like name (`*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `*_PASSWD`, `*_KEY`, `*_CREDENTIALS`, a bare `TOKEN`, `SECRET` or `PASSWORD`, or a secret of the instance) is refused with a pointer to `stackfork secret`; `set --allow-secret` stores it anyway, and its value is then masked (`"value": null, "masked": true` in `--json`) and redacted in run output. Agents may run `set`, `unset` and `overrides`; the guard blocks `set --allow-secret`, `overrides --clear` and `reset --clear-env`. A value is at most 4096 bytes with no NUL or newline; a name matches `[A-Za-z_][A-Za-z0-9_]*`. `env overrides --json` prints `{"count": N, "services": [{"service", "kind": "container|host|unknown", "overrides": [{"name", "value", "masked"}]}]}`.
```sh
stackfork env set web ENABLE_GUEST_ACCESS=true
stackfork env unset web ENABLE_GUEST_ACCESS --no-restart
stackfork env overrides --json
```

### open
Open this instance's app URL in the browser (the recorded entry, the portless one when the proxy routes it); `--print` only prints it. Add a NAME to open a named link (`stackfork open email`) or `app`, then a service's URL (`stackfork open mailpit`); names match without regard to case.
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
The sanctioned way to run an app on the host in a managed checkout: `stackfork host NAME [--port N] -- COMMAND...` in the foreground (it ends with your shell), or, for agents and anything that must outlive a shell, `stackfork host start NAME [--port N] -- COMMAND...`, which is detached and supervised. A foreground run also records how to start it again, so `host restart NAME` and `up` can start it detached later. The instance must be running. It gives COMMAND a stable free port (kept in the manifest), the same environment as `env --with-passwords` plus `PORT`, `NUXT_PORT`, `NITRO_PORT` and `HOST` (and each `--port-env` name), and a proxy route `NAME` for as long as it runs; signals are forwarded and the route is removed on exit. The guard allows it.
Sub-forms:
- `host start NAME [--port N] [--dir D] [--port-env A,B] [--wait-timeout D] -- COMMAND...` runs it detached: a supervisor outlives your terminal, writes `host-NAME.log` in the instance directory (truncated at each start), records itself in the manifest and removes its route on exit. Returns once the app listens on its port (127.0.0.1 or all interfaces) and its supervisor is still alive half a second later, waiting up to 90 s (`--wait-timeout 3m`, or `STACKFORK_HOST_WAIT_TIMEOUT`; an explicit `--wait-timeout 0` returns as soon as it is recorded, and a restart or `up` keeps the value). When the wait runs out the app keeps running (a worker may never listen, or may not use `PORT`): the command prints a warning that names the app, the port and the last 15 lines of the log (redacted), and the dashboard shows the app as `starting`. Refused when NAME already runs.
- `host ls` lists the host apps of the instance: state (`running` only when the recorded pid and start time match and the port answers), port, and pid, memory, CPU and uptime, or `exited CODE at HH:MM`.
- `host stop NAME` sends SIGTERM to the supervisor's process group, waits 10 s (`STACKFORK_HOST_STOP_TIMEOUT`, for example `2s`), then SIGKILL; removes the route. It signals only when the recorded PID, start time and command line still match; a stale entry is only cleaned up.
- `host restart NAME` stops it and starts it again with the same command.
- `host logs NAME [-f]` prints the log (secrets redacted); `-f` follows it.
`stackfork down` stops host processes before it runs the compose down. Known limit: if the supervisor itself is killed with SIGKILL (not through `host stop`), its command keeps running without a supervisor; `host stop NAME` still finds the entry, sees the supervisor is gone and cleans up, but cannot reach the orphaned command, so stop it by hand. A foreground `host` run from an interactive shell leads its own job group, so `host stop` can stop it too; inside a pipeline the signal also reaches the other members. On Windows it stops the process tree with `taskkill /T /F`, with no graceful stop; see SECURITY.md.
With several host apps one is "the app" for the proxy route, the App chip, Open App and `urls`: the recorded entry's service, else the plan's `app_service`, else a host app whose command runs a frontend dev server (vite, next, nuxt, astro, webpack serve, react-scripts, ng serve, remix, svelte-kit; or `npm|pnpm|yarn|bun [run] dev|start|serve` when the `package.json` in its directory, which is the only file read, depends on one of them; a tie prefers app, web, frontend, ui, client over api, backend, server, worker), else the first by name. The other host apps get a chip with their own name. Chips and URLs use `http://<hostname>:<port>`.
Each run also writes `host-NAME.json` (mode 0600) in the instance directory: the command's pid and start time, the supervisor's pid, the argv with resolved secret values redacted and the `KEY=VALUE` arguments of an `env` prefix masked as `KEY=***`, the working directory, the port, and on exit `exited_at` and `exit_code` (128 plus the signal number for a signal). The environment is never recorded. The manifest (`manifest.json`, also 0600) keeps the real command line, because `host restart` and `up` need it to run the app again.
The dashboard shows each host app as a row of the service table: state chip, an endpoint `http://<hostname>:<port>` while it listens, and under the name `pid 4242 · 312 MB · 3% CPU · up 12m` (memory and CPU sum the process and its children, from one `ps` call per refresh; unknown on Windows) or `exited 1 at 17:12`. Logs, Stop, Start and Restart work like the other rows. `/api/instances` lists them with a `host` object (`pid`, `rss_bytes`, `cpu_percent`, `started_at`, `exited_at`, `exit_code`, `listening`, `measured`). An instance whose host app is down is `partial` with a warning, not `running`.
In hybrid mode `up` (and the dashboard Start) also starts the host apps that are down: each host step of the plan (its command wins over an older recorded one), and each recorded app only if it was a server (its port answered at least once), ended by itself (a signal or a non-zero code) and nobody stopped it. A command that exited 0, a one-shot that never listened, and an app stopped with `host stop` or the dashboard Stop stay down (`stopped_by_person` in `host-NAME.json`); `host start` or `host restart` clears that, and `host restart NAME` always runs it. Each start is a `host` step of the run. A plan with host steps that this machine did not approve starts nothing (`stackfork plan --trust`).
Flags: `--port`, `--dir` (working directory, relative to the checkout), `--port-env` (extra env names set to the port, comma separated), `--wait-timeout` (`host start` only)
```sh
stackfork host ls
stackfork host start app -- bun run dev
stackfork host app --port 3000 --instance sf-310-parsed-report-8d0cca37ec66 -- bun run dev
stackfork host start web --port-env VITE_PORT -- bun run dev
stackfork host logs web -f
stackfork host stop web
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

### seed
Run the recipe's `seed_once` command (`learn record recipe --seed-once CMD [--seed-service SVC]`) in its service. `up` and `start-env` run it automatically, once, when that `up` creates the data volume (a script detected in the checkout counts when no decision is recorded; `--seed-once none` disables it). A step with no service never runs in a web server or proxy image (nginx, caddy, traefik, httpd, haproxy, envoy, openresty); when no other service can run a detected seed, `up` skips it with a `seed not run:` note and the stack stays up. While the checkout's knowledge is a draft (not verified), `up`, `start-env`, `reset` and the dashboard Start run no migrate or seed that was not recorded (`learn record step` or `learn record recipe`): an auto-detected seed and a plan step that is only proposed are skipped with `seed not run: setup is a draft; record it with ...` (or `migrate not run: ...`), the run step is `skipped` with that reason, `seeded` stays false and the seed runs on a later `up` once recorded; `plan` shows the same reason as the step's source. Recorded steps, a verified knowledge and a project with no knowledge are unchanged. `seed` is the retry when that failed (the dashboard warns about a failed seed). Already seeded: does nothing; `--force` reruns it (agents: ask first, the guard blocks it). `--mark-done` records the instance as seeded without running anything, for data that is already there; the dashboard's failed-seed warning shows the end of the seed step's log of the last run that seeded (`stackfork runs show --step seed`). Instances seeded before runs existed fall back to `seed.log`.
Flags: `--force`, `--mark-done`
```sh
stackfork seed
```

### runs
List the recorded runs (`start-env`, `up`, `seed`) of this checkout's instance, newest first: id, command, trigger, mode, outcome, start, duration and the failed step. `runs show [ID]` prints one run (the latest by default) with each step's state, duration and reason; `--step STEP` prints that step's log (secret values are already redacted). Read-only; agents may run it. After a failed start, read `stackfork runs show --step <failed step>`. Works with `--instance NAME` from any directory.
Flags: `--json`, `--limit`, `--step`
```sh
stackfork runs
stackfork runs show --step seed
```
Each run is stored in the instance directory: `runs/<id>.json` holds the run (steps, states, reasons, durations and the retry command), and `runs/<id>/<step>.log` holds one step's output. Files are 0600 and directories 0700. The last 20 runs are kept. A step log keeps its first and last 512 KiB and marks the bytes it dropped in between.

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
A local web page (127.0.0.1-only) listing every instance, plus a Repos tab (find a repo under your configured folders, see its branches and worktrees, Start one) and a Settings panel for those folders.
Flags: `--port`, `--open`, `--supervised`
`--supervised` is for a parent process such as the macOS app: it listens on a port of its own (`<state>/dashboard/app-port`, never `port`), prints one JSON ready line (`url`, `port`, `token`, `pid`, `version`) as its first output instead of the `listening on` line, registers no `stackfork.localhost` route, and shuts down when standard input closes. It refuses `--port` and `--open` (exit 2).
```sh
stackfork dashboard --open
```

### config
Show or edit `<config dir>/config.json` (`STACKFORK_CONFIG_DIR` respected): the folders (`roots`) the repo browser scans, folder names to skip (`exclude`) and `maxDepth` (default 6). Sub-forms: `config get`, `config add-root PATH`, `config remove-root PATH`, `config add-exclude NAME`, `config remove-exclude NAME`, `config set max-depth N`, `config set mode container|hybrid`, `config set-mode REPO container|hybrid`, `config set roots PATH...`, `config set exclude NAME...`. A root must exist, be a folder, not be `/` or the bare home directory, and not sit inside (or contain) another root. Removing a root deletes nothing on disk. `mode` is the startup mode on this machine (default `container`); `set-mode` overrides it for one repo, stored under the repo's resolved main-checkout path.

Two keys are edited in the file itself:
- `terminal`: where **Learn with your agent** opens the agent: `warp`, `iterm` or `terminal`. Default: the terminal the dashboard was started from, else Terminal.app.
- `agentRules`: keep an agent away from a folder, for example a client whose policy forbids it. The modal shows that agent disabled with the reason, and `POST /api/learn` refuses it:
  ```json
  "agentRules": [{ "path": "~/code/client", "deny": ["claude"], "reason": "client policy: use Codex or Copilot" }]
  ```
```sh
stackfork config add-root ~/code
```

### secret
Named secrets: which reference each name uses on this machine, and whether it resolves. No `secret` command prints a value, and none takes one as an argument.
- `secret ls [REPO] [--json]`: one row per name with its reference, source (`flag`, `map`, `folder`, `manifest`, `sibling`, `declared`; a `folder` row also has `from`, the folder that holds the reference), hint and status (`resolves`, `missing`, `locked`, `unavailable`, `error`, `not checked`). It reads nothing that would prompt, so `op://` rows are `not checked`. REPO is a path or a scanned repo name; the default is this checkout's repository.
- `secret check [REPO] [--json]`: the same rows with every value read and thrown away (1Password may prompt). Exit status 1 when any row is not `resolves`.
- `secret set ITEM [--store keychain]`: stores a value in the OS credential store (service `stackfork`, account ITEM). It reads the value from standard input when that is not a terminal, else it asks twice with echo off. It refuses an empty value, a newline inside the value, a value over 64 KiB, and on macOS a value with non-ASCII characters (the `security` tool returns those as hex). Then it prints `stored keychain:ITEM` and, when no repository maps the item yet, the `secret map` command.
- `secret map REPO|FOLDER NAME=REF` and `secret unmap REPO|FOLDER NAME`: edit the machine map in `config.json`. FOLDER is an existing directory that is not inside a repository, such as `~/clients/acme`: every repository under it that declares or recorded NAME uses the reference (a folder never adds a name to a repository). A repository's own mapping beats a folder's, and a nearer folder beats a farther one. The filesystem root, your home directory, a file and a missing path are refused. REF is `env:VAR`, `npmrc:PATH#HOST`, `keychain:ITEM` or `op://VAULT/ITEM/FIELD`. `map` prints the status without a prompt. A mapped reference beats the one a prepared instance recorded, with no new `prepare`; a name the instance does not know needs `start-env`.

The `--secret` flag takes only `env:` and `npmrc:` references; a flag that names `keychain:` or `op://` is refused. `exec` and `run` redact their output with the values known for the instance (best effort, and not on a terminal; see SECURITY.md). `keychain:` is the macOS Keychain, the Linux Secret Service or the Windows Credential Manager (target `stackfork/ITEM`, value at most 2560 bytes). Agents may run `ls` and `check`; `set`, `map` and `unmap` are for people (the guard blocks them for agents). Every run starts with a `secrets` step that resolves the references and fails, before any Docker command, with one line per problem and the command that fixes it. A Start from the dashboard does not see your shell environment, so keep those values in the keychain or 1Password.

Dashboard: Settings has a **Secrets** table of every mapping in `config.json` (repository and folder), with its status, **Set value…** for a `keychain:` reference and **Unmap** (after a confirmation in the page). **Add secret**, there and in every repository's Secrets section (which is always shown, with "No secrets" when it has none), opens a dialog for a name, a reference and a scope (this repository, or a folder that defaults to its parent). It does what `secret map` does, with the same refusals shown in the dialog, and when a `keychain:` item has no value yet it offers **Set value…**. The page sends a reference, never a value: a text that is not a reference is refused without being echoed. The endpoints are `POST /api/secrets/map` and `/api/secrets/unmap`; they need the dashboard token and an allowed Origin like every other POST, and the guard blocks agents from calling them.
Flags: `--json`, `--store`
```sh
printenv NPM_TOKEN | stackfork secret set acme-npm-token
stackfork secret map . NPM_TOKEN=keychain:acme-npm-token
stackfork secret map . STRIPE_TEST_KEY=op://Dev/acme-stripe/test-key
stackfork secret check --json
```

### live
Live mode: while the dashboard runs, the repository follows its upstream and rebuilds what changed. Per repository and per machine; off by default.
- `live on [REPO]`: REPO is a path or a scanned repo name; the default is this checkout's repository. It refuses when the watch set is over 2,000 files, a `live` override in `knowledge.json` is invalid, any effective secret reference is `env:` (the dashboard does not see your shell environment; use `secret set` and `secret map`), or the plan's host or prepare steps are not trusted (`plan --trust`, with the step diff). It clears a pause, prints what the setting means and, with no dashboard running, `paused: dashboard not running; start it with stackfork dashboard`. An `op://` reference may show a 1Password prompt during a live run; unanswered for 30 seconds, the run fails at `secrets` and live mode pauses.
- `live off [REPO]`: removes the setting; a live run in progress finishes.
- `live status [REPO] [--json]`: one block per repository with live mode on (or REPO only): `off`, `on`, `on, paused: REASON` (a failed run, `plan needs trust`, a rate limit, or `dashboard not running`), the cleanup mode, and each instance with its pending changes by category, its last live run and any warning. `--instance` is not accepted.
- `live cleanup off|suggest|auto [REPO] [--grace HOURS] [--delete-branch|--keep-branch]`: the branch-cleanup setting. Default with live mode on: `suggest`, 24 hours, keep the branch. `--grace` is 1 to 720.
- `live clean [REPO] BRANCH [--yes]`: prints what a cleanup of the merged worktree BRANCH would delete (the worktree, the instance's Docker volumes, the local branch if `--delete-branch` is set, ignored files that go with the worktree) and deletes nothing. With `--yes` it re-checks that the worktree is clean and the branch is merged, then removes the worktree (without `--force`), the instance with its volumes, and the branch when allowed. It refuses while live mode is paused for the repository (resume first with `live on`) and while another cleanup of the repository runs. A worktree that also holds an instance of another branch, an initialized submodule or refs of its own is not cleaned; `live status` says why. `live status` lists the candidates and their warnings (`PR merged, but this worktree has uncommitted changes`, `PR closed without merge`).
Only people may turn live mode on, choose `auto` or `--delete-branch`, or run `live clean --yes` (the guard blocks them for agents); `live off`, `live status`, `live cleanup off|suggest`, `--keep-branch` and `live clean` without `--yes` stay allowed. Nothing in the CLI reconciles: only the dashboard does.
Flags: `--json`, `--grace`, `--delete-branch`, `--keep-branch`
```sh
stackfork live on
stackfork live status --json
stackfork live cleanup suggest --grace 48
```

### repos
List the git repos under the configured roots: group, readiness (`ready` = verified learned knowledge in any checkout; `needs-learn` = it could run from an adapter, a prepared instance or a profile reference, but nobody verified how; `draft`; `unknown`), running count, worktrees, branches without a worktree, last commit. Local git only, never a network call; results are cached per repo while its git metadata is unchanged. With no roots it says how to add one. `--prompt REPO BRANCH` prints the text to give an agent to learn and verify a project that is not `ready`.
Flags: `--json`, `--rescan`, `--prompt`
```sh
stackfork repos --json
stackfork repos --prompt storefront fix/login
```

### worktree
`worktree add REPO BRANCH [--from BASE]` creates `<repo>/.worktrees/<branch>` (slashes become dashes) for an existing local branch, or a tracking branch for one that exists only as `origin/<branch>`; a new branch needs `--from`. It excludes `/.worktrees/` in the repo's local git exclude and copies only the files the project's `.stackfork/knowledge.json` lists under `adapter_files`, never `.env*`, keys, certificates or a token-bearing `.npmrc` (what was copied and skipped is printed). REPO is a path or a scanned repo name. Refuses when the folder already exists.
Flags: `--from`
```sh
stackfork worktree add storefront fix/login
```

### start-env
`start-env [--force] [--mode MODE] [CHECKOUT]`: for a project whose startup was learned and verified (readiness `ready`), `prepare` only when needed (no prepared instance, or its inputs changed) with the recorded recipe/adapter, then `up`; progress lines are printed. The dashboard's Start button runs this same code. Refuses `needs-learn`, `draft` and `unknown` projects and prints the `repos --prompt` command; `--force` starts a `needs-learn` one anyway with a warning (people only: the guard blocks it for agents, who should learn the project instead). Not the per-service `start`. Runs the startup plan for the machine's mode (`--mode container|hybrid` overrides it); a plan with a host step refuses until `stackfork plan --trust` ran on this machine.
Flags: `--force`, `--mode`
```sh
stackfork start-env .worktrees/fix-login
```

### reset
`reset [--yes] [--from-main] [--clear-env]`: throw away this checkout's environment and start it again from zero, from the code in the worktree. Steps of the recorded run (`reset`): `sync` (fast-forward when clean, as every start does), `secrets`, `down` with volumes (this deletes the instance's data), `prepare` (re-render), `up --build`, then the plan's schema and data steps on the new database and, in hybrid mode, the host steps. It never touches your code: no `git reset`, no discarded changes. When the checkout is behind its upstream and has local changes or commits, it stops before deleting anything and asks `reset with the current code?`. Without `--yes` it lists the volumes it will delete and asks `y/N` on a terminal; with no terminal and no `--yes` it refuses. `--yes` confirms the deletion and means continue with the current code. The checkout comes from `--root`/the current directory or `--instance NAME`; the main checkout works too (it resets the environment, not git). The instance's env overrides are kept (the `down` step logs `keeping N env overrides`); `--clear-env` drops them. The dashboard has the same action in the instance and worktree menus. Destructive: the guard blocks it for agents, who should ask the user to run it.

`--from-main` (linked worktrees only) rebuilds the worktree from the main checkout's recipe, for a worktree prepared with fewer Compose files than the main checkout (see the recipe gap under `prepare`). Before anything changes it checks that this is a linked worktree, that the main checkout has a prepared instance and that its recipe can be reused here (a tracked file of the recipe must exist in the worktree). The confirmation adds the recipe it will use and the untracked files it will move. The `prepare` step then moves the worktree's untracked adapter files (the regular files of the adapter folders, such as `.stackfork` or `.agent-dev`, and an untracked env file) to `.stackfork-aside/<UTC time>/`, copies the main checkout's files and runtime env (paths rewritten to this worktree), and prepares with the main checkout's files and env file. Tracked files are never touched, nothing is deleted (delete `.stackfork-aside/<time>` yourself when you do not need it), and a failed `sync`, `secrets` or `down` step moves nothing. The instance's own secret references stay. The retry of a failed run is `stackfork --root WORKTREE reset --yes --from-main`.
Flags: `--yes`, `--from-main`
```sh
stackfork reset
stackfork --instance sf-fix-login-1a2b3c reset --yes
stackfork --root .worktrees/feature-login reset --from-main
```

### doctor
Check the local environment the proxy/tool depend on (Docker context, Compose version, `*.localhost` resolution, proxy port, `host.docker.internal`, Docker disk usage). The disk check warns at 85% used, because a full Docker disk makes installs fail later with `no space left on device`.

The `host apps` check warns about a host app whose supervisor is gone but whose port still answers. Find the process on that port (macOS/Linux: `lsof -i :PORT`; Windows: `netstat -ano | findstr :PORT`), stop it, then run `stackfork host start`. A stopped app that shows as an exited row is normal and is not reported.

`--free-space` removes the Docker build cache, dangling images and anonymous volumes that no container uses, then runs the checks. It never removes a named volume, so databases are kept. The dashboard's "Free Docker space" button (in the disk banner and the "…" panel) runs the same steps.
```sh
stackfork doctor
stackfork doctor --free-space
```

### version
Print the version.
```sh
stackfork version
```
