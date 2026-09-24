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

## Reporting a vulnerability

Please report security issues privately through
[GitHub Security Advisories](https://github.com/pedropinheiro75/stackfork/security/advisories/new)
rather than opening a public issue. Include enough detail to reproduce the
problem (Compose fixture, command, expected vs. actual behaviour). We'll
acknowledge the report and follow up with next steps.
