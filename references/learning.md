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
| `exec` | Runs `cmd` in `service`. `once` is `data` (seed, new database only) or `schema` (migrate). |
| `host` | Runs `cmd` on the host. `cmd` is an argument vector: no shell, so no pipes, redirects or `&&`. |
| `note` | Shows `text` to the person. Runs nothing. |

A `host` step has a `name`, an optional `dir` and `port` (`--port N`, 1 to 65535), and `port_env`. A `prepare` step takes `args` (`--args -f,compose.yaml`). The step always sets `PORT`, plus each name in `port_env`. `no_seed` records the answer "never seed".

Record, change or remove a step:

```sh
stackfork learn record step --id web --kind host --name web --cmd "bun run dev" --port-env VITE_PORT --modes hybrid
stackfork learn record step --id web --remove
```

`learn verify` needs a recorded plan with a `compose-up` step.

### Modes and trust

Each machine runs the plan in a mode: `container` (default) or `hybrid`. Set it with `stackfork config set mode hybrid`, per repo with `stackfork config set-mode REPO hybrid`, or for one run with `start-env --mode`. Hybrid mode drops the app service and every service that depends on it, and runs the `host` step instead.

A host step runs a command from a file in the repository, so a person must trust it first. Trust is needed only when the active mode runs a host step. The trust hash covers all `host` and `prepare` steps. Adding a field to a step changes the hash and needs a new trust.

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

## Verifying

Step 7 only, never on your own initiative — `learn verify` refuses if the recipe's `--prepare`/`--up` were never recorded (recording the steps is what step 3 means by "try it"):

```sh
stackfork learn verify --check "database identity" --check "app readiness" --confirm "<the user's own words>"
```

`--confirm` must be the user's actual confirmation, quoted — never a paraphrase, never written by the agent itself. This is what moves `status` from `draft` to `verified`.

## Reusing verified knowledge

Once `.stackfork/knowledge.md` is `verified`, a new worktree of the same project applies its adapter and recipe directly at `prepare`/`up` — no scan, no questions, asking only for secrets the knowledge names by reference (`references/adapting-compose.md`). If `stackfork where` reports it `stale` (an input's content changed since it was verified — reported at read time, the file itself is never silently rewritten), re-run `learn`; it resumes the existing draft/verified session (answers, gotchas, recipe, verification all carry forward) and only the scanned facts refresh.
