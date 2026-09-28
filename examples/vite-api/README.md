# vite-api example

The "host-run app" pattern (`references/adapting-compose.md`): Postgres is
isolated by stackfork, but the app itself - a Vite dev server with one API
middleware route, `/api/visits` - runs natively on the host, not in a
container. Routed to the shared proxy with `stackfork route add` instead
of a container port, exactly the case a project whose frontend/backend run
on the host (no Dockerfile for the app at all) needs.

This example lives inside the stackfork repo itself rather than its own
Git checkout, so `--root`/`-f` below run from the **repo root**.

## Try it

```sh
git clone <this repo> stackfork
cd stackfork
stackfork prepare -f examples/vite-api/docker-compose.yml --hostname vite-api-demo.localhost
stackfork up
stackfork urls   # note the db port, e.g. tcp://127.0.0.1:20080
```

Install and start the app on the host, pointed at that port:

```sh
cd examples/vite-api
npm install
DATABASE_URL=postgres://postgres:example@127.0.0.1:20080/app npm run dev -- --port 5173
```

In another terminal, register the route and start the shared proxy if it
isn't already running:

```sh
stackfork proxy up
stackfork route add app 5173
```

A route always lands on the instance's own hostname (`urls` doesn't show
routes - that's a dashboard-only view; `stackfork where` shows the
hostname if you didn't set one explicitly with `--hostname`), so open
`http://vite-api-demo.localhost`, or `curl` it - `visits` climbs on every
request, same as the node-postgres example, proving the host-run app is
really talking to the isolated Postgres through the port stackfork
published.

## Clean up

```sh
stackfork route remove app
stackfork down --volumes
```

(and stop the `npm run dev` process).

## CI check

`ci-check.sh` runs the whole thing above for real - installs the app,
starts it, registers the route, curls it through the proxy, and confirms
the count is real Postgres data, not a stub.
