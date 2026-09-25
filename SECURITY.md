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

## Reporting a vulnerability

Please report security issues privately through
[GitHub Security Advisories](https://github.com/pedropinheiro75/stackfork/security/advisories/new)
rather than opening a public issue. Include enough detail to reproduce the
problem (Compose fixture, command, expected vs. actual behaviour). We'll
acknowledge the report and follow up with next steps.
