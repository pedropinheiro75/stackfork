# The learn session

`stackfork learn` only collects facts: a deterministic, offline scan of Compose files, entrypoints, scripts and env-key names, with no LLM judgment and no network access. It writes `.stackfork/knowledge.json` and human-readable `.stackfork/knowledge.md` with status `draft`, plus a `.stackfork/.gitignore` that keeps `runtime.env` and any real `.env` out of git. Unlike `.agent-dev/` (`references/worktrees.md`), `.stackfork/` is meant to be **committed** — it is project knowledge, not private local state. A checkout reads its own `knowledge.json`, else the main checkout's, else the newest *verified* one in another linked worktree of the repo (drafts elsewhere are not borrowed, and borrowed knowledge is never written back); the Repos panel and `plan` say which worktree it came from, and `start-env` copies the untracked adapter files (env file included) the recorded prepare step names from there when the worktree lacks them. Re-running `learn` on a `draft` resumes it instead of starting over; it also re-scans a `stale` file (one whose recorded inputs changed) and reports only what changed.

The scan never records a secret value — only variable names and their inferred purpose (database, auth, mail, storage, registry). Read `.stackfork/knowledge.md` after scanning: it lists services, images, database version, ports, volumes, networks, isolation findings (what `prepare` would refuse or rewrite, from `references/adapting-compose.md`), the migrate or seed a service's own start already runs (Compose `entrypoint`/`command`, the Dockerfile `ENTRYPOINT`/`CMD`, and the scripts they run; also `startup_migrate` and `startup_seed` per service in `knowledge.json`), and risky startup behaviour (resets/seeds on every start, scripts that rewrite `.env`/`.npmrc`, or that hardcode `docker compose`/`docker exec` names) with file:line.

## Running the session (agent-driven, not scripted)

The CLI only scans; you drive the rest, because it needs judgment the CLI can't apply on its own:

1. **Scan.** Run `stackfork learn` and read `.stackfork/knowledge.md`.
2. **Write the adapter.** Follow `references/adapting-compose.md` (and `references/nuxt-template.md` if applicable), informed by what the scan found.
3. **Try it.** Run the plan: `stackfork plan` lists the steps, then `prepare` → `up` → the project's real migrate/seed-once command. Read the startup findings and the first-start logs first: when the app's container already migrates or seeds on start, do not record a `migrate` or `seed` step for it (the proposal shows a note instead; say so in a note). A seed step must not start with the migrate command: stackfork already runs the schema step before the seed. `learn record step` and `learn verify` warn about both, with the fix. Before you record any step, `plan` prints a derived plan: what Start does today, with the source of each step. Record it with `learn record step` to make it the plan of the project.
4. **Verify.** Database identity and app readiness at minimum; a representative flow when this is the project's first adoption.
5. **On failure,** fix the adapter (or ask the user when the fix isn't safe to pick alone), retry, and record what broke and how it was fixed.
5b. **Confirm the seed command.** If the app's entrypoint already seeds, the scan proposes no seed step: record `--seed-once none` or a note. When the scan found a seed or migrate script it pre-fills `recipe.migrate` and a proposed `recipe.seed_once`, marked pending. Confirm the command with the user and record it with `learn record recipe --seed-once "<cmd>"` (add `--seed-service SVC` when it is not the primary app service), or `--seed-once none` if this project must never be seeded. Without a decision the default is automatic: `up` seeds a new database (one whose data volume `up` just created) with the detected script, once, after migrations and health; a database that already existed is never seeded. `learn verify` only warns when no decision was recorded. `stackfork seed` retries a failed seed; `--force` reruns it (ask first). While the knowledge is still a `draft`, `up`, `start-env`, `reset` and the dashboard Start run no migrate or seed that nobody recorded: an auto-detected seed and a plan step that is only proposed are skipped with `seed not run: setup is a draft; record it with ...`, and the seed runs on a later `up` once you record it (`learn record step` or `learn record recipe`). Recorded steps and verified knowledge are not affected.
6. **On an ambiguous or unsafe decision** (several seed datasets, an optional service, where a credential comes from), ask the user — never guess — and record the question and their answer.
7. **Ask the user to confirm the app works.** Never self-verify: knowledge only moves from `draft` to `verified` on the user's own confirmation, and the confirmation is recorded, quoted, in the knowledge.

## Recording answers, gotchas and the recipe

Never hand-edit `knowledge.json` to add these — always the CLI, so the file stays valid and atomic writes hold:

```sh
stackfork learn record answer "<question>" "<answer>"
stackfork learn record gotcha "<problem>" "<fix>"
stackfork learn record recipe --prepare "<cmd>" --up "<cmd>" [--migrate "<cmd>"] [--seed-once "<cmd>"|none] [--seed-service SVC] [--login "<step>" ...]
```

To declare a secret the project needs, use `stackfork learn record secret NAME [--hint TEXT]` (or `--remove NAME`). It records the name and a hint for the person, never a reference or a value. Ask the user to set the value (`stackfork secret set`) and map it (`stackfork secret map`); run `stackfork secret ls` or `secret check` to see the state.

`record recipe` is an alias that writes plan steps and marks the plan recorded. It sets only the flags given in each call, so you can record it step by step as the session goes. Each `record` call requires a prior `stackfork learn` (there is no draft to record into otherwise).

## The startup plan

The recipe is a startup plan: an ordered list of steps in `.stackfork/knowledge.json` (schema 2; a schema 1 file is converted when read). `learn` proposes the plan, and you correct it with `learn record step`. A step has an `id` and a `kind`:

| Kind | What it does |
|---|---|
| `prepare` | Runs `prepare` with the recorded arguments. |
| `compose-up` | Starts the stack. `exclude` lists services to leave out. |
| `exec` | Runs `cmd` in an already-running `service`. Set `on: one-off` for a temporary container, or `on: host` for the host. `once` is `data` (seed, new database only), `schema` (migrate) or `deps` (install dependencies). |
| `host` | Runs `cmd` on the host. `cmd` is an argument vector: no shell, so no pipes, redirects or `&&`. |
| `note` | Shows `text` to the person. Runs nothing. |

A `host` step has a `name`, an optional `dir` and `port` (`--port N`, 1 to 65535; leave it out and use `{{port}}`, see below), and `port_env`. A `host` step can list `depends_on`: the Compose services or other host apps (by `name`; `host:NAME` for an app, checked) it needs (`--depends-on postgres,api`). `learn record stop-cascade on` sets `plan.stop_cascade`: stopping or losing a service then stops the host apps that depend on it (they stay as exited rows, and Start brings them back). Record `depends_on` for each host app that needs the database or another service, and turn `stop-cascade` on when the person wants a lost database to take its apps down instead of leaving them running against nothing. It is off by default. A `prepare` step takes `args` (`--args -f,compose.yaml`). The step always sets `PORT`, plus each name in `port_env`. `no_seed` records the answer "never seed".

An `exec` step with `on: host` runs `cmd` on the host, in the checkout or in `dir` (relative, inside the checkout), with the instance's environment: the `DATABASE_URL`, `REDIS_URL`, `SMTP_HOST` and `<SERVICE>_URL` values that `stackfork env --with-passwords` prints, plus `STACKFORK_HOST_RUN=1`. Use it when no container can run the project's migrate or seed, for example when only the database runs in Compose. `cmd` is an argument vector like a `host` step: no shell, so no `&&`. A host exec step takes `cmd`, `once`, `modes` and `dir`, and no `service`, `name`, `port` or `port_env`. Put it after the `compose-up` step: it needs a running instance. It runs in `container` mode and in `hybrid` mode, and does not make `hybrid` available by itself.

When the `once: data` seed and the first `once: schema` step are both `on: host` with the same `dir`, the seed entry runs the migrate first and the seed second, as two commands, and stops at the first failure. The migrate step then shows as skipped (`ran with the seed`). A migrate and a seed that do not both run on the host each run at their own place in the plan. An auto-detected seed always runs in a container.

The seed runs where it is recorded. The decision "is this a new data volume?" is made at `compose-up`, where the volume is created, but the seed executes at the place of the `once: data` step. Record `install`, `build` and `migrate` steps before it and they run first (the migrate prefix still applies, and the separate migrate step shows as skipped when the seed runs it). Only a data step that comes right after `compose-up`, or a seed nobody recorded (auto-detected or from an adapter), seeds right after the `compose-up`, as before. `stackfork up` alone (and the dashboard's plain up) seeds right after the `compose-up` only when nothing the seed needs sits between them. When the recorded plan has install, build or other exec steps there (a migrate the seed command runs itself does not count), `up` does not seed: it keeps the seed pending, prints `seed pending: it runs with start-env, after the plan's install/build/migrate steps (stackfork start-env)`, and `start-env` runs it at its place. `start-env` also retries a seed that failed on this data (never one that succeeded or was marked done with `seed --mark-done`). `stackfork plan` shows the seed at the place it runs.

#### Placeholders in host commands

Do not write a port or a hostname into a `host` step or a host `exec` step. A second worktree would collide with the first, and the hostname differs for each instance. Write a placeholder in `cmd` instead. Each argument is expanded right before the command runs, after `cmd` is split into arguments. No shell reads the result, so a value stays one argument. This includes the `KEY=VALUE` arguments after `env`.

| Placeholder | Value |
|---|---|
| `{{port}}` | The port of this host app (host steps only). |
| `{{url}}` | `http://<hostname>:<port>` of this host app (host steps only). |
| `{{hostname}}` | The hostname of this instance. |
| `{{APP.port}}`, `{{APP.url}}` | The same for the host app `APP` (its step `name`) of this plan. |
| `{{service.NAME.port}}`, `{{service.NAME.url}}` | The host-published port and URL of the Compose service `NAME`, as `urls` shows them. A service that is not HTTP has no scheme: the URL is `127.0.0.1:PORT`. |

- A host step with no `port:` keeps the stable port of the instance. Every host app of the plan gets its port before the first command that names it runs, so `{{api.port}}` works before `api` starts.
- Only the names in the table expand. Other `{{...}}` text (Go templates such as `{{.State.Health.Status}}`, Helm, `{{json .}}`) stays as written. A typo such as `{{prot}}` also stays as written, and `learn record step` and `learn verify` warn about it.
- A name in the table that cannot resolve (an app that is not in the plan, an app with no port, a service that is not published) fails the step and names the placeholder. Nothing runs.
- A value with whitespace, a control character or a leading `-` is refused.
- A host exec step runs under the instance lock, so it cannot allocate ports. `up` and `start-env` allocate them before the first step. A host exec step that names an app with no port fails with that app's name.
- `{{port}}` is the port the route uses: `--port`, else the remembered or allocated port. A literal `port:` in the step reaches it only as the `--port` flag of a plan start.
- A running app keeps the peer URL it started with. If the peer's port changes, restart the app (`host restart NAME`) to pick up the new one.
- The trust hash covers the text with the placeholders as written. Trust the plan once for each machine. It works in every worktree.
- `stackfork plan --expand` shows the values for the current checkout. `plan --json` has them in `expanded`.
- `learn record step` and `learn verify` warn when a host step has a literal `port:`, a `--port N` or `:N` in `cmd`, or the instance hostname.

Example: a project with an API, a web app and a database browser, with no fixed port:

```sh
stackfork learn record step --id api --kind host --name api --dir apps/api --modes hybrid \
  --cmd "env UI_URL={{app.url}} bun run dev --port {{port}}"
stackfork learn record step --id app --kind host --name app --dir apps/web --modes hybrid \
  --cmd "env VITE_GRAPHQL_API_URL={{api.url}} vite --port {{port}} --strictPort"
stackfork learn record step --id studio --kind host --name studio --dir apps/api --modes hybrid \
  --cmd "prisma studio --port {{port}}"
```

#### Dependencies after a sync

`start-env` fast-forwards the checkout, so a new commit can add a dependency. Record the install command as an `exec` step with `once: deps`, on the host (`--on host`) or in a container, before the build and migrate steps. Use `yarn install --frozen-lockfile`, `npm ci`, `pnpm install --frozen-lockfile` or `bun install`.

```sh
stackfork learn record step --id install --kind exec --on host --once deps --dir apps/api --cmd "yarn install --frozen-lockfile" --before migrate
```

The step runs on the first start, and again when the hash of the lockfiles changed since its last successful run for this instance. The lockfiles are `yarn.lock`, `package-lock.json`, `pnpm-lock.yaml`, `bun.lockb`, `bun.lock`, `go.sum`, `Gemfile.lock`, `poetry.lock`, `uv.lock`, `requirements*.txt`, `composer.lock` and `Cargo.lock`, at the checkout root and in the step's `dir`. The hash is stored in the manifest only when the step succeeds, so a failed install runs again. An unchanged step shows as skipped (`lockfiles unchanged`). The step is part of the trusted plan text like any other step.

After the first Start, check that the host command used this instance's database: a tool that reads its own `.env` file can ignore `DATABASE_URL`.

Record, change or remove a step:

```sh
stackfork learn record step --id web --kind host --name web --cmd "bun run dev" --port-env VITE_PORT --modes hybrid
stackfork learn record step --id migrate --kind exec --on host --cmd "bun run db:migrate" --once schema
stackfork learn record step --remove web
```

`learn verify` needs a recorded plan with a `compose-up` step.

### Modes and trust

Each machine runs the plan in a mode: `container` (default) or `hybrid`. Set it with `stackfork config set mode hybrid`, per repo with `stackfork config set-mode REPO hybrid`, or for one run with `start-env --mode`. Hybrid mode drops the app service and every service that depends on it, and runs the `host` step instead. A host step named like a Compose service replaces it: the `compose-up` leaves that service out without an `exclude`, and a hybrid `up` stops one left running from container mode.

A host step runs a command from a file in the repository, so a person must trust it first. Trust is needed only when the active mode runs a host step or an `exec` step with `on: host`. The trust hash covers all `host` and `prepare` steps and all `exec` steps with `on: host`. Adding a field to a step changes the hash and needs a new trust.

To trust the host steps:

1. Run `stackfork plan --mode hybrid` and read every host and prepare step.
2. Run `stackfork plan --trust`.

Or click **Trust** in the dashboard: it shows every field of the plan in a native dialog and trusts exactly the plan shown. Do not run `plan --trust` yourself and do not write the trust file. The guard blocks `plan --trust` for agents. Ask the person to do it.

Trust is stored once for each main checkout. Two worktrees with different host steps invalidate each other's trust.

## Live mode overrides and migration directories

Live mode watches a set of files that it derives from the plan: build contexts, Dockerfiles, lockfiles, Compose and adapter files, and migration directories. `learn` records the migration directories it finds in `facts.migration_dirs`: the existing ones among `prisma/migrations`, `db/migrate`, `migrations`, `drizzle`, `supabase/migrations`, `alembic/versions`, `src/migrations` and `database/migrations`, at the repository root and in each build context. The scan only lists directories and runs nothing. Add one that the scan missed:

```sh
stackfork learn record fact migration-dir db/changes
stackfork learn record fact migration-dir db/changes --remove
```

When the plan has no `once: schema` step, a change in a migration directory has no action, and `live status` says so.

To change the watch set for this repository, use the `live` object of `knowledge.json`:

```sh
stackfork learn record live-watch config/features.yaml --action prepare
stackfork learn record live-watch services/api/seeds --action rebuild --service api
stackfork learn record live-ignore docs
stackfork learn record live-watch config/features.yaml --remove
```

- `--action` is `rebuild` (rebuild `--service` and recreate it), `prepare` (run `prepare` and `up` again) or `migrate` (run the plan's schema steps). `rebuild` needs `--service`, and the service must exist in the instance.
- A path is relative to the checkout, uses `/`, has no `..` and stays inside the checkout.
- `live-ignore` stops watching a default path.
- The `live` object and `migration_dirs` are not part of the plan's trust hash. Only a changed `host` or `prepare` step needs a new trust.

## Entry and quick links

stackfork guesses where the app is. Record the real answer: read the README, the docs and the Compose file for the local URLs that matter (the app, an admin or dashboard page, a mail UI, API docs, Storybook), open each one, and record what answers.

```sh
stackfork learn record entry --service nginx --port 80 [--path /app]
stackfork learn record entry --service app     # a host app of a hybrid project
stackfork learn record entry --none            # a worker or CLI with no web entry
stackfork learn record link "API docs" --path /api/docs
stackfork learn record link Email --service smtp4dev --port 80
stackfork learn record link --remove Email
```

- `--port` is the container port of the Compose service, never a published host port. A link with `--path` only is a path on the entry; a link with `--service` and `--port` is another service of the stack. A name is unique without regard to case, and `App` is the entry's own name.
- `learn` writes a proposed entry (`"proposed": true`) from its own guess. It counts as a guess until you record the entry; a re-scan keeps a recorded entry, `none` and the links.
- `--service` may also name a host app: a `host` step of the plan or a host app recorded for the instance. For a host app `--port` is optional and defaults to the port stackfork allocated to it; if none is allocated yet, pass `--port`. The entry resolves to the host app's URL (`http://<hostname>:<host port>`), and `learn verify` probes it on 127.0.0.1 and refuses with "start the instance" while the host app is stopped. `learn record link` accepts the same names.
- Targets are checked against the prepared instance's Compose model (else the scanned services). Links stay inside the instance: a path starts with one `/`, and stackfork builds every URL from the instance's own hostname and port. Absolute URLs, `//host` and `..` are refused.
- `learn verify` refuses until the entry is recorded (or `none`), needs the instance running (and, always, a prepared instance: see "The recipe that verify records"), and sends one GET to the entry (`Host` is the instance hostname; no redirect is followed). Any status below 500 counts as answering, so a login redirect or a `401` passes; a refused connection or a `5xx` fails verify. Each link is probed too and a link that does not answer is a warning in the output, not a failure. The probe result is stored in `verification.entry` and `verification.links`.
- `entry` and `links` are not part of the trust hash: a change needs no `plan --trust`.

## How code changes reach the app

Each app (a Compose service or a host app) has one `reload` mode. The dashboard shows it as a small chip on the service row and on the Project page.

- `hot`: edits are picked up live (a dev server or watcher on the host, or in a container with the source bind-mounted: vite, next dev, nodemon, tsx watch, uvicorn --reload, air ...).
- `restart`: the code is mounted or on the host, but the process must restart after an edit.
- `rebuild`: the code is baked into the image. After an edit, run Start or `stackfork up --build`.

`learn` proposes a mode per app service and host step from the Compose bind mounts, the command and entrypoint, the `package.json` scripts and the Dockerfile `COPY`, and stores it with `evidence` (file:line) and `"proposed": true`. A service with none of these signs (a database image) has no entry, and the mode is `unknown`. Confirm it in the running app: edit a visible string, check that it changes without a restart, then revert the edit. Record the result:

```sh
stackfork learn record reload web hot [--note "vite in the container"]
stackfork learn record reload api rebuild
```

- The name is a Compose service or a host app of the plan. A recorded mode replaces the proposal; a re-scan keeps it.
- `learn verify` warns when the entry app's mode is unknown or still only proposed. It does not refuse.
- `reload` is not part of the trust hash. `stackfork status` adds `reload` to a row when the mode is known.

### Preparing an app before starting its server

Use `--on one-off` when a setup command must finish before the app starts. Each task uses `docker compose run --rm --no-deps -T`. It uses the prepared instance's image, network, environment, secrets and volumes. It publishes no service ports and starts no dependencies. The temporary container is removed when the command ends; named volumes remain.

Start database dependencies first, then install packages, migrate and seed, then start the app. Keep `--once deps` on installation and `--once data` on the seed. A failed task stops the recipe; it does not fall back to the host or a running container.

```sh
stackfork learn record step --id dependencies --kind compose-up --exclude app --before install
stackfork learn record step --id install --kind exec --on one-off --service app --once deps --cmd "bun install --frozen-lockfile" --before migrate
stackfork learn record step --id migrate --kind exec --on one-off --service app --cmd "bun run db:migrate" --before seed
stackfork learn record step --id seed --kind exec --on one-off --service app --once data --cmd "bun run db:seed:dev" --before up
```

These examples update an existing recipe. Use the commands and service names from your project. Ordinary container `exec` steps still require an earlier `compose-up` that starts their service.

## Verifying

Step 7 only, never on your own initiative. Run the recorded recipe with `stackfork start-env` before verification. A prepared instance or an accessible page alone is not enough. Verification requires the latest run of that exact recipe and mode to have succeeded. A plain `up`, a live rebuild, an interrupted run or a failed run does not qualify. The app's entry must also be recorded (see "Entry and quick links"):

```sh
stackfork learn verify --check "database identity" --check "app readiness" --confirm "<the user's own words>"
```

### The recipe that verify records

`learn verify` needs a prepared instance for the checkout and its branch (`no prepared instance for this checkout; prepare and start it, then verify`). It writes the plan's `prepare` step from that instance, so a worktree prepared from knowledge later gets the full recipe, not the short list written early in the session:

- the `args` are one `-f PATH` for each Compose file of the instance, in order, then `--env-file PATH` when it has one, all relative to the checkout; the step's `--hostname`, `--reassign-ports` and `--random-ports` stay after them;
- a plan with no `prepare` step gets one (id `prepare`) before the first `compose-up` step;
- when the instance uses a file outside the checkout, verify prints a warning and leaves the step as it was (a committed plan cannot name such a file);
- the JSON output lists the recorded args as `prepare`.

If verification completes or changes the `prepare` step, it saves the corrected recipe as a draft. Run `stackfork start-env` with that recipe, then repeat verification. A person must trust changed host plans again with `stackfork plan --trust`.

Verification stores the successful run ID, mode and a hash of the complete plan. Changing the plan invalidates that evidence. Older verification records without execution evidence appear as drafts and need a new start and confirmation. Reading them does not rewrite files or start any work. Verification never resets a database or forces a seed. A successful restart does not prove a fresh database setup; check that separately when needed.

A skipped `once` task needs evidence that the same task definition previously succeeded in this instance and mode. Changing its command does not force installation or seed on existing data. If that task is skipped without matching evidence, verification stays pending. Validate the changed task in a new isolated instance instead of deleting existing data.

Compact execution hashes pass through every run, including Live, Up and Seed. This keeps task evidence when older runs expire, without retaining extra logs or secret values.

`--confirm` must be the user's actual confirmation, quoted — never a paraphrase, never written by the agent itself. This is what moves `status` from `draft` to `verified`.

## Reusing verified knowledge

Once `.stackfork/knowledge.md` is `verified` in this checkout, in the main checkout or in another worktree of the repository, a new worktree of the same project applies its adapter and recipe directly at `prepare`/`up` — no scan, no questions, asking only for secrets the knowledge names by reference (`references/adapting-compose.md`). A worktree that borrows knowledge from a sibling never writes it back. `prepare` and `up` refuse a repository that was never learned (`stackfork learn` first); a draft is allowed. If `stackfork where` reports it `stale` (an input changed since it was verified; for `package.json` only `scripts`, `packageManager` and `workspaces` count, because a dependency bump is the install step's job, not a reason to learn again; reported at read time, the file itself is never silently rewritten), re-run `learn`; it resumes the existing draft/verified session (answers, gotchas, recipe, verification all carry forward) and only the scanned facts refresh.
