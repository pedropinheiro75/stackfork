# Demo media

Everything in `docs/` that the README shows comes from one fake world: two example
projects, `acme-web` (app in a container plus Postgres, with a `feature-invoices`
worktree) and `acme-api` (hybrid: Postgres in Compose, the Vite app on the host).
No real project, path or host name appears in any frame.

| File | Shows |
|---|---|
| `../demo.tape`, `../demo.gif` | Terminal demo: `learn`, `start-env` with its steps, a worktree, `list`, `urls`. |
| `../dashboard.gif` | Dashboard tour: Instances, the env overrides list and Environment dialog, the Project page (workflow, resources, config), Repos with the worktrees lane. |
| `instances-overview-{light,dark}.png` | Instances page: acme-web (main and feature-invoices) and acme-api, with endpoints. |
| `instances-hybrid-host-app-{light,dark}.png` | The acme-api card: Postgres in Docker and the host app row with pid, memory and CPU. |
| `project-workflow-{light,dark}.png` | acme-api workflow canvas: secrets, prepare, compose up, the "New data volume?" decision, seed, the Mode? split and the host app. |
| `project-resources-{light,dark}.png` | acme-web Resources: memory and CPU charts per checkout, disk, per-service table (about 30 minutes of samples). |
| `environment-dialog-{light,dark}.png` | Environment dialog of the app service on the feature-invoices worktree (3 overrides). |
| `repos-{light,dark}.png` | Repos page with acme-web expanded: recorded plan and the main / feature-invoices lane. |
| `demo-world.sh` | Builds and resets the demo world (below). |

The PNGs are 1440 px wide (the dialog is cropped to its box, the Repos page starts under the top bar).

## Regenerate

You need Go, Docker, npm (network, for `npm ci`), vhs, ttyd, ffmpeg and agent-browser.
Run from the repository root. The world lives in a short temp path so the dashboard
shows `/private/tmp/sf-demo/code/...` and never a personal path.

```sh
export DEMO_TMP=/private/tmp/sf-demo
docs/screenshots/demo-world.sh build      # copy examples/, git init, learn, record plans, trust the host step, verify; own proxy sf-demo-proxy on :8099
. $DEMO_TMP/env.sh                        # wrappers: sfs = stackfork --state-dir $T/state ; own config dir
docs/screenshots/demo-world.sh start      # acme-web main + feature-invoices worktree (3 env overrides) + acme-api in hybrid mode
docs/screenshots/demo-world.sh dashboard  # dashboard on http://127.0.0.1:8765 (pid in $DEMO_TMP/dashboard.pid)
# let it run at least 3 minutes (the Resources charts need samples); light traffic makes the CPU chart move:
#   while true; do curl -s -o /dev/null http://<instance hostname>.localhost:8099/; sleep 0.3; done
```

Screenshots: with agent-browser at 1440x1040 (project pages at 1440x2900 so every section paints),
`agent-browser set media light|dark`, then open `#instances`, `#project/acme-api`, `#project/acme-web`, `#repos`:

- overview: viewport screenshot of `#instances`;
- hybrid: click the `app` button of the acme-api host row, crop `1440:392:0:62`;
- dialog: click the `3 env overrides` chip, then `Environment...`, crop the dialog box plus 30 px;
- workflow: `#project/acme-api`, element screenshot of `#sec-workflow` (crop with 14 px margin);
- resources: `#project/acme-web`, `#sec-resources`;
- repos: `#repos`, expand `Worktrees of acme-web`, crop `1440:722:0:48`.

Dashboard video: open `#instances` at 1440x900 dark, `agent-browser record start dash.webm` (no `--cursor`: its overlay is huge),
scroll, open the env chip and dialog, go to Repos, click acme-web, scroll the Project page, back to Repos and expand the worktrees,
`record stop`; then `ffmpeg -i dash.webm -vf "setpts=PTS/1.15,fps=10,scale=1100:-1,split[a][b];[a]palettegen=max_colors=64:stats_mode=diff[p];[b][p]paletteuse=dither=none:diff_mode=rectangle" docs/dashboard.gif`.

Terminal demo: `docs/screenshots/demo-world.sh reset` is run by the tape itself.

```sh
DEMO_TMP=$DEMO_TMP vhs docs/demo.tape
```

Clean up (stops the dashboard, removes instances, volumes, images, the demo proxy and the temp dir):

```sh
docs/screenshots/demo-world.sh destroy
```
