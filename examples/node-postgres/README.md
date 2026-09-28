# node-postgres example

The smallest realistic stackfork target: a plain Node HTTP server (no
framework, one dependency - `pg`), Postgres, and one idempotent migration
run at startup. Every request records and counts a visit, so running two
worktrees of this example side by side visibly keeps two separate counts -
proof the isolation is real.

## Try it

```sh
git clone <this repo> stackfork
cd stackfork/examples/node-postgres
stackfork prepare
stackfork up
stackfork urls    # click the http:// link, or curl it
```

Now open a second worktree of the *same* example and run it again - both
run at once, on different ports, with different Postgres data:

```sh
git worktree add ../node-postgres-2
cd ../node-postgres-2/examples/node-postgres
stackfork prepare
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
