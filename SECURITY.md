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

## Host steps in knowledge.json

A committed `.stackfork/knowledge.json` can name a host command in a `host` step. That command runs only after a person ran `stackfork plan --trust` (or clicked **Trust** in the dashboard) on that machine. Trust applies when the active mode runs a host step. Its hash covers all `host` and `prepare` steps, so any change to them, including a new field, needs a new trust. The `host` command is an argument vector and never goes through a shell. The guard blocks `plan --trust` for agents, and the dashboard asks in a native dialog that lists every field.

Known limits:

- The guard does not watch the state directory. An agent with file access can write the trust file directly.
- Trust is stored once for each main checkout. Worktrees with different host steps invalidate each other's trust.
- If you kill a host supervisor with `kill -9`, its app keeps running. `doctor` reports it (the `host apps` check) when its port still answers.
- A foreground `host NAME` command that ignores SIGTERM is orphaned after its stackfork gets SIGKILL.

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
