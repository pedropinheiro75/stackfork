# Command reference

A compact cheat sheet, one section per subcommand. `stackfork help` (or bare `stackfork`) prints the same list; `stackfork <command> --help` prints that command's own flags. Every flag named here is checked against the real `flag.FlagSet` by `TestCommandsMdMatchesRealFlagSets` (`internal/cli/commands_md_test.go`), so it can't silently drift.

Global flags, accepted anywhere (before or after the command): `--root DIR`, `--state-dir DIR`, `--legacy-state-dir DIR`, `--npmrc PATH`, `--secret NAME=REF`, `--profile-dir DIR`, `--instance NAME`. `--instance NAME` (up, down, restart, start, stop, status, urls, logs, exec, run, env, open, host) addresses any prepared instance from any directory; `stackfork list` shows the names. A relative `-f`/`--env-file` resolves against `--root` (the checkout), not the directory you happen to run `stackfork` from.

### prepare
Isolate this checkout+branch's copy of the Compose stack.
Flags: `-f`/`--file`, `--env-file`, `--hostname`, `--reassign-ports`, `--random-ports`
```sh
stackfork --root .worktrees/pr630 prepare -f .agent-dev/nuxt.compose.yaml --env-file .agent-dev/runtime.env
```

Dependency caches across restarts: `prepare` turns an anonymous volume on a dependency or build-cache folder (`node_modules`, `.nuxt`, `.next`, `.output`, `vendor`, `.venv`, `target`, ...) into a named volume of that instance (`<project>_<service>_<path>`). A `down` then `up` reuses it, so an install inside the container only fetches what changed instead of everything. `down --volumes` and `prune` remove it like any other volume. Run `prepare` again for an existing instance to pick this up.

### up
Start the prepared stack.
Flags: `--build`, `--mode`, `--profile`, `--service`, `--wait-timeout`
`--mode container|hybrid` names the plan mode that decides whether this `up` runs the one-time seed (default: the configured mode); `start-env` sets it.
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
Sub-forms: `learn` (scan), `learn record answer <question> <answer>`, `learn record gotcha <problem> <fix>`, `learn record recipe --prepare ... --up ... [--migrate ...] [--seed-once ...|none] [--seed-service ...] [--login ...]` (an alias that writes steps and marks the plan recorded; a seed step the scan proposed stays in the recorded plan, so it will run, until you record `--seed-once none`), `learn record step --id ID --kind K [--cmd ...] [--service ...] [--once data|schema|none] [--modes a,b] [--exclude a,b] [--name ...] [--dir ...] [--port-env A,B] [--port N] [--args A,B] [--text ...] [--before ID]` or `--remove ID`, `learn verify --check ... --confirm ...`
```sh
stackfork learn
stackfork learn verify --check "database identity" --check "app readiness" --confirm "yes, it works"
```

### plan
Print the startup plan (`.stackfork/knowledge.json`) for a mode: each step with its state (proposed, recorded, verified), the steps skipped in this mode, and whether this machine trusts the host steps. `--trust` records that trust (host and prepare steps) without prompting; agents are blocked from running it. Trust is needed only when the mode runs a host step; its hash covers all host and prepare steps (a new step field needs a new trust) and it is stored once for each main checkout, so worktrees with different host steps invalidate each other. The dashboard's Trust button shows every field in a native dialog. Host steps print `port: unknown` when no port was detected.
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
The sanctioned way to run an app on the host in a managed checkout: `stackfork host NAME [--port N] -- COMMAND...`. The instance must be running. It gives COMMAND a stable free port (kept in the manifest), the same environment as `env --with-passwords` plus `PORT`, `NUXT_PORT`, `NITRO_PORT` and `HOST` (and each `--port-env` name), and a proxy route `NAME` for as long as it runs; signals are forwarded and the route is removed on exit. The guard allows it.
Sub-forms:
- `host start NAME [--port N] [--dir D] [--port-env A,B] -- COMMAND...` runs it detached: a supervisor outlives your terminal, writes `host-NAME.log` in the instance directory (truncated at each start), records itself in the manifest and removes its route on exit. Returns once it is recorded. Refused when NAME already runs.
- `host stop NAME` sends SIGTERM to the supervisor's process group, waits 10 s (`STACKFORK_HOST_STOP_TIMEOUT`, for example `2s`), then SIGKILL; removes the route. It signals only when the recorded PID, start time and command line still match; a stale entry is only cleaned up.
- `host restart NAME` stops it and starts it again with the same command.
- `host logs NAME [-f]` prints the log (secrets redacted); `-f` follows it.
`stackfork down` stops host processes before it runs the compose down. Known limit: if the supervisor itself is killed with SIGKILL (not through `host stop`), its command keeps running without a supervisor; `host stop NAME` still finds the entry, sees the supervisor is gone and cleans up, but cannot reach the orphaned command, so stop it by hand. A foreground `host` run from an interactive shell leads its own job group, so `host stop` can stop it too; inside a pipeline the signal also reaches the other members. On Windows it stops the process tree with `taskkill /T /F`, with no graceful stop; see SECURITY.md.
Flags: `--port`, `--dir` (working directory, relative to the checkout), `--port-env` (extra env names set to the port, comma separated)
```sh
stackfork host app -- bun run dev
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
Run the recipe's `seed_once` command (`learn record recipe --seed-once CMD [--seed-service SVC]`) in its service. `up` and `start-env` run it automatically, once, when that `up` creates the data volume (a script detected in the checkout counts when no decision is recorded; `--seed-once none` disables it). `seed` is the retry when that failed (the dashboard warns about a failed seed). Already seeded: does nothing; `--force` reruns it (agents: ask first, the guard blocks it). `--mark-done` records the instance as seeded without running anything, for data that is already there; the dashboard's failed-seed warning shows the end of the last run's output (`seed.log` in the instance dir).
Flags: `--force`, `--mark-done`
```sh
stackfork seed
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
A local web page (127.0.0.1-only) listing every instance, plus a Repos tab (find a repo under your configured folders, see its branches and worktrees, Start one) and a Settings panel for those folders.
Flags: `--port`, `--open`
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
