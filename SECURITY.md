# Security policy

## Scope

stackfork's safety model is **collision isolation between checkouts**: making
sure parallel worktrees/branches of the same project don't collide on ports,
volumes, networks, container names, databases or browser cookies, and
refusing configurations it can't isolate safely.

It is **not** a sandbox. It does not protect against a malicious or
untrusted Compose file, does not isolate the containers it starts from the
host beyond what Docker itself provides, and assumes the project being
isolated is one you already trust enough to run its Compose stack directly.
Running an untrusted project's Compose file through stackfork is no safer
than running it with `docker compose` yourself.

## Dashboard credentials

`stackfork dashboard` (design.md D2-D5) is read-mostly and never shows a
password: every connection string, CLI command and `.env` block on the page
uses a literal `<password>` placeholder, in `GET /api/instances`, `list
--json` and the rendered HTML alike.

The one exception is `POST /api/copy`. It is the only place stackfork ever
returns a real credential, and only for a password written literally in the
instance's own source Compose file(s), under one of a fixed set of known
local-dev keys (`POSTGRES_PASSWORD`, `MYSQL_PASSWORD`,
`MYSQL_ROOT_PASSWORD`, `MARIADB_PASSWORD`, `MARIADB_ROOT_PASSWORD`,
`MONGO_INITDB_ROOT_PASSWORD`, `REDIS_PASSWORD`) or a Redis `--requirepass`
command-line argument. It refuses - keeping the placeholder in its response
- whenever it can't positively verify that:

- the value is `${VAR}`-interpolated rather than a literal string;
- the service loads any `env_file` at all (stackfork can't tell which keys
  came from the file without a full Compose-aware parse, so it refuses the
  whole service rather than guess); or
- one of the instance's source Compose files can't be read.

A value that only ever reaches a service through `--secret` follows the
same path: stackfork's own `--secret` support wires a secret into a
service's environment through `${VAR}` interpolation, which the check above
already refuses.

The endpoint carries the same boundary as every other dashboard action
(design.md D5): the per-process token, an allowed `Origin`, an allowed
`Host`, POST only, and its responses are sent with `Cache-Control:
no-store`. The page itself only calls it for a pill whose displayed text
already contains the `<password>` placeholder, and writes the response
straight to the clipboard without logging or rendering it.

## Commands run from the dashboard

`POST /api/run` runs a command the page shows, behind the same token,
`Origin` and `Host` checks. The token is in the page, so any local process
that can reach `127.0.0.1` can send the request. The endpoint therefore does
not trust the caller:

- It runs only an allowlist (stackfork lifecycle commands, `git -C REPO
  worktree remove PATH` for a scanned worktree, `docker rm` of stackfork's
  own containers), as an argument vector with no shell. Shell syntax and
  any other command are refused.
- Anything that deletes data (`down --volumes`, `seed --force`, `prune`,
  `gc`, worktree removal) runs only after a person clicks Run in a native
  macOS dialog. An agent can forge the HTTP request but cannot answer the
  dialog. On other systems these commands are refused.

## Environment reset

| Threat | Control | Residual risk |
|---|---|---|
| An agent, or a forged dashboard request, deletes an environment's volumes with `reset` | The guard blocks `stackfork reset` (any flags, global flags first, wrappers) and its forged-call check covers the dashboard's actions endpoint. The dashboard asks in a native dialog that names the volumes. The CLI asks `y/N` on a terminal and refuses without one unless `--yes` is given | `--yes` is the person's choice, and the guard is not a sandbox: an agent can run other tools as the person. Reset never touches the code, but a person may lose data they kept only in the volumes. |

## Live mode

Live mode (`stackfork live on`) changes who starts a run. After you turn it on for a repository, a commit pushed to a worktree's upstream is fetched, fast-forwarded and built on this machine while the dashboard runs, without a command from you. Turning it on is your consent to that. The dashboard asks in a native dialog, and the CLI prints the same text.

What runs without new trust, as it does on a Start after a `git pull`:

- `exec` steps in containers, such as a changed migrate command.
- Docker builds of a changed Dockerfile.
- A changed Compose file. `prepare` applies its usual refusals to it (host namespaces, Docker socket, writable binds outside the checkout, fixed container names, fixed subnets).

What needs trust, in every mode: before the first step of each live run, stackfork compares the plan's `host` and `prepare` steps with the trust file. If either changed, no run starts. The repository pauses with `plan needs trust` and the step diff, until you run `stackfork plan --trust` or click **Trust on this machine**, and then choose **Resume**.

| Threat | Control | Residual risk |
|---|---|---|
| An agent turns on automatic execution of its own pushed commits | The guard blocks `live on` (global flags with one or two dashes, before or after the action word; an action word that is not first; wrappers). The guard and the CLI split the command line with the same function (`internal/argv`), so every spelling the CLI accepts is seen. It treats a direct call to `/api/live` as a forged dashboard call. `live off` and `live status` stay allowed. The dashboard asks in a native dialog | The guard matches command text. A script file, a renamed binary or a new tool gets past it. An agent with file access can edit `config.json` and set `live` or `cleanup` directly, as it can write the trust file. The guard does not watch files. |
| A hostile commit on a trusted upstream | Trust for `host` and `prepare` steps, `prepare`'s refusals, the fast-forward rule (never with uncommitted changes to tracked files or local commits, never a merge, rebase or reset, and in live mode never over an ignored or untracked file that the incoming commits add), a pause on the first failed or interrupted run, and rate limits | The commit can still run code in a container or a Docker build, as it can on a Start after a `git pull`. |
| A live run deletes data | A live run never runs `down`, `--volumes`, `--renew-anon-volumes`, `--remove-orphans` or `--force-recreate`. Branch cleanup is the one path that deletes data (below) | A rebuilt image does not refresh a named volume that already holds data. |
| A 1Password prompt appears during a run that nobody expects | `live on` prints the warning. An unanswered prompt times out after 30 seconds, the run fails at `secrets`, and live mode pauses. `live on` refuses `env:` references, which the dashboard cannot resolve | You may approve a prompt without knowing which run opened it. |

### Branch cleanup

Branch cleanup removes a merged worktree, the instance's Docker volumes (its database included) and, if you set `--delete-branch`, the local branch. It is the only live action that deletes data.

- **Merged proof.** A branch is `merged` only when its upstream is gone after `git fetch --prune` and its work is in the default branch, by ancestry, by `git cherry`, or by the patch identity of the whole branch (at most 500 commits). The forge adds a label only: `gh` can turn `merged` into `closed`, never the reverse. `gh` is found at fixed paths, never through `PATH`.
- **Never cleaned.** The main checkout, the default branch, a detached or locked worktree, a branch that is also checked out in the main checkout, a branch that never had an upstream, a branch closed without merge, a worktree with uncommitted or untracked files, and a branch with a commit that is not in the default branch.
- **Re-check.** Eligibility is read again from disk right before the first step. The worktree is removed first, without `--force`, so git's own refusal protects uncommitted work; a refusal deletes nothing. The volumes go only after that. Git deletes the branch with `-d`, and with `-D` only after the patch check passed again.
- **People only.** The default `suggest` mode needs a click and a native dialog that names the worktree, the volumes, the branch and the ignored files that go with the worktree. The guard blocks `live cleanup auto`, `live cleanup --delete-branch` and `live clean --yes` for agents, and `/api/live` is a forged-call endpoint. Where no native dialog exists, the page shows the command and deletes nothing.
- **Grace period.** In `auto` mode, a cleanup waits `grace_hours` (1 to 720, default 24) after the branch first became eligible, re-checks, and then runs. **Keep** holds a branch until its head commit changes. Ignored files outside known cache folders block `auto`.

Residual risks: the data in the instance's database is deleted by design, so choose **Keep** or `suggest` if you need it. A squash merge whose resolution changed the diff is not detected, so nothing is offered.

## Host steps in knowledge.json

A committed `.stackfork/knowledge.json` can name a host command in a `host` step. That command runs only after a person ran `stackfork plan --trust` (or clicked **Trust** in the dashboard) on that machine. Trust applies when the active mode runs a host step. Its hash covers all `host` and `prepare` steps, so any change to them, including a new field, needs a new trust. The `host` command is an argument vector and never goes through a shell. The guard blocks `plan --trust` for agents, and the dashboard asks in a native dialog that lists every field.

Known limits:

- The guard does not watch the state directory. An agent with file access can write the trust file directly.
- Trust is stored once for each main checkout. Worktrees with different host steps invalidate each other's trust.
- If you kill a host supervisor with `kill -9`, its app keeps running. `doctor` reports it (the `host apps` check) when its port still answers.
- A foreground `host NAME` command that ignores SIGTERM is orphaned after its stackfork gets SIGKILL.

## Secrets

stackfork reads named secrets (an npm token, a test API key) from places the person already trusts and hands them to the Compose child only. A `secret` command never prints a value and takes none as an argument. A reference is `env:VAR`, `npmrc:PATH#HOST`, `keychain:ITEM` (macOS Keychain, Linux Secret Service, Windows Credential Manager, service or target prefix `stackfork`) or `op://VAULT/ITEM/FIELD` (1Password). References live in the machine's `config.json`; the committed `knowledge.json` names secrets and hints only.

| Threat | Control | Residual risk |
|---|---|---|
| A malicious `knowledge.json` (from a pull request) names references to exfiltrate unrelated secrets | Knowledge holds names only; a plan `prepare` step takes only file, env-file, hostname and port flags, never `--secret`; references live in the machine map, written only by people; `keychain:` reaches only the `stackfork` service | A person maps a name to a sensitive reference because a hint asked for it. The preflight shows the reference before the first use. |
| A fake `op`, `security` or `secret-tool` earlier on `PATH` captures values or lies | Fixed absolute paths, ownership and permission checks, never `PATH`, not inside a scanned root | A process running as the person can replace a binary the person owns (a Homebrew `op`). Out of scope: same user. |
| Shoulder-surfing and screen sharing | No command or page prints a value; prompts are hidden-answer dialogs or echo-off terminal reads | The store's own applications show values; that is their behavior. |
| Values in process arguments | Reads through pipes, writes through stdin or an API | None known. |
| Values in the environment of unrelated children | No `os.Setenv`; values only in the Compose child | The Compose child and the containers have them, by design. `docker inspect` shows the environment of a running container to anyone who can use the Docker socket. |
| Values in logs and the terminal | Run redactor from the preflight values; the dashboard log view stays gated (**Check secrets to view logs**) while any reference has no value to mask with, whatever its kind | Values shorter than 8 characters are not matched (`internal/redact`). A value the app transforms (URL-encoded, split) is not matched. The CLI `logs` and `host logs` skip a reference that does not resolve and print the log unredacted, as they did before this change. |
| An agent points a name at a store item with `--secret` and reads it back through `exec` | `--secret` accepts only `env:` and `npmrc:`; `keychain:` and `op://` come only from the machine map; `run` and `exec` output is redacted with the values known for the instance (not when it goes to a terminal; an agent can get one with `script`, `unbuffer` or python `pty.spawn`, so this skip is bypassable the same way as the transformed-output residual) | `exec` cannot redact output sent to a pseudo-terminal, an `op://` value that no run or Check in this process resolved, a value under 8 characters, or one the app transforms. |
| An agent reads values | Guard rules (below) | An agent can run other tools as the person; the guard is not a sandbox. |
| Tampering with `config.json` | The file is 0600 in the user's config directory | An agent with file access can edit it, as it can the trust file. |
| A forged dashboard request opens the value dialog | Token, `Origin` and `Host` checks; the dialog names the repository, the secret and the store item; only a person can answer it; the guard blocks System Events keystrokes | A person may type a value into a dialog they did not expect. The dialog text says which action opened it. |

Residual risks that no control removes:

- **Same-user processes.** Any process that runs as you can read what you can read: the store, your environment, `config.json`.
- **`docker inspect`.** A value passed to a container as an environment variable is visible to whoever can run `docker inspect` on it.
- **The 8-character redaction limit.** Short values stay in logs.
- **The keychain access list.** An item created through `/usr/bin/security` trusts that tool, so any process of yours that runs `security` can read it. macOS treats items created this way like this. Linux and Windows have the same property for their own tools.
- **Windows.** A Credential Manager value is at most 2560 bytes (a UTF-16 limit of the API). The Windows code is built and vetted but has not run on a Windows machine.

The guard blocks `stackfork secret set|map|unmap`, store reads (`security find-generic-password -w`/`-g` unless `-s` names another service, so also with no `-s` or with `-l stackfork`; `security dump-keychain -d`; `security -i`; `secret-tool lookup|search` unless a `service` attribute names another service; `cmdkey /list` with a stackfork target; PowerShell that reads a `stackfork/` target), `op read|inject|run` of a mapped `op://` reference (compared without case), `op item get` of a mapped item with `--reveal`, `--fields` or `--format json`, `op item list --format json`, and forged `/api/run`, `/api/actions`, `/api/learn` and `/api/secrets` calls (the URL is cut at `?`/`#`, percent-decoded and cleaned first). If `config.json` cannot be read, the guard blocks `op read|inject|run|item get` for agents. It allows `secret ls` and `secret check`. It matches command text, so a script file, a renamed binary or a new tool gets past it. It is a guardrail for cooperative agents, not a boundary.

## Run logs

A run's step logs (`runs/<id>/<step>.log` in the instance directory) hold the output of Compose, the seed and host steps. Values of 8 or more characters that stackfork knows as secrets are redacted before they are written, as they are on the terminal and in the dashboard stream. Shorter values are not matched (`internal/redact`). Run files and step logs are created 0600, in directories 0700, as the other state files are.

## Windows

The Windows runtime is built and vetted (`GOOS=windows go vet`), but it has not run on a Windows machine. Its ceilings:

- No graceful stop. `host stop` and `down` use `taskkill /T /F`, so the app gets no chance to clean up.
- Process identity is the PID plus its creation time. stackfork does not read the command line, so a recycled PID is not matched, but a process is not checked for being the app it started.
- Children of a supervisor that was killed first can survive. Their tree was not killed, and `doctor` reports their port.
- The guard treats a backslash as a literal character (paths such as `C:\Users\x\stackfork.exe`). It unwraps `cmd /c` and `powershell -Command` before it checks the command, and it still splits on `&&`, `||`, `;`, `|` and `&`.

## Reporting a vulnerability

Please report security issues privately through
[GitHub Security Advisories](https://github.com/pedropinheiro75/stackfork/security/advisories/new)
rather than opening a public issue. Include enough detail to reproduce the
problem (Compose fixture, command, expected vs. actual behaviour). We'll
acknowledge the report and follow up with next steps.
