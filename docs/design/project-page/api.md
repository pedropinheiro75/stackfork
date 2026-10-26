# Project page: API contract and file ownership

The design is in `design.md` and `prototype/` (the prototype's `data.js` is the
shape every UI part renders). This file fixes the JSON the server returns so
the parts can be built in parallel. Field names are snake_case. Times are RFC 3339.

## Routes

| Route | Owner | Returns |
|---|---|---|
| `GET /api/project?name=NAME` | B | `Project` (below) |
| `GET /api/project/resources?name=NAME&range=15m\|1h\|6h` | A | `Resources` (below) |

`NAME` is the repo name as the Repos list shows it (`repos.Repo.Name`); when two
repos share a name, `path=` (the main checkout path) disambiguates. Unknown
project: 404 `{"error": "..."}`. Both routes are read-only GETs with the
dashboard's usual Host check; no token needed (same as `/api/instances`).

The page view is `#project/NAME` (query part after `?`: `mode=container|hybrid`,
`checkout=<checkout key>`). The Repos list links each repo name to it.

## Project

```json
{
  "name": "acme-web", "path": "/abs/main/checkout", "default_branch": "main",
  "default_mode": "container", "modes": ["container", "hybrid"],
  "knowledge": {"state": "verified|draft|borrowed|none", "from": "worktree name when borrowed", "verified_at": "..."},
  "live": {"on": false, "state": "off|on|paused", "reason": ""},
  "entry": {"state": "recorded|guessed|none", "target": "app:5173 on host|nginx:80/", "url": "http://…"},
  "checkouts": [Checkout],
  "branches_not_checked_out": [{"branch": "backup/x", "upstream": "none|ahead N behind M"}],
  "workflow": {"container": Graph, "hybrid": Graph},
  "config": {
    "secrets": [ {"name","reference","status","source","from","hint"} ],
    "env_overrides": [ {"service","name","value","masked","checkout"} ],
    "branch_cleanup": "off|suggest|auto"
  }
}
```

`Checkout`:
```json
{"key": "feature/invoices", "branch": "feature/invoices", "path": "…", "main": false,
 "changes": true, "ahead": 3, "behind": 12, "upstream": "origin/feature/invoices",
 "mode": "hybrid", "state": "running|partial|starting|stopped|no-stack",
 "project": "sf-…",                      // instance name, "" when no stack
 "open_url": "http://…",                  // Open App target, "" when none
 "last_run": {"id": 44, "command": "start-env", "outcome": "succeeded|failed|running", "mode": "hybrid",
              "failed_step": "", "started_at": "…", "duration_ms": 52000,
              "steps": {"<step id>": {"state": "succeeded|failed|skipped|running|pending", "ms": 4200, "reason": ""}}}}
```

`Graph` (one per mode; built by `lifecycle.PlanGraph(plan, mode)` from the same
rules the runner uses: `Plan.ForMode`, seed decision, host steps, derived plan
when no knowledge):
```json
{"nodes": [
   {"id": "start", "kind": "terminal"},
   {"id": "secrets", "kind": "step", "step_kind": "secrets", "where": "stackfork|docker|host",
    "service": "", "title": "secrets", "subtitle": "preflight", "command": "resolve secrets",
    "command_expanded": "", "once": "", "plan_state": "at-start|verified|trusted|draft|untrusted|derived",
    "skip_reason": "", "in_mode": true},
   {"id": "mode", "kind": "decision", "label": "Mode?", "branches": ["container","hybrid"]},
   {"id": "new-volume", "kind": "decision", "label": "New data volume?", "branches": ["yes","no"]},
   {"id": "ready", "kind": "terminal", "entry_url": "…"}],
 "edges": [{"from": "start", "to": "secrets", "label": ""},
           {"from": "mode", "to": "up", "label": "container"}],
 "parallel": [["api","app"]]}
```
Every step node has `in_mode` (false = shown dimmed on the inactive branch).

## Resources

```json
{"range": "6h", "since": "…", "interval_s": 10, "short_history": true,
 "machine": {"cores": 10, "memory_bytes": 34359738368},
 "totals": {"cpu_pct": 110.5, "mem_bytes": 3758096384, "disk_bytes": 4187593113,
            "volumes_bytes": 2147483648, "images_bytes": 1932735283},
 "series": [ {"checkout": "main", "kind": "app|database|host-app|utility",
              "service": "app", "where": "docker|host",
              "points": [[unix_seconds, cpu_pct, mem_bytes], …]} ],
 "services": [ {"checkout": "main", "service": "app", "where": "docker|host", "image": "acme-web-app:dev",
                "pid": 0, "state": "running|stopped|starting",
                "cpu_pct": 6.2, "mem_bytes": 1825361100, "mem_limit_bytes": 2147483648,
                "disk_bytes": 0, "uptime_s": 7200} ],
 "disk": [ {"checkout": "main", "volumes_bytes": 854589440} ],
 "images": [ {"name": "postgres:17", "bytes": 459276288} ]}
```
A stopped period is a gap: no points for that span (never zeros). An empty
`points` list means "collecting samples".

## File ownership (each agent edits only its files; shared files below are done)

- **A (metrics backend):** new `internal/metrics/` (sampler, ring buffer, docker stats, volume and image sizes, host app samples via the existing host process code), `internal/dashboard/project_resources.go` (`handleProjectResources`, starts/stops the sampler with the server).
- **B (project read model):** new `internal/lifecycle/plangraph.go` (+ tests), `internal/dashboard/project.go` (`handleProject`).
- **C (page shell, checkouts, config):** `internal/dashboard/static/project.js`, `internal/dashboard/static/project.css`, the `#view-project` markup in `templates/index.html.tmpl` (already stubbed), the Repos list link to the page (`templates/repos.html.tmpl` name cell only).
- **D (workflow canvas):** `internal/dashboard/static/project-workflow.js`, `project-workflow.css`. Exposes `window.sfProjectWorkflow.render(el, project, {mode, checkout, onSelect})`.
- **E (resources charts):** `internal/dashboard/static/project-resources.js`, `project-resources.css`. Exposes `window.sfProjectResources.render(el, resources, {range, onRange})`.

Shared, already in place: routes in `dashboard.go`, stub handlers, the empty
static files and their `<script>`/`<link>` tags, `<main id="view-project">`.
C wires D and E into the page.
