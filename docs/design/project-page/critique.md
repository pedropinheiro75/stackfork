# Critique: the expanded repository panel (Repos view)

Target: `internal/dashboard/templates/repos.html.tmpl` (the `repo-panel` block) and
`instances.html.tmpl` (ProjectCard, VersionCard), read against DESIGN.md (Berth).
Method: single-context review from the templates and the Berth spec. No live
render: the running dashboard was out of bounds for this task, and the committed
screenshots in `docs/screenshots/` predate Berth.

## What works

- **Identity is solid.** BranchPill in the lane colour, the LaneGraph column and
  relative paths make each checkout easy to recognise.
- **Status is honest.** Every chip is glyph plus word, and commands stay visible as
  mono lines under menu items. Nothing destructive is one click away.
- **The copy is calm and literal** ("A Start would be refused", "Names and status
  only. A value never reaches this page.").

## Priority issues

| Sev | Problem | Why it matters | Fix |
|---|---|---|---|
| P1 | **The most used part is at the bottom.** The panel stacks up to 11 regions in one column: errors, meta, live bar, setup callout, plan bar, entry line, plan table, gap and trust callouts, secrets, worktrees, branches. The worktree table, where Start and Open App live, comes after the plan and the secrets. | People open the panel to start or open a checkout. They scroll past configuration every time. | Put checkouts first. Move plan, secrets and entry into their own sections on a dedicated page. |
| P1 | **The plan table does not show where a step runs.** KIND reads `exec`, `host`, `compose-up`. "In Docker" or "on the host" is only a suffix of the command text (`in app`, `on host`). | The first thing the person asked: "which command runs on the host and which in a container". Today the answer needs reading every command. | Show the location as a lane (Docker / Host), and keep it as text for screen readers. |
| P1 | **Mode is invisible as a path.** Mode is a select in the plan bar. Changing it rebuilds the list, and skipped steps move to the end, out of order. | A person cannot compare container and hybrid. The difference ("migrate runs on the host in hybrid") is the whole point of the mode. | Keep steps in plan order. Draw the active path; show other-mode steps in place, dimmed, with the reason. |
| P2 | **One STATE column holds three vocabularies.** Trust (`trusted`, `untrusted`), knowledge (`verified`, `proposed`, `derived`) and mode (`skipped`) share one chip column. The worktree table has another STATE column with instance states. | Same chip shape, four meanings in one panel. "Verified" next to "Running" reads like a health state. | Split plan state (how much we trust the step) from run state (what happened last time). |
| P2 | **The last run is hidden.** The plan shows what would run. What did run (timing, skipped reasons, the failed step) lives in a menu item on the Instances card. | "Why is fix/login broken?" needs two views and a menu. | Overlay the selected checkout's last run on the plan. |
| P2 | **Resources are invisible.** Host apps show `pid · MB · % CPU` as a mono line under the service name. Containers show nothing. Disk per instance is not shown, and a stopped instance still holds its volumes. | The person asked for RAM, CPU and disk per app and in total. | Add a resource section with totals and a per-service table. |
| P3 | **The live bar is a sentence of controls.** Switch, chip, reason, Resume, Trust, cleanup select, grace input and delete-branch checkbox share one wrapping flex line. | It reads as one control and wraps unpredictably. | Header holds the switch and chip. Cleanup settings go to a settings group. |
| P3 | **Three nesting levels.** Table row, then `surface-2` panel, then inner tables with their own hairlines. At 400 px the plan rows and the secrets rows wrap inside a narrow panel. | Berth allows card plus one nested layer. A third layer costs width and clarity. | A page gives every section the full content width. |

## Heuristics (0 to 4)

Status visibility 2 · real-world match 3 · control and freedom 3 · consistency 2 ·
error prevention 3 · recognition over recall 2 · efficiency 2 · minimalist design 2 ·
error recovery 3 · help 3. **Total 25/40**, acceptable. The page is honest and safe,
but it is hard to read as a whole.

## Personas

- **Daily user (power user):** to see the run of one worktree, they expand the repo,
  go to Instances, open `⋯`, then **Last run**. There is no URL to reopen the state.
- **First-time user:** "compose-up", "exec", "once schema" and "derived" have no
  visible meaning. In hybrid mode nothing tells them that two apps will run on their
  machine, which needs trust.

## Questions

- Does the Repos list need the plan at all, or only "can I start this, and where"?
- Is mode a property of the repository (the default for Start) or of each instance
  (how this one runs)? Today it is both, and the UI shows only the first.
