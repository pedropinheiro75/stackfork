# Project knowledge

Status: **verified**

## Compose

Files: docker-compose.yml

| service | image | db | ports | healthcheck |
|---|---|---|---|---|
| app | (build) | - | 3000:3000 | false |
| db | postgres:17 | postgres 17 | 5432:5432 | true |

## Scripts

- **dev** (start, package.json): `node server.js`

## What prepare would refuse or rewrite

- [rewrite] volumes.pgdata: volumes.pgdata: renamed to sf-learn_pgdata so concurrent stacks don't collide
- [rewrite] networks.default: networks.default: renamed to sf-learn_default so concurrent stacks don't collide
- [rewrite] services.app: services.app: image renamed to sf-learn-app:dev for the isolated build
- [rewrite] services.app.ports[0]: services.app: published port stripped so Docker assigns a random host port
- [rewrite] services.db.ports[0]: services.db: published port stripped so Docker assigns a random host port

## Answers

- Q: the app service has no Docker healthcheck - should one be added, or is polling curl from the caller enough?
  A: polling is enough for this example: the app already retries its own migration on startup, and ci-check.sh already polls with curl before asserting anything, so a Docker-level healthcheck would just duplicate that without adding real value here

## Verified

- Date: 2026-09-28
- Checks: database identity (fresh Postgres volume per instance, verified via clone-db test), app readiness (GET / returns real, incrementing Postgres-backed visit count), two instances run side by side with independent data (ci-check.sh, real Docker)
- Confirmation: "verified via examples/node-postgres/ci-check.sh, which passed against real Docker: two worktrees ran side by side, ports and Postgres data stayed independent, and the visits counter reflected real inserted rows"

