# node-postgres example

The smallest realistic stackfork target: a plain Node HTTP server (no
framework, one dependency - `pg`), Postgres, and one idempotent migration
run at startup. Every request records and counts a visit, so running two
worktrees of this example side by side visibly keeps two separate counts -
proof the isolation is real.

This example lives inside the stackfork repo itself rather than its own
Git checkout, so every command below runs from the **repo root**, not this
directory - `--root` always resolves to the enclosing checkout's toplevel.

## Try it

```sh
git clone <this repo> stackfork
cd stackfork
stackfork prepare -f examples/node-postgres/docker-compose.yml --hostname node-postgres-demo.localhost
stackfork up
stackfork urls    # click the http:// link, or curl it
```

Now open a second worktree of the *same* checkout - both run at once, on
different ports, with different Postgres data:

```sh
git worktree add ../stackfork-2
cd ../stackfork-2
stackfork prepare -f examples/node-postgres/docker-compose.yml --hostname node-postgres-demo-2.localhost
stackfork up
stackfork urls
```

```sh
stackfork list   # both instances, side by side
```

Each `curl`'s `visits` count climbs independently - two fully isolated
copies of the same app and database on one machine, no port or volume
collision to manage by hand.

## Clean up

```sh
stackfork down --volumes
```

## CI check

`ci-check.sh` runs the whole thing above for real (two real `git worktree`
checkouts, the real `stackfork` binary, real Docker) and asserts the two
instances' visit counts stay independent. It's what CI's `live` job runs.
