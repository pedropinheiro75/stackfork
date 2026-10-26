// Project page prototype: hash routing, render functions and the four
// interactions (mode switch, checkout overlay, theme, collapse). Static data
// comes from data.js. Not product code: it shows behaviour for approval.
(function () {
  "use strict";
  var D = window.PP;
  var main = document.getElementById("main");
  var crumbs = document.getElementById("crumbs");
  var LANES = ["sf", "docker", "host"];
  var LANE_X = ["16.667%", "50%", "83.333%"];
  var LANE_NAME = { sf: "stackfork", docker: "Docker", host: "this machine" };

  var state = { mode: "hybrid", sel: "invoices", collapsed: {}, log: false, node: null, palette: false, range: "1h", memBy: "checkout", history: "full" };

  // ---------- helpers ----------
  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function chip(cls, glyph, label, title) {
    return '<span class="chip chip-' + cls + '"' + (title ? ' title="' + esc(title) + '"' : "") +
      '><span class="chip-glyph" aria-hidden="true">' + glyph + "</span>" + esc(label) + "</span>";
  }
  function plural(n, word) { return n + " " + word + (n === 1 ? "" : /(ch|sh|s|x)$/.test(word) ? "es" : "s"); }
  function mb(v) {
    if (v == null) return "—";
    if (v >= 1000) return (v / 1024).toFixed(1).replace(/\.0$/, "") + "\u00A0GB";
    return Math.round(v) + "\u00A0MB";
  }
  function pct(v) { return v == null ? "—" : v.toFixed(1) + "\u00A0%"; }
  function checkout(key) { return D.checkouts.filter(function (c) { return c.key === key; })[0]; }
  function inMode(step, mode) { return !step.modes || step.modes.indexOf(mode) >= 0; }
  function abText(ab) {
    if (typeof ab === "string") return ab;
    if (!ab[0] && !ab[1]) return "up to date";
    return ab[0] + " ahead · " + ab[1] + " behind";
  }
  function abBar(ab) {
    if (typeof ab === "string") return '<span class="ab-none">' + esc(ab) + "</span>";
    var t = ab[0] + ab[1], a = Math.max(ab[0] ? 4 : 0, Math.round(56 * ab[0] / t)), b = ab[1] ? 56 - a : 0;
    return '<span class="ab"><svg class="ab-bar" width="56" height="4" viewBox="0 0 56 4" aria-hidden="true" focusable="false"><rect class="ab-track" width="56" height="4" rx="2"/>' +
      (a ? '<rect class="ab-ahead" width="' + a + '" height="4" rx="2"/>' : "") +
      (b ? '<rect class="ab-behind" x="' + a + '" width="' + b + '" height="4" rx="2"/>' : "") +
      '</svg><span class="mono ab-count" aria-hidden="true">' + ab[0] + " · " + ab[1] + '</span><span class="sr-only">' + abText(ab) + " upstream</span></span>";
  }
  var STATE_CHIP = {
    running: ["ok", "●", "Running"], stopped: ["off", "○", "Stopped"],
    starting: ["busy", "◔", "Starting…"], none: ["off", "○", "No stack"]
  };
  var PLAN_CHIP = {
    start: ["off", "○", "at start", "stackfork resolves the secrets before anything else runs"],
    verified: ["ok", "●", "verified", "Recorded and verified by learn"],
    trusted: ["ok", "●", "trusted", "Runs on this machine; trusted 3 d ago"],
    untrusted: ["attention", "▲", "untrusted", "Runs on this machine; trust it first"],
    draft: ["attention", "▲", "draft", "Proposed by learn, not recorded; a Start skips it"]
  };
  var RUN_CHIP = {
    succeeded: ["ok", "●", "Succeeded"], failed: ["failed", "⊘", "Failed"], skipped: ["off", "○", "Skipped"],
    running: ["busy", "◔", "Running"], pending: ["off", "○", "Pending"]
  };
  function stateChip(s) { var c = STATE_CHIP[s]; return chip(c[0], c[1], c[2]); }

  // ---------- section frame ----------
  function section(id, title, sub, tools, body) {
    var collapsed = state.collapsed[id] ? "1" : "0";
    return '<section class="pp-section" id="sec-' + id + '" data-collapsed="' + collapsed + '" aria-labelledby="h-' + id + '">' +
      '<div class="pp-section-head">' +
      '<button type="button" class="btn btn-ghost btn-icon chevron" data-collapse="' + id + '" aria-expanded="' + (collapsed === "1" ? "false" : "true") +
      '" aria-controls="body-' + id + '" aria-label="Show or hide ' + esc(title) + '"><span class="chevron-glyph" aria-hidden="true"></span></button>' +
      '<h2 id="h-' + id + '">' + esc(title) + "</h2>" +
      (sub ? '<span class="pp-section-sub">' + sub + "</span>" : "") +
      (tools ? '<div class="pp-section-tools">' + tools + "</div>" : "") +
      "</div>" +
      '<div class="pp-section-body" id="body-' + id + '">' + body + "</div></section>";
  }

  // ---------- header ----------
  function header() {
    var p = D.project;
    var draft = D.steps.filter(function (s) { return s.plan === "draft"; }).length;
    return '<header class="pp-head lane-' + p.lane + '">' +
      '<span class="avatar" aria-hidden="true">AW</span>' +
      '<div class="pp-head-id"><div class="pp-title-line"><h1 class="pp-title">' + esc(p.name) + "</h1>" +
      '<div class="pp-chips">' + chip("ok", "●", "2 running") + chip("busy", "◔", "1 starting") +
      (draft ? chip("attention", "▲", plural(draft, "draft step"), "Learn proposed a step that is not recorded yet") : "") + "</div></div>" +
      '<p class="pp-meta"><span class="mono" title="' + esc(p.path) + '">' + esc(p.path) + '</span><span class="dot" aria-hidden="true">·</span>' +
      '<span>default <code class="mono">' + esc(p.defaultBranch) + '</code></span><span class="dot" aria-hidden="true">·</span><span>fetched ' + esc(p.fetched) + "</span></p></div>" +
      '<div class="pp-head-actions">' +
      '<button type="button" role="switch" class="switch" aria-checked="false" data-live aria-label="Live mode for acme-web"><span class="switch-track" aria-hidden="true"><span class="switch-thumb"></span></span><span class="switch-label">Live</span></button>' +
      '<button type="button" class="btn" title="Pick a branch or worktree to start">Start…</button>' +
      '<div class="menu-anchor"><button type="button" class="btn btn-ghost btn-icon" data-menu aria-haspopup="menu" aria-expanded="false" aria-label="More actions for acme-web"><span aria-hidden="true">&#8943;</span></button>' +
      '<div class="menu" role="menu" hidden>' +
      '<button type="button" role="menuitem" class="menu-item" tabindex="-1"><span class="menu-label">Fetch</span><span class="menu-cmd mono">git fetch --prune</span></button>' +
      '<button type="button" role="menuitem" class="menu-item" tabindex="-1"><span class="menu-label">Copy path</span><span class="menu-cmd mono">' + esc(p.path) + "</span></button>" +
      '<button type="button" role="menuitem" class="menu-item" tabindex="-1"><span class="menu-label">Verify setup with your agent</span></button>' +
      '<button type="button" role="menuitem" class="menu-item" tabindex="-1"><span class="menu-label">Rescan this repo</span></button>' +
      "</div></div></div></header>";
  }

  // ---------- checkouts ----------
  function checkouts() {
    var head = '<div class="pp-co-head micro" aria-hidden="true"><span></span><span>Branch</span><span class="pp-co-path-col">Path</span><span class="pp-co-ab">Ahead / behind</span><span>Mode</span><span>State</span><span>Last run</span><span></span></div>';
    var rows = D.checkouts.map(function (c, i) {
      var r = c.run, sel = state.sel === c.key;
      var rc = RUN_CHIP[r.outcome];
      var lane = '<span class="wt-lane" aria-hidden="true"><svg width="24" height="100%" focusable="false"><rect class="lane-trunk" x="11" y="' + (i === 0 ? "50%" : "0") + '" width="2" height="' + (i === 0 || i === D.checkouts.length - 1 ? "50%" : "100%") + '"/>' +
        (c.main ? "" : '<rect class="lane-stub" x="11" y="50%" width="9" height="2"/>') + '<circle class="lane-dot" cx="' + (c.main ? 12 : 20) + '" cy="50%" r="3"/></svg></span>';
      var action = c.state === "running" ? '<a class="btn" href="#project/acme-web" title="http://' + esc(c.host) + '">Open App</a>' :
        c.state === "stopped" ? '<button type="button" class="btn">Start</button>' :
        '<button type="button" class="btn btn-ghost pp-secondary">Logs</button>';
      return '<div class="pp-co-row lane-' + D.project.lane + (c.main ? " wt-main" : "") + '" data-co="' + c.key + '" data-selected="' + sel + '">' +
        lane +
        '<span class="wt-branch"><span class="branch-pill" title="' + esc(c.branch) + '">' + esc(c.branch) + "</span>" +
        (c.main ? chip("off", "", "main checkout").replace('<span class="chip-glyph" aria-hidden="true"></span>', "") : "") +
        (c.dirty ? chip("attention", "●", "changes", "Uncommitted changes") : "") + "</span>" +
        '<span class="pp-co-path mono" title="' + esc(D.project.path + "/" + c.path) + '">' + esc(c.path) + "</span>" +
        '<span class="pp-co-ab">' + abBar(c.ab) + "</span>" +
        '<span class="pp-co-mode"><span class="pp-mode" data-mode="' + c.mode + '">' + c.mode + "</span></span>" +
        '<span class="wt-state">' + stateChip(c.state) + "</span>" +
        '<span class="pp-co-run"><button type="button" class="pp-run-btn" data-select="' + c.key + '" aria-pressed="' + sel + '" aria-label="Show run ' + r.id + " of " + esc(c.branch) + ' in the workflow">' +
        chip(rc[0], rc[1], r.outcome === "running" ? "Running " + r.dur : rc[2] + (r.failedAt ? " at " + r.failedAt : "")) +
        '<span class="mono">#' + r.id + '</span><span class="pp-run-when">' + esc(r.when) + "</span></button></span>" +
        '<span class="pp-co-actions">' + action +
        '<button type="button" class="btn btn-ghost btn-icon" aria-label="More actions for ' + esc(c.branch) + '"><span aria-hidden="true">&#8943;</span></button></span>' +
        '<span class="pp-co-meta small mono">' + esc(c.path) + " · " + esc(abText(c.ab)) + " · " + c.mode + "</span>" +
        "</div>";
    }).join("");
    var br = '<details class="pp-disclosure"><summary>' + plural(D.branches.length, "branch") + ' not checked out</summary><div class="wt-table wt-branches">' +
      D.branches.map(function (b) {
        return '<div class="wt-row wt-branch-row"><span class="wt-lane" aria-hidden="true"><svg width="24" height="100%" focusable="false"><rect class="lane-trunk lane-dashed" x="11" y="0" width="2" height="100%"/><rect class="lane-stub lane-none lane-dashed" x="11" y="50%" width="9" height="2"/><circle class="lane-dot lane-none lane-hollow" cx="20" cy="50%" r="3"/></svg></span>' +
          '<span class="wt-branch"><span class="branch-pill branch-pill-plain">' + esc(b.name) + '</span></span><span class="wt-path mono muted">not checked out</span><span class="wt-ab">' + abBar(b.ab) +
          '</span><span class="wt-state">' + stateChip("none") + '</span><span class="wt-actions"><button type="button" class="btn">Start</button></span></div>';
      }).join("") + "</div></details>";
    var running = D.checkouts.filter(function (c) { return c.state === "running"; }).length;
    return section("checkouts", "Checkouts", plural(D.checkouts.length, "checkout") + " · " + running + " running",
      '<span class="small muted">Select a last run to show it on the workflow</span>',
      '<div class="pp-co">' + head + rows + "</div>" + br);
  }

  // ---------- workflow: node canvas ----------
  // The flow mirrors what the runner decides (internal/lifecycle): the mode
  // filters the steps; prepare is skipped when already prepared; the data
  // seed runs right after compose-up only when this up creates the data
  // volume; host apps start together. Nothing else branches.
  var FLOW = {
    common: ["secrets", "prepare"],
    container: ["up", { decision: "New data volume?", yes: "seed", no: "data already exists" }, "migrate"],
    hybrid: ["up-deps", { decision: "New data volume?", yes: "seed-host", no: "data already exists" }, "migrate-host", { parallel: ["api", "app"] }]
  };
  var ICON = {
    sf: '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><rect x="2" y="2" width="12" height="12" rx="3" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M10 6.2c-.4-.6-1.1-.9-2-.9-1.1 0-1.9.6-1.9 1.4 0 1.9 4 1 4 2.9 0 .9-.9 1.5-2.1 1.5-.9 0-1.7-.4-2.1-1" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>',
    docker: '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M8 1.8 13.5 5v6L8 14.2 2.5 11V5z M2.5 5 8 8.2 13.5 5 M8 8.2v6" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/></svg>',
    host: '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><rect x="3" y="3" width="10" height="7" rx="1" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M1.5 13h13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
    decision: '<svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true" focusable="false"><path d="M6 1 11 6 6 11 1 6z" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"/></svg>'
  };
  var SERVICES = { up: "app, db, nginx, mailpit", "up-deps": "db, mailpit", migrate: "app", seed: "app" };
  function stepById(id) { return D.steps.filter(function (s) { return s.id === id; })[0]; }
  function whereText(s) { return s.lane === "sf" ? "stackfork" : s.lane === "host" ? "this machine" : "Docker · " + (SERVICES[s.id] || "app"); }
  function viewRun(mode) { var sel = state.sel ? checkout(state.sel) : null; return sel && sel.run.mode === mode ? sel.run : null; }
  function runState(run, id) { return run ? run.steps[id] || null : null; }
  function reachedId(run, id) { var r = runState(run, id); return !run || (r && r[0] !== "pending"); }
  function runBadge(rs) {
    if (!rs) return "";
    var c = RUN_CHIP[rs[0]], label = c[2] + (rs[1] ? " " + rs[1] : "");
    return '<span class="pp-node-run">' + chip(c[0], c[1], label, rs[2] || "") + (rs[2] && rs[0] !== "succeeded" ? '<span class="pp-node-reason">' + esc(rs[2]) + "</span>" : "") + "</span>";
  }
  function nodeCard(id, mode, active, run) {
    var s = stepById(id), rs = active ? runState(run, id) : null, p = PLAN_CHIP[s.plan];
    var where = s.lane === "sf" ? "sf" : s.lane === "host" ? "host" : "docker";
    var sub = s.kind + " · " + whereText(s);
    var stateWord = rs ? RUN_CHIP[rs[0]][2] : p[2];
    var sel = state.node === id && active;
    return '<button type="button" class="pp-node" data-node="' + id + '" data-where="' + where + '" data-run="' + (rs ? rs[0] : "") + '"' + (active ? "" : ' tabindex="-1" aria-hidden="true"') +
      ' aria-pressed="' + sel + '" aria-label="' + esc(id + ", " + sub + ", " + stateWord + ". Open details") + '">' +
      '<span class="pp-node-head"><span class="pp-node-icon">' + ICON[where] + '</span><span class="pp-node-id"><span class="pp-node-title">' + esc(id) + '</span><span class="pp-node-sub">' + esc(sub) + "</span></span>" + chip(p[0], p[1], p[2], p[3]) + "</span>" +
      '<code class="pp-node-cmd" title="' + esc(s.raw || s.cmd) + '">' + esc(s.cmd) + "</code>" + runBadge(rs) + "</button>";
  }
  function edge(reached, label, cls) {
    return '<span class="pp-edge' + (cls ? " " + cls : "") + '" data-reached="' + (reached ? 1 : 0) + '">' + (label ? '<span class="pp-edge-label">' + label + "</span>" : "") + "</span>";
  }
  // One branch column: a list of steps, decisions and parallel groups.
  function chain(items, mode, active, run, startReached) {
    var out = "", prevReached = startReached;
    items.forEach(function (it, k) {
      if (typeof it === "string") {
        var r = active && prevReached && reachedId(run, it);
        if (k > 0) out += edge(active && r);
        out += nodeCard(it, mode, active, run); prevReached = r;
      } else if (it.decision) {
        var taken = run ? run.data : null;
        out += edge(active && prevReached) + '<span class="pp-decision">' + ICON.decision + esc(it.decision) + "</span>";
        var yesOn = active && prevReached && (!run || taken === "yes"), noOn = active && prevReached && (!run || taken === "no");
        out += '<div class="pp-split" role="group" aria-label="' + esc(it.decision) + '">' +
          '<div class="pp-branch" data-on="' + (yesOn || !run ? 1 : 0) + '">' + edge(yesOn, "yes", "pp-edge-tall") + nodeCard(it.yes, mode, active, run) + edge(yesOn, "", "pp-edge-grow") + "</div>" +
          '<div class="pp-branch pp-branch-skip" data-on="' + (noOn || !run ? 1 : 0) + '">' + edge(noOn, "no", "pp-edge-tall") + '<span class="pp-skip-note">skip seed<span class="pp-dim"> · ' + esc(it.no) + "</span></span>" + edge(noOn, "", "pp-edge-grow") + "</div></div>";
        prevReached = active && prevReached && (!run || !!taken);
      } else if (it.parallel) {
        var rr = it.parallel.map(function (id) { return active && prevReached && reachedId(run, id); });
        out += edge(active && prevReached, "start together") + '<div class="pp-split pp-parallel" role="group" aria-label="Host apps that start together">' + it.parallel.map(function (id, j) {
          return '<div class="pp-branch" data-on="1">' + edge(rr[j], "", "pp-edge-short") + nodeCard(id, mode, active, run) + edge(rr[j], "", "pp-edge-grow") + "</div>";
        }).join("") + "</div>";
        prevReached = rr.every(Boolean);
      }
    });
    return { html: out, reached: prevReached };
  }
  function workflow() {
    var mode = state.mode, run = viewRun(mode), sel = state.sel ? checkout(state.sel) : null;
    var com = chain(FLOW.common, mode, true, run, true);
    var start = '<span class="pp-terminal pp-start">' + (run ? "Start · run #" + run.id + " · " + esc(run.when) : "Start") + "</span>";
    var branches = ["container", "hybrid"].map(function (m) {
      var on = m === mode, c = chain(FLOW[m], m, on, on ? run : null, com.reached);
      var ready = on && c.reached && (!run || run.outcome === "succeeded");
      return '<div class="pp-branch pp-mode-branch" data-on="' + (on ? 1 : 0) + '" data-mode="' + m + '">' +
        edge(on && com.reached, '<button type="button" class="pp-branch-btn" data-mode-to="' + m + '" aria-pressed="' + on + '">' + m + "</button>", "pp-edge-tall") +
        '<span class="pp-branch-more">' + D.steps.filter(function (st) { return st.modes && st.modes.indexOf(m) >= 0; }).length + " steps only in " + m + " mode</span>" +
        c.html + edge(on && c.reached && (!run || run.outcome !== "failed")) +
        '<span class="pp-terminal pp-ready" data-on="' + (ready ? 1 : 0) + '">' + (ready && run ? '<span class="chip-glyph pp-ok" aria-hidden="true">●</span>' : "") + "Ready · App → " + esc(D.project.entry[m]) + "</span>" +
        (on && run && run.outcome === "failed" ? '<span class="pp-skip-note">not reached: run #' + run.id + " stopped at " + esc(run.failedAt) + "</span>" : "") + "</div>";
    }).join("");
    var flow = '<div class="pp-flow">' + start + edge(true) + com.html + edge(com.reached) +
      '<span class="pp-decision">' + ICON.decision + "Mode?</span>" +
      '<div class="pp-split pp-modes" role="group" aria-label="Mode">' + branches + "</div></div>";

    var seg = '<fieldset class="segmented"><legend class="sr-only">Show the workflow for</legend>' +
      D.project.modes.map(function (m) {
        return '<label><input type="radio" name="wf-mode" value="' + m + '"' + (m === mode ? " checked" : "") + "><span>" + m.charAt(0).toUpperCase() + m.slice(1) +
          (m === D.project.defaultMode ? ' <span class="pp-default-tag">default</span>' : "") + "</span></label>";
      }).join("") + "</fieldset>";
    var makeDefault = mode !== D.project.defaultMode ? '<button type="button" class="btn btn-ghost" data-make-default title="Start uses this mode for new environments">Make default</button>' : "";
    var overlay = "";
    if (sel) {
      var rc = RUN_CHIP[sel.run.outcome];
      overlay = '<span class="pp-overlay-tag lane-' + D.project.lane + '"><span class="small muted">Last run of</span><span class="branch-pill">' + esc(sel.branch) + '</span><span class="mono">#' + sel.run.id + "</span>" +
        chip(rc[0], rc[1], rc[2]) + '<span class="small muted">' + esc(sel.run.when) + (sel.run.outcome === "running" ? "" : " · " + esc(sel.run.dur)) + "</span>" +
        (run ? "" : '<button type="button" class="btn" data-mode-to="' + sel.run.mode + '">Show ' + sel.run.mode + "</button>") +
        '<button type="button" class="btn btn-ghost" data-clear aria-label="Hide the last run">Hide</button></span>';
    }
    var counts = { sf: 0, docker: 0, host: 0 };
    D.steps.forEach(function (s) { if (inMode(s, mode)) counts[s.lane]++; });
    var summary = mode === "container"
      ? "<strong>Container mode:</strong> every service runs in Docker. " + counts.sf + " steps by stackfork, " + counts.docker + " in Docker, none on this machine."
      : "<strong>Hybrid mode:</strong> db and mailpit run in Docker; api and app run on this machine. " + counts.sf + " steps by stackfork, " + counts.docker + " in Docker, " + counts.host + " on this machine. " + esc(D.project.trust) + ".";
    var palette = '<button type="button" class="btn btn-ghost" data-palette aria-expanded="' + !!state.palette + '" aria-controls="pp-palette">Step kinds</button>';
    var list = '<details class="pp-disclosure"><summary>Steps as a list</summary><ol class="pp-steps-list">' + D.steps.filter(function (s) { return inMode(s, mode); }).map(function (s) {
      var rs = runState(run, s.id);
      return "<li><code class=\"mono\">" + esc(s.id) + "</code> · " + esc(whereText(s)) + " · " + esc(s.cmd) + " · plan: " + esc(PLAN_CHIP[s.plan][2]) + (rs ? " · run: " + esc(RUN_CHIP[rs[0]][2] + (rs[1] ? " " + rs[1] : "") + (rs[2] ? ", " + rs[2] : "")) : "") + "</li>";
    }).join("") + "</ol></details>";
    var draftCallout = mode === "hybrid" ? '<div class="callout callout-attention"><span class="callout-glyph" aria-hidden="true">&#9650;</span><div class="callout-body"><p class="callout-title">seed-host is a draft</p><p class="callout-detail">A Start in hybrid mode skips it, so a new hybrid environment has an empty database. Verify it once with your agent to record it.</p></div><div class="callout-actions"><button type="button" class="btn">Verify with your agent</button></div></div>' : "";
    return section("workflow", "Startup workflow", "Entry: <code class=\"mono\">App → " + esc(D.project.entry[mode]) + "</code>",
      seg + makeDefault + overlay + palette,
      '<p class="pp-wf-summary">' + summary + "</p>" +
      '<div class="pp-wf-body" data-palette="' + (state.palette ? 1 : 0) + '" data-panel="' + (state.node ? 1 : 0) + '">' +
      '<aside class="pp-palette" id="pp-palette" aria-label="Step kinds"' + (state.palette ? "" : " hidden") + ">" + paletteHTML() + "</aside>" +
      '<div class="pp-canvas" role="group" aria-label="Startup flow, ' + mode + ' mode. Use the arrow keys to move between steps.">' + flow + "</div>" +
      '<aside class="pp-panel" id="pp-panel" aria-label="Step details"' + (state.node ? "" : " hidden") + ">" + (state.node ? panelHTML(state.node, run) : "") + "</aside></div>" +
      list + draftCallout);
  }
  function paletteHTML() {
    return '<p class="pp-palette-note">Read only. Your agent records steps with <code class="mono">stackfork learn</code>; you trust them.</p><ul>' + [
      ["sf", "secrets", "Preflight. Resolves secret names before anything runs."],
      ["sf", "prepare", "Writes the instance: ports, env, Compose overrides."],
      ["docker", "compose-up", "Starts the services, or some of them in hybrid."],
      ["docker", "exec", "Runs a command in a service, or on: host. once: schema or data."],
      ["host", "host app", "A dev server on this machine, with its port in env."],
      ["sf", "note", "Text the run prints. Runs nothing."]
    ].map(function (k) { return '<li><span class="pp-node-icon" data-where="' + k[0] + '">' + ICON[k[0]] + '</span><span><b class="mono">' + k[1] + "</b><span>" + k[2] + "</span></span></li>"; }).join("") + "</ul>";
  }
  function panelHTML(id, run) {
    var s = stepById(id), rs = runState(run, id), sel = state.sel ? checkout(state.sel) : null, p = PLAN_CHIP[s.plan];
    var raw = s.raw || s.cmd.replace(/\s{2,}(in|on) \S+$/, "");
    var whereLong = s.lane === "sf" ? "stackfork itself, before any project code" : s.lane === "host" ? "This machine, in " + (s.dir || "the checkout") : "Docker, in the " + (SERVICES[s.id] || "app") + " service";
    var exec = s.lane === "docker" && s.kind.indexOf("exec") === 0 ? "docker compose exec " + (SERVICES[s.id] || "app") + " sh -c '" + raw + "'" : s.lane === "host" ? "cd " + (s.dir || ".") + " && " + raw : raw;
    var vals = s.portEnv ? s.portEnv.map(function (e) { return "<tr><th scope=\"row\"><code class=\"mono\">$" + e + "</code></th><td><code class=\"mono\">" + s.port + "</code></td><td>port assigned to this checkout</td></tr>"; }).join("") : "";
    if (s.lane === "host" && s.id !== "seed-host") vals += '<tr><th scope="row"><code class="mono">$DATABASE_URL</code></th><td><code class="mono">postgres://acme:••••@127.0.0.1:25432/acme</code></td><td>db in Docker, from the instance .env</td></tr>';
    if (s.lane === "host" && s.kind.indexOf("exec") === 0) vals += '<tr><th scope="row"><code class="mono">$DATABASE_URL</code></th><td><code class="mono">postgres://acme:••••@127.0.0.1:25432/acme</code></td><td>db in Docker, from the instance .env</td></tr>';
    var history = "";
    if (sel) {
      var rows = [[sel.run.id, sel.run.when, rs]].concat((sel.older || []).map(function (o) { return [o[0], o[1], inMode(s, sel.run.mode) ? ["succeeded", s.plan === "draft" ? "" : (1 + (o[0] % 5) * 0.7).toFixed(1) + " s"] : null]; }));
      if (s.plan === "draft") rows = rows.map(function (r) { return [r[0], r[1], r[2] ? ["skipped", "", "setup is a draft"] : null]; });
      history = '<ol class="pp-panel-runs">' + rows.map(function (r) {
        var c = r[2] ? RUN_CHIP[r[2][0]] : null;
        return '<li><span class="mono">#' + r[0] + "</span>" + (c ? chip(c[0], c[1], c[2]) : '<span class="pp-dim">not in this mode</span>') + '<span class="mono pp-dim">' + (r[2] && r[2][1] ? r[2][1] : "") + "</span><span class=\"pp-dim\">" + esc(r[1]) + "</span></li>";
      }).join("") + "</ol>";
    }
    var fail = rs && rs[0] === "failed" && run.log ? '<div class="callout callout-failed"><span class="callout-glyph" aria-hidden="true">&#8856;</span><div class="callout-body"><p class="callout-title">Failed with exit 1 in run #' + run.id + '</p><pre class="mono-log" tabindex="0" aria-label="Log of ' + esc(id) + '">' + esc(run.log) + '</pre></div><div class="callout-actions"><button type="button" class="btn" title="' + esc(run.retry) + '">Retry</button></div></div>' : "";
    var record = "stackfork learn record step --id " + id + " --kind " + (s.kind.indexOf("exec") === 0 ? "exec" : s.kind === "host app" ? "host" : s.kind === "compose up" ? "compose-up" : s.kind) +
      (s.kind.indexOf("once") > 0 ? " --once " + s.kind.split("once ")[1] : "") + (s.lane === "host" && s.kind.indexOf("exec") === 0 ? " --on host" : "") + (s.lane === "docker" && s.kind.indexOf("exec") === 0 ? " --service " + (SERVICES[s.id] || "app") : "") + " --cmd \"" + raw + "\"";
    return '<div class="pp-panel-head"><span class="pp-node-icon" data-where="' + (s.lane === "sf" ? "sf" : s.lane) + '">' + ICON[s.lane === "sf" ? "sf" : s.lane] + '</span><h3 class="pp-panel-title"><span class="mono">' + esc(id) + '</span><span class="pp-node-sub">' + esc(s.kind) + "</span></h3>" +
      '<button type="button" class="btn btn-ghost btn-icon" data-close-panel aria-label="Close step details">&#10005;</button></div>' +
      '<dl class="pp-panel-dl"><dt>Runs on</dt><dd>' + esc(whereLong) + "</dd>" +
      "<dt>Plan</dt><dd>" + chip(p[0], p[1], p[2]) + ' <span class="pp-dim">' + esc(p[3] || "") + "</span></dd>" +
      (rs ? "<dt>Run #" + run.id + "</dt><dd>" + chip(RUN_CHIP[rs[0]][0], RUN_CHIP[rs[0]][1], RUN_CHIP[rs[0]][2] + (rs[1] ? " " + rs[1] : "")) + (rs[2] ? ' <span class="pp-dim">' + esc(rs[2]) + "</span>" : "") + "</dd>" : "") + "</dl>" +
      '<h4 class="pp-panel-h">Command</h4><pre class="pp-panel-cmd mono">' + esc(exec) + "</pre>" +
      (vals ? '<table class="pp-panel-vals"><caption class="pp-panel-h">Values stackfork fills in</caption>' + vals + "</table>" : "") +
      fail +
      (history ? '<h4 class="pp-panel-h">Last 3 runs of this step</h4>' + history : "") +
      '<h4 class="pp-panel-h">Change this step</h4><p class="pp-panel-note">Steps are recorded by your agent with <code class="mono">stackfork learn</code>. You review and trust them; this page does not edit them.</p>' +
      '<p class="command-row pp-panel-record"><code class="mono">' + esc(record) + '</code><button type="button" class="btn btn-ghost" data-copy="' + esc(record) + '">Copy</button></p>';
  }

  function memTone(r) { return r >= 0.95 ? "failed" : r >= 0.85 ? "attention" : ""; }
  // ---------- resources ----------
  // History lives in D.history (data.js). Series colours are the four chart
  // tokens --series-1..4; slots follow the entity, never its rank.
  var H = D.history;
  var RANGES = { "15m": 900, "1h": 3600, "6h": 21600 };
  var CO_SLOT = { main: 1, invoices: 2, login: 3, search: 4 };
  var KINDS = ["database", "app", "host app", "utility"];
  function kindOf(s) { return s.where === "host" ? "host app" : s.kind === "postgres" ? "database" : (s.name === "nginx" || s.name === "mailpit") ? "utility" : "app"; }
  function dashStart() { return state.history === "short" ? -2400 : state.history === "empty" ? 1 : -Infinity; }
  function window_() {
    var span = RANGES[state.range], start = Math.max(-span, dashStart());
    var idx = []; H.times.forEach(function (t, i) { if (t >= start) idx.push(i); });
    return { idx: idx, span: span, start: start, short: dashStart() > -span };
  }
  function last(key) { var s = H.s[key]; return s[s.length - 1]; }
  function ago(t) { t = -t; if (t < 30) return "now"; if (t < 3600) return Math.round(t / 60) + " min ago"; var h = Math.floor(t / 3600), m = Math.round((t % 3600) / 60); return h + " h" + (m ? " " + m + " min" : "") + " ago"; }
  // Sum a metric (0 cpu, 1 mem) over services per time index; null when none ran.
  function group(keys, metric, idx) {
    return idx.map(function (i) { var sum = null; keys.forEach(function (k) { var v = H.s[k][i]; if (v) sum = (sum || 0) + v[metric]; }); return sum; });
  }
  function seriesBy(by, metric, idx) {
    if (by === "kind") return KINDS.map(function (k, j) {
      var keys = []; D.resources.forEach(function (g) { g.services.forEach(function (s) { if (kindOf(s) === k) keys.push(s.hist); }); });
      return { name: k, slot: j + 1, values: group(keys, metric, idx) };
    });
    return D.resources.slice().sort(function (a, b) { return CO_SLOT[a.checkout] - CO_SLOT[b.checkout]; }).map(function (g) {
      return { name: checkout(g.checkout).branch, slot: CO_SLOT[g.checkout], values: group(g.services.map(function (s) { return s.hist; }), metric, idx) };
    });
  }
  function nice(max, step) { return Math.max(step, Math.ceil(max / step) * step); }
  function fmtMem(v) { return v == null ? "stopped" : mb(v); }
  function fmtCpu(v) { return v == null ? "stopped" : pct(v); }

  function chartCard(id, title, sub, tools, legend, note) {
    return '<figure class="pp-chart-card" aria-labelledby="ct-' + id + '">' +
      '<div class="pp-chart-head"><figcaption><h3 class="pp-chart-title" id="ct-' + id + '">' + title + '</h3><p class="pp-chart-sub">' + sub + "</p></figcaption>" + (tools || "") + "</div>" +
      '<ul class="pp-legend-list" aria-label="Legend">' + legend + "</ul>" +
      '<div class="pp-chart" data-chart="' + id + '" tabindex="0" role="group" aria-roledescription="chart" aria-describedby="cs-' + id + '"></div>' +
      '<p class="sr-only" id="cs-' + id + '" aria-live="polite"></p>' + (note || "") + "</figure>";
  }
  function legendItems(series, kind, fmt) {
    return series.map(function (s) {
      var cur = s.values[s.values.length - 1];
      return '<li><span class="pp-key pp-key-' + kind + ' pp-s' + s.slot + '" aria-hidden="true"></span><span class="pp-legend-name">' + esc(s.name) + '</span><span class="pp-legend-val">' + fmt(cur) + "</span></li>";
    }).join("");
  }
  function sparkline(key, metric, idx, ymax) {
    var vals = idx.map(function (i) { var v = H.s[key][i]; return v ? v[metric] : null; });
    var w = 72, h = 20, n = vals.length, max = ymax || Math.max.apply(null, vals.filter(function (v) { return v != null; }).concat([metric ? 64 : 5]));
    if (!n || vals.every(function (v) { return v == null; })) return '<svg class="pp-spark" width="72" height="20" aria-hidden="true" focusable="false"><line class="pp-spark-empty" x1="0" x2="72" y1="19" y2="19"/></svg>';
    var d = "", pen = false, lx = 0, ly = 0;
    vals.forEach(function (v, i) {
      if (v == null) { pen = false; return; }
      var t0 = H.times[idx[0]], x = 1 + (H.times[idx[i]] - t0) / (0 - t0 || 1) * (w - 4), y = h - 2 - (v / max) * (h - 4);
      d += (pen ? "L" : "M") + x.toFixed(1) + " " + y.toFixed(1); pen = true; lx = x; ly = y;
    });
    var end = vals[n - 1] != null ? '<circle class="pp-spark-dot" cx="' + lx.toFixed(1) + '" cy="' + ly.toFixed(1) + '" r="2.5"/>' : "";
    return '<svg class="pp-spark" width="72" height="20" viewBox="0 0 72 20" aria-hidden="true" focusable="false"><path class="pp-spark-line" d="' + d + '"/>' + end + "</svg>";
  }
  function diskRows() {
    // Each checkout's bar: its volumes, then its share of the images it uses
    // (an image used by three checkouts adds a third of its size to each).
    var IMG = { "acme-web-app:dev": 1341, "postgres:17": 438, "nginx:1.27": 52, "axllent/mailpit": 31 };
    var users = {};
    D.resources.forEach(function (g) { g.services.forEach(function (s) { if (IMG[s.image]) (users[s.image] = users[s.image] || {})[g.checkout] = 1; }); });
    var rows = D.resources.slice().sort(function (a, b) { return CO_SLOT[a.checkout] - CO_SLOT[b.checkout]; }).map(function (g) {
      var vol = 0, img = 0, seen = {};
      g.services.forEach(function (s) { vol += s.disk || 0; if (IMG[s.image] && !seen[s.image]) { seen[s.image] = 1; img += IMG[s.image] / Object.keys(users[s.image]).length; } });
      return { g: g, vol: vol, img: img };
    });
    var max = Math.max.apply(null, rows.map(function (r) { return r.vol + r.img; }));
    return rows.map(function (r) {
      var c = checkout(r.g.checkout), slot = CO_SLOT[r.g.checkout];
      var label = c.branch + ": " + mb(r.vol) + " volumes and " + mb(r.img) + " share of images";
      return '<div class="pp-disk-row" tabindex="0" aria-label="' + esc(label) + '" title="' + esc(label) + '">' +
        '<span class="pp-disk-name mono">' + esc(c.branch) + "</span>" +
        '<span class="pp-disk-track"><svg class="pp-disk-bar" viewBox="0 0 100 14" preserveAspectRatio="none" aria-hidden="true" focusable="false">' +
        '<rect class="pp-disk-vol pp-s' + slot + '" width="' + Math.max(0.5, r.vol / max * 100).toFixed(2) + '" height="14"/>' +
        '<rect class="pp-disk-img" x="' + (r.vol / max * 100 + 0.6).toFixed(2) + '" width="' + (r.img / max * 100).toFixed(2) + '" height="14"/></svg>' +
        '<span class="pp-disk-val mono">' + mb(r.vol) + ' <span class="pp-dim">+ ' + mb(r.img) + "</span></span></span></div>";
    }).join("");
  }
  function resources() {
    var w = window_(), idx = w.idx, empty = state.history === "empty";
    var tCpu = 0, tMem = 0, tDisk = 0, nC = 0, nH = 0;
    var rows = D.resources.map(function (g) {
      var c = checkout(g.checkout), cpu = null, mem = null, disk = 0;
      var svc = g.services.map(function (s) {
        var cur = empty ? null : last(s.hist);
        if (cur) { cpu = (cpu || 0) + cur[0]; mem = (mem || 0) + cur[1]; }
        disk += s.disk || 0;
        if (cur) { if (s.where === "host") nH++; else nC++; }
        var r = cur && s.limit ? cur[1] / s.limit : null, tone = r ? memTone(r) : "";
        var where = s.where === "host"
          ? '<span>Host app</span><span class="mono">pid ' + s.pid + " · up " + s.up + "</span>"
          : "<span>Docker</span><span class=\"mono\">" + esc(s.image) + "</span>";
        var memVal = !cur ? "—" : mb(cur[1]) + (s.limit ? " / " + mb(s.limit) : "");
        var memExtra = !cur ? "" : s.limit
          ? '<svg class="pp-bar" width="40" height="4" viewBox="0 0 40 4" aria-hidden="true" focusable="false"' + (tone ? ' data-tone="' + tone + '"' : "") + '><rect class="pp-bar-track" width="40" height="4" rx="2"/><rect class="pp-bar-fill" width="' + Math.max(2, Math.round(r * 40)) + '" height="4" rx="2"/></svg>'
          : '<span class="pp-share" title="Share of the ' + H.machine.memMB / 1024 + ' GB on this machine">' + (cur[1] / H.machine.memMB * 100).toFixed(1) + " % RAM</span>";
        return '<div class="pp-res-row" role="row"><span class="pp-res-name" role="cell"><span class="service-logo" data-kind="' + s.kind + '" aria-hidden="true"></span><b>' + esc(s.name) + "</b>" +
          (s.note && cur ? '<span class="small muted">' + esc(s.note) + "</span>" : "") + "</span>" +
          '<span class="pp-res-where" role="cell">' + where + "</span>" +
          '<span role="cell" class="pp-cell-spark" data-label="CPU">' + sparkline(s.hist, 0, idx) + '<span class="mono pp-spark-val">' + (cur ? pct(cur[0]) : "—") + "</span></span>" +
          '<span role="cell" class="pp-cell-spark" data-label="Mem"' + (r ? ' title="' + Math.round(r * 100) + ' % of the container limit"' : "") + ">" + sparkline(s.hist, 1, idx, s.limit) + '<span class="mono pp-spark-val' + (tone ? " pp-tone-" + tone : "") + '">' + memVal + "</span>" + memExtra + "</span>" +
          '<span role="cell" class="pp-cell-disk" data-label="Disk">' + (s.where === "host" ? '<span class="pp-dim" title="Host apps write to the checkout; not measured">—</span>' : '<span class="mono">' + (s.disk ? mb(s.disk) : '<span class="pp-dim">0</span>') + "</span>") + "</span></div>";
      }).join("");
      tCpu += cpu || 0; tMem += mem || 0; tDisk += disk;
      var groupWhere = cpu == null ? "<span>Stopped · volumes kept</span>" : "<span>" + c.mode + "</span>";
      return '<div class="pp-res-row pp-res-group lane-' + D.project.lane + '" role="row"><span class="pp-res-name" role="rowheader"><span class="branch-pill">' + esc(c.branch) + "</span>" + stateChip(c.state) + "</span>" +
        '<span class="pp-res-where" role="cell">' + groupWhere + "</span>" +
        '<span role="cell" class="pp-cell-spark" data-label="CPU"><span class="mono pp-spark-val">' + (cpu == null ? "—" : pct(cpu)) + "</span></span>" +
        '<span role="cell" class="pp-cell-spark" data-label="Mem"><span class="mono pp-spark-val">' + (mem == null ? "—" : mb(mem)) + "</span></span>" +
        '<span role="cell" class="pp-cell-disk" data-label="Disk"><span class="mono">' + mb(disk) + "</span></span></div>" + svc;
    }).join("");
    var total = '<div class="pp-res-row pp-res-total" role="row"><span role="rowheader">All checkouts</span><span class="pp-res-where" role="cell"><span>' + nC + " containers · " + nH + ' host apps</span></span>' +
      '<span role="cell" class="pp-cell-spark" data-label="CPU"><span class="mono pp-spark-val">' + pct(tCpu) + '</span></span><span role="cell" class="pp-cell-spark" data-label="Mem"><span class="mono pp-spark-val">' + mb(tMem) + '</span></span><span role="cell" class="pp-cell-disk" data-label="Disk"><span class="mono">' + mb(tDisk) + "</span></span></div>";
    var m = H.machine;
    var summary = empty ? '<p class="pp-res-summary">No samples yet.</p>' : '<p class="pp-res-summary">acme-web uses <strong>' + (tCpu / 100).toFixed(1) + " cores</strong> of " + m.cores + ", <strong>" + mb(tMem) + "</strong> of " + m.memMB / 1024 +
      " GB memory and <strong>" + mb(tDisk + D.images.mb) + "</strong> of disk (" + mb(tDisk) + " volumes, " + mb(D.images.mb) + " images).</p>";

    var range = '<fieldset class="segmented pp-range"><legend class="sr-only">Time range</legend>' + ["15m", "1h", "6h"].map(function (r) {
      return '<label><input type="radio" name="res-range" value="' + r + '"' + (r === state.range ? " checked" : "") + "><span>" + { "15m": "15 min", "1h": "1 h", "6h": "6 h" }[r] + "</span></label>";
    }).join("") + "</fieldset>" + (w.short ? '<span class="small muted pp-since">since dashboard start (' + ago(w.start).replace(" ago", "") + ")</span>" : "");
    var memSeries = seriesBy(state.memBy, 1, idx), cpuSeries = seriesBy("checkout", 0, idx);
    var memToggle = '<fieldset class="segmented pp-mini-seg"><legend class="sr-only">Stack memory by</legend>' + [["checkout", "Checkout"], ["kind", "Kind"]].map(function (o) {
      return '<label><input type="radio" name="mem-by" value="' + o[0] + '"' + (o[0] === state.memBy ? " checked" : "") + "><span>" + o[1] + "</span></label>";
    }).join("") + "</fieldset>";
    var emptyNote = empty ? '<p class="pp-chart-empty" role="status">Collecting samples… (first point in 10 s)</p>' : "";
    var charts = '<div class="pp-charts">' +
      chartCard("mem", "Memory", "Stacked by " + (state.memBy === "kind" ? "service kind" : "checkout") + " · GB", memToggle, empty ? "" : legendItems(memSeries, "area", fmtMem), emptyNote) +
      chartCard("cpu", "CPU", "Per checkout · % of one core", "", empty ? "" : legendItems(cpuSeries, "line", fmtCpu), emptyNote) +
      '<figure class="pp-chart-card pp-disk-card" aria-labelledby="ct-disk"><div class="pp-chart-head"><figcaption><h3 class="pp-chart-title" id="ct-disk">Disk</h3><p class="pp-chart-sub">Now, per checkout · volumes and a share of the images</p></figcaption></div>' +
      '<ul class="pp-legend-list" aria-label="Legend"><li><span class="pp-key pp-key-area pp-s0" aria-hidden="true"></span><span class="pp-legend-name">Volumes (checkout colour)</span></li><li><span class="pp-key pp-key-img" aria-hidden="true"></span><span class="pp-legend-name">Share of images</span></li></ul>' +
      '<div class="pp-disk">' + diskRows() + "</div>" +
      '<p class="pp-res-foot">Images are counted once in total (' + mb(D.images.mb) + "); a checkout's share splits each image between the checkouts that use it.</p></figure>" +
      "</div>";
    var table = empty ? "" : '<details class="pp-disclosure pp-data-table"><summary>Show chart data as a table</summary>' + dataTable(memSeries, cpuSeries, idx) + "</details>";
    var head = '<div class="pp-res-head micro" aria-hidden="true"><span>Service</span><span>Runs in</span><span class="num">CPU · ' + { "15m": "15 min", "1h": "1 h", "6h": "6 h" }[state.range] + '</span><span class="num">Memory</span><span class="num">Disk</span></div>';
    var mainApp = empty ? null : last("main/app");
    var callout = mainApp && mainApp[1] / 2048 >= 0.85 ? '<div class="callout callout-attention"><span class="callout-glyph" aria-hidden="true">&#9650;</span><div class="callout-body"><p class="callout-title">app on main uses ' + Math.round(mainApp[1] / 2048 * 100) + ' % of its memory limit, and it keeps growing</p><p class="callout-detail">' + mb(mainApp[1]) + " of 2 GB, up from 1.2 GB 6 h ago. Above the limit, Docker stops the container. Raise <code class=\"mono\">mem_limit</code> in the Compose file or restart app.</p></div></div>" : "";
    return section("resources", "Resources", empty ? "Collecting samples…" : "Sampled 4 s ago · every 10 s while this page is open, every 60 s otherwise",
      range,
      summary + charts + table + callout + '<div role="table" aria-label="Resources by checkout and service">' + head + rows + total + "</div>" +
      '<p class="pp-res-foot">CPU is % of one core. Memory without a limit shows its share of this machine. History stays in memory for 6 h and starts over when the dashboard restarts.</p>');
  }
  function dataTable(memSeries, cpuSeries, idx) {
    var stepN = Math.max(1, Math.round(idx.length / 12)), rows = [];
    for (var k = idx.length - 1; k >= 0; k -= stepN) rows.push(k);
    rows.reverse();
    var th = "<tr><th scope=\"col\">Time</th>" + memSeries.map(function (s) { return '<th scope="col">' + esc(s.name) + " memory</th>"; }).join("") + cpuSeries.map(function (s) { return '<th scope="col">' + esc(s.name) + " CPU</th>"; }).join("") + "</tr>";
    var tb = rows.map(function (k) {
      return "<tr><th scope=\"row\">" + ago(H.times[idx[k]]) + "</th>" + memSeries.map(function (s) { return "<td>" + fmtMem(s.values[k]) + "</td>"; }).join("") + cpuSeries.map(function (s) { return "<td>" + fmtCpu(s.values[k]) + "</td>"; }).join("") + "</tr>";
    }).join("");
    return '<div class="pp-table-scroll" tabindex="0" role="region" aria-label="Chart data"><table class="pp-table"><caption class="sr-only">Memory and CPU per ' + (state.memBy === "kind" ? "service kind and checkout" : "checkout") + "</caption><thead>" + th + "</thead><tbody>" + tb + "</tbody></table></div>";
  }

  // Charts are drawn after the markup is in the page, at the real width.
  function drawCharts() {
    if (state.history === "empty") return;
    var w = window_(), idx = w.idx;
    main.querySelectorAll("[data-chart]").forEach(function (el) {
      var id = el.dataset.chart, isMem = id === "mem";
      var series = isMem ? seriesBy(state.memBy, 1, idx) : seriesBy("checkout", 0, idx);
      drawChart(el, series, idx, isMem, w);
    });
  }
  function drawChart(el, series, idx, stacked, w) {
    var W = Math.max(240, el.clientWidth), Hh = W < 360 ? 150 : 180, L = 44, Rr = 8, T = 8, B = 22;
    var pw = W - L - Rr, ph = Hh - T - B, n = idx.length;
    var tops = idx.map(function () { return 0; }), layers = [];
    series.forEach(function (s) {
      var lo = tops.slice(), hi = s.values.map(function (v, i) { return v == null ? null : (stacked ? tops[i] + v : v); });
      if (stacked) s.values.forEach(function (v, i) { if (v != null) tops[i] += v; });
      layers.push({ s: s, lo: lo, hi: hi });
    });
    var maxV = 0; layers.forEach(function (l) { l.hi.forEach(function (v) { if (v != null && v > maxV) maxV = v; }); });
    var ymax = stacked ? nice(maxV, 1024) : nice(maxV, 100);
    var X = function (i) { return L + (n === 1 ? pw : (H.times[idx[i]] - w.start) / (0 - w.start) * pw); };
    var Y = function (v) { return T + ph - v / ymax * ph; };
    var svg = '<svg width="' + W + '" height="' + Hh + '" aria-hidden="true" focusable="false">';
    var step = stacked ? 1024 : 100;
    for (var v = 0; v <= ymax; v += step) {
      svg += '<line class="pp-grid" x1="' + L + '" x2="' + (L + pw) + '" y1="' + Y(v).toFixed(1) + '" y2="' + Y(v).toFixed(1) + '"/>' +
        '<text class="pp-axis" x="' + (L - 6) + '" y="' + (Y(v) + 4).toFixed(1) + '" text-anchor="end">' + (stacked ? v / 1024 + " GB" : v + " %") + "</text>";
    }
    var span = -w.start, ticks = [w.start, w.start / 2, 0];
    ticks.forEach(function (t, k) {
      var x = L + (t - w.start) / span * pw;
      svg += '<text class="pp-axis" x="' + x.toFixed(1) + '" y="' + (Hh - 6) + '" text-anchor="' + (k === 0 ? "start" : k === 2 ? "end" : "middle") + '">' + (t === 0 ? "now" : ago(t)) + "</text>";
    });
    layers.forEach(function (l) {
      // split into runs of samples; a stopped period is a gap
      var runs = [], cur = null;
      l.hi.forEach(function (v, i) { if (v == null) { cur = null; return; } if (!cur) { cur = []; runs.push(cur); } cur.push(i); });
      runs.forEach(function (r) {
        var top = r.map(function (i) { return X(i).toFixed(1) + " " + Y(l.hi[i]).toFixed(1); });
        if (stacked) {
          var bot = r.slice().reverse().map(function (i) { return X(i).toFixed(1) + " " + Y(l.lo[i]).toFixed(1); });
          svg += '<path class="pp-area pp-s' + l.s.slot + '" d="M' + top.join("L") + "L" + bot.join("L") + 'Z"/>';
        }
        svg += '<path class="pp-line pp-s' + l.s.slot + '" d="M' + top.join("L") + '"/>';
      });
    });
    svg += '<line class="pp-cross" x1="0" x2="0" y1="' + T + '" y2="' + (T + ph) + '" hidden/></svg>';
    el.innerHTML = svg + '<div class="pp-tip" hidden></div>';
    el.setAttribute("aria-label", (stacked ? "Memory" : "CPU") + " over the last " + { "15m": "15 minutes", "1h": "hour", "6h": "6 hours" }[state.range] + ". Use the left and right arrow keys to read values; the table below has every value.");
    var cross = el.querySelector(".pp-cross"), tip = el.querySelector(".pp-tip"), live = document.getElementById("cs-" + el.dataset.chart);
    function show(i, announce) {
      i = Math.max(0, Math.min(n - 1, i)); el.dataset.i = i;
      var x = X(i); cross.setAttribute("x1", x); cross.setAttribute("x2", x); cross.removeAttribute("hidden");
      tip.textContent = "";
      var hd = document.createElement("p"); hd.className = "pp-tip-time"; hd.textContent = ago(H.times[idx[i]]); tip.appendChild(hd);
      var total = 0, parts = [];
      series.forEach(function (s) {
        var v = s.values[i]; if (v != null) total += v;
        var row = document.createElement("p"); row.className = "pp-tip-row";
        var key = document.createElement("span"); key.className = "pp-key pp-key-line pp-s" + s.slot;
        var val = document.createElement("strong"); val.textContent = stacked ? fmtMem(v) : fmtCpu(v);
        var nm = document.createElement("span"); nm.textContent = s.name;
        row.appendChild(key); row.appendChild(val); row.appendChild(nm); tip.appendChild(row);
        parts.push(s.name + " " + val.textContent);
      });
      if (stacked) { var tr = document.createElement("p"); tr.className = "pp-tip-row pp-tip-total"; var tv = document.createElement("strong"); tv.textContent = mb(total); var tn = document.createElement("span"); tn.textContent = "total"; tr.appendChild(tv); tr.appendChild(tn); tip.appendChild(tr); }
      tip.hidden = false;
      var left = x + 12; if (left + 200 > W) left = x - 212; tip.style.left = Math.max(0, left) + "px";
      if (announce && live) live.textContent = hd.textContent + ": " + parts.join(", ");
    }
    function hide() { cross.setAttribute("hidden", ""); tip.hidden = true; }
    el.onpointermove = function (e) { var r = el.getBoundingClientRect(), x = e.clientX - r.left; var best = 0, bd = Infinity; for (var i = 0; i < n; i++) { var d = Math.abs(X(i) - x); if (d < bd) { bd = d; best = i; } } show(best); };
    el.onpointerleave = function () { if (document.activeElement !== el) hide(); };
    el.onfocus = function () { show(el.dataset.i ? +el.dataset.i : n - 1, true); };
    el.onblur = hide;
    el.onkeydown = function (e) {
      var i = el.dataset.i ? +el.dataset.i : n - 1, jump = Math.max(1, Math.round(n / 12));
      var k = { ArrowLeft: i - 1, ArrowRight: i + 1, PageUp: i - jump, PageDown: i + jump, Home: 0, End: n - 1 }[e.key];
      if (e.key === "Escape") { hide(); return; }
      if (k == null) return; e.preventDefault(); show(k, true);
    };
  }

  // ---------- configuration ----------
  function config() {
    var secrets = '<div class="secret-list" role="table" aria-label="Secrets of acme-web"><div class="secret-head micro" role="row"><span role="columnheader">Name</span><span role="columnheader">Reference</span><span role="columnheader">Status</span><span role="columnheader"><span class="sr-only">Actions</span></span></div>' +
      D.secrets.map(function (s) {
        return '<div class="secret-row" role="row"><span class="secret-name" role="cell"><span class="mono secret-id">' + esc(s.name) + '</span><span class="small muted secret-hint">' + esc(s.hint) + '</span></span><span class="secret-ref" role="cell"><code class="mono">' + esc(s.ref) +
          '</code></span><span class="secret-state" role="cell">' + chip(s.chip[0], s.chip[1], s.chip[2]) + '</span><span class="secret-actions" role="cell"><button type="button" class="btn btn-ghost btn-icon" aria-label="More actions for ' + esc(s.name) + '"><span aria-hidden="true">&#8943;</span></button></span></div>';
      }).join("") + "</div>";
    var env = '<div class="pp-env lane-' + D.project.lane + '">' + D.env.map(function (e) {
      var c = checkout(e.checkout);
      return '<div class="pp-env-row"><code class="mono">' + esc(e.key) + '</code><code class="mono">' + esc(e.value) + '</code><span class="pp-env-where small muted">' + esc(e.service) + ' on <span class="branch-pill">' + esc(c.branch) + '</span></span><button type="button" class="btn btn-ghost">Reset</button></div>';
    }).join("") + "</div>";
    var body = '<div class="pp-config">' +
      '<div><h3>Entry</h3><p>Where Open App goes.</p></div><div class="entry-line"><span class="micro">App</span><span aria-hidden="true">&rarr;</span><code class="mono">nginx:80/</code>' + chip("ok", "●", "recorded") + '<span class="small muted">hybrid: <code class="mono">app:5173</code> on this machine</span></div>' +
      '<div><h3>Secrets</h3><p>Names and status only. A value never reaches this page.</p></div><div>' + secrets + '<p class="pp-actions-line"><button type="button" class="btn">Add secret</button> <button type="button" class="btn btn-ghost">Check</button></p></div>' +
      '<div><h3>Env overrides</h3><p>Replace the project value until you reset them.</p></div>' + env +
      '<div><h3>Branch cleanup</h3><p>Used while Live is on.</p></div><div><label class="plan-mode"><span class="small muted">When a PR merges</span> <select aria-label="Branch cleanup for acme-web"><option>off</option><option selected>suggest</option><option>auto</option></select></label></div>' +
      "</div>";
    return section("config", "Configuration", "Entry, secrets, env overrides", "", body);
  }

  // ---------- views ----------
  function setCrumbs(name) {
    crumbs.innerHTML = name ? '<a href="#repos">Repos</a><span class="pp-crumb-sep" aria-hidden="true">/</span><span aria-current="page">' + esc(name) + "</span>" : '<span aria-current="page">Repos</span>';
    document.title = (name ? name + " · " : "Repos · ") + "stackfork";
  }
  function viewRepos() {
    setCrumbs(null);
    main.innerHTML = '<h1 class="sr-only">Repos</h1><div class="repo-table pp-repos"><div class="repo-head micro" aria-hidden="true"><span>Repo</span><span>Status</span><span class="num">Worktrees</span><span class="num">Branches</span><span class="num">Last activity</span><span></span></div>' +
      D.repos.map(function (r) {
        return '<div class="repo"><div class="repo-row"><span class="repo-toggle"><span class="repo-id"><a class="repo-name" href="#project/' + r.name + '">' + r.name + '</a><span class="repo-folder">' + r.folder + "</span></span></span>" +
          '<span class="repo-status">' + chip(r.chip[0], r.chip[1], r.chip[2]) + '</span><span class="repo-counts"><span class="repo-num"><span class="num">' + r.wt + '</span><span class="unit"> worktrees</span></span><span class="sep" aria-hidden="true">·</span><span class="repo-num"><span class="num">' + r.br +
          '</span><span class="unit"> branches</span></span><span class="sep" aria-hidden="true">·</span><span class="repo-last">' + r.last + '</span></span><span class="repo-actions"><a class="btn" href="#project/' + r.name + '">Open</a></span></div></div>';
      }).join("") + "</div>" +
      '<p class="pp-states">Prototype states: <a href="#project/acme-web">acme-web (full)</a> · <a href="#project/notes-cli">not set up</a> · <a href="#project/billing-api">loading</a> · <a href="#project/old-site">not found</a></p>';
  }
  function viewProject(name) {
    setCrumbs(name);
    if (name === "acme-web") {
      main.innerHTML = '<div class="pp-page">' + header() + checkouts() + workflow() + resources() + config() + "</div>";
      return;
    }
    if (name === "billing-api") {
      main.innerHTML = '<div class="pp-page" aria-busy="true"><p class="muted" role="status">Loading billing-api…</p><div class="pp-skel"></div><div class="pp-skel pp-skel-lg"></div><div class="pp-skel pp-skel-lg"></div></div>';
      return;
    }
    if (name === "notes-cli") {
      main.innerHTML = '<div class="pp-page"><header class="pp-head lane-5"><span class="avatar" aria-hidden="true">NC</span><div class="pp-head-id"><div class="pp-title-line"><h1 class="pp-title">notes-cli</h1>' + chip("off", "○", "Not set up") +
        '</div><p class="pp-meta"><span class="mono">~/side/notes-cli</span><span class="dot" aria-hidden="true">·</span><span>default <code class="mono">main</code></span></p></div></header>' +
        '<div class="callout"><span class="callout-glyph" aria-hidden="true">&#9675;</span><div class="callout-body"><p class="callout-title">stackfork has not verified how this project starts. Set it up once with your agent.</p><p class="callout-detail">The workflow, resources and secrets appear here after setup.</p></div><div class="callout-actions"><button type="button" class="btn btn-primary">Set up with your agent</button></div></div></div>';
      return;
    }
    main.innerHTML = '<div class="pp-empty"><h2>No repository named ' + esc(name) + '</h2><p>It may have moved or been removed from the scanned folders.</p><p><a class="btn" href="#repos">Back to Repos</a> <button type="button" class="btn btn-ghost">Rescan</button></p></div>';
  }
  function route() {
    var h = decodeURIComponent(location.hash.replace(/^#/, ""));
    var m = /^project\/([^?]+)(?:\?(.*))?$/.exec(h);
    if (m) {
      var q = new URLSearchParams(m[2] || "");
      if (q.get("mode") && D.project.modes.indexOf(q.get("mode")) >= 0) state.mode = q.get("mode");
      if (q.has("checkout")) state.sel = checkout(q.get("checkout")) ? q.get("checkout") : null;
      if (RANGES[q.get("range")]) state.range = q.get("range");
      if (q.get("by") === "kind" || q.get("by") === "checkout") state.memBy = q.get("by");
      if (q.get("history") === "short" || q.get("history") === "empty") state.history = q.get("history");
      if (q.has("step")) state.node = stepById(q.get("step")) ? q.get("step") : null;
      viewProject(m[1]);
      drawCharts();
      centerBranch();
    } else viewRepos();
  }

  // ---------- interactions ----------
  // The mode and the selected checkout live in the URL, so a link reopens
  // the same view (for example a failed run to share or to come back to).
  function syncUrl() {
    var h = "#project/acme-web?mode=" + state.mode + (state.sel ? "&checkout=" + state.sel : "") + "&range=" + state.range + (state.memBy === "kind" ? "&by=kind" : "") + (state.history !== "full" ? "&history=" + state.history : "") + (state.node ? "&step=" + state.node : "");
    if (location.hash !== h) history.replaceState(null, "", h);
  }
  function rerender(flashFrom) {
    syncUrl();
    var y = window.scrollY, focusKey = document.activeElement && document.activeElement.getAttribute("data-focus-key");
    route();
    window.scrollTo(0, y);
    if (flashFrom) {
      main.querySelectorAll(".pp-mode-branch").forEach(function (r) {
        if (flashFrom[r.dataset.mode] !== r.dataset.on) r.dataset.flash = "1";
      });
    }
    if (focusKey) { var el = main.querySelector('[data-focus-key="' + focusKey + '"]'); if (el) el.focus(); }
  }
  function activeMap() {
    var m = {};
    main.querySelectorAll(".pp-mode-branch").forEach(function (r) { m[r.dataset.mode] = r.dataset.on; });
    return m;
  }
  function setMode(mode, focusSel) {
    if (mode === state.mode) return;
    var before = activeMap();
    state.mode = mode; if (state.node && !inMode(stepById(state.node), mode)) state.node = null;
    rerender(before);
    if (focusSel) { var el = main.querySelector(focusSel); if (el) el.focus(); }
  }

  main.addEventListener("change", function (e) {
    if (e.target.name === "wf-mode") setMode(e.target.value, 'input[name="wf-mode"][value="' + e.target.value + '"]');
    if (e.target.name === "res-range" || e.target.name === "mem-by") {
      var sel = 'input[name="' + e.target.name + '"][value="' + e.target.value + '"]';
      if (e.target.name === "res-range") state.range = e.target.value; else state.memBy = e.target.value;
      rerender(); var f = main.querySelector(sel); if (f) f.focus();
    }
  });
  main.addEventListener("click", function (e) {
    var t = e.target.closest("button, a, .pp-co-row");
    if (!t) return;
    if (t.matches("[data-collapse]")) {
      var id = t.getAttribute("data-collapse");
      state.collapsed[id] = !state.collapsed[id];
      var sec = document.getElementById("sec-" + id);
      sec.dataset.collapsed = state.collapsed[id] ? "1" : "0";
      t.setAttribute("aria-expanded", state.collapsed[id] ? "false" : "true");
      return;
    }
    if (t.matches("[data-select], .pp-co-row")) {
      if (t.matches(".pp-co-row") && e.target.closest(".pp-co-actions, .btn")) return;
      var key = t.getAttribute("data-select") || t.getAttribute("data-co");
      var c = checkout(key), before = activeMap();
      state.sel = key; state.node = c.run.outcome === "failed" ? c.run.failedAt : null;
      if (c.run.mode !== state.mode) { state.mode = c.run.mode; }
      rerender(before);
      var b = main.querySelector('[data-select="' + key + '"]'); if (b) b.focus();
      return;
    }
    if (t.matches("[data-mode-to]")) { setMode(t.getAttribute("data-mode-to"), 'input[name="wf-mode"]:checked'); return; }
    if (t.matches("[data-clear]")) { state.sel = null; rerender(); var s = main.querySelector('input[name="wf-mode"]:checked'); if (s) s.focus(); return; }
    if (t.matches("[data-make-default]")) { D.project.defaultMode = state.mode; rerender(); var d = main.querySelector('input[name="wf-mode"]:checked'); if (d) d.focus(); return; }
    if (t.matches("[data-node]")) { if (t.tabIndex !== -1) openPanel(t.getAttribute("data-node")); return; }
    if (t.matches("[data-close-panel]")) { closePanel(); return; }
    if (t.matches("[data-palette]")) { state.palette = !state.palette; var pal = document.getElementById("pp-palette"); pal.hidden = !state.palette; pal.parentNode.dataset.palette = state.palette ? 1 : 0; t.setAttribute("aria-expanded", String(state.palette)); return; }
    if (t.matches("[data-live]")) { t.setAttribute("aria-checked", t.getAttribute("aria-checked") === "true" ? "false" : "true"); return; }
    if (t.matches("[data-menu]")) {
      var menu = t.nextElementSibling, open = menu.hidden;
      menu.hidden = !open; t.setAttribute("aria-expanded", String(open));
      if (open) { var first = menu.querySelector(".menu-item"); if (first) first.focus(); }
      return;
    }
    if (t.matches(".menu-item")) { var mm = t.closest(".menu"); mm.hidden = true; mm.previousElementSibling.setAttribute("aria-expanded", "false"); mm.previousElementSibling.focus(); }
  });
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    var open = main.querySelector(".menu:not([hidden])");
    if (!open && state.node) { closePanel(); return; }
    if (open) { open.hidden = true; open.previousElementSibling.setAttribute("aria-expanded", "false"); open.previousElementSibling.focus(); }
  });
  var rz; window.addEventListener("resize", function () { clearTimeout(rz); rz = setTimeout(drawCharts, 120); });
  window.addEventListener("hashchange", function () { route(); main.focus({ preventScroll: true }); window.scrollTo(0, 0); });

  // Keep the viewed branch in sight when the canvas is wider than its box.
  function centerBranch() {
    var cv = main.querySelector(".pp-canvas"), b = main.querySelector(".pp-mode-branch[data-on='1']");
    if (!cv || !b || cv.scrollWidth <= cv.clientWidth) return;
    var cr = cv.getBoundingClientRect(), br = b.getBoundingClientRect();
    cv.scrollLeft += (br.left + br.width / 2) - (cr.left + cr.width / 2);
  }
  // Side panel: opens on click or focus of a node, without re-rendering the
  // page, so focus stays where it is.
  function openPanel(id) {
    if (state.node === id && !document.getElementById("pp-panel").hidden) return;
    state.node = id;
    var panel = document.getElementById("pp-panel");
    panel.innerHTML = panelHTML(id, viewRun(state.mode)); panel.hidden = false; panel.parentNode.dataset.panel = "1"; centerBranch();
    main.querySelectorAll(".pp-node").forEach(function (n) { n.setAttribute("aria-pressed", String(n.dataset.node === id && n.tabIndex !== -1)); });
    syncUrl();
  }
  function closePanel() {
    var id = state.node; state.node = null;
    var panel = document.getElementById("pp-panel"); panel.hidden = true; panel.parentNode.dataset.panel = "0";
    main.querySelectorAll(".pp-node").forEach(function (n) { n.setAttribute("aria-pressed", "false"); });
    syncUrl();
    var n = main.querySelector('.pp-node[data-node="' + id + '"]:not([tabindex="-1"])'); if (n) n.focus({ preventScroll: true });
  }
  main.addEventListener("focusin", function (e) { var n = e.target.closest && e.target.closest(".pp-node"); if (n && n.tabIndex !== -1) openPanel(n.dataset.node); });
  main.addEventListener("keydown", function (e) {
    var n = e.target.closest && e.target.closest(".pp-node"); if (!n) return;
    var nodes = Array.prototype.slice.call(main.querySelectorAll('.pp-node:not([tabindex="-1"])')), i = nodes.indexOf(n);
    var k = { ArrowDown: i + 1, ArrowRight: i + 1, ArrowUp: i - 1, ArrowLeft: i - 1, Home: 0, End: nodes.length - 1 }[e.key];
    if (k == null) return; e.preventDefault(); var to = nodes[Math.max(0, Math.min(nodes.length - 1, k))]; to.focus(); to.scrollIntoView({ block: "nearest", inline: "nearest" });
  });

  // Theme: system -> dark -> light -> system, as the dashboard does.
  var themeBtn = document.getElementById("theme-toggle");
  function themeLabel() {
    var t = document.documentElement.dataset.theme || "system";
    var next = t === "system" ? "dark" : t === "dark" ? "light" : "system";
    themeBtn.textContent = "Theme: " + t;
    themeBtn.setAttribute("aria-label", "Theme: " + t + ". Switch to " + next);
    return next;
  }
  themeBtn.addEventListener("click", function () {
    var next = themeLabel();
    if (next === "system") delete document.documentElement.dataset.theme; else document.documentElement.dataset.theme = next;
    try { if (next === "system") localStorage.removeItem("stackfork-theme"); else localStorage.setItem("stackfork-theme", next); } catch (err) { /* storage unavailable */ }
    themeLabel();
  });
  themeLabel();
  route();
})();
