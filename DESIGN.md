---
name: Berth
description: The stackfork dashboard design system — a dense git + Docker ops console derived from Company the earlier design system, dark-first, monospace-led, status by glyph and text.
colors:
  # dark theme values (normative for dark); the light theme table in Colors is normative for light
  bg: "#0B0B0E"
  surface-1: "#131318"
  surface-2: "#1A1A21"
  surface-3: "#22222B"
  line: "#272730"
  line-strong: "#3A3A46"
  line-control: "#5E5D70"
  fg: "#F2F1F5"
  fg-muted: "#A9A8B8"
  fg-subtle: "#8E8D9E"
  accent: "#614DFF"
  accent-strong: "#7462FF"
  accent-text: "#A295FF"
  on-accent: "#FFFFFF"
  focus: "#A295FF"
  accent-soft: "#614DFF29"
  ok: "#34C286"
  attention: "#F0BF4A"
  failed: "#FF6B83"
  busy: "#5CCBEE"
  off: "#8E8D9E"
  ok-bg: "#34C2862E"
  ok-line: "#34C28666"
  attention-bg: "#F0BF4A2E"
  attention-line: "#F0BF4A66"
  failed-bg: "#FF6B832E"
  failed-line: "#FF6B8366"
  busy-bg: "#5CCBEE2E"
  busy-line: "#5CCBEE66"
  off-bg: "#8E8D9E24"
  off-line: "#8E8D9E52"
  lane-1: "#2F5BD8"
  lane-2: "#6D3FD6"
  lane-3: "#B8266A"
  lane-4: "#A85A00"
  lane-5: "#137A4B"
  lane-6: "#0B6F86"
  lane-7: "#B9231F"
  lane-8: "#4A4AD0"
typography:
  title:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', Helvetica, Arial, sans-serif"
    fontSize: "20px"
    fontWeight: 600
    lineHeight: "26px"
    letterSpacing: "-0.01em"
  heading:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', Helvetica, Arial, sans-serif"
    fontSize: "15px"
    fontWeight: 600
    lineHeight: "20px"
  body:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', Helvetica, Arial, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: "18px"
  body-strong:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', Helvetica, Arial, sans-serif"
    fontSize: "13px"
    fontWeight: 600
    lineHeight: "18px"
  small:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', Helvetica, Arial, sans-serif"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: "16px"
  micro:
    fontFamily: "system-ui, -apple-system, 'Segoe UI', Helvetica, Arial, sans-serif"
    fontSize: "11px"
    fontWeight: 600
    lineHeight: "14px"
    letterSpacing: "0.06em"
  mono:
    fontFamily: "'Geist Mono', ui-monospace, 'SF Mono', Menlo, monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: "18px"
  mono-strong:
    fontFamily: "'Geist Mono', ui-monospace, 'SF Mono', Menlo, monospace"
    fontSize: "12px"
    fontWeight: 500
    lineHeight: "18px"
  mono-log:
    fontFamily: "'Geist Mono', ui-monospace, 'SF Mono', Menlo, monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: "17px"
rounded:
  xs: "4px"
  sm: "6px"
  md: "8px"
  lg: "12px"
  full: "9999px"
spacing:
  "1": "4px"
  "2": "8px"
  "3": "12px"
  "4": "16px"
  "5": "20px"
  "6": "24px"
  "8": "32px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
    typography: "{typography.body-strong}"
    rounded: "{rounded.sm}"
    height: "32px"
    padding: "0 12px"
  button-primary-hover:
    backgroundColor: "{colors.accent-strong}"
  button-secondary:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.fg}"
    typography: "{typography.body-strong}"
    rounded: "{rounded.sm}"
    height: "32px"
    padding: "0 12px"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.fg-muted}"
    typography: "{typography.body-strong}"
    rounded: "{rounded.sm}"
    height: "32px"
    padding: "0 10px"
  button-danger:
    backgroundColor: "transparent"
    textColor: "{colors.failed}"
    typography: "{typography.body-strong}"
    rounded: "{rounded.sm}"
    height: "32px"
    padding: "0 12px"
  button-icon:
    backgroundColor: "transparent"
    textColor: "{colors.fg-muted}"
    rounded: "{rounded.sm}"
    size: "32px"
  status-chip:
    backgroundColor: "{colors.off-bg}"
    textColor: "{colors.off}"
    typography: "{typography.micro}"
    rounded: "{rounded.xs}"
    height: "20px"
    padding: "0 6px"
  branch-pill:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.fg}"
    typography: "{typography.mono-strong}"
    rounded: "{rounded.xs}"
    height: "22px"
    padding: "0 8px"
  copy-chip:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.fg-muted}"
    typography: "{typography.mono}"
    rounded: "{rounded.xs}"
    height: "24px"
    padding: "0 8px"
  command-row:
    backgroundColor: "{colors.surface-2}"
    textColor: "{colors.fg}"
    typography: "{typography.mono}"
    rounded: "{rounded.sm}"
    height: "32px"
    padding: "0 10px"
  input:
    backgroundColor: "{colors.surface-1}"
    textColor: "{colors.fg}"
    typography: "{typography.body}"
    rounded: "{rounded.sm}"
    height: "32px"
    padding: "0 10px"
  card:
    backgroundColor: "{colors.surface-1}"
    rounded: "{rounded.md}"
    padding: "12px 16px"
  table-header:
    backgroundColor: "{colors.surface-1}"
    textColor: "{colors.fg-subtle}"
    typography: "{typography.micro}"
    height: "28px"
    padding: "0 12px"
  banner:
    backgroundColor: "{colors.attention-bg}"
    textColor: "{colors.fg}"
    typography: "{typography.body}"
    padding: "8px 16px"
  modal:
    backgroundColor: "{colors.surface-3}"
    rounded: "{rounded.lg}"
    padding: "20px"
  log-panel:
    backgroundColor: "{colors.surface-3}"
    textColor: "{colors.fg}"
    typography: "{typography.mono-log}"
    rounded: "{rounded.md}"
    padding: "8px 12px"
---

# Berth

Berth is the design system of the stackfork dashboard: the place where a developer's worktrees dock. It is derived from Company the earlier design system (token rigor, naming, status pairs, motion values) and rebuilt for a dense git + Docker console that one developer scans dozens of times a day, offline, from an embedded Go binary.

## Overview

**Mode: Operate.** Every screen is a task: find the stack, open the app, see what is broken, start a branch, free disk. Scanability, alignment and state legibility outrank expression. Brand lives in the precise details: the lane colours, the mono voice, one violet action per screen.

**Principles**

1. **Status is glyph + text, never colour alone.** A dot or icon always sits next to a word. Colour reinforces; it does not carry.
2. **Stopped is normal.** Most stacks are stopped most of the time. Stopped is neutral grey. Red means broken (exited unexpectedly, unhealthy, missing checkout, failed seed).
3. **One filled action per screen.** The violet fill (`accent`) appears on at most one button per view: the thing a newcomer should do next. Every row action is secondary or ghost, with the rest in an overflow menu.
4. **Branch over path.** The identity of a worktree is its branch; the path is secondary, relative, mono, and never truncated into noise.
5. **Columns, not sentences.** Lists are tables with micro uppercase headers and aligned cells (the GitLens graph pattern). Explanatory prose lives in tooltips, callouts and docs, not in rows.
6. **Calm by default, loud when it matters.** Hairlines, tinted chips, no glow. The only saturated surfaces are banners for conditions that block work.
7. **Keyboard and polling coexist.** Re-renders never steal focus or selection; every clickable row is a real button or link.

**Theme.** Dark and light both ship. The default follows the OS (`prefers-color-scheme`); a toggle in the top-right system cluster sets `data-theme="dark|light"` on `<html>`, persisted in `localStorage` (`stackfork:theme`) with try/catch. Dark is designed first and screenshots in reviews are taken in both. (Changed from the earlier design system, which is dark-only by default: an ops console sits next to a terminal and an IDE that already follow the OS.)

**Fonts.** `Geist Mono` is embedded (variable woff2, Latin subset, SIL OFL 1.1, roughly 60–90 KB; served from `/static/fonts/` and declared with `font-display: swap`) because mono is the voice of this product: branches, paths, URLs, ports, commands, logs. The UI sans is the **system stack** (`system-ui, -apple-system, "Segoe UI", …`): a console that lives beside macOS apps should match their text, it costs nothing offline, and Sora's wide geometric letterforms would waste horizontal space in dense rows. Sora is dropped (the earlier design system's title face); its role is taken by the system sans at 600 weight. If Geist Mono fails to load, `ui-monospace / SF Mono / Menlo` carry the same metrics closely enough that nothing reflows.

**References.** Borrowed from GitLens Commit Graph and GitKraken Desktop (patterns only, no marks or brand): the column-headed table with micro uppercase headers and relative dates; coloured branch pills whose colour ties the row to a lane; the small proportional bar for quantities (their +/− changes bar becomes our ahead/behind bar); the toolbar with a breadcrumb and verb buttons that carry a count and freshness ("Fetch · 2 h ago"); the narrow left icon rail with counts; a search field inline with the table. Not borrowed: the full commit graph (a worktree lane only shows "branched from default"), avatars, the activity minimap, gradients and glow. From the earlier design system: token names and usage notes, the status bg/line pairs, violet as the single accent, 120/180/240 ms motion, and the ease-out curve.

## Colors

Tokens are role-based. The frontmatter holds the dark values; the table below is normative for both themes. Alpha tints are written as 8-digit hex over the surface they sit on.

### Neutrals

| Token | Dark | Light | Usage |
|---|---|---|---|
| `bg` | `#0B0B0E` | `#F4F3F8` | Page background behind the rail and content. |
| `surface-1` | `#131318` | `#FFFFFF` | Cards (instance, repo), table bodies, toolbar, rail. |
| `surface-2` | `#1A1A21` | `#F1F0F6` | Row hover, chips, secondary buttons, inputs inside a card, command rows, nested tables. |
| `surface-3` | `#22222B` | `#FFFFFF` | Popovers, overflow menus, modal, log drawer (light uses white plus `shadow-popover`). |
| `line` | `#272730` | `#E2E0EB` | Hairline between rows, card border, table header rule. |
| `line-strong` | `#3A3A46` | `#C7C4D6` | Toolbar/rail edge, divider between regions, chip border. |
| `line-control` | `#5E5D70` | `#8A889C` | Input and secondary-button border (≈3:1 against its surface; light 3.45 / 3.05, dark 2.9 / 2.7 so dark inputs also get a `surface-2` fill to keep the boundary visible). |
| `fg` | `#F2F1F5` | `#15151C` | Primary text on every surface (≥14:1). |
| `fg-muted` | `#A9A8B8` | `#56556B` | Secondary text: paths, images, counts (≥6.3:1 everywhere). |
| `fg-subtle` | `#8E8D9E` | `#6B6A7F` | Column headers, placeholders, timestamps (≥4.6:1 on all surfaces; the floor for text). |

### Accent

| Token | Dark | Light | Usage |
|---|---|---|---|
| `accent` | `#614DFF` | `#614DFF` | Fill of the one primary button per screen and the active rail item marker. Text on it: `on-accent`. |
| `accent-strong` | `#7462FF` | `#4F3AEF` | Primary button hover. |
| `accent-text` | `#A295FF` | `#4B36F0` | Links (app URLs, endpoints), active tab/rail label, selected row text. ≥6:1 on all surfaces. |
| `on-accent` | `#FFFFFF` | `#FFFFFF` | Text and icon on `accent` (5.23:1). |
| `focus` | `#A295FF` | `#4B36F0` | 2 px focus ring, offset 2 px, on every interactive element (≥6:1 against all surfaces). |
| `accent-soft` | `#614DFF29` | `#614DFF40` | Selected row / selected card wash. |

### State system

Five states, each with a text colour, a `-bg` tint (18 % dark / 10 % light) and a `-line` (40 % / 32 %) for chips and callouts. Every state renders as **icon + word**; the colour is the third cue.

| Token | Dark | Light | Meaning in stackfork | Glyph |
|---|---|---|---|---|
| `ok` | `#34C286` | `#116A42` | running, healthy, ready, Docker ok, proxy up | filled circle / check |
| `attention` | `#F0BF4A` | `#7A5200` | partial, unhealthy, restarting, stale knowledge, uncommitted changes, disk ≥85 %, leftover one-off | triangle |
| `failed` | `#FF6B83` | `#B01B38` | exited/dead unexpectedly, missing checkout or branch, seed failed, Docker unreachable, destructive actions | cross in circle |
| `busy` | `#5CCBEE` | `#0A6276` | starting, waiting for a dependency, health check starting, scanning, fetching, action in flight | pulsing ring (static ring under reduced motion) |
| `off` | `#8E8D9E` | `#5F5E73` | stopped, not started, optional service, no instance, never set up | hollow circle |

Text contrast on `surface-1` / `surface-2` (dark): ok 8.1 / 7.6, attention 10.8 / 10.1, failed 6.8 / 6.3, busy 9.9 / 9.3, off 5.7 / 5.3. Light: ok 6.6 / 5.9, attention 6.9 / 6.1, failed 6.9 / 6.1, busy 6.9 / 6.1, off 6.3 / 5.6. On their own tints: ok 5.5, attention 7.6, failed 5.3, busy 6.9 (dark); over `surface-1` / `surface-2` with the 10 % tint (light): ok 5.7 / 5.1, attention 6.0 / 5.3, failed 5.8 / 5.1, busy 6.0 / 5.3, off 5.7 / 5.0. All pass 4.5:1; all dots pass 3:1 as non-text.

Lightness ladder (dark, relative luminance): off 0.27 → failed 0.33 → ok 0.41 → busy 0.51 → attention 0.56, so the five are separable by lightness alone in dark. In light, AA text contrast forces all five to similar luminance (0.13–0.15); the glyph and the word carry the difference there, which is why rule 1 is absolute.

**The Red Budget Rule.** `failed` text appears only next to a thing that is broken or a button that deletes. A stopped stack, a stopped service and a stopped chip use `off`.

### Lanes (instance colours)

Eight fills with white text (all ≥5.1:1): `lane-1 #2F5BD8`, `lane-2 #6D3FD6`, `lane-3 #B8266A`, `lane-4 #A85A00`, `lane-5 #137A4B`, `lane-6 #0B6F86`, `lane-7 #B9231F`, `lane-8 #4A4AD0`. Same values in both themes. A lane colour is assigned per app (not per instance) by the existing byte-sum hash of the app name, so every instance of the same app shares one colour, and it is the one colour that follows an app everywhere: the avatar on its cards, the branch pills of its worktrees in Repos, the lane line in the worktree mini-graph, and the title stripe of its log drawer. The lane never carries state.

### Surfaces and shadows

Depth is mostly hairlines. `shadow-popover` (`0 8px 24px rgba(0,0,0,.55)` dark / `0 8px 24px rgba(21,21,28,.12)` light) on overflow menus and the log drawer; `shadow-modal` (`0 20px 48px rgba(0,0,0,.65)` / `0 20px 48px rgba(21,21,28,.18)`) on the Learn modal. the earlier design system's `shadow-glow` is dropped: no glow anywhere.

## Typography

Two families, one scale, tabular numerals on.

| Style | Family | Size / line | Weight | Usage |
|---|---|---|---|---|
| `title` | sans | 20 / 26, −0.01em | 600 | Modal title; page title in the empty state. One per surface. |
| `heading` | sans | 15 / 20 | 600 | Instance name on its card, repo name in the table, panel headings. |
| `body` | sans | 13 / 18 | 400 | Default text: table cells, callouts, descriptions. |
| `body-strong` | sans | 13 / 18 | 600 | Button labels, service names, emphasised cell. |
| `small` | sans | 12 / 16 | 400 | Secondary cell text, timestamps, hints. |
| `micro` | sans | 11 / 14, +0.06em, uppercase | 600 | Table column headers, section labels ("RUNNING 2"), status chip text, rail counts. |
| `mono` | Geist Mono | 12 / 18 | 400 | Branches, paths, URLs, images, ports, commands, SHAs. `font-variant-numeric: tabular-nums`. |
| `mono-strong` | Geist Mono | 12 / 18 | 500 | Branch pill, the command inside a command row. |
| `mono-log` | Geist Mono | 12 / 17 | 400 | Log drawer and activity feed. `white-space: pre-wrap`, `overflow-wrap: anywhere`. |

Rules: body text is never below 12 px and never below 4.5:1. `micro` is only for labels of four words or fewer. Paths wrap with `overflow-wrap: anywhere` inside their cell; branch names truncate with an ellipsis **only** inside a pill and always carry the full name as `title` and as visually-hidden text. Numbers in columns are right-aligned and tabular. No italics for state.

**The Mono Means Data Rule.** Anything the user could paste into a terminal or a browser is mono. Labels, sentences and buttons are sans. Never mono as decoration.

## Layout

### Shell

```
┌────┬──────────────────────────────────────────────────────────────┐
│    │ Toolbar: breadcrumb/filter ········· system cluster · theme  │
│rail│ Banner strip (0..n, stacked, each dismissible when non-block)│
│    │ Content (full width, 16–24 px gutters, max 1600 px)          │
└────┴──────────────────────────────────────────────────────────────┘
```

- **Rail** (left, 56 px, `surface-1`, right edge `line-strong`): wordmark letter at the top, then icon items with a count under each: **Instances** (running count, e.g. `2/3`), **Repos** (repo count), **Clean up** (reclaimable GB when known, `attention` tint when disk ≥85 %), **Settings**. Active item: `accent-text` icon + 2 px `accent` bar on the left edge. Hover shows the name as a tooltip; at ≥1280 px the rail may expand to 180 px with labels (user toggle, remembered). Keyboard: arrow keys move, Enter selects; the rail is a `nav` with `aria-current`.
- **Toolbar** (48 px, `surface-1`, bottom edge `line`): left side is view-specific (filter input 260 px + toggles, or a breadcrumb); right side is the **system cluster**: `Docker ● ok` / `Proxy ● :80` / `Start proxy` (ghost button, only when the proxy is down and the user has not dismissed it) / theme toggle / version (`fg-subtle`, mono). While loading, status reads `Docker · checking…` in `busy`, never `failed`. Each status is a `StatusPill` (dot + word); clicking one opens a popover with detail and the relevant command.
- **Banner strip**: full width under the toolbar. One banner per condition, tiers by state tint (see Banner). The disk banner shows at ≥85 % and is `attention`; it becomes `failed` only at ≥95 % or when a build has failed with ENOSPC. Docker unreachable is `failed` and non-dismissible. Proxy hint is `busy`/info and dismissible.
- **Content**: full width minus gutters (16 px at <720, 24 px at ≥720), `max-width: 1600px`, left-aligned (not centred). Wide screens get more columns, not more margin.

Breakpoints: **≥1024** full table columns; **720–1023** the rail stays, tables drop the least important column (last activity moves under the name); **<720** the rail becomes a 48 px icon strip above the toolbar, tables become two-line rows, every row's actions collapse to one visible button plus overflow. Nothing scrolls horizontally at 400 px: cells wrap, pills shrink to `min-width: 0`, action clusters move to the second line.

### Instances view

Toolbar left: filter input (`placeholder="Filter instances"`), `Running only` switch, sort menu (Running first · Recent · Name). Section labels `RUNNING 2` / `STOPPED 1` in `micro`, 24 px above, 8 px below.

**InstanceCard** (full-width, `surface-1`, radius `md`, hairline border, 12 px 16 px padding):

```
[AW] acme-web  ⌥ main           ● Running  draft  [Open App] [Stop] [⋯]
    .worktrees/feature-login · acme-web-2ffa6841c75d.localhost
    App ↗  web ↗  Mail inbox ↗
    ▲ Database seed failed — npm: not found            [Retry seed] [⋯]
    ───────────────────────────────────────────────────────────────
    SERVICE      STATE          ENDPOINTS                      ACTIONS
    ◉ api        ● Running      http://…:25182 ↗  (proxy) ↗   [Logs] [⋯]
    ◉ web        ● Running      http://…:28928 ↗               [Logs] [⋯]
    ◉ worker     ● Running      —                               [Logs] [⋯]
    ◉ cache      ○ Stopped      127.0.0.1:26518 ⧉              [Start] [⋯]
```

- **Identity**: lane avatar + app name (`heading`) + branch pill (lane colour). The app is the folder of the repo's main checkout (a worktree card shows its main repo, never the worktree slug; non-git roots fall back to the project name), exposed as `app` on the instance. Avatar initials are the first letter of the first two words of the app name (split on `-`, `_`, `.`, space and camelCase), or its first two letters when it is one word. The hostname hash moves to the second line in `mono fg-muted` and the path is shown relative to the repo (`.worktrees/feature-login`; the main checkout shows `main checkout`). Absolute path lives in the overflow ("Copy path", "Reveal in Finder" command) and in the `title`.
- **State chip** (`StatusChip`): sits on the right of line 1, grouped with the knowledge chip (`draft`/`verified`) directly before the actions: `● Running`, `◐ Partial`, `○ Stopped`, `⊘ Checkout missing`, `? Unknown`, `legacy` as an `off` chip. At 400 px the actions stay on line 1 right, the pill moves to line 2 and the chips wrap after it.
- **Actions**: one visible verb that fits the state (`Open App` when running and an app URL exists; `Start` when stopped), then `Stop`/`Restart` as ghost, then overflow (`⋯`): Down keeping volumes, Copy `.env`, Copy down command, Remove instance (danger, with the dry-run confirmation), and the warning commands. The command text is shown in the menu item as a mono second line, so the user still sees what will run.
- **App strip**: link chips in `accent-text` with an external-link icon; a stopped app's chip is `off` with no `href`, tooltip "Starts with the stack".
- **Quick-access chips** (a recorded entry and links): the label is followed by ` →` (`App →`, `Dashboard →`, `Email →`), in recorded order, entry first, and the `aria-label` reads `Open Dashboard in a new tab`. A service chip for a target the entry or a link already shows is not repeated. A target that is not running is the `off` chip with no `href`. **Open App** opens the entry. A recorded target the stack lacks shows the existing `attention` warning callout, not a chip with a link. The template and `app.js` render the same markup; chips are keyed `project/app/NAME` and update in place.
- **Callouts** (`Callout`): warnings (`attention`), seed failed (`failed`), one-off leftovers (`attention`). Title in `body-strong`, detail in `small`, error text in `mono-log` capped at 6 lines with a scroll, actions on the right: one secondary button + overflow. The seed callout's three commands become **Retry seed** (secondary), **Mark as seeded** and **Re-seed, overwriting data** (overflow, the last one danger). Each menu item shows its command in mono.
- **Service table**: columns SERVICE (logo + name + image under it in `mono fg-muted`) · STATE (chip) · ENDPOINTS (links and copy chips; `connection URL` copy chip; `use` command chips) · ACTIONS (`Logs` ghost + overflow with Start/Stop/Restart). Clicking the service name toggles the details drawer (ports, volumes, healthcheck, depends on, env keys, source files) as a `KeyValueList` indented under the row. Unknown endpoints read `port assigned on start` in `fg-subtle`.
- Collapsed card (stopped by default): identity line + state + app strip only; the chevron is a real button with `aria-expanded`.
- 400 px: line 1 avatar + name + chip, line 2 branch pill + relative path, line 3 app strip, actions become `[Start] [⋯]` right-aligned on line 1; service rows become name + chip on line 1 and endpoints on line 2, actions in overflow only.
- Empty: "No instances yet" with the two ways to get one (`Repos → Start` link, `stackfork prepare && stackfork up` command row). Loading: three skeleton cards (static blocks, no shimmer).

### Instances: grouped by project

Replaces the flat list of InstanceCards. One **ProjectCard** per app; every version of that app (a worktree or the main checkout that has a stack) is a **VersionCard** inside it. Figma: page "Instances — grouped" in the Berth file (components `ProjectCard`, `VersionCard`, frames 1440 dark/light, 400 dark, "States · Dark").

```
┌ ProjectCard ───────────────────────────────────────────────────────────────┐
│ ⌄ [AW] acme-web  ▲ 1 PARTIAL  3 versions                                 ⋯ │
│ ┌ VersionCard ───────────────────────────────────────────────────────────┐ │
│ │ ⌄ [feature-login] ▲ PARTIAL                       [Open App] Stop  ⋯  │ │
│ │   .worktrees/feature-login · acme-web-2ffa6841c75d.localhost          │ │
│ │   App ↗  Mail inbox ↗                                                 │ │
│ │   ⊘ Database seed failed …                               [Retry seed] │ │
│ │   SERVICE        STATE        ENDPOINTS                               │ │
│ │   web            ● Running    http://…:28928 ↗               Logs  ⋯  │ │
│ ├ VersionCard ───────────────────────────────────────────────────────────┤ │
│ │ › [main] ○ STOPPED                                         [Start] ⋯  │ │
│ │   main checkout · acme-web-9b21e07d4c3a.localhost                     │ │
│ └────────────────────────────────────────────────────────────────────────┘ │
└────────────────────────────────────────────────────────────────────────────┘
```

**ProjectCard** (`surface-1`, 1 px `line`, radius `md`, 12 px 16 px padding, 10 px row gap). Header, left to right: toggle (icon button, `aria-expanded`) · LaneAvatar (initials, lane by app) · app name (`heading`, an `h2`) · summary chip · `n versions` (`small fg-muted`) · spacer · `⋯`. The summary chip uses the Repos STATUS vocabulary: `● 2 running` (ok), `◐ 1 partial` (attention), `○ Stopped` (off; every version stopped). When both running and partial exist, show both chips. The project `⋯` menu: `Start a branch…` (opens the Repos start popover for this repo), `Show in Repos`, `Copy repo path`. There is no start-all or stop-all: starting several stacks of one app at once is rare and heavy, and stopping all is the kind of action that should take one click per stack.

Collapsed project: header plus one row of BranchPills in the app lane colour (running versions first). Collapse state is remembered per app (`stackfork:project-collapsed:<app>`, try/catch). Expanded is the default.

**VersionCard** (`surface-2`, no border, radius `md`, 10 px 12 px padding, 8 px apart; the nested layer from Elevation). Content under line 1 is inset 36 px so it aligns with the pill, not the toggle.

- Line 1: toggle (`aria-expanded`, controls the service table) · BranchPill in the app lane colour (the primary label; the app name is never repeated here) · state chip (`● Running`, `◐ Partial`, `○ Stopped`, `◔ Starting…`, `⊘ Checkout missing`) · `▲ Setup unverified` only when knowledge is `draft` or `stale` (verified is silent) · spacer · actions: `Open App` (secondary) when live and an app URL exists, `Start` (secondary) when stopped, `Stop` (ghost) when live, `⋯` with the existing instance menu, which has `Reset environment…` (a plain item above the danger separator; a native confirmation names the volumes, then the run streams like Start; the card shows `Resetting…` and, on failure, `Reset failed at step` with a Retry that asks again). A linked worktree whose main checkout has a prepared instance also has `Reset from main checkout…` right after it (same confirmation, which adds the recipe and the files moved aside; its Retry keeps the same action). When the worktree's Compose files lack one the main checkout uses, the warnings callout shows the gap message and the same `Reset from main checkout…` as its one secondary action.
- Line 2 (`mono fg-muted`): relative path `· ` hostname. The main checkout reads `main checkout` instead of a path; that is how a worktree and the main checkout differ, together with the pill text.
- App strip: shown when the version is live or expanded. **At most one App chip per version.** Other services keep their role labels (`Mail inbox`, `DB admin`, service name).
- Callouts and the ServiceTable are unchanged from InstanceCard, inset 36 px. Chips, copy chips and inputs inside a VersionCard use `surface-3` so they stay visible on `surface-2`.
- Collapsed version (stopped by default): lines 1 and 2 only. No app strip: on a stopped stack it is all `off` text and carries nothing.

**App chip states** (one chip, the label names the state, colour is the third cue):

| Condition | Chip |
|---|---|
| Live, URL known | `App ↗` link in `accent-text`, `title` shows the URL and the direct address |
| Live, port not assigned yet | `App · port assigned on start` in `off`, no link |
| Stack stopped | `App` in `off`, tooltip "Starts with the stack" |
| Knowledge-only host app (no route, no service endpoint) | `App · on host` in `off` with a dotted underline; the tooltip and the version `⋯` item **How to run the host app** carry the command (`npm run dev`, then `stackfork route add <name> <port>`). Emitted once per version, never once per dev script. |

**Sort.** Projects: any live (running or partial) version first, then most recent activity (manifest mtime of the newest version), then name. Inside a project: live versions first, then the main checkout, then branch name. The `RUNNING n` / `STOPPED n` section labels go away; the rail count and the toolbar `3 of 7 running` keep the totals.

**Filter and Running only.** The filter matches the app name and each version's branch, path and hostname. A match on the app name shows the whole project. Otherwise only matching versions show, and a project with no match is hidden. `Running only` hides stopped versions and any project with nothing live left. In both cases the header keeps the totals and adds the hidden count: `3 versions · 2 hidden`.

**States.** One version: the project card still wraps it (identity stays in one place). Many versions (6+): running first, stopped collapsed to two lines; the project toggle collapses the whole card to pills. All stopped: `○ Stopped` summary, every version collapsed. Callout: inside the version it belongs to, never at project level. Loading: static skeleton project cards (header blocks and two version blocks, no shimmer). Empty: the existing `No instances yet` EmptyState.

**400 px.** Project header: name stacked over `n versions`, summary chip right-aligned before `⋯`. Version line 1: toggle · pill · spacer · `Start` or `Open App` · `⋯`; line 2: chips; line 3: path (wraps); app strip wraps. Service rows become two lines (name + image + chip + `⋯`, endpoint under), `Logs` and `Stop` move into `⋯`. Nothing scrolls horizontally.

**Keyboard and semantics.** `main` holds a list of `section` elements (one per project), each labelled by its `h2`; versions are `h3`. Focus order follows the DOM: project toggle → project `⋯` → version toggle → version actions → app links → service rows. Toggles are buttons with `aria-expanded` and `aria-controls`; collapsing is instant (no animation, per Motion). Re-renders keep the keyed focus and open menus as today, with keys prefixed by the app (`<app>/toggle`, `<app>/<project>/...`).

### Repos view

Toolbar left: search input (`placeholder="Search repos and branches"`), `Ready only` switch, **Rescan · 5 min ago** (ghost button with freshness; shows `busy` ring while scanning), result count `fg-subtle`. Right: system cluster.

**RepoTable** (one table, full width, header row in `micro fg-subtle`, rows 40 px, hairlines):

| REPO | STATUS | WORKTREES | BRANCHES | LAST ACTIVITY | |
|---|---|---|---|---|---|
| ▸ **acme-web** `code/me` | `● 1 running` | 2 | 1 | 2 d ago | `[Start…] [⋯]` |
| ▸ **billing-api** `code/me` | `◐ 1 partial` | 1 | 1 | 5 h ago | `[Start…] [⋯]` |
| ▸ **notes-cli** `code/me` | `○ Not set up` | 1 | 0 | 3 w ago | `[Set up]` ← the one filled button on this view, only on the first row that needs it |

- **Sorting** (default): rows with a running or partial instance first, then by last activity (most recent commit or start), then name. Sort menu in the header: Activity · Name · Folder. The folder is a `small fg-muted` suffix under the name, not a group header; grouping by folder is a toggle in the sort menu, and a group header then reads the folder name (`Pedro`, `Backup/Work`), never `(top level)`.
- **STATUS column** replaces the readiness badge. Vocabulary: `● 2 running` (ok), `◐ 1 partial` (attention), `○ Stopped` (off; instances exist, none running), `○ Not set up` (off; readiness `unknown` or `needs-learn`), `◔ Setup unverified` (attention; readiness `draft`), `✓ Ready` (ok, no instance). The underlying reason sentence ("prepared instance … but no verified knowledge") moves to the tooltip and to the expanded panel.
- **WORKTREES / BRANCHES** are plain counts, right-aligned, tabular. No "(s)".
- **LAST ACTIVITY** is relative (`2 d ago`), full timestamp in `title`; the header is the label.
- **Row actions**: one visible secondary button (`Start…` opens a small popover to pick a branch or worktree; `Set up` when not set up) plus overflow: Fetch (with freshness in the item, "Fetch · last 2 h ago"), Open in Finder, Copy path, Learn with your agent, Rescan this repo.
- **Expanded row** (chevron or Enter): a nested panel on `surface-2` spanning the row width:

```
  ~/code/acme-web · default main · verified 3 d ago · Fetch · 2 h ago
  derived — not recorded; run learn to record it   [Learn with your agent]          Mode [container ▾]
  App →         nginx:80/app                                                              recorded

  LANE  BRANCH                 PATH                     AHEAD / BEHIND upstream STATE        
  ┃     main                   main checkout            —                     ● Running    [Open App] [⋯]
  ┣━    feature-login          .worktrees/feature-login ▮▮▮░░ 3 · 12          ○ Stopped    [Start]    [⋯]
  ┣━    detached @ 07c0f1a     .worktrees/hotfix        ▮░░░░ 1 · 0  ● changes ○ No stack  [Start]    [⋯]

  BRANCHES NOT CHECKED OUT
  ┊     fix-invoices           —                        ▮▮░░░ 14 · 36         —            [Start]    [⋯]
```

  - **LANE** is a 24 px column of inline SVG: a vertical line in `line-strong` for the default branch, a short horizontal stub in the worktree's lane colour (the instance colour when a stack exists, `fg-subtle` otherwise) ending in a 6 px dot. Dashed stub for branches without a worktree. No merges, no crossing lines: the graph only says "branched from `main`". It costs one `<svg>` of two paths per row and is omitted below 720 px.
  - **BRANCH** is a `BranchPill` in the lane colour, mono, full name (wraps inside the cell at two lines max; truncation only below 720 px with the full name in `title`). Detached worktrees read `detached @ 07c0f1a` with a mono short SHA. The main checkout gets a `main checkout` `off` chip after the pill; uncommitted changes get `● changes` in `attention`.
  - **PATH** is relative to the repo (`.worktrees/feature-login`); the main checkout says `main checkout`. Full path in `title` and in the overflow.
  - **AHEAD / BEHIND** is an `AheadBehindBar`: a 56 px two-segment bar (ahead `ok`, behind `attention`, proportional to `ahead / (ahead+behind)` with a 4 px minimum segment when non-zero) followed by `3 · 12` in tabular mono. The counts are against the branch's upstream (what git tracks for it), not the default branch. Screen readers get "3 ahead, 12 behind upstream". Zero/zero shows `up to date` and a branch with no upstream shows `no upstream`, both in `fg-subtle`.
  - **STATE** is the instance's `StatusChip`; `○ No stack` when none. Clicking it jumps to the card on Instances (same lane colour, card flashes `accent-soft` for 600 ms).
  - **Actions**: one secondary button (`Open App` when running, `Start` otherwise; disabled with tooltip when the repo is not set up) + overflow: Stop, Reset environment… (only when the worktree has an environment; native confirmation), Reset from main checkout… (a linked worktree with an environment whose main checkout has a prepared instance; native confirmation), Remove worktree (danger; confirmation; refuses uncommitted changes), Copy path, Open in terminal command. No red button is visible without opening the overflow.
- **Secrets table** (expanded repo, under the plan list; shown only when the repo has a declared or mapped secret name): micro headers NAME · REFERENCE · STATUS, 40 px rows, name in `mono-strong` with the hint in `small fg-muted` under it, reference in `mono`. STATUS is a `StatusChip` with glyph and word: `● Resolves` ok, `⊘ Missing` failed, `▲ Locked` attention, `▲ Unavailable` attention (no store tool on this system), `⊘ Error` failed, `○ Not checked` off, `○ Shell only` off (an `env:` reference the dashboard cannot see). One secondary **Check** for the section. A `keychain:` row's overflow has **Set value…** where the system has a hidden-answer dialog (macOS, Windows); `secret set` and `secret map`/`unmap` are copy-only `CommandRow`s with no Run slot, by design, because a person must choose the value and the reference. Rows are keyed `repo/secret/NAME` and update in place; there is no value input in the page.
- **LiveSwitch** (expanded repo, on the line under the setup line): a `role="switch"` button labelled `Live` (`aria-checked`) and a state chip. Turning it on posts `/api/live` and opens a native dialog that states the consent (commits on each upstream are fetched, fast-forwarded and built on this machine); turning it off and **Resume** need no dialog. Where no dialog exists, a popover shows the `stackfork live on REPO` CommandRow. Focus stays on the switch across a render. Chip states:
  - `○ Off` (off).
  - `● Live` (ok).
  - `◔ Reconciling…` (busy, while a live run runs; the step is in `title`).
  - `▲ Paused` (attention) with the reason in `small fg-muted` and a secondary **Resume**. A failed live run reads the same way: its action is **Resume**, not Retry, because it resumes live mode for the repository. The reason `plan needs trust` adds **Trust on this machine**.
  - `○ In another dashboard` (off; the switch is disabled because another dashboard holds the live lock).
  The collapsed repository row shows a `Live` micro tag after the name, in `attention` when paused. A succeeded live run raises no toast; a failed one raises one toast. The instance card's last run reads `live · …`.
- **Worktree warnings** (live mode on): a `small` `attention` note under the worktree row. `Behind, not updated` plus the git reason when the fast-forward is blocked (uncommitted changes, local commits). `Branch switched` when the checkout is on another branch than the instance's; live mode takes no action.
- **Cleanup callout** (a worktree whose branch is merged, in Repos and on the instance's card): an `attention` Callout `PR merged — clean up?` (with `#42` and a link when `gh` gave one) with **Clean up** (secondary) and **Keep** (ghost). **Clean up** opens a native confirmation that names the worktree, the volumes, the branch and the ignored files. In `auto` mode with a grace period the title reads `Cleanup in N h` (`Cleanup in under 1 h` in the last hour) with **Clean up now** and **Keep**. Warnings replace it when the worktree is not ready: `PR merged, but this worktree has uncommitted changes`, `PR closed without merge`. The callout is shown on collapsed cards too, because a person who collapsed the card still needs to see that data will be deleted.
- **Cleanup selector** (expanded repo, next to the LiveSwitch, shown while live mode is on): a `Branch cleanup` select with `off`, `suggest` and `auto`. Choosing `auto` opens a native confirmation that lists the worktrees and volumes that a cleanup would delete today.
- **Entry line** (under the setup line, from the main checkout's knowledge): `App → nginx:80/app` with an `ok` chip `recorded`; `App → web:3000` with an `attention` chip `entry guessed` for a proposed or missing entry, and the `stackfork learn record entry --service S --port N` command as a copy-only `CommandRow` in its overflow; `No web entry` in `fg-muted` when the entry is `none`.
- **Derived plan list** (replaces the setup line, for a repo whose knowledge has no plan steps): the plan list shows the steps a Start would run, under `derived — not recorded; run learn to record it` in `small fg-muted` with **Learn with your agent** (ghost) on a ready repo. Each State cell is an `off` chip `derived`, and the step's source is a `small fg-subtle` line under its command and the chip's `title`. When a Start would be refused, an `attention` Callout `A Start would be refused` with the reason replaces the list, and a worktree with fewer Compose files than the main checkout adds the recipe-gap Callout. The list is built by the same code as a Start and cached until a scan or an action.
- **Set up**: when the repo is not set up, the expanded panel starts with a `Callout` (`off` tint): "stackfork has not verified how this project starts. Set it up once with your agent." + **Set up with your agent** (the view's one primary) + overflow "Copy prompt".
- **Error on a row** (failed start / worktree add): `Callout failed` under the worktree row: title "Start failed", stderr in `mono-log` (6 lines, scroll), a `CommandRow` with the retry command and `Run`, `Dismiss` ghost. Stays until dismissed or the next success.
- **Empty / loading**: no folders → one centred card "Pick the folder that holds your repos" with `Add folder` (primary) and the `stackfork config add-root PATH` command row; scanning → table header + 5 skeleton rows + `Scanning folders…` with the `busy` ring in the toolbar; no repos found → "No git repositories under ~/code" + `Rescan` + `Change folders`.
- **400 px**: repo rows become two lines (name + status chip; counts · activity) with `[Start…]` and `⋯` right-aligned; worktree rows become branch pill on line 1 with the state chip, path and ahead/behind text (`3 ahead · 12 behind`) on line 2, one action button right, lane column hidden.

### Clean up and Settings

Both become **views** reached from the rail, not panels injected into the page. Clean up: a disk gauge row (`AheadBehindBar` reused as a used/free bar with the text `55.4 of 56 GB used`), `Free Docker space` as the view's primary, then a `RepoTable`-style list of prune targets (TARGET · INSTANCES · COMMAND · action) with `Run` on each row and the dry-run confirmation copy kept. Settings: a two-column form (label 160 px + control) on `surface-1`, lists of folders as rows with a ghost `Remove`, `Add folder` as the primary.

## Elevation & Depth

Three layers: page (`bg`), card (`surface-1` + 1 px `line`), nested (`surface-2`, no border, inset by the card padding). Floating layers (`surface-3`) are the only ones with a shadow: overflow menus and popovers use `shadow-popover`, the Learn modal `shadow-modal`, the log drawer `shadow-popover`. Hover on a row is a `surface-2` wash, not a shadow. Selected card or row: `accent-soft` wash + 1 px `focus` border. No glow, no gradient, no blur backdrops; the modal backdrop is `rgba(11,11,14,.6)`.

**The Hairline Rule.** Separation inside a card is a 1 px `line`; separation between regions is `line-strong`. Never a 2 px+ coloured left border on cards, rows or callouts; callouts are tinted surfaces with a glyph.

## Shapes

| Token | Value | Usage |
|---|---|---|
| `xs` | 4 px | Status chips, branch pills, copy chips, key/value chips, skeleton blocks. |
| `sm` | 6 px | Buttons, inputs, command rows, menu items, tooltips. |
| `md` | 8 px | Cards, nested panels, callouts, log drawer, popovers. |
| `lg` | 12 px | Modal. |
| `full` | 9999 px | Status dots, the busy ring, lane dots, the lane avatar when a service logo is round. |

Smaller than the earlier design system (6/10/16/22) on every step: rows are 32–40 px tall and a 16 px card radius would read as a tile, not a table. Avatars are squares with `sm`. Chips are never fully round: a pill reads as a tag, a rounded rectangle reads as a data cell.

## Components

Mapped against the 51 the earlier design system components. "Adapted" keeps the the earlier design system contract and changes density, radius or copy; "new" exists only here.

### Carried over (adapted)

| the earlier design system | Berth | What changes |
|---|---|---|
| Button (primary/secondary/ghost/danger/compact) | **Button** | 32 px (28 px compact), radius `sm`, no glow; `:active` scales to 0.97; focus ring 2 px `focus`; danger is text-only until hover. Primary appears once per view. Icon buttons are 32×32 with an SVG icon and an `aria-label`; the icon set is a single 16 px, 1.5 px-stroke line set (play, stop, restart, logs, overflow, copy, external, chevron, check, warning, cross, ring, folder, terminal, search, sun/moon). No unicode glyphs. |
| StatusChip | **StatusChip** | Five states (ok, attention, failed, busy, off) with glyph + text, `micro`, 20 px, radius `xs`, `-bg` fill and `-line` border. Also the system StatusPill in the toolbar. |
| Banner | **Banner** | Tiers by state tint instead of solid red; icon + text + one action + dismiss (when allowed); `role="status"` for attention/busy, `role="alert"` for failed. DiskBanner is a preset. |
| Tabs | **Rail** (replaces tabs) and **SegmentedControl** (for sort/filter groups) | The view switcher is the rail with counts; the segmented control keeps the earlier design system's bordered track with the active item in `surface-3`. |
| Input | **Input / Search** | 32 px, radius `sm`, `surface-1` on toolbars and `surface-2` inside cards, `line-control` border, search has a leading icon and a clear button. |
| Modal | **Modal** (LearnModal preset) | Radius `lg`, `surface-3`, 560 px max, `shadow-modal`; uses `<dialog>`; enters at 220 ms ease-out from 0.97/0, exits at 120 ms. |
| EmptyState | **EmptyState** | Title + one sentence + one primary + an optional command row; left-aligned inside the content width, never a dashed box. |
| KeyValueList | **KeyValueList** | Service details drawer: 120 px label column in `small fg-subtle`, mono chip values, wraps at <720 px. |
| ProgressBar | **AheadBehindBar** / **DiskBar** | Two-segment proportional bar, 4 px tall, radius `full`, always followed by the numbers. |
| Toast | **ActionToast** | Replaces the activity log as feedback: bottom-right stack of at most 3 on `surface-3` with `shadow-popover`, state glyph + text, dismiss button. Success dismisses itself after 5 s (polite `role="status"` region); an error stays until dismissed (`role="alert"` region). The activity feed stays in the page log and is also written there. |
| TruncatedId | **ShortSha** | 7-char mono SHA with the full value in `title` and copy on click. |
| ConfigPreview | **CommandRow** | A mono command on `surface-2`, radius `sm`, with a Copy icon button and, when the server allows it, a `Run` secondary button whose label names the verb (`Run`, `Seed`, `Prune`); placeholders (`PATH`, `CMD`) make it copy-only and the Run slot is empty, not disabled. |
| DataTable | **RepoTable** / **ServiceTable** / **PruneTable** | Micro headers, 40 px rows, hairlines, right-aligned numbers, one visible action + overflow per row, keyboard row focus (`tabindex=0`, Enter expands). |
| PageHeader | **Toolbar** | 48 px; left tools, right system cluster; breadcrumb (`repo › branch`) when a repo row is expanded on a narrow screen. |
| CheckList | **SetupSteps** (inside the Learn modal) | Lists what the agent will verify (start, migrations, seed, login) with `busy`/`ok` glyphs as the learn run reports. |

### New

| Component | Notes |
|---|---|
| **Rail** | 56 px nav with icon + count per view; active marker; expands to 180 px with labels on wide screens. |
| **StatusPill** | Toolbar system status: dot + word (+ port); click opens a popover with detail and command. |
| **LaneAvatar** | 28 px square, lane fill, white initial, `sm` radius; used on cards and as the lane colour source. |
| **BranchPill** | Mono branch name on a lane-tinted chip (12 % tint, 40 % line, text in `fg`); detached variant with ShortSha; icons for local/remote/detached. |
| **InstanceCard** | See Layout → Instances. Collapsible; keyed re-render that preserves focus and open state. Superseded by ProjectCard + VersionCard once the grouped view ships. |
| **ProjectCard** | One per app: LaneAvatar, name, summary chip, `n versions`, `⋯`; holds VersionCards or, collapsed, a row of BranchPills. See Layout → Instances: grouped by project. |
| **VersionCard** | A worktree or main checkout with a stack, on `surface-2` inside a ProjectCard: BranchPill, state chip, actions, path · hostname, one App chip, callouts, ServiceTable. |
| **ServiceRow** | A ServiceTable row with ServiceLogo (16 px mask icon in `fg-muted`), StatusChip, endpoint links, CopyChips and the actions cluster. |
| **EndpointLink** | `accent-text` mono with an external-link icon; stopped variant is `off` text with no link. |
| **CopyChip** | Mono value or label on `surface-2`, copy icon, 24 px; success flips the icon to a check for 1.2 s and toasts; password-bearing values fetch on click as today. |
| **Callout** | Tinted block inside a card: glyph, title, detail (optional mono error), actions on the right; variants attention / failed / busy / off. |
| **WorktreeTable** | Nested table in an expanded repo: Lane · Branch · Path · Ahead/behind · State · action. |
| **LaneGraph** | The 24 px SVG lane column (trunk line, stub, dot; dashed for branches without a worktree). Hidden <720 px. |
| **AheadBehindBar** | Above. |
| **OverflowMenu** | `surface-3` popover, 220 px, items with an icon, label and optional mono command line; danger items last with a separator; arrow-key navigable; origin-aware scale-in 180 ms. |
| **LogDrawer** | Bottom drawer, full content width, resizable 240–60 vh, `surface-3`, title row with lane stripe + `project / service`, tabs `Logs · Activity`, `Follow` toggle, Copy, Close (Escape). Replaces the fixed 640 px panel. |
| **SkeletonRow** | Static `surface-2` blocks matching row height; no shimmer. |
| **ThemeToggle** | Icon button in the system cluster; cycles system → dark → light; label in tooltip. |
| **DiskBanner** | Banner preset with the disk bar inline and `Free Docker space` as its action. |
| **LearnModal** | Modal preset: agent options as a radio-style list (name, "opens in Warp" / reason when unavailable), SetupSteps, prompt in a collapsed CommandRow. |

### Dropped from the earlier design system

(component inventory of the earlier internal design system removed)

## Motion

Tokens carried from the earlier design system: `duration-fast` 120 ms (colour, press), `duration-base` 180 ms (menus, chips, drawer tabs), `duration-slow` 240 ms (modal, log drawer); `ease-out: cubic-bezier(0.23, 1, 0.32, 1)` for anything entering, `ease-in-out: cubic-bezier(0.77, 0, 0.175, 1)` for the drawer resize; `ease` for hover colour.

What animates: button press (`scale(.97)`, 120 ms), overflow menu and popover (origin-aware, 0.97/0 → 1/1, 180 ms), modal (centred, 0.97/0 → 1/1, 240 ms in / 120 ms out), log drawer (translateY 100 % → 0, 240 ms), toast (translateY 8 px + fade, 180 ms in, 120 ms out), the `busy` ring (1.2 s linear opacity pulse), the card flash on jump (`accent-soft` fade 600 ms). What never animates: tab/rail switches, card collapse, details drawers, re-renders from polling, anything triggered by the keyboard.

Only `transform`, `opacity` and colour transition; never `transition: all`, never layout properties. Hover effects sit behind `@media (hover: hover) and (pointer: fine)`.

**Reduced motion.** Under `prefers-reduced-motion: reduce`: all transforms are removed (menus, modal, drawer and toast appear in place with a 120 ms opacity fade so the state change is still visible), the busy ring becomes a static ring with the word `Starting…`, the card flash becomes a 600 ms border highlight. Nothing is set to `0.01ms` globally.

## Content

English, sentence case, short. Controls name their verb; state words are nouns or participles; errors name the problem and the recovery.

- Buttons: `Start`, `Stop`, `Restart`, `Open App`, `Logs`, `Set up`, `Fetch`, `Rescan`, `Retry seed`, `Resume`, `Clean up`, `Keep`, `Free Docker space`, `Add folder`, `Remove worktree`. Never a sentence on a button; the sentence goes in the tooltip or the confirmation.
- States: `Running`, `Partial`, `Stopped`, `Starting…`, `Unhealthy`, `Checkout missing`, `Branch missing`, `Not set up`, `Setup unverified`, `Ready`, `No stack`. Live mode adds `Live`, `Reconciling…`, `Paused`, `In another dashboard`, `Behind, not updated`, `Branch switched`, `PR merged — clean up?` and `Cleanup in N h`.
- Counts: `2 worktrees`, `1 branch`, `0 running` (plurals computed, no "(s)"); relative times `just now`, `5 min ago`, `2 h ago`, `3 d ago`, `2 w ago`, then `Oct 3`.
- Commands are never labels: the label says what it does (`Retry seed`), the mono command sits under it.
- Confirmations keep the honest wording already in the product ("shows what would be removed; nothing is deleted until you confirm").

Proposed label changes:

| Current | New |
|---|---|
| `Docker: checking...` (red) | `Docker · checking…` (busy) → `Docker · ok` / `Docker · unreachable` |
| `Proxy: running on 80` / `Start proxy for portless URLs` | `Proxy · :80` / ghost `Start proxy` with tooltip "Portless *.localhost URLs" |
| `2 / 3 running` | rail count `2/3` under Instances; toolbar `2 of 3 running` |
| `…` (button) | `⋯` icon button `aria-label="More"` → mostly replaced by the rail |
| `filter by project, branch or checkout` | `Filter instances` |
| `running only` / `ready only` | `Running only` / `Ready only` (switches) |
| `acme-web-2ffa6841c75d.localhost` as title | `acme-web` + branch pill; hostname on line 2 |
| `Down (keeps volumes)` | `Stop` (tooltip: keeps volumes) |
| `stackfork down … --volumes` chip in a menu | overflow item `Remove instance and data` with the command under it |
| `The database seed failed — retry with … (or, to run it again on a populated database, …)` | Callout `Database seed failed` + stderr + `Retry seed` / `Mark as seeded` / `Re-seed, overwriting data` |
| `N one-off task(s) running` / `leftover one-off container(s)` | `1 one-off task running` / `2 leftover one-off containers` + `Clean up` action |
| `(TOP LEVEL)` | folder name, or no header |
| `needs learn` / `unknown` / `draft` | `Not set up` / `Not set up` / `Setup unverified` |
| `1 running` (green text) | `● 1 running` chip in STATUS |
| `2 worktree(s) · 1 branch(es)` | columns WORKTREES `2`, BRANCHES `1` |
| bare `2026-10-19` | `2 d ago` under LAST ACTIVITY |
| `default branch main` + `Fetch` | `default main · Fetch · 2 h ago` |
| `never learned: no knowledge, adapter, instance or profile reference` | `stackfork has not verified how this project starts.` (tooltip keeps the technical reason) |
| `Learn with your agent` | `Set up with your agent` |
| `Startup recipe … change with stackfork learn record recipe --seed-once … (or none)` | the derived plan list (`derived` chip, source under each step) |
| `main checkout` after the path | `main checkout` chip in PATH |
| `detached-07c0f1a…` | `detached @ 07c0f1a` |
| `modified` | `● changes` (tooltip "Uncommitted changes") |
| `ahead 14 behind 36` | bar + `14 · 36` under AHEAD / BEHIND UPSTREAM |
| `Remove` (red, every row) | overflow `Remove worktree` |
| `Clean up` panel copy | kept, split into a view with a disk bar and a prune table |
| `Copy .env for host app` (code block) | overflow `Copy .env for host app` |
| `click to copy` tooltips | `Copy` icon button labels |
| `loading...` / `scanning...` | skeleton rows + `Scanning folders…` |

## Screens

For Figma (desktop 1440 × 900 and 400 × 860, dark and light each):

1. Instances — running + stopped, one card expanded with a seed-failed callout and a partial stack, disk banner on.
2. Instances — empty state and loading skeleton.
3. Repos — table, one row expanded with worktrees, a detached worktree, branches not checked out, one "Not set up" row.
4. Repos — empty (no folders), scanning, start failed callout on a worktree.
4b. Repos — a repository with live mode on and one with live mode paused (reason, **Resume**), a worktree with a cleanup callout, and the cleanup selector, in light and dark at 1440 and 400 px.
5. Clean up view.
6. Settings view.
7. Overlays — overflow menu, Learn modal, log drawer, toast, StatusPill popover.
8. Component sheet — buttons, chips, pills, bars, callouts, inputs, rail, toolbar.
9. Instances — grouped by project (page "Instances — grouped"): 1440 dark and light, 400 dark, and a state sheet (one version, 7 versions expanded and collapsed, all stopped, attention callout, the four App chip states, loading, empty, filter active).

## Do's and Don'ts

- Do put the branch first and the path second, relative to the repo.
- Do show state as glyph + word; do colour the dot; don't rely on the colour.
- Do keep one filled violet button per view; don't put a primary on every row.
- Do use `off` grey for stopped; don't spend red on anything that is not broken.
- Do render tables with micro headers and aligned cells; don't write sentences with buttons inside them.
- Do keep focus and selection across polls; don't `innerHTML` a focused subtree.
- Do name icon buttons with `aria-label`; don't ship unicode glyphs as icons.
- Do wrap at 400 px into two-line rows; don't let a row scroll horizontally.
- Do use Geist Mono for data and the system sans for labels; don't use mono as a costume.
- Don't add a glow, a gradient, a blurred backdrop or a coloured left border.

## Appendix: token CSS

```css
:root, [data-theme="dark"] {
  --bg:#0B0B0E; --surface-1:#131318; --surface-2:#1A1A21; --surface-3:#22222B;
  --line:#272730; --line-strong:#3A3A46; --line-control:#5E5D70;
  --fg:#F2F1F5; --fg-muted:#A9A8B8; --fg-subtle:#8E8D9E;
  --accent:#614DFF; --accent-strong:#7462FF; --accent-text:#A295FF; --on-accent:#fff;
  --focus:#A295FF; --accent-soft:#614DFF29;
  --ok:#34C286; --attention:#F0BF4A; --failed:#FF6B83; --busy:#5CCBEE; --off:#8E8D9E;
  --ok-bg:#34C2862E; --ok-line:#34C28666; --attention-bg:#F0BF4A2E; --attention-line:#F0BF4A66;
  --failed-bg:#FF6B832E; --failed-line:#FF6B8366; --busy-bg:#5CCBEE2E; --busy-line:#5CCBEE66;
  --off-bg:#8E8D9E24; --off-line:#8E8D9E52;
  --shadow-popover:0 8px 24px rgba(0,0,0,.55); --shadow-modal:0 20px 48px rgba(0,0,0,.65);
  --backdrop:rgba(11,11,14,.6);
}
[data-theme="light"] {
  --bg:#F4F3F8; --surface-1:#FFFFFF; --surface-2:#F1F0F6; --surface-3:#FFFFFF;
  --line:#E2E0EB; --line-strong:#C7C4D6; --line-control:#8A889C;
  --fg:#15151C; --fg-muted:#56556B; --fg-subtle:#6B6A7F;
  --accent:#614DFF; --accent-strong:#4F3AEF; --accent-text:#4B36F0; --on-accent:#fff;
  --focus:#4B36F0; --accent-soft:#614DFF40;
  --ok:#116A42; --attention:#7A5200; --failed:#B01B38; --busy:#0A6276; --off:#5F5E73;
  --ok-bg:#116A421A; --ok-line:#116A4252; --attention-bg:#7A52001A; --attention-line:#7A520052;
  --failed-bg:#B01B381A; --failed-line:#B01B3852; --busy-bg:#0A62761A; --busy-line:#0A627652;
  --off-bg:#5F5E7314; --off-line:#5F5E7340;
  --shadow-popover:0 8px 24px rgba(21,21,28,.12); --shadow-modal:0 20px 48px rgba(21,21,28,.18);
  --backdrop:rgba(21,21,28,.45);
}
@media (prefers-color-scheme: light) { :root:not([data-theme]) { /* same values as [data-theme="light"] */ } }
:root {
  --lane-1:#2F5BD8; --lane-2:#6D3FD6; --lane-3:#B8266A; --lane-4:#A85A00;
  --lane-5:#137A4B; --lane-6:#0B6F86; --lane-7:#B9231F; --lane-8:#4A4AD0;
  --space-1:4px; --space-2:8px; --space-3:12px; --space-4:16px; --space-5:20px; --space-6:24px; --space-8:32px;
  --radius-xs:4px; --radius-sm:6px; --radius-md:8px; --radius-lg:12px; --radius-full:9999px;
  --duration-fast:120ms; --duration-base:180ms; --duration-slow:240ms;
  --ease-out:cubic-bezier(.23,1,.32,1); --ease-in-out:cubic-bezier(.77,0,.175,1);
  --font-sans:system-ui,-apple-system,"Segoe UI",Helvetica,Arial,sans-serif;
  --font-mono:"Geist Mono",ui-monospace,"SF Mono",Menlo,monospace;
}
@font-face { font-family:"Geist Mono"; src:url(/static/fonts/GeistMono[wght].woff2) format("woff2"); font-weight:100 900; font-display:swap; }
```
