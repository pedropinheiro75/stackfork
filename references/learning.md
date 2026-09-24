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

## Recording answers and gotchas today

`learn record answer|gotcha|recipe` and `learn verify` are CLI subcommands that don't exist yet (they land in a later change). Until then, there is nothing to run to persist step 6/7's answers and gotchas into `.stackfork/knowledge.json` — do not hand-edit that file to fake it. Instead, report each recorded answer and gotcha in your response to the user, exactly as you would have written it to the file, so nothing found during the session is lost.

## Reusing verified knowledge

Once `.stackfork/knowledge.md` is `verified`, a new worktree of the same project applies its adapter and recipe directly at `prepare`/`up` — no scan, no questions, asking only for secrets the knowledge names by reference (`references/adapting-compose.md`). If `status`/`where` report it `stale`, re-run `learn`; it re-scans only the changed inputs.
