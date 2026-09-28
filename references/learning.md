# The learn session

`stackfork learn` only collects facts: a deterministic, offline scan of Compose files, entrypoints, scripts and env-key names, with no LLM judgment and no network access. It writes `.stackfork/knowledge.json` and human-readable `.stackfork/knowledge.md` with status `draft`, plus a `.stackfork/.gitignore` that keeps `runtime.env` and any real `.env` out of git. Unlike `.agent-dev/` (`references/worktrees.md`), `.stackfork/` is meant to be **committed** — it is project knowledge, not private local state. Re-running `learn` on a `draft` resumes it instead of starting over; it also re-scans a `stale` file (one whose recorded inputs changed) and reports only what changed.

The scan never records a secret value — only variable names and their inferred purpose (database, auth, mail, storage, registry). Read `.stackfork/knowledge.md` after scanning: it lists services, images, database version, ports, volumes, networks, isolation findings (what `prepare` would refuse or rewrite, from `references/adapting-compose.md`), and risky startup behaviour (resets/seeds on every start, scripts that rewrite `.env`/`.npmrc`, or that hardcode `docker compose`/`docker exec` names) with file:line.

## Running the session (agent-driven, not scripted)

The CLI only scans; you drive the rest, because it needs judgment the CLI can't apply on its own:

1. **Scan.** Run `stackfork learn` and read `.stackfork/knowledge.md`.
2. **Write the adapter.** Follow `references/adapting-compose.md` (and `references/nuxt-template.md` if applicable), informed by what the scan found.
3. **Try it.** `prepare` → `up` → run the project's real migrate/seed-once command.
4. **Verify.** Database identity and app readiness at minimum; a representative flow when this is the project's first adoption.
5. **On failure,** fix the adapter (or ask the user when the fix isn't safe to pick alone), retry, and record what broke and how it was fixed.
6. **On an ambiguous or unsafe decision** (several seed datasets, an optional service, where a credential comes from), ask the user — never guess — and record the question and their answer.
7. **Ask the user to confirm the app works.** Never self-verify: knowledge only moves from `draft` to `verified` on the user's own confirmation, and the confirmation is recorded, quoted, in the knowledge.

## Recording answers, gotchas and the recipe

Never hand-edit `knowledge.json` to add these — always the CLI, so the file stays valid and atomic writes hold:

```sh
stackfork learn record answer "<question>" "<answer>"
stackfork learn record gotcha "<problem>" "<fix>"
stackfork learn record recipe --prepare "<cmd>" --up "<cmd>" [--migrate "<cmd>"] [--seed-once "<cmd>"] [--login "<step>" ...]
```

`record recipe` only sets the flags given each call, so it can be recorded incrementally as the session goes. Each `record` call requires a prior `stackfork learn` (there is no draft to record into otherwise).

## Verifying

Step 7 only, never on your own initiative — `learn verify` refuses if the recipe's `--prepare`/`--up` were never recorded (recording the steps is what step 3 means by "try it"):

```sh
stackfork learn verify --check "database identity" --check "app readiness" --confirm "<the user's own words>"
```

`--confirm` must be the user's actual confirmation, quoted — never a paraphrase, never written by the agent itself. This is what moves `status` from `draft` to `verified`.

## Reusing verified knowledge

Once `.stackfork/knowledge.md` is `verified`, a new worktree of the same project applies its adapter and recipe directly at `prepare`/`up` — no scan, no questions, asking only for secrets the knowledge names by reference (`references/adapting-compose.md`). If `stackfork where` reports it `stale` (an input's content changed since it was verified — reported at read time, the file itself is never silently rewritten), re-run `learn`; it resumes the existing draft/verified session (answers, gotchas, recipe, verification all carry forward) and only the scanned facts refresh.
