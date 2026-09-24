# testdata

Synthetic Docker Compose fixtures used by the isolation tests (`STACKFORK_LIVE=1 go test ./...`).
Each fixture is a self-contained
directory with one `compose.yaml`; nothing here names a real project.

- `pg17-fixed-name/`: a `db` service on `postgres:17` with a fixed `container_name`, a fixed
  published host port (5432), a named volume at `/var/lib/postgresql/data`, and a healthcheck.
  Exercises stripping `container_name` and `published` so two instances of the same fixture can
  run concurrently without colliding, plus (via an adapter the test writes at runtime) a
  re-prepare that drops a service from the source configuration and expects it torn down.
- `pg18-redis/`: a `postgres` service on `postgres:18-alpine` (no declared volume) plus a
  `redis:7-alpine` service. Exercises isolating multiple services in one stack and adapting
  onto the PG18 data directory layout (`/var/lib/postgresql`, not `/var/lib/postgresql/data`)
  via a volume override.
- `pg14-missing-initdb/`: a `postgres` service on `postgres:14` with a bind mount of a `db.sql`
  init file that does not exist in a scratch checkout. Exercises adapting/removing a broken
  bind mount via a volume override, alongside a healthcheck.
