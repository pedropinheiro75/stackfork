# The learn session

`stackfork learn` only collects facts: a deterministic, offline scan of Compose files, entrypoints, scripts and env-key names, with no LLM judgment and no network access. It writes `.stackfork/knowledge.json` and human-readable `.stackfork/knowledge.md` with status `draft`, plus a `.stackfork/.gitignore` that keeps `runtime.env` and any real `.env` out of git. Unlike `.agent-dev/` (`references/worktrees.md`), `.stackfork/` is meant to be **committed** — it is project knowledge, not private local state. Re-running `learn` on a `draft` resumes it instead of starting over; it also re-scans a `stale` file (one whose recorded inputs changed) and reports only what changed.

The scan never records a secret value — only variable names and their inferred purpose (database, auth, mail, storage, registry). Read `.stackfork/knowledge.md` after scanning: it lists services, images, database version, ports, volumes, networks, isolation findings (what `prepare` would refuse or rewrite, from `references/adapting-compose.md`), and risky startup behaviour (resets/seeds on every start, scripts that rewrite `.env`/`.npmrc`, or that hardcode `docker compose`/`docker exec` names) with file:line.

## Running the session (agent-driven, not scripted)

The CLI only scans; you drive the rest, because it needs judgment the CLI can't apply on its own:

1. **Scan.** Run `stackfork learn` and read `.stackfork/knowledge.md`.
2. **Write the adapter.** Follow `references/adapting-compose.md` (and `references/nuxt-template.md` if applicable), informed by what the scan found.
3. **Try it.** Run the plan: `stackfork plan` lists the steps, then `prepare` → `up` → the project's real migrate/seed-once command.
4. **Verify.** Database identity and app readiness at minimum; a representative flow when this is the project's first adoption.
5. **On failure,** fix the adapter (or ask the user when the fix isn't safe to pick alone), retry, and record what broke and how it was fixed.
5b. **Confirm the seed command.** When the scan found a seed or migrate script it pre-fills `recipe.migrate` and a proposed `recipe.seed_once`, marked pending. Confirm the command with the user and record it with `learn record recipe --seed-once "<cmd>"` (add `--seed-service SVC` when it is not the primary app service), or `--seed-once none` if this project must never be seeded. Without a decision the default is automatic: `up` seeds a new database (one whose data volume `up` just created) with the detected script, once, after migrations and health; a database that already existed is never seeded. `learn verify` only warns when no decision was recorded. `stackfork seed` retries a failed seed; `--force` reruns it (ask first).
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
| `exec` | Runs `cmd` in `service`. `once` is `data` (seed, new database only) or `schema` (migrate). With `on: host` it runs on the host instead (see below). |
| `host` | Runs `cmd` on the host. `cmd` is an argument vector: no shell, so no pipes, redirects or `&&`. |
| `note` | Shows `text` to the person. Runs nothing. |

A `host` step has a `name`, an optional `dir` and `port` (`--port N`, 1 to 65535), and `port_env`. A `prepare` step takes `args` (`--args -f,compose.yaml`). The step always sets `PORT`, plus each name in `port_env`. `no_seed` records the answer "never seed".

An `exec` step with `on: host` runs `cmd` on the host, in the checkout or in `dir` (relative, inside the checkout), with the instance's environment: the `DATABASE_URL`, `REDIS_URL`, `SMTP_HOST` and `<SERVICE>_URL` values that `stackfork env --with-passwords` prints, plus `STACKFORK_HOST_RUN=1`. Use it when no container can run the project's migrate or seed, for example when only the database runs in Compose. `cmd` is an argument vector like a `host` step: no shell, so no `&&`. A host exec step takes `cmd`, `once`, `modes` and `dir`, and no `service`, `name`, `port` or `port_env`. Put it after the `compose-up` step: it needs a running instance. It runs in `container` mode and in `hybrid` mode, and does not make `hybrid` available by itself.

When the `once: data` seed and the first `once: schema` step are both `on: host` with the same `dir`, the seed entry runs the migrate first and the seed second, as two commands, and stops at the first failure. The migrate step then shows as skipped (`ran with the seed`). A migrate and a seed that do not both run on the host each run at their own place in the plan. An auto-detected seed always runs in a container.

After the first Start, check that the host command used this instance's database: a tool that reads its own `.env` file can ignore `DATABASE_URL`.

Record, change or remove a step:

```sh
stackfork learn record step --id web --kind host --name web --cmd "bun run dev" --port-env VITE_PORT --modes hybrid
stackfork learn record step --id migrate --kind exec --on host --cmd "bun run db:migrate" --once schema
stackfork learn record step --id web --remove
```

`learn verify` needs a recorded plan with a `compose-up` step.

### Modes and trust

Each machine runs the plan in a mode: `container` (default) or `hybrid`. Set it with `stackfork config set mode hybrid`, per repo with `stackfork config set-mode REPO hybrid`, or for one run with `start-env --mode`. Hybrid mode drops the app service and every service that depends on it, and runs the `host` step instead.

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
stackfork learn record entry --none            # a worker or CLI with no web entry
stackfork learn record link "API docs" --path /api/docs
stackfork learn record link Email --service smtp4dev --port 80
stackfork learn record link --remove Email
```

- `--port` is the container port of the Compose service, never a published host port. A link with `--path` only is a path on the entry; a link with `--service` and `--port` is another service of the stack. A name is unique without regard to case, and `App` is the entry's own name.
- `learn` writes a proposed entry (`"proposed": true`) from its own guess. It counts as a guess until you record the entry; a re-scan keeps a recorded entry, `none` and the links.
- Targets are checked against the prepared instance's Compose model (else the scanned services). Links stay inside the instance: a path starts with one `/`, and stackfork builds every URL from the instance's own hostname and port. Absolute URLs, `//host` and `..` are refused.
- `learn verify` refuses until the entry is recorded (or `none`), needs the instance running (and, always, a prepared instance: see "The recipe that verify records"), and sends one GET to the entry (`Host` is the instance hostname; no redirect is followed). Any status below 500 counts as answering, so a login redirect or a `401` passes; a refused connection or a `5xx` fails verify. Each link is probed too and a link that does not answer is a warning in the output, not a failure. The probe result is stored in `verification.entry` and `verification.links`.
- `entry` and `links` are not part of the trust hash: a change needs no `plan --trust`.

## Verifying

Step 7 only, never on your own initiative — `learn verify` refuses if the recipe's `--prepare`/`--up` were never recorded (recording the steps is what step 3 means by "try it") or the app's entry was not recorded (see "Entry and quick links"; the instance has to run):

```sh
stackfork learn verify --check "database identity" --check "app readiness" --confirm "<the user's own words>"
```

### The recipe that verify records

`learn verify` needs a prepared instance for the checkout and its branch (`no prepared instance for this checkout; prepare and start it, then verify`). It writes the plan's `prepare` step from that instance, so a worktree prepared from knowledge later gets the full recipe, not the short list written early in the session:

- the `args` are one `-f PATH` for each Compose file of the instance, in order, then `--env-file PATH` when it has one, all relative to the checkout; the step's `--hostname`, `--reassign-ports` and `--random-ports` stay after them;
- a plan with no `prepare` step gets one (id `prepare`) before the first `compose-up` step;
- when the instance uses a file outside the checkout, verify prints a warning and leaves the step as it was (a committed plan cannot name such a file);
- the JSON output lists the recorded args as `prepare`.

The `prepare` step is part of the trust hash. When verify changes it and the plan has host steps, the output has a `note` (`the prepare step changed; a person must trust the plan again: stackfork plan --trust`) and verify still succeeds. Knowledge verified before this change keeps its `prepare` step until the next `learn verify`.

`--confirm` must be the user's actual confirmation, quoted — never a paraphrase, never written by the agent itself. This is what moves `status` from `draft` to `verified`.

## Reusing verified knowledge

Once `.stackfork/knowledge.md` is `verified`, a new worktree of the same project applies its adapter and recipe directly at `prepare`/`up` — no scan, no questions, asking only for secrets the knowledge names by reference (`references/adapting-compose.md`). If `stackfork where` reports it `stale` (an input's content changed since it was verified — reported at read time, the file itself is never silently rewritten), re-run `learn`; it resumes the existing draft/verified session (answers, gotchas, recipe, verification all carry forward) and only the scanned facts refresh.
