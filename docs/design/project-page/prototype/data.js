// Fake data for the prototype. Shapes follow what the dashboard already has
// (knowledge plan steps, runs/<id>.json steps, host app stats) plus the
// resource fields the design adds (docker stats, volume and image sizes).
// Names and paths are neutral examples.
window.PP = {
  project: {
    name: "acme-web",
    lane: 1,
    path: "~/clients/acme/acme-web",
    folder: "clients/acme",
    defaultBranch: "main",
    fetched: "2 h ago",
    defaultMode: "container",
    modes: ["container", "hybrid"],
    machine: { memGB: 32, cores: 10 },
    entry: { container: "nginx:80/", hybrid: "app:5173 on host" },
    trust: "Host steps trusted on this machine 3 d ago"
  },

  // lane: sf = stackfork itself, docker = a container, host = this machine.
  steps: [
    { id: "secrets", kind: "preflight", lane: "sf", cmd: "resolve secrets", plan: "start" },
    { id: "prepare", kind: "prepare", lane: "sf", cmd: "prepare --profile dev", plan: "verified" },
    { id: "up", kind: "compose up", lane: "docker", cmd: "compose up", modes: ["container"], plan: "verified", note: "app, db, nginx, mailpit" },
    { id: "up-deps", kind: "compose up", lane: "docker", cmd: "compose up  without app, nginx", modes: ["hybrid"], plan: "verified", note: "db, mailpit" },
    { id: "migrate", kind: "exec · once schema", lane: "docker", cmd: "npm run db:migrate  in app", modes: ["container"], plan: "verified" },
    { id: "migrate-host", kind: "exec · once schema", lane: "host", cmd: "npm run db:migrate  on host", modes: ["hybrid"], plan: "trusted" },
    { id: "seed", kind: "exec · once data", lane: "docker", cmd: "npm run db:seed  in app", modes: ["container"], plan: "verified" },
    { id: "seed-host", kind: "exec · once data", lane: "host", cmd: "npm run db:seed  on host", modes: ["hybrid"], plan: "draft",
      note: "Proposed by learn, not recorded. A Start skips it until you record it: stackfork learn record step --id seed-host --kind exec --once data --cmd …" },
    { id: "api", kind: "host app", lane: "host", cmd: "npm run dev -w api  port 4000", modes: ["hybrid"], plan: "trusted" },
    { id: "app", kind: "host app", lane: "host", cmd: "npm run dev -w web  port 5173", modes: ["hybrid"], plan: "trusted" }
  ],

  checkouts: [
    { key: "main", branch: "main", path: "main checkout", main: true, ab: "up to date",
      mode: "container", state: "running", host: "acme-web-9b21e07d4c3a.localhost",
      run: { id: 41, mode: "container", when: "3 h ago", outcome: "succeeded", dur: "1 min 12 s", steps: {
        secrets: ["succeeded", "0.4 s"], prepare: ["skipped", "", "already prepared"],
        up: ["succeeded", "38 s"], migrate: ["succeeded", "6.1 s"], seed: ["skipped", "", "already seeded (once: data)"] } } },
    { key: "invoices", branch: "feature/invoices", path: ".worktrees/feature-invoices", ab: [3, 12], dirty: true,
      mode: "hybrid", state: "running", host: "acme-web-2ffa6841c75d.localhost",
      run: { id: 44, mode: "hybrid", when: "2 h ago", outcome: "succeeded", dur: "52 s", steps: {
        secrets: ["succeeded", "0.5 s"], prepare: ["succeeded", "2.1 s"], "up-deps": ["succeeded", "14 s"],
        "migrate-host": ["succeeded", "4.2 s"], "seed-host": ["skipped", "", "setup is a draft; seed not run"],
        api: ["succeeded", "3.1 s", "listening on :4000"], app: ["succeeded", "2.4 s", "listening on :5173"] } } },
    { key: "login", branch: "fix/login", path: ".worktrees/fix-login", ab: [1, 0],
      mode: "container", state: "stopped", host: "acme-web-51d0a9e3b7c2.localhost",
      run: { id: 38, mode: "container", when: "1 d ago", outcome: "failed", dur: "51 s", failedAt: "migrate", steps: {
        secrets: ["succeeded", "0.4 s"], prepare: ["succeeded", "1.8 s"], up: ["succeeded", "41 s"],
        migrate: ["failed", "3.4 s", "exit 1"], seed: ["pending", "", "not reached"] },
        log: "> acme-web@1.4.0 db:migrate\n> knex migrate:latest\nUsing environment: development\nmigration file \"20261019_add_login_attempts.js\" failed\nmigration failed with error: relation \"login_attempts\" already exists",
        retry: "stackfork start-env --resume" } },
    { key: "search", branch: "spike/search", path: ".worktrees/spike-search", ab: "no upstream",
      mode: "container", state: "starting", host: "acme-web-c4e8f61a0d27.localhost",
      run: { id: 45, mode: "container", when: "just now", outcome: "running", dur: "0:47", steps: {
        secrets: ["succeeded", "0.3 s"], prepare: ["succeeded", "2.4 s"], up: ["running", "0:44", "building image app"],
        migrate: ["pending"], seed: ["pending"] } } }
  ],

  branches: [
    { name: "release/1.5", ab: [14, 36] },
    { name: "chore/deps-bump", ab: [2, 0] }
  ],

  // cpu: % of one core, as docker stats and ps report it. mem/limit/disk: MB.
  resources: [
    { checkout: "main", services: [
      { name: "app", kind: "node", where: "docker", image: "acme-web-app:dev", cpu: 6.2, mem: 1760, limit: 2048, disk: 0 },
      { name: "db", kind: "postgres", where: "docker", image: "postgres:17", cpu: 1.4, mem: 186, disk: 812, vol: "pgdata" },
      { name: "nginx", kind: "nginx", where: "docker", image: "nginx:1.27", cpu: 0.0, mem: 11, disk: 0 },
      { name: "mailpit", kind: "mail", where: "docker", image: "axllent/mailpit", cpu: 0.1, mem: 24, disk: 3, vol: "mail" } ] },
    { checkout: "invoices", services: [
      { name: "db", kind: "postgres", where: "docker", image: "postgres:17", cpu: 0.9, mem: 152, disk: 644, vol: "pgdata" },
      { name: "mailpit", kind: "mail", where: "docker", image: "axllent/mailpit", cpu: 0.0, mem: 21, disk: 1, vol: "mail" },
      { name: "api", kind: "node", where: "host", pid: 48211, up: "2 h", cpu: 11.8, mem: 286, disk: null },
      { name: "app", kind: "node", where: "host", pid: 48230, up: "2 h", cpu: 2.9, mem: 412, disk: null } ] },
    { checkout: "search", services: [
      { name: "app", kind: "node", where: "docker", image: "building…", cpu: 84.0, mem: 620, disk: 0, note: "building image" },
      { name: "db", kind: "postgres", where: "docker", image: "postgres:17", cpu: 3.2, mem: 96, disk: 48, vol: "pgdata" } ] },
    { checkout: "login", stopped: true, services: [
      { name: "db", kind: "postgres", where: "docker", image: "postgres:17", cpu: null, mem: null, disk: 590, vol: "pgdata" } ] }
  ],
  images: { mb: 1870, list: "acme-web-app:dev 1.31 GB · postgres:17 438 MB · nginx:1.27 52 MB · mailpit 31 MB" },

  secrets: [
    { name: "STRIPE_SECRET_KEY", hint: "payments in api", ref: "op://Dev/acme-stripe/secret", chip: ["ok", "●", "Resolves"] },
    { name: "MAPS_API_KEY", hint: "store locator", ref: "keychain:acme-maps", chip: ["failed", "⊘", "Missing"] }
  ],
  env: [
    { checkout: "invoices", service: "api", key: "LOG_LEVEL", value: "debug" },
    { checkout: "invoices", service: "api", key: "FEATURE_INVOICES", value: "1" }
  ],

  repos: [
    { name: "acme-web", folder: "clients/acme", chip: ["ok", "●", "2 running"], wt: 4, br: 2, last: "just now" },
    { name: "billing-api", folder: "clients/acme", chip: ["off", "○", "Stopped"], wt: 1, br: 3, last: "5 h ago" },
    { name: "notes-cli", folder: "side", chip: ["off", "○", "Not set up"], wt: 1, br: 0, last: "3 w ago" }
  ]
};
