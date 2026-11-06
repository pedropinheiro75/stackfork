# Changelog

All notable changes to this project are documented here. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

- Instance addresses follow a pattern you choose. The new `hostname` setting (`stackfork config set hostname PATTERN`, or **Addresses** in the dashboard Settings) builds the address from `{app}`, `{worktree}` and `{hash}`. The default is `{app}-{worktree}`, for example `acme-web-feature-invoices.localhost`; the old format is `{worktree}-{hash}`. An instance that exists keeps its address, `--hostname` still wins, and a second instance with the same address gets a short suffix.
- `stackfork start SERVICE` works on a service that uses a Compose secret. It resolves the secret references like `up` does and gives the values only to the Compose child, so `stop` then `start` no longer fails with `environment variable ... is not set`. The dashboard's per-service Start checks the secrets first, like Start does, and shows the dashboard message when a reference does not resolve. `stackfork start SERVICE` does the same: it runs the `secrets` step first and lists every problem with its fix, instead of Compose's message for the first missing variable.
- A plain `up` and `seed` check the secrets the Compose file uses, like Start does. A name with no reference now fails at the `secrets` step with the fix, not later in Docker.
- A reference that `prepare --secret` recorded and that does not resolve now says how to leave it: give `prepare` a new `--secret NAME=REF`, or run `stackfork secret forget NAME`.
- `env set` with an argument that has no `=` no longer prints the argument back. The error names the expected form, `NAME=VALUE`.

## [0.2.1] - 2026-11-05

- Windows support (new). Releases ship `stackfork_VERSION_windows_amd64.zip` and `stackfork_VERSION_windows_arm64.zip`, and `install.ps1` installs one: `irm https://raw.githubusercontent.com/pedropinheiro75/stackfork/main/install.ps1 | iex`. The offline test suite runs on Windows in CI; the Docker flows are not tested on Windows yet (see the Windows section of `SECURITY.md`). Fixed on Windows: `docker compose config` now gets the system variables it needs (`Path`, `SystemRoot`, `ProgramData`), paths that git prints as `C:/x` are read as Windows paths, `STACKFORK_PROFILE_DIRS` is split on `;`, and the dashboard groups worktrees by `\.worktrees\`.
- New `internal/ecosystem` package: what stackfork knows per language ecosystem lives there, JS/TS first. `learn` and the stale check hash only the parts of `package.json` the scan reads (`scripts`, `packageManager`, `workspaces`). Bumping a dependency or a version no longer marks the knowledge stale; the install step already re-runs when the lockfile changes.

## [0.2.0] - 2026-10-30

First public release.

### Changed

- Host-command URL placeholders follow the proxy. While the proxy runs, `{{url}}`, `{{APP.url}}` and `{{service.NAME.url}}` expand to the portless URL the dashboard opens (for the app the proxy serves and for the service it routes), so a CORS origin such as `UI_URL={{app.url}}` matches the browser's Origin; other apps keep the port form. `proxy up`, `proxy down` and the dashboard's Start proxy and new Stop proxy buttons restart the running host apps whose command uses such a URL, as a `proxy` run per instance. A run in progress skips the instance; an untrusted plan is not restarted and the card warns to restart host apps.
- The dashboard's "Environment · SERVICE" dialog is easier to read. It lists the service's overrides first, as Variable, Project value and Your value, with a reset button per row; the other variables sit under "Show all N variables", read-only, with their origin (secret-like values stay hidden). A boolean shows "true" or "false" beside its toggle. The "OVERRIDE" badge is gone. Reset is pending until Save, with an Undo. The button reads "Save and restart SERVICE" and a "No changes" note explains why it is disabled. A long value wraps in full. The name field suggests the service's variables, and adding a name that exists edits its row.

### Fixed

- A plain `up` (CLI or dashboard) no longer seeds before the plan's install, build and migrate steps. It keeps the seed pending and says so; `start-env` runs the seed at its place in the plan. `start-env` also retries a seed that failed on the same data volume, so the dashboard no longer shows "Database seed failed" for it forever. A seed that succeeded or was marked done never runs again.
- A one-shot job that exited 0 (a migrate service) no longer makes its stack "partial": a host app can read the instance's env and start next to it, and the card shows no "exited" warning for it.
- `resume` also finishes a stack that an earlier resume left half started when the dashboard was forced to quit.
- The dashboard retries a resume it skipped because a run or lock was busy, logs why it skipped an instance, and the cascade watcher keeps its sightings across a busy tick, so a database lost meanwhile is still caught.
- Run cards and the version menu name a `resume` and a `cascade` run.

### Added

- How code changes reach each app. `knowledge.json` has a `reload` list: per Compose service or host app, `hot` (live reload), `restart` (code mounted or on the host, restart after an edit) or `rebuild` (code baked into the image). `learn` proposes it with file:line evidence from the bind mounts, the command, the `package.json` scripts and the Dockerfile `COPY`. `stackfork learn record reload SERVICE|HOSTAPP hot|restart|rebuild [--note TEXT]` records it, and `learn verify` warns while the entry app's mode is unconfirmed. The Instances card shows a quiet chip per service and host app ("hot reload", "restart on change", "rebuild on change") with a tooltip that says what to do after an edit, the Project page lists them under Configuration, and `stackfork status` adds `reload` to a row. The "Learn with your agent" prompt asks the agent to confirm hot reload by editing a visible string and reverting it.
- Resume what was running. Each instance records whether you want it running (`want_running` in its manifest): a whole-stack `up`, `start-env`, `reset` and Start set it; `down`, Stop, and a `stop` that leaves nothing running clear it; `up SERVICE` leaves it alone. When the dashboard starts (the macOS app's too), and again when Docker comes back after two failed polls, it starts every instance that wants to run and has no Compose container running, one at a time, as a `resume` run on the instance's card. It starts only the services the mode's plan starts and never seeds: a pending seed stays pending and the card says so. It skips an instance whose checkout is now on another branch, or that has a run or the instance lock in progress. Your data volumes are kept. `stackfork resume` does the same once from a terminal. `stackfork config set resume-on-start off` (or the Settings page) turns the automatic part off. Only one dashboard per machine watches (it holds `<state>/watch.lock`), so the macOS app's dashboard and a terminal one do not both act.
- Per-project stop cascade. With `plan.stop_cascade` on in `knowledge.json` (`learn record stop-cascade on`) and `depends_on` on host steps (`learn record step --depends-on postgres,api`), stopping a service or host app (`stop`, `host stop`, the dashboard) first stops the host apps that depend on it and prints `stopped postgres and its dependents: api, app`. The rows stay, so Start respawns them. The dashboard also watches for a dependency that vanished without a stop (a removed container): after two checks 15 s apart, with Docker answering and no run or action in flight, it stops the dependent host apps and records a `cascade` run. Off by default; a Docker error never stops anything. The watcher runs in the same loop as resume, after it and never on the tick that resumed; while Docker is out of reach and after a resume it starts over, so a service must be seen running again. A job that exited 0 (no ports and no healthcheck, or awaited with `service_completed_successfully`) is finished, not lost; a server such as postgres that exited 0 is lost. A service the mode's plan does not start (one a hybrid host app replaces) is not watched.
- `stackfork host rm NAME` (and Remove in a host row's menu) forgets a host app that is not running: its record, route and `HostProcs` entry.
- The "N env overrides" chip on the Instances page is a button that lists each override by service and name, with its value (hidden for secret-like names) and a button to that service's Environment dialog.
- `learn` records, per Compose service, whether its own start already runs a migrate or a seed (`startup_migrate` and `startup_seed` in `knowledge.json`, with file:line, and a line in `knowledge.md`). It reads the Compose `entrypoint` and `command`, the Dockerfile `ENTRYPOINT` and `CMD` the service builds, and the scripts they run. The proposed plan and the derived plan then show a note ("migrate runs in the app's entrypoint (file:line)") instead of a second migrate or seed step, and an auto-detected seed no longer runs on top of an entrypoint that seeds or migrates. `learn record step` and `learn verify` warn (never refuse) when an exec step repeats what the entrypoint runs, or when a `once: data` seed starts with the command of a `once: schema` step, and print the fix. The "Learn with your agent" prompt and `references/learning.md` tell the agent to check this first.

### Fixed

- Instances page, failed run card: it names the real command ("Env change failed at step restart-api", "Seed failed", "Reset failed", not always "Start failed") and has a Dismiss button. Dismiss hides that run in this browser; a newer run shows again.
- A host app that never listened on a port and exited 0 (a one-shot task started with `host start`) shows as "Finished · exit 0 at HH:MM" (neutral). It no longer makes the instance partial or adds a "not running" warning. A server that exited, and a non-zero exit, still warn. `host start` and the docs now point agents to `run`/`exec` for one-shot commands.
- A host app whose process lives but whose port does not answer for more than 30 s now shows "Running · not answering on :PORT" (attention), not a plain "Running" or an endless "Starting…". A host app with no port is "Running" while its process lives.
- Workflow canvas: a step's command stays on one line inside its card (cut with an ellipsis, full text in the tooltip), and a decision with one skip branch ("New data volume?") keeps a straight spine: the real branch sits under the pill and the skip line goes around it on the right.
- A secret the Compose source uses but nothing supplies (a top-level `secrets:` entry that a service or its build uses and that has no `file:`, `content:` or `external`, or an `environment:` variable that is unset) now fails the `secrets` step before prepare when no layer holds a reference. The line names the secret, the Compose file and the `secret map` command. Before, the step said "Skipped · no secrets" and `up` failed later with "container … is unhealthy". Such a name also counts as declared: a folder map applies to it and `secret ls` lists it (status `missing`). The step now runs `docker compose config` (read only).
- Project page: clicking a workflow step with a run overlay no longer fires the document's run-a-command handler. The node carried `data-run="failed"`, which posted `failed` to `/api/run` (toast "failed: not a command the page runs") and rewrote the node's text into one run-together line. The node now uses `data-state`. The step panel's failure log opens scrolled to its tail with whole lines, and its Retry does nothing while the Start button is disabled.
- A secret reference borrowed from a sibling worktree no longer fails a Start when it does not resolve here (`step secrets failed: NAME (env:VAR) missing` in a new worktree). The reference is skipped with a note naming the secret and the `secret map` command. A `--secret` flag and the instance's own reference still win.
- `learn record step --modes hybrid` on a plan whose `modes` lack `hybrid` (a scan with no app service writes `container` only) now adds the mode, so `start-env --mode hybrid` no longer fails with "mode hybrid is not supported by this plan". Record the step, then `plan --trust` again.
- `secret ls` and `secret check` in a linked worktree list the names that worktree's own knowledge declares (own, then main, then the newest verified sibling), not only the main checkout's.
- `references/learning.md` shows `learn record step --remove ID`, the real form.
- `host restart NAME` (and the dashboard's restart) runs the current command of the trusted plan's host step for NAME, expanded like `start-env` does, instead of the old recorded one. With an untrusted plan it keeps the recorded command and says the plan is not trusted here.
- In hybrid mode a host step replaces the Compose service of the same name (learn's default host step `app` next to a service `app`): the `compose-up` leaves that service out without an `exclude` (and a hybrid `up` stops one left running from container mode), and `plan` shows it as `exclude`. `env set|unset NAME` acts on the one that runs in the instance's mode, the host app in hybrid, else the container, instead of refusing the name. Container mode is unchanged.
- `config set-mode REPO MODE` on a linked worktree stores the mode under its main checkout, where it is read; before, it had no effect.
- A hybrid `up` stops a service its `compose-up` step excludes when an earlier container-mode run left it running, so it no longer answers next to its host app.
- Removing an instance ("Remove instance and data", `down --volumes --forget`, `prune`, `gc`) no longer loses its secret references. They lived only in the manifest, so the next Start prepared the checkout from its recipe without them (an `npm_token` from `--npmrc`, say) and the app could not install. The references now stay in `kept-secrets.json` in the state directory while the checkout exists, and the next prepare records them again, ahead of a sibling worktree's. Values are never stored.
- `start-env` no longer fails with "already running with a different command" after a plan's host steps were re-recorded: when the plan is trusted here, it stops the running app the safe way (recorded pid and start time, SIGTERM then SIGKILL) and starts the new command, printing `restarting NAME: the plan's command changed`. `up` and the dashboard Start do the same for a running host app that has a plan step (the same command stays "already running"). Untrusted plans and processes stackfork cannot identify are left alone.
- The `once: data` seed now runs at its recorded place in the plan when exec steps (install, build, migrate) sit between the `compose-up` and it, instead of right after the `compose-up`, where it failed on a fresh worktree with no dependencies or built packages. The new-volume decision is still made at `compose-up`. `plan`, the derived plan view and the run's step list show the seed where it runs. A data step right after `compose-up`, an unrecorded seed and a bare `up` behave as before.
- `start-env` no longer ends Succeeded when a host app fails to start. A host step whose app exits while it starts fails its run step, with the redacted log tail; one that declares a `port` or `port_env` and is still not listening when the wait ends fails too (a step with no port keeps the warning). The other host apps still start, then the run ends Failed naming the step and `start-env` exits non-zero.
- `learn record entry` and `learn record link` accept a host app (a `host` step of the plan or a recorded host app) as `--service`, with `--port` defaulting to its allocated port, so a hybrid project can record its real entry. The entry resolves to the host app's URL, `learn verify` probes it and says to start the instance while the app is stopped, and the refusal for an unknown name lists both Compose services and host apps.
- `host start` waits for the app to listen on its port (90 s by default, `--wait-timeout` or `STACKFORK_HOST_WAIT_TIMEOUT`) instead of failing after 10 s with "did not report". On timeout it stops the app and names the app, the port and the redacted last 15 lines of `host-NAME.log`.
- Host app URLs use the instance hostname (`http://<hostname>:<port>`) in `urls`, the dashboard and `/api/instances`, like the container services. `env` keeps 127.0.0.1 for the host-to-container database URLs.
- `up` and the dashboard Start start the host apps of a hybrid instance that are down (recorded command through `host start`, and the plan's host steps without a record when the plan is trusted), as one `host` step each.
- While a checkout's knowledge is a draft (`learn` scanned it, `learn verify` has not run), `up`, `start-env`, `reset` and the dashboard Start no longer run a migrate or seed nobody recorded: an auto-detected seed and a proposed-only plan step are skipped with a `seed not run: setup is a draft; record it with ...` note, recorded as a skipped run step. Recorded steps, verified knowledge and projects with no knowledge behave as before.
- Knowledge verified in any linked worktree now serves the whole repo. A checkout looks for `.stackfork/knowledge.json` in itself, then in the main checkout, then in the newest verified one among the repo's other worktrees (a draft in another worktree is never borrowed). Readiness, `plan`, `start-env`, `prepare`, `up`, `reset`, the entry resolver and the Repos panel agree, and the panel says `from worktree NAME (verified)` instead of `derived — not recorded; run learn`. Borrowed knowledge is read-only. `start-env` copies the untracked files the recorded prepare step names under `.stackfork/` or `.agent-dev/` (env file included, any name that is not a credential name) from that worktree when they are missing, never over an existing file, and prints where they came from. An env file outside those folders is still refused; the message names the file and the source checkout and says to copy it or record a `.stackfork/runtime.env` with learn.
- A stale network endpoint (Docker keeps one after a crash or a full disk) no longer breaks `up` and `reset` with "endpoint with name X already exists in network N". `up` force-disconnects it when N is a network of this project and X is not a running container, says so, and retries once. `down` force-disconnects leftover endpoints of this project's networks whose container is gone and removes the network. Other projects' networks and containers are never touched.
- `prepare` and `up` refuse a repository that was never learned (no knowledge file, adapter, instance or profile reference), like `start-env`; `--force` overrides it for people and the guard blocks it for agents. A draft and an existing instance are not blocked.
- A step with no service, and an auto-detected seed, never run in a web server or proxy image (nginx, caddy, traefik, httpd, haproxy, envoy, openresty). A detected seed no service can run is skipped with a note instead of failing `up`.

### Added

- `stackfork secret forget NAME [CHECKOUT]` removes a recorded secret reference from an instance (manifest and `kept-secrets.json`). The agent guard blocks it like `secret map`.
- Dashboard Project page (`#project/NAME`, linked from each repo name in Repos): header, checkouts with their last run, a read-only startup workflow canvas (Container/Hybrid switch, decisions, host apps that start together, last-run overlay, per-step panel with recent runs and the log tail of a failed step), resources charts (memory, CPU, disk per checkout; sampled from dashboard start, 6 h in memory) and configuration. New read-only routes `GET /api/project` and `GET /api/project/resources`.
- `exec` steps (in a container or `on: host`) take `once: deps`: the step runs on the first start and again when the hash of the project's lockfiles (`yarn.lock`, `package-lock.json`, `pnpm-lock.yaml`, `bun.lock[b]`, `go.sum`, `Gemfile.lock`, `poetry.lock`, `uv.lock`, `requirements*.txt`, `composer.lock`, `Cargo.lock`, at the root and in the step's `dir`) changed since its last success. The hash is stored in the manifest only on success. `learn record step --once deps` records it and the learn prompt asks for the install command.
- Placeholders in the `cmd` of host steps and host exec steps: `{{port}}`, `{{url}}`, `{{hostname}}`, `{{APP.port}}`, `{{APP.url}}`, `{{service.NAME.port}}` and `{{service.NAME.url}}`. They expand per argument in Go right before exec, never through a shell; an unknown one, or an app with no port, fails the step and names it. Every host app of the plan gets a stable port up front, so apps can point at each other and a second worktree no longer collides on fixed ports. The trust hash covers the text as written, so one approval works in every worktree. `plan --expand` (and `expanded` in `plan --json`) shows the values for the current checkout; `learn record step` and `learn verify` warn about a literal `port:`, `--port N` or hostname in a host command.
- Env overrides (`add-env-overrides`): `stackfork env set SERVICE NAME=VALUE`, `env unset SERVICE NAME`, `env overrides [SERVICE] [--json]` and `env overrides --clear [SERVICE]` flip an environment variable of one service of one instance without touching the repository. The values live in `env-overrides.json` (0600) in the instance's state directory and win over `env_file` and `environment`; containers get them through a derived second Compose file, so the snapshot and `config_sha256` do not change, and host apps get them after the instance's environment block. A change recreates only that service (`up -d --no-deps`) or restarts only that host app, as a run of command `env`; `--no-restart` stores it only. Overrides survive `up`, `down` and `reset` (`reset --clear-env` drops them) and go with `down --volumes --forget`. Secret-like names are refused with a pointer to `stackfork secret`; `--allow-secret` is people-only, like `overrides --clear`.
- Host app visibility: each `host` run writes `host-NAME.json` (pid, start time, redacted argv, cwd, port, exit time and code). The dashboard shows host apps with state, pid, memory, CPU, uptime or exit code, and `/api/instances` carries the same under `host`. An instance whose host app is down is `partial` with a warning. `stackfork host ls` prints the same from the terminal.
- Secrets from the dashboard: Settings has a Secrets table of every mapping (repository and folder) with status, **Set value…** and **Unmap**, and an **Add secret** dialog (name, reference, scope: a repository or a folder). Every repository in the Repos panel now shows its Secrets section, with "No secrets" and **Add secret** when it has none. `POST /api/secrets/map` and `/api/secrets/unmap` do what `secret map|unmap` do, take a reference and never a value, and need the dashboard token and Origin check; the guard blocks agents from them. **Set value…** also works for a folder mapping.
- `stackfork down --volumes --forget` removes the instance's state directory after a successful `down --volumes` (`--forget` alone is refused). The dashboard's "Remove instance and data" now runs it, so the card disappears instead of staying as Stopped; a later Start prepares it again. The guard blocks `--forget` for agents.
- macOS app (`add-macos-app`): `Stackfork.app`, a menu bar app that runs its own dashboard (`stackfork dashboard --supervised`), shows instances with **Start** and **Stop**, opens the dashboard in a window and can open at login. `--supervised` listens on its own stable port (`<state>/dashboard/app-port`), prints one JSON ready line (URL, port, token, pid, version) as its first output, registers no `stackfork.localhost` route and exits when standard input closes; it refuses `--port` and `--open` (exit 2). `scripts/build-macos-app.sh VERSION` builds a universal, ad-hoc-signed app and a DMG with a `.sha256` file; the release workflow attaches both. The app is not notarized: clear the quarantine attribute as the README says.
- Folder secret mappings (`add-secret-vault`): `stackfork secret map FOLDER NAME=REF` maps a reference once for every repository under a folder that declares or recorded NAME. A repository's own mapping beats a folder's, a nearer folder beats a farther one, and a folder never adds a name to a repository. `secret ls` shows the source `folder` and the folder it came from (`from`); the dashboard shows both on the reference. The root, your home directory, files and missing paths are refused. The guard still blocks `secret map|unmap` for agents.
- Derived plan (`add-derived-plan-view`): for a repo whose knowledge is missing or has no plan steps, the Repos plan list and `stackfork plan` show the steps a Start would run, from the same code as `start-env` (the implicit `prepare` and `up`, the seed entry, the prepare recipe from the instance, knowledge, an adapter file or a prepared sibling), each with its source, under the label `derived — not recorded; run learn to record it`. A start that would be refused shows the reason instead. Deriving writes nothing and runs no plan step, no Docker and no host plan command (only read-only git queries). The refusal it shows is the one `start-env` prints; `start-env --force` can override it for a project that only needs learn. The "Starts with" recipe line is gone. `stackfork plan --json` adds `derived`, `prepare_from`, `refusal`, `gap` and a `source` on each step.
- Worktree recipes (`improve-worktree-recipes`): a linked worktree that prepares without a Compose file the main checkout's last prepare used gets a warning from `prepare`, `start-env`, `reset` and the dashboard card, naming both lists (never a refusal). `stackfork learn verify` now needs a prepared instance and writes the plan's `prepare` step from it (every `-f`, the `--env-file`, kept `--hostname` and port flags; it may need `stackfork plan --trust` again). `stackfork reset --from-main` rebuilds a worktree from the main checkout's recipe: it checks everything first, moves the worktree's untracked adapter files to `.stackfork-aside/<UTC time>/` inside the `prepare` step (nothing is deleted, tracked files stay), copies the main checkout's files and runtime env and prepares with them. The dashboard offers **Reset from main checkout…** in the card menu, the worktree row menu and the gap warning. The guard blocks `reset --from-main` like `reset`.
- Host exec steps (`add-host-exec`): an `exec` step with `on: host` runs its command on the host, in the checkout or a confined `dir`, with the instance's environment (`DATABASE_URL` and the other URLs `env --with-passwords` prints). Use it for a migrate or seed when only the database runs in a container. It runs without a shell, needs this machine's trust like a `host` step (a plan without `on: host` keeps its hash), is refused before any Docker call when untrusted, and is a recorded run step with a redacted log. A host seed runs once on a new database, after a host migrate with the same `dir`. `stackfork learn record step --on host|container` records it, `stackfork plan` shows it, and a missing-service error suggests it when no service of the stack can run the app. Live mode skips host exec steps. Windows code is built and vetted, not run on a Windows machine.
- App entry and quick links (`add-learn-entry-links`): `knowledge.json` can record the app's `entry` (a Compose service, its container port and an optional path, or `none`) and named `links` (a path on the entry, or another service of the stack, such as `Email` or `API docs`). `stackfork learn record entry` and `learn record link` write them, checked against the prepared instance; `learn` proposes an entry from its own guess; `learn verify` needs a recorded entry that answers one GET on the running instance and probes each link. The proxy route, the inventory's App links, `stackfork urls` and `stackfork open [NAME]` follow the record and fall back to the old guess when none is recorded. Targets stay inside the instance and are outside the trust hash (SECURITY.md). The learn prompt asks the agent to record them.
- Live mode (`add-live-mode`): `stackfork live on|off|status [REPO]` and a **Live** switch in the Repos view keep the running instances of a repository in step with their checkouts. While the dashboard runs, it fetches each upstream, fast-forwards a clean checkout and rebuilds what changed: images for a changed Dockerfile or lockfile, the Compose model for a changed Compose or adapter file, and the schema for a new migration file. Every reconcile is a recorded run with `trigger: live` and its cause. A failed run pauses live mode until **Resume**, a plan with a changed `host` or `prepare` step pauses until you trust it, and no live run removes a volume. Live mode does not run while the dashboard is stopped, and the repository is the only desired state. `learn record live-watch`, `live-ignore` and `fact migration-dir` adjust what is watched. The guard blocks `live on` for agents.
- Branch cleanup (`add-live-mode`): when a worktree's branch is merged into the default branch (proved from the local repository, optionally labeled by `gh`), the Repos view and the instance card offer **Clean up**, which removes the worktree, then the instance with its volumes, then (if set) the local branch. `stackfork live cleanup off|suggest|auto [--grace HOURS] [--delete-branch|--keep-branch]` sets the mode (default `suggest`, 24 hours), and `stackfork live clean [REPO] BRANCH [--yes]` previews or runs one cleanup. **Keep** holds a branch until it gets a new commit. The guard blocks `auto`, `--delete-branch` and `live clean --yes` for agents.
- Environment reset (`add-env-reset`): `stackfork reset [--yes]` throws away one worktree's environment, volumes included, and starts it from zero from the code in the worktree, as a recorded run with the steps `sync`, `secrets`, `down`, `prepare`, `up` (rebuilding images), the plan's schema and data steps and the host steps. It never touches the code: when the checkout is behind and cannot be fast-forwarded it asks before going on with the current code. The CLI lists the volumes and asks `y/N` (refuses without a terminal and without `--yes`). The dashboard has "Reset environment..." in the instance and worktree menus, with a native confirmation naming the volumes. The guard blocks `reset` for agents.
- Secret vault (`add-secret-vault`): `keychain:ITEM` (macOS Keychain, Linux Secret Service, Windows Credential Manager target `stackfork/ITEM`, at most 2560 bytes) and `op://VAULT/ITEM/FIELD` (1Password) references, a machine `secrets` map in `config.json` and secret names in `knowledge.json` (`learn record secret`). `secret ls|check|set|map|unmap` never print or take a value. Every run starts with a `secrets` step that fails before Docker with each problem and its fix. Values go only to the Compose child (`os.Setenv` is gone) and are redacted in logs. The `--secret` flag now accepts only `env:` and `npmrc:` references; `keychain:` and `op://` come from `secret map`. `run` and `exec` redact their output with the values known for the instance. The guard blocks agents from `secret set|map|unmap`, store reads of stackfork items and mapped `op://` reads. The dashboard Repos panel has a Secrets table with Resolves, Missing, Locked, Not checked, Shell only, Unavailable and Error chips, **Check**, and a hidden-answer **Set value…** dialog. Windows code is built and vetted, not run on a Windows machine.
- Every start begins with a `sync` step: it fetches the checkout's upstream, fast-forwards a clean checkout with no local commits, and otherwise touches nothing and warns how many commits it is behind and how to update. A failed fetch is a note, never a failed start.
- `worktree add` for a branch that is already checked out names that checkout and the `start-env` command for it.
- Plan runs (`add-plan-runs`): each `start-env`, dashboard Start, `up` and `seed` is recorded as a run with its steps, states and step logs under the instance's `runs/` directory, the last 20 kept. `stackfork runs` and `runs show [ID] [--step STEP]` read them. The dashboard's Repos list shows the live plan of a start, each instance card shows its last run, and the log drawer reads any step of a run. A recorded `up` and `start-env` merge Compose's stderr into stdout in the step log (and the terminal output), so the two streams are no longer separate.
- Startup plan (`add-learn-plan`): `learn` records an ordered plan of steps (prepare, compose-up,
  exec, host, note) instead of a flat recipe. Check it with `stackfork plan [--mode] [--json]`.
  Hybrid mode (`config set mode hybrid`, `config set-mode REPO hybrid`, `start-env --mode`) runs
  your app on the host and the rest in containers. Host steps run only after you trust them with
  `stackfork plan --trust` or the dashboard's Trust button. `host start|stop|restart|logs NAME`
  runs an app detached and supervised, and `down` stops it first.
- Windows support (`add-learn-plan` slices 2 and 3): host steps, instance locks, the dashboard's
  trust and confirm dialogs, the Learn terminal and the agent guard (Windows paths, `cmd /c` and
  `powershell -Command`) are built for native Windows, and state and config live under
  `%LOCALAPPDATA%`. `env --format powershell` prints PowerShell lines. It is built and vetted but
  has not run on a Windows machine; the limits are in SECURITY.md.
- `doctor` reports a `host apps` warning for a host app whose supervisor is gone but whose port
  still answers.
- A new worktree of a repo that another worktree already runs can be started: `start-env` (and
  `worktree add`) rebuild the recipe from that sibling's prepared instance, copying its untracked
  adapter files and runtime env with checkout paths rewritten (never `.env`, keys or tokens), and
  `ready` now means a worktree is startable. A failed Start shows on the repo row with the CLI command to retry.
- Repo browser (`add-repo-browser`): `stackfork config` (scan roots, excludes, max
  depth in `config.json`), `stackfork repos [--json] [--rescan] [--prompt REPO BRANCH]`
  (repos under the roots with branches, worktrees, instance state and a
  `ready`/`draft`/`unknown` readiness, local git only, mtime-cached),
  `stackfork worktree add REPO BRANCH [--from BASE]` (`<repo>/.worktrees/<branch>`,
  local git exclude, allowlisted adapter-file copy with a credential denylist) and
  `stackfork start-env [CHECKOUT]` (prepare when needed, then up). The dashboard
  gets a Repos tab (search, ready-only, Start, "Set up with your agent", per-repo
  Fetch, copy-only worktree removal), a Settings panel for the roots
  (`POST /api/config`, server-side validation) and `GET /api/repos`
  (byte-identical to `repos --json`); new POST actions `start`, `worktree-add`
  and `fetch` only accept repos and worktrees the scan found under a root.
- `stackfork prune [--orphaned|--stopped|--all] [--older-than DURATION] [--keep-volumes] [--yes] [--json]`
  (`add-prune`): dry run by default (table, groups, disk estimate), typed
  confirmation for running instances or `--all`, exit 1 without a TTY and
  `--yes`. Removes containers, networks, built images, state, routes and
  volumes (unless `--keep-volumes`) of exactly the matching instances, legacy
  `idv-` included; never by name prefix, never `docker system prune`. `gc` is
  now an alias of `prune --orphaned --keep-volumes`. The guard blocks
  `prune --yes`, `prune --all` and `gc --apply` for agents; the dashboard has a
  read-only "Clean up" panel in the header menu (`GET /api/cleanup`).
- The proxy container starts with `--restart unless-stopped` (an existing one
  is updated on the next `proxy up`), and `proxy up` starts a stopped proxy
  instead of recreating it. `status`, `urls`, `doctor` and the dashboard warn
  when routes exist but the proxy is down.
- Agent guard (`add-agent-guard`): `stackfork guard [--tool claude|codex|copilot]`
  blocks a dev server (`bun|npm|pnpm|yarn dev|start|serve`, `nuxt dev`, `vite`,
  `next dev`, `nodemon`) or a raw `docker compose up/start` started directly in
  a stackfork-managed checkout, and allows everything else;
  `stackfork hooks install|uninstall|status` wires it into Claude Code, Codex
  and Copilot CLI (idempotent, existing hooks kept, a backup first).
- `stackfork host NAME [--port N] -- COMMAND...`: the sanctioned host-run path
  (stable port, `env --with-passwords` plus `PORT`/`NUXT_PORT`/`NITRO_PORT`/`HOST`,
  a proxy route while it runs, signals forwarded).
- Global `--instance NAME` on up, down, restart, start, stop, status, urls,
  logs, exec, run, env, open and host; a checkout with no stack now lists this
  repository's instances in other worktrees, and `restart <instance-name>` says
  it is an instance, not a service.
- `stackfork open [SERVICE] [--print]`: the instance's primary app URL.
- `status`, `list` and the dashboard hint `stackfork restart <service>` for an
  unhealthy or exited service (`status` adds an optional `hint` to the JSON row).

- `assets/go/`: an optional Go web app template (mirrors the Nuxt one -
  app + Postgres, `go mod download` entrypoint, optional private-module
  netrc via a Compose secret). `assets/nuxt.compose.yaml` and friends moved
  to `assets/nuxt/` alongside it, so a template's assets live under its own
  runtime rather than flat at the top level. See `references/go-template.md`.
- `stackfork list` / `list --json`: every instance across every checkout,
  from a new `internal/inventory` package shared with the dashboard, so the
  two can never disagree.
- `stackfork dashboard [--port N] [--open]`: a local, read-mostly web page
  (embedded assets, no Node toolchain) showing the same inventory, grouped
  by checkout, with clickable HTTP endpoints, learned-knowledge status and
  warnings. Safe actions only (`up`, `down` with volumes always kept,
  `restart SERVICE`, log viewing, proxy up/down/reload), behind a
  127.0.0.1-only bind, a `Host` allowlist, and a per-process action token.
  Destructive or input-heavy commands (`down --volumes`, `gc --apply`,
  `prepare --reassign-ports`, `clone-db`, `learn`) show up as a command to
  copy, never a button.
- `stackfork restart SERVICE` and `stackfork logs [SERVICE] [--tail N]
  [--follow]`, and a new `internal/redact` package that replaces resolved
  secret values in log output with `[redacted:<name>]`.
- `stackfork env [--format shell|dotenv] [--with-passwords]`: the current
  instance's `DATABASE_URL`/`REDIS_URL`/`MONGODB_URL`, `SMTP_HOST`/
  `SMTP_PORT`, and one `<SERVICE>_URL` per HTTP service (the portless proxy
  URL when routed), for a host-run or hybrid app - `eval "$(stackfork
  env)"`. Reuses the dashboard's own env-block/connection-URL logic; a real
  password only ever appears with `--with-passwords`, and only for a
  literal Compose value, never `--secret`/`env_file`/`${VAR}`.
- `stackfork clone-db --from-dump FILE [--service NAME] [--database NAME]
  [--replace]`: seeds this instance's own running database from a local
  `.sql`, `.sql.gz`, or Postgres custom-format dump, streamed directly into
  the container (never a temp copy on disk). Postgres supports all three
  forms; MySQL/MariaDB gets plain SQL.

### Changed

- `stackfork plan` in a checkout without `knowledge.json` (or with a plan without steps) now prints the derived plan and exits 0; before, it failed with `no startup plan; run stackfork learn first`. A `knowledge.json` that cannot be read still fails.
- The seed is a step of its run: `seed.log` is no longer written, and the seed's output is the seed step's log. A failed seed now stops `start-env`.

### Fixed

- `start-env`, `reset` and `prepare` in a linked worktree copy the untracked adapter files the prepare args name (`-f`, `--env-file`, e.g. `.agent-dev/compose.yml`) from the main checkout when the worktree lacks them, with the sibling recipe's rules (runtime env paths rewritten, no `.env`, keys or token-bearing `.npmrc`, never over an existing file). A missing file that git tracks in the main checkout is refused by name.
- `seed` in a stack that lacks its service says so and names the stack's services and the `learn record step` command, instead of "Specify a service in this snapshot and a command".
- A stopped or exited host app stays as an exited row with Start; `down` forgets it.
- The card's Start on a hybrid instance runs the startup plan, not a plain `compose up`.

### Rewritten in Go

The CLI (`scripts/compose.py`, Python standard library) has been ported to
Go: module `github.com/pedropinheiro75/stackfork`, standard library only,
building a single static binary. Behaviour was ported for parity first
(golden tests comparing the Python and Go `prepare` output on shared
`testdata/` fixtures), then the Python CLI and its tests
(`scripts/compose.py`, `scripts/test_compose.py`, `scripts/test_template.py`)
were deleted once the Go binary reached parity. Tests run offline with
`go test ./...`; `STACKFORK_LIVE=1 go test ./...` adds the Docker-backed
tests.

### Renamed to stackfork, with legacy compatibility

The tool is renamed from `isolated-dev` to `stackfork`
(prefix `sf`, label namespace `io.stackfork`, state directory
`~/.local/state/stackfork`). Existing `idv-`-prefixed instances are not
migrated or renamed: `where`, `status`, `urls`, `list` and `down` still read
`~/.local/state/isolated-dev/` and recognise `idv-` instance names and
`io.isolated-dev.root` labels, so stacks created before the rename stay
manageable without data loss. `RR_DEV_HOSTNAME` is still exported alongside
the new `DEV_HOSTNAME` for existing adapters, but is deprecated in favor of
`DEV_HOSTNAME`.

### Private profiles

Client- and company-specific material (recipes, references, assets) no
longer lives in this repository. It moves to a *profile*: a directory the
user owns outside the repo, selected with `--profile-dir` or
`STACKFORK_PROFILE_DIRS`, or picked up automatically from
`~/.config/stackfork/profiles/`. The CLI never reads profile content; the
agent skill reads the matching reference and follows the profile's
`context.md` if present.

### Synthetic fixtures replace real checkouts in tests

The live and parity test suites now run against synthetic multi-service
Compose fixtures under `testdata/` instead of a local, private checkout, so
`--live`/`STACKFORK_LIVE=1` passes on a machine with no prior project
checkouts.

### Removed

- The Python CLI (`scripts/compose.py`) and its tests (`scripts/test_compose.py`,
  `scripts/test_template.py`). All CLI code is now Go.

### Deprecated

- `RR_DEV_HOSTNAME` environment variable: still set for backward
  compatibility, superseded by `DEV_HOSTNAME`.
