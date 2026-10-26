# Project page: design

Status: proposal for approval. Prototype: `prototype/index.html`. Screenshots: `shots/`.
Figma (for comments): [stackfork: Project page](https://www.figma.com/design/dXsrbi4Q1c8kR1MYd5ZHnN).
Read `critique.md` first for the problems this page solves.

To run the prototype, serve the repository root with any static server and open
`docs/design/project-page/prototype/index.html` (it links the real `tokens.css` and
`app.css`). Opening the file directly also works. Deep links:
`#project/acme-web?mode=container&checkout=login&step=migrate` shows the failed run
with its side panel open; `&range=6h`, `&by=kind` and `&history=short|empty` set the
Resources charts.

The Figma file holds the Berth tokens as variables (color with Dark and Light modes,
the four chart series, spacing, radius), the components (Chip, Button, Branch pill,
Meter, Checkout row, Node card, Decision pill, Terminal pill, Connector) and these
frames, each in dark and light: the page at 1440, "Workflow · canvas", "Workflow ·
canvas · failed step panel" and "Resources · charts". The lane-table frames and the
first 400 px frame moved to the page "Superseded (lane table)". Figma renders the system sans with Inter, because
SF Pro is a local font that the Figma renderer cannot use; the product keeps
`system-ui`.

## Goal

One page per repository that answers four questions, in this order:

1. What is checked out, and what runs right now?
2. How does it start, and where does each step run (Docker or this machine)?
3. What does it cost (CPU, memory, disk), per service and in total?
4. How is it configured (entry, secrets, env overrides)?

The page is an Operate surface in Berth: tables first, glyph plus word for every
state, one hairline system. The only new colours are four chart series tokens.

## Key decisions

| # | Decision | Why | Rejected |
|---|---|---|---|
| 1 | A dedicated page at `#project/<name>`, with the rail still on **Repos** and a breadcrumb `Repos / acme-web`. | The dashboard already routes by hash (`#repos`). A child of Repos keeps the rail honest and the back button works. | `/project/<name>` server route: needs a new handler and CSP review for no gain. A modal or side drawer: too much content, no deep link. |
| 2 | The mode and the selected checkout are in the URL: `#project/acme-web?mode=container&checkout=login`. | A failed run becomes a link that someone can share or reopen. | Local storage only: state that a link cannot carry. |
| 3 | Section order: Checkouts, Startup workflow, Resources, Configuration. Each section collapses. | Frequency of use. Configuration changes rarely, so it goes last. | Workflow first: the person asked for it, but people come to start or open a checkout. The workflow sits one scroll below and is linked from each row. |
| 4 | The workflow is a **node canvas**: a vertical flow of node cards on a dotted canvas, from a **Start** pill to a **Ready** pill that shows the entry. One card per step: an icon for where it runs (stackfork, Docker, this machine), the step id, kind and once, the command on one line, a plan badge and, with an overlay, a run badge. | The person asked for an n8n/Zapier-style flow after seeing the lane table. Cards carry more per step (where, plan, run) than a table row, and branches read as branches. | The lane graph inside a table (the first proposal): rejected on the person's feedback; it read as a table, not a flow. A horizontal canvas: too wide with commands in the cards. |
| 5 | **Decision pills only where the runner decides.** `Mode?` splits into container and hybrid. `New data volume?` splits into yes (the seed runs right after compose-up) and no (skip seed, data already exists). Host apps that start together sit side by side and join into Ready. | These are the real decisions in `internal/lifecycle` (`Plan.ForMode`, `UpAndSeed` with `PlanSeedIn`, `RunOrder`). A once-schema step has no decision: it runs on every Start, unless the seed already ran it ("ran with the seed") or it is a draft. | A `Schema changed?` pill: the runner does not decide that. Showing prepare's "already prepared" as a pill: real, but it is a skip, so it is the card's run badge. |
| 6 | Mode is a **path difference**. Both mode branches are drawn; the viewed one is solid, the other dimmed with dashed connectors. The segmented control chooses the viewed mode; clicking a branch label does the same. | The person sees what moves between modes (migrate goes from Docker to this machine, two apps start on the host) without leaving the page. | Hiding the other mode: the difference disappears. |
| 7 | Two badges per card: **plan** (verified, trusted, draft, untrusted, at start) and **run** (Succeeded 4.2 s, Failed, Skipped with reason, Running, Pending). | One vocabulary per badge; the plan says how far stackfork trusts the step, the run says what happened. | One merged badge: "verified" next to "Running" reads like health. |
| 8 | The last run is an **overlay**: select a checkout's last run and its path is coloured; the branch the run took is solid, the other dashed; after a failure the rest is dashed and the Ready pill says "not reached". When the run used the other mode, the page switches mode, and offers **Show hybrid** if the person switches back. | It joins "how it starts" and "what happened". Never draw a run on a plan it did not follow. | A separate Runs table. |
| 8a | Clicking or focusing a card opens a **side panel** (a bottom sheet at 400 px), not a modal: where it runs, plan and trust, the full command as stackfork runs it, the values it fills in (`$PORT` → 5173, `$DATABASE_URL` from the instance `.env`), the last 3 runs of the step, the log tail with **Retry** on a failure, and how to change the step: the copyable `stackfork learn record step …` command. | The panel keeps the canvas in view. Agents record steps; a person reads and trusts them, so the panel shows the command instead of an edit form. | A modal: hides the flow. An edit form: editing goes through learn. `{{port}}` placeholders: stackfork has no template syntax; `port_env` sets environment variables, so the panel shows those. |
| 8b | No "+" on connectors, and the **Step kinds** palette is read-only and collapsed by default (a toggle in the bar). | Nothing on this page adds a step; a "+" would promise an editor. The palette documents the kinds. | An always-open left rail: it costs 220 px of canvas for reference text. |
| 9 | The default mode is a tag in the segmented control (`Container DEFAULT`). **Make default** appears only when the viewed mode is not the default. | Viewing a mode and changing what Start uses are separate acts. Today the select does both. | A separate settings row: hides the link between the two. |
| 10 | Resources open with **charts**, then the per-service table. Memory over time is a **stacked area by checkout**; a toggle stacks it by service kind (database, app, host app, utility). CPU over time is **one line per checkout**. Disk is a **horizontal bar per checkout**: its volumes plus its share of the images. The one-sentence machine comparison stays above the charts. | The person asked for charts. Stacked area shows the total and who adds to it; lines suit CPU, which spikes and does not sum meaningfully on one scale; disk is not a time series. | Dual-axis CPU and memory on one plot. Donut for disk. Hero metric tiles. |
| 11 | CPU uses **% of one core**, as `docker stats` and `ps` report it (a build reaches 250 %). The summary converts the total to cores ("1.1 cores of 10"). | Same unit as the tools people know. | Normalising to the machine: hides one pegged process. |
| 12 | **History:** a server-side ring buffer per service, a sample every 10 s while the project page is open and every 60 s otherwise while the dashboard runs, for the last 6 h, in memory only. A restart loses it; when the buffer is shorter than the range the bar says "since dashboard start (40 min)". Range control: **15 min, 1 h, 6 h**, one row above the charts, scoping charts and sparklines. | Decided with the person. In memory keeps it cheap and private. | Persisting samples to disk. Client-side history (lost on reload). |
| 13 | A stopped period is a **gap** in every series, never a zero. Stopped checkouts stay in the table and the disk chart ("Stopped · volumes kept"). | A zero says "it ran and used nothing". | Zero-filling. |
| 14 | Per-service rows get **sparklines** for CPU and memory over the selected range, with the current value beside each. A container with a memory limit keeps the limit meter; without a limit the row shows its share of machine memory ("0.6 % RAM"). | The trend sits next to the number it explains (main's app climbing toward 2 GB). | Sparklines without values. |
| 15 | **Chart colours** are four new tokens `--series-1..4` (blue, orange, violet, magenta), assigned to checkouts in a fixed order (main, feature/invoices, fix/login, spike/search) and to kinds in the kind view. Green, aqua, yellow, red and cyan are not used because Berth reserves them for ok, attention, failed and busy. Validated with the dataviz palette validator against `surface-1`: light worst adjacent CVD ΔE 19.9, normal 28.1; dark 16.0 and 19.7; light slot 4 is 2.7:1, so every chart has a legend with values and a table view. | A colour follows the checkout, never its rank, and never impersonates a state. Lane colours stay for app identity. | Lane colours as series (they mean "the app"). Status colours (they mean state). |
| 16 | Images count once. The disk bar gives each checkout an equal share of each image it uses, and the footnote states the total once. | Checkouts share images; summing them per checkout double-counts. | An images column. |

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

- **Bar**: segmented control (Container | Hybrid, `DEFAULT` tag), **Make default** when
  needed, the overlay tag (`Last run of [fix/login] #38 ⊘ Failed 3 h ago · 51 s  Hide`)
  and **Step kinds**. The subtitle shows the entry for the viewed mode.
- **Summary sentence**: what runs where, steps per place, host trust.
- **Canvas**: `bg` with a 16 px dot grid in `line-strong`, `md` radius, scrolls
  sideways when the side panel takes room (the viewed branch is scrolled into view).
  Flow: Start pill → secrets → prepare → `Mode?` → two branches. Container: up →
  `New data volume?` (yes: seed / no: skip seed) → migrate → Ready `nginx:80/`.
  Hybrid: up-deps → `New data volume?` (yes: seed-host / no) → migrate-host →
  `start together` → api ‖ app → Ready `app:5173 on host`.
- **Node card** (264 px, `surface-1`, `line-strong` border, `md`): icon tile (stackfork
  mark, container cube for Docker, laptop for this machine on `surface-3`), id in mono,
  `kind · once · where` (the Docker service names), plan badge, the command on one
  line in a `surface-2` strip, and with an overlay the run badge plus its reason.
  States: selected (`accent-soft` and `focus` border), failed run (`failed-line`
  border), running (`busy-line`), dimmed (other mode, 50 %).
- **Connectors**: 2 px `fg-muted` when on the path, dashed `line-strong` when not
  reached or not taken. Labels sit beside them (`yes`, `no`, `start together`); mode
  branch labels are buttons. Split and join rules are drawn per branch, so a dimmed
  branch dims its own half.
- **Side panel** (360 px, `surface-3`, `shadow-popover`): see decision 8a. Escape or
  the close button returns focus to the card.
- **Steps as a list**: a disclosure under the canvas with the same steps as an
  ordered list, for reading and screen readers.
- **Draft**: one `attention` callout when the viewed mode has a draft step.

### Resources

- **Bar**: subtitle `Sampled 4 s ago · every 10 s while this page is open, every 60 s
  otherwise`; on the right the range control (15 min · 1 h · 6 h), and "since dashboard
  start (40 min)" when the buffer is shorter than the range.
- **Summary**: `acme-web uses 1.1 cores of 10, 3.5 GB of 32 GB memory and 3.9 GB of disk
  (2 GB volumes, 1.8 GB images).`
- **Charts** (two columns at ≥ 1100 px, then one): each is a card with title, subtitle,
  a legend that names every series with its current value (the direct labels), and
  the plot. Marks: 2 px lines with round joins; area fills at 28 % of the series
  colour with the series line on top; 1 px solid `line` gridlines; axis text 11 px
  `fg-subtle`, tabular. Y ticks: whole GB for memory, 100 % steps for CPU. X: start,
  middle, "now". Text never uses a series colour.
- **Crosshair**: pointer or keyboard. A 1 px vertical line snaps to the nearest
  sample; the tooltip (`surface-3`) lists every series, value first in mono, then the
  name with a line key, and the stacked total. The chart is focusable: arrows move one
  sample, Page Up/Down a twelfth of the range, Home/End the ends, Escape hides it; a
  polite live region reads the values.
- **Table view**: "Show chart data as a table", a disclosure with the memory and CPU
  values per series at twelve points of the range.
- **Disk**: one row per checkout: name, a 14 px bar (volumes in the checkout colour,
  2 px gap, the image share in `fg-subtle` at 50 %, 4 px rounded end), and
  `815 MB + 848 MB`.
- **Callout**: the memory-limit warning names the trend ("up from 1.2 GB 6 h ago").
- **Table**: Service · Runs in · CPU (sparkline + value) · Memory (sparkline + value +
  limit meter or "% RAM") · Disk. Sparklines are 72 × 20, 1.5 px `fg-muted`, a 2.5 px
  end dot, gaps where the service did not run, x by time.
- **Thresholds** as before: memory against its limit ≥ 85 % `attention`, ≥ 95 %
  `failed`; CPU never red, `attention` only after 2 min above 80 % outside a build.
- **Units**: `MB` under 1 GB, `GB` with one decimal, `%` with one decimal, a
  non-breaking space before every unit.
- **Empty**: before the first sample each chart reads "Collecting samples… (first
  point in 10 s)"; the disk chart still shows.
- **Cost**: `docker stats --no-stream` for the project's containers only; volume sizes
  from `docker system df -v`, cached 5 min; host apps already have RSS and CPU.

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
| No recorded plan | The canvas shows the derived steps with `derived` plan badges and **Learn with your agent**. |
| Start would be refused | The `attention` callout replaces the canvas, as today. |
| Docker unreachable | Shell banner (existing). Container rows show `—` with "Docker unreachable" in the title; host apps still show. |
| Stats unavailable | Resources subtitle reads `Stats unavailable: docker stats timed out` in `attention`; last values stay, marked stale (`fg-subtle`). |
| No checkouts running | Resources summary reads "Nothing runs. Volumes keep 2 GB."; the charts show gaps. |
| No samples yet | Each chart: "Collecting samples… (first point in 10 s)". |
| Dashboard restarted recently | "since dashboard start (40 min)" beside the range control; the axes start there. |

## Responsive

- **≥ 1400 px**: all columns; charts side by side.
- **1100 to 1399 px**: Checkouts drops Ahead/behind.
- **720 to 1099 px**: Checkouts drops Path; charts stack; the side panel is 320 px and
  the palette drops below.
- **< 720 px (400 px target)**: the rail becomes the top strip. The canvas is one
  column: branches stack, the viewed mode first; the other mode collapses to its
  label and "n steps only in container mode"; parallel cards stack. The side panel is
  a bottom sheet (max 72 vh). Charts stack and keep their legends; resource rows show
  CPU, MEM and DISK with their labels and keep the sparklines. Nothing scrolls
  sideways (checked: `scrollWidth` is 400).

## Accessibility

- The canvas is a group labelled "Startup flow, hybrid mode". Every card is a button
  in flow order with a label that reads id, kind, where and state; the other mode's
  cards are hidden from assistive tech and from Tab. Arrow keys move between cards,
  Home and End jump to the ends, and focus opens the side panel. "Steps as a list"
  gives the same content as an ordered list.
- Charts are focusable groups with keyboard crosshairs and a live region; every value
  is also in the table view. Legends name every series, so no identity is colour
  only. Series colours pass the CVD and normal-vision checks.
- The mode control and the range control are radio groups in `fieldset`s; branch
  labels are buttons with `aria-pressed`; collapse buttons use `aria-expanded`.
- Focus stays on the control that caused a re-render. Escape closes the side panel and
  returns focus to the card.
- Skip link, one `h1` per view, `h2` per section, `scroll-padding-top` for the sticky
  toolbar.

## Motion

One moment: after a mode switch, the branches that changed get the Berth flash
(`accent-soft` over 600 ms). Card borders change colour in 120 ms. The crosshair and
tooltip do not animate. Section collapse, the side panel and polling re-renders do
not animate. Under reduced motion the flash becomes a static `focus` border and the
busy ring stops.

## Data the page needs

| Data | Status |
|---|---|
| Plan steps with kind, lane (from kind plus `on`), modes, once, trust, verified, draft | Exists (`knowledge.Plan`, `lifecycle.PlanView`). Add a `lane` field to the step view. |
| Last run per instance: steps with state, reason, timing, log path, exit code | Exists (`runs/<id>.json`). Add the run to the page view; only the menu uses it today. |
| Host apps: pid, RSS, CPU, uptime, listening | Exists (`inventory.HostInfo`). |
| Container CPU %, memory usage and limit, net and block I/O | **Add**: `docker stats --no-stream --format json` filtered by the project label. |
| Sample history | **Add**: an in-memory ring buffer per service (10 s while the page is open, 60 s otherwise, 6 h), served with the page data. |
| Run history per step | Exists (`runs/<id>.json` per instance). The panel reads the last 3 runs that contain the step. |
| Seed decision of a run | Exists as the seed step's state and reason ("data already exists", draft note). The canvas maps it to the yes/no branch. |
| Volume and image sizes | **Add**: `docker system df -v --format json`, cached 5 min. |
| Machine memory and cores | **Add**: `runtime.NumCPU()` and total memory (sysctl `hw.memsize` on macOS). |

## Open questions

1. Is the default mode a repository setting (as drawn) or does every Start ask?
2. Should Resources add net and block I/O charts? Same `docker stats` call, two more
   charts.
3. Do these Compose files usually set memory limits? If not, the limit meter rarely
   shows and "% RAM" carries the row.
4. Should the Repos expanded row go away completely (click opens the page), or stay
   as the quick-start list?
5. Is the read-only **Step kinds** palette worth keeping, or should the side panel
   carry that help alone?
