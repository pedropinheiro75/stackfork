# Project page: design

Status: proposal for approval. Prototype: `prototype/index.html`. Screenshots: `shots/`.
Figma (for comments): [stackfork: Project page](https://www.figma.com/design/dXsrbi4Q1c8kR1MYd5ZHnN).
Read `critique.md` first for the problems this page solves.

To run the prototype, serve the repository root with any static server and open
`docs/design/project-page/prototype/index.html` (it links the real `tokens.css` and
`app.css`). Opening the file directly also works. Deep links:
`#project/acme-web?mode=container&checkout=login` shows the failed run.

The Figma file holds the Berth tokens as variables (color with Dark and Light modes,
spacing, radius), the components (Chip, Button, Branch pill, Workflow node,
Meter, Checkout row) and five frames: the page at 1440 in dark and light, the
page at 400, the workflow in container and hybrid side by side, and a selected
checkout with its failed run. Figma renders the system sans with Inter, because
SF Pro is a local font that the Figma renderer cannot use; the product keeps
`system-ui`.

## Goal

One page per repository that answers four questions, in this order:

1. What is checked out, and what runs right now?
2. How does it start, and where does each step run (Docker or this machine)?
3. What does it cost (CPU, memory, disk), per service and in total?
4. How is it configured (entry, secrets, env overrides)?

The page is an Operate surface in Berth: tables first, glyph plus word for every
state, one hairline system, no new colours.

## Key decisions

| # | Decision | Why | Rejected |
|---|---|---|---|
| 1 | A dedicated page at `#project/<name>`, with the rail still on **Repos** and a breadcrumb `Repos / acme-web`. | The dashboard already routes by hash (`#repos`). A child of Repos keeps the rail honest and the back button works. | `/project/<name>` server route: needs a new handler and CSP review for no gain. A modal or side drawer: too much content, no deep link. |
| 2 | The mode and the selected checkout are in the URL: `#project/acme-web?mode=container&checkout=login`. | A failed run becomes a link that someone can share or reopen. | Local storage only: state that a link cannot carry. |
| 3 | Section order: Checkouts, Startup workflow, Resources, Configuration. Each section collapses. | Frequency of use. Configuration changes rarely, so it goes last. | Workflow first: the person asked for it, but people come to start or open a checkout. The workflow sits one scroll below and is linked from each row. |
| 4 | The workflow is a **lane graph inside a table**: three lanes (stackfork, Docker, Host), one row per step, in plan order. | It keeps Berth's "columns, not sentences" rule and the LaneGraph look from Repos. Commands stay readable at full width. | A horizontal node-and-edge canvas: commands do not fit in nodes, and it fails at 400 px. Swimlane columns with cards: loses the table scan. |
| 5 | Mode is a **path difference**. Both modes' steps stay in place. Steps the viewed mode skips are hollow dashed "stations" off the path. Switching the segmented control redraws the path, and the rows that changed flash once. | The person sees *what changes* between container and hybrid (migrate moves from Docker to the host), not two unrelated lists. | Hiding the skipped steps (today's behaviour moves them to the end). A diff view: too abstract. |
| 6 | Two state columns: **Plan** (how much stackfork trusts the step: `verified`, `trusted`, `draft`, `untrusted`, `at start`, `<mode> only`) and **Run #N** (what happened: `Succeeded`, `Failed`, `Skipped`, `Running`, `Pending`, with time and reason). | Today one column mixes the two vocabularies. | One merged chip: "verified" next to "Running" reads like health. |
| 7 | The last run is an **overlay**. Select a checkout's last run (a button in the Checkouts row, or the whole row) and its run draws on the graph: node glyphs show the run state, the path after a failure is dashed ("not reached"), and a failed step opens its log tail with Retry. | It joins "how it starts" and "what happened" in one place. | A separate Runs section: one more table to cross-reference. |
| 8 | When the selected run used the other mode, the page switches to that mode. When the person then switches away, the overlay says "Run #44 used hybrid mode" and offers **Show hybrid**. | Never draw a run on a plan it did not follow. | Drawing it anyway, greyed: misleading. |
| 9 | The default mode is a tag in the segmented control (`Container DEFAULT`). **Make default** appears only when the viewed mode is not the default. | Viewing a mode and changing what Start uses are separate acts. Today the select does both. | A separate settings row: hides the link between the two. |
| 10 | Resources are **one table** grouped by checkout, with an "All checkouts" total row and a one-sentence summary that compares the total to the machine. | Berth bans hero metric cards. A table lines up host apps and containers in the same units. | Metric tiles per resource. Charts: no trend data yet (see decision 12). |
| 11 | CPU uses **% of one core**, as `docker stats` and `ps` report it (a build can show 84 % or 250 %). The summary converts the total to cores ("1.1 cores of 10"). | The same unit as the tools people already know; the summary gives the machine view. | Normalising to the machine (110 % becomes 11 %): it hides a single pegged process. |
| 12 | No sparklines in the first version. | `docker stats --no-stream` gives one sample per call. A trend needs a server-side ring buffer. Add it when sampling exists; the CPU and Memory cells have room for a 56 px line. | Client-side history: lost on every reload, wrong on a hidden tab. |
| 13 | Stopped checkouts stay in Resources with their volume size ("Stopped · volumes kept"). | The disk cost of a stopped stack is real and today it is invisible. | Running only. |
| 14 | Images count once, in a footnote under the table. | Checkouts share images; adding them per checkout double-counts. | An image column. |

## What moves out of the Repos list

The expanded repository row keeps only what helps to pick and start: the worktree
table (Lane, Branch, Path, Ahead/behind, State, action) and the branches not
checked out. Everything else moves to the page: live bar, plan bar and mode,
entry line, plan list, trust and gap callouts, secrets. The repository name becomes
a link to the page, and the expanded panel ends with an **Open project** link.
The Instances ProjectCard `⋯` menu changes **Show in Repos** to **Open project**.

## Page anatomy

### Header

`[AW] acme-web  ● 2 running  ◔ 1 starting  ▲ 1 draft step` · actions on the right:
**Live** switch, **Start…** (secondary; opens the existing branch picker), `⋯`
(Fetch, Copy path, Verify setup with your agent, Rescan this repo). Line 2 in mono:
path, default branch, fetch freshness. No primary button: nothing on a set-up
project is "the next step for a newcomer". A project that is not set up gets the
setup callout with **Set up with your agent** as the page's one primary.

### Checkouts

Columns: Lane · Branch · Path · Ahead/behind · Mode · State · Last run · actions.
Mode is mono (`container`, `hybrid`; hybrid in `fg` so it stands out). Last run is
a button: run chip (`● Succeeded`, `⊘ Failed at migrate`, `◔ Running 0:47`), run
number and age. The selected row gets the Berth selected style (`accent-soft` wash
plus a 1 px `focus` border). One visible action per row: **Open App** when running,
**Start** when stopped, **Logs** while starting, then `⋯`. Branches not checked out
sit in a closed disclosure under the table.

Default selection: the newest *finished* run. A run in progress shows its chip but is
not selected by default, so the first view is stable.

### Startup workflow

- **Bar**: segmented control (Container | Hybrid, with the `DEFAULT` tag), **Make
  default** when needed, and the overlay tag (`Last run of [fix/login] #38 ⊘ Failed
  1 d ago · 51 s  Hide`). The section subtitle shows the entry for the viewed mode
  (`App → nginx:80/` or `App → app:5173 on host`).
- **Summary sentence**: names what runs where ("db and mailpit run in Docker; api and
  app run on this machine"), counts steps per lane, and states host trust.
- **Lanes**: `stackfork` (preflight secrets, prepare), `Docker` (compose up, exec in a
  service), `Host` (exec `on: host`, host apps). Lane labels are lowercase 11 px so
  "stackfork" fits in 72 px. A lane whose steps all skip in the viewed mode keeps its
  dashed guide but its label goes to `fg-subtle`.
- **Path**: 2 px `fg-muted`, orthogonal: down the lane, then across at the node's
  centre when the next step runs elsewhere. A lane change is the visual answer to
  "this command runs on the host". After a failure the rest of the path is dashed
  `line-strong`.
- **Nodes** reuse the Berth glyphs as SVG: filled circle `ok`, triangle `attention`,
  cross in circle `failed`, ring `busy` (pulses; static under reduced motion), hollow
  circle `off`, hollow dashed circle `line-strong` for "not in this mode". Without an
  overlay the node shows the plan state; with one it shows the run state.
- **Step cell**: id in mono, kind under it (`exec · once schema`, `host app`,
  `compose up`, `preflight`). The command is mono; a note line carries the compose
  service list, the draft reason (with the `stackfork learn record step …` command) or
  "Skipped in hybrid mode".
- **Run cell**: chip, duration, reason (`already prepared`, `setup is a draft; seed not
  run`, `listening on :4000`, `not reached`). A failed step adds **Show log**, which
  opens a `Callout failed` under the row with the last lines of the log and **Retry**.
- **Draft**: one `attention` callout under the graph when the viewed mode has a draft
  step, with what it means ("a new hybrid environment has an empty database") and
  **Verify with your agent**.
- **Legend** under the graph, adapted to the overlay (plan glyphs or run glyphs).

### Resources

- Subtitle: `Sampled 4 s ago · every 10 s while this page is open`. Right: `CPU: 100 %
  = one core`.
- Summary sentence: `acme-web uses 1.1 cores of 10, 3.5 GB of 32 GB memory and 3.9 GB
  of disk (2 GB volumes, 1.8 GB images).`
- Table columns: Service · Runs in · CPU · Memory · Disk. Group rows per checkout
  (branch pill, state chip, mode, subtotals) on `surface-2`; service rows indented,
  with the service logo. Runs in: `Docker` plus the image, or `Host app` plus `pid ·
  up`. "All checkouts" total row at the bottom.
- Bars: 56 × 4 px, the AheadBehindBar shape. CPU bar against one core. Memory bar only
  when the container has a limit. Disk shows the volume size; host apps show `—`
  (their files live in the checkout).
- Thresholds (the same steps as the disk banner): memory against its limit ≥ 85 %
  `attention`, ≥ 95 % `failed`; text and bar take the tone, and one `attention`
  callout names the service and the fix. CPU never goes red (busy is not broken); it
  gets `attention` only after 2 min above 80 % on a running service, never while
  building. No limit means no memory tone.
- Units: `MB` under 1 GB (integer), `GB` with one decimal above; `%` with one decimal;
  a non-breaking space before every unit; numbers right-aligned and tabular.
- Cost: `docker stats --no-stream` for the project's containers only, every 10 s, only
  while the page is visible and the section is open (Page Visibility API). Volume
  sizes come from `docker system df -v`, cached for 5 min (it is slow). Host apps
  already have RSS and CPU.

### Configuration

Label column (160 px) plus content, like Settings: **Entry** (recorded target, and
the hybrid target), **Secrets** (the existing secret table and actions), **Env
overrides** (key, value, service on branch, **Reset**), **Branch cleanup** (the select
that today sits in the live bar).

## States

| State | What shows |
|---|---|
| Loading | Breadcrumb, `Loading billing-api…` status line, a header block and two section skeletons (static, no shimmer). |
| Not found | EmptyState: "No repository named old-site", one sentence, **Back to Repos**, **Rescan**. |
| Not set up | Header with `○ Not set up`; the setup callout with the page's one primary. No workflow or resources sections. |
| No recorded plan | Workflow shows the derived steps with `derived` plan chips and **Learn with your agent** (today's derived list, drawn on the lanes). |
| Start would be refused | The `attention` callout replaces the graph, as today. |
| Docker unreachable | Shell banner (existing). Container rows show `—` with "Docker unreachable" in the title; host apps still show. |
| Stats unavailable | Resources subtitle reads `Stats unavailable: docker stats timed out` in `attention`; last values stay, marked stale (`fg-subtle`). |
| No checkouts running | Resources summary reads "Nothing runs. Volumes keep 2 GB." |

## Responsive

- **≥ 1400 px**: all columns.
- **1100 to 1399 px**: Checkouts drops Ahead/behind (it stays in Repos).
- **720 to 1099 px**: Checkouts drops Path; lanes shrink to 60 px.
- **< 720 px (400 px target)**: the rail becomes the top strip (existing). Checkout
  rows become three lines (branch and state; path · ahead/behind · mode; run button
  and actions). The graph keeps three 22 px lanes on the left of every step; the step
  row stacks id plus location word (`in Docker`, `on this machine`), the command, then
  the run chip. Steps of the other mode drop their run line. Resource rows become two
  lines with `CPU`, `MEM`, `DISK` labels and no bars. Nothing scrolls sideways
  (checked: `scrollWidth` is 400).

## Accessibility

- The graph is `aria-hidden`. Every step carries its location and skip state as text
  (`, runs on this machine, skipped in container mode`), visible at 400 px.
- Steps are an `ol` labelled "Startup steps in hybrid mode". The mode control is a
  radio group in a `fieldset`. The run selector is a button with `aria-pressed`; the
  whole row is only a larger mouse target for the same button.
- Collapse buttons use `aria-expanded` and `aria-controls`; collapsing is instant.
- Every chip is glyph plus word; the run state never depends on node colour.
- After a mode switch or a selection, focus stays on the control that caused it.
- The failed log is a focusable `pre` with a label; **Show log** uses `aria-expanded`.
- Skip link, one `h1` per view, `h2` per section, `scroll-padding-top` for the sticky
  toolbar.

## Motion

Only one moment: after a mode switch or a new overlay, rows whose state changed get
the existing Berth card flash (`accent-soft` fading over 600 ms). Edges change colour
in 180 ms. Section collapse, overlay changes from the keyboard and polling re-renders
do not animate. Under reduced motion the flash becomes a static `focus` border and
the busy ring stops pulsing.

## Data the page needs

| Data | Status |
|---|---|
| Plan steps with kind, lane (from kind plus `on`), modes, once, trust, verified, draft | Exists (`knowledge.Plan`, `lifecycle.PlanView`). Add a `lane` field to the step view. |
| Last run per instance: steps with state, reason, timing, log path, exit code | Exists (`runs/<id>.json`). Add the run to the page view; only the menu uses it today. |
| Host apps: pid, RSS, CPU, uptime, listening | Exists (`inventory.HostInfo`). |
| Container CPU %, memory usage and limit, net and block I/O | **Add**: `docker stats --no-stream --format json` filtered by the project label. |
| Volume and image sizes | **Add**: `docker system df -v --format json`, cached 5 min. |
| Machine memory and cores | **Add**: `runtime.NumCPU()` and total memory (sysctl `hw.memsize` on macOS). |

## Open questions

1. Is the default mode a repository setting (as drawn) or does every Start ask?
2. Should Resources include net and block I/O? They are cheap (same `docker stats`
   call) but add two columns; drawn as off for now.
3. Is a memory limit common in these Compose files? If not, the memory tone rarely
   fires and a "share of machine" bar may serve better.
4. Should the Repos expanded row go away completely (click opens the page), or stay
   as the quick-start list drawn above?
5. Sparklines: worth a small server-side sample buffer (60 samples per service)?
