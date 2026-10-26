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

  var state = { mode: "hybrid", sel: "invoices", collapsed: {}, log: false };

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

  // ---------- workflow ----------
  function node(kind, x) {
    var o = '<svg x="' + x + '" y="50%" overflow="visible">';
    if (kind === "ok") o += '<circle r="6" class="pp-node-ok"/>';
    else if (kind === "attention") o += '<circle r="8" class="pp-node-halo"/><polygon points="0,-6.5 6.5,5 -6.5,5" class="pp-node-attention"/>';
    else if (kind === "failed") o += '<circle r="6.5" class="pp-node-failed"/><path d="M-2.5,-2.5L2.5,2.5M2.5,-2.5L-2.5,2.5" class="pp-node-failed-x"/>';
    else if (kind === "busy") o += '<circle r="5.5" class="pp-node-busy"/>';
    else if (kind === "ghost") o += '<circle r="5" class="pp-node-ghost"/>';
    else o += '<circle r="5" class="pp-node-off"/>';
    return o + "</svg>";
  }
  function graph(opts) {
    // opts: lane, active, inLane (previous active lane), out (has next active), through (lane of a pass-through), reachedIn, reachedOut, ghost
    var g = '<span class="pp-graph" aria-hidden="true"><svg focusable="false">';
    LANE_X.forEach(function (x) { g += '<line class="pp-guide" x1="' + x + '" x2="' + x + '" y1="0" y2="100%"/>'; });
    if (opts.through != null) {
      g += '<line class="pp-edge" data-reached="' + (opts.reachedOut ? 1 : 0) + '" x1="' + LANE_X[opts.through] + '" x2="' + LANE_X[opts.through] + '" y1="0" y2="100%"/>';
    }
    if (opts.active) {
      if (opts.inLane != null) {
        g += '<line class="pp-edge" data-reached="' + (opts.reachedIn ? 1 : 0) + '" x1="' + LANE_X[opts.inLane] + '" x2="' + LANE_X[opts.inLane] + '" y1="0" y2="50%"/>';
        if (opts.inLane !== opts.lane) g += '<line class="pp-edge" data-reached="' + (opts.reachedIn ? 1 : 0) + '" x1="' + LANE_X[opts.inLane] + '" x2="' + LANE_X[opts.lane] + '" y1="50%" y2="50%"/>';
      }
      if (opts.out) g += '<line class="pp-edge" data-reached="' + (opts.reachedOut ? 1 : 0) + '" x1="' + LANE_X[opts.lane] + '" x2="' + LANE_X[opts.lane] + '" y1="50%" y2="100%"/>';
    }
    if (opts.node) g += node(opts.node, LANE_X[opts.lane]);
    return g + "</svg></span>";
  }
  function reached(run, id) {
    if (!run) return true;
    var s = run.steps[id];
    return !!s && s[0] !== "pending";
  }
  function workflow() {
    var mode = state.mode, steps = D.steps;
    var sel = state.sel ? checkout(state.sel) : null;
    var run = sel && sel.run.mode === mode ? sel.run : null;
    var activeIdx = [];
    steps.forEach(function (s, i) { if (inMode(s, mode)) activeIdx.push(i); });
    var other = mode === "container" ? "hybrid" : "container";

    var rows = steps.map(function (s, i) {
      var active = inMode(s, mode), lane = LANES.indexOf(s.lane);
      var prev = null, next = null;
      activeIdx.forEach(function (j) { if (j < i) prev = j; if (j > i && next === null) next = j; });
      var prevLane = prev === null ? null : LANES.indexOf(steps[prev].lane);
      var rs = run && active ? run.steps[s.id] : null;
      var nodeKind;
      if (!active) nodeKind = "ghost";
      else if (run) nodeKind = !rs ? "off" : { succeeded: "ok", failed: "failed", running: "busy", skipped: "off", pending: "off" }[rs[0]];
      else nodeKind = { verified: "ok", trusted: "ok", draft: "attention", untrusted: "attention", start: "off" }[s.plan];
      var g = graph({
        lane: lane, active: active, node: nodeKind,
        inLane: active ? prevLane : null, out: active && next !== null,
        through: !active && prev !== null && next !== null ? prevLane : null,
        reachedIn: reached(run, s.id), reachedOut: next !== null && reached(run, steps[next].id)
      });
      var where = s.lane === "sf" ? "stackfork" : s.lane === "docker" ? "in Docker" : "on this machine";
      var plan = active ? PLAN_CHIP[s.plan] : ["off", "○", s.modes[0] + " only", "Runs only in " + s.modes[0] + " mode"];
      var note = active && s.note ? '<span class="pp-cmd-note">' + esc(s.note) + "</span>" : "";
      if (!active) note = '<span class="pp-cmd-note">Skipped in ' + mode + " mode</span>";
      var runCell = "";
      if (run && active) {
        var c = RUN_CHIP[rs ? rs[0] : "pending"];
        runCell = chip(c[0], c[1], c[2]) + (rs && rs[1] ? '<span class="pp-run-dur">' + esc(rs[1]) + "</span>" : "") +
          (rs && rs[2] ? '<span class="pp-run-reason">' + esc(rs[2]) + "</span>" : "") +
          (rs && rs[0] === "failed" ? '<button type="button" class="btn btn-ghost" data-log aria-expanded="' + state.log + '" aria-controls="log-' + s.id + '">' + (state.log ? "Hide log" : "Show log") + "</button>" : "");
      } else if (run) {
        runCell = '<span class="pp-run-reason">not in this mode</span>';
      }
      var row = '<li class="pp-wf-row" data-step="' + s.id + '" data-active="' + (active ? 1 : 0) + '">' + g +
        '<span class="pp-step"><span class="pp-step-id">' + esc(s.id) + '</span><span class="pp-step-kind">' + esc(s.kind) + '</span><span class="pp-where">' + where + "</span>" +
        '<span class="sr-only">, runs ' + where + (active ? "" : ", skipped in " + mode + " mode") + "</span></span>" +
        '<span class="pp-cmd"><code class="mono">' + esc(s.cmd) + "</code>" + note + "</span>" +
        '<span class="pp-plan">' + chip(plan[0], plan[1], plan[2], plan[3]) + "</span>" +
        '<span class="pp-run">' + runCell + "</span></li>";
      if (run && rs && rs[0] === "failed" && run.log) {
        row += '<li class="pp-log-row" id="log-' + s.id + '"' + (state.log ? "" : " hidden") + ">" +
          graph({ lane: lane, active: false, through: next !== null ? lane : null, reachedOut: false }) +
          '<div class="callout callout-failed pp-log"><span class="callout-glyph" aria-hidden="true">&#8856;</span><div class="callout-body"><p class="callout-title">' +
          esc(s.id) + ' failed with exit 1 in run <span class="mono">#' + run.id + '</span></p><pre class="mono-log" tabindex="0" aria-label="Output of step ' + esc(s.id) + '">' + esc(run.log) +
          '</pre></div><div class="callout-actions"><button type="button" class="btn" title="' + esc(run.retry) + '">Retry</button></div></div></li>';
      }
      return row;
    }).join("");

    var counts = { sf: 0, docker: 0, host: 0 };
    activeIdx.forEach(function (i) { counts[steps[i].lane]++; });
    var summary = mode === "container"
      ? "<strong>Container mode:</strong> every service runs in Docker. " + plural(activeIdx.length, "step") + ": " + counts.sf + " by stackfork, " + counts.docker + " in Docker, none on this machine."
      : "<strong>Hybrid mode:</strong> db and mailpit run in Docker; api and app run on this machine. " + plural(activeIdx.length, "step") + ": " + counts.sf + " by stackfork, " + counts.docker + " in Docker, " + counts.host + " on this machine. " + esc(D.project.trust) + ".";

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
    var overlayNote = sel && !run ? '<p class="pp-wf-summary">Run #' + sel.run.id + " of " + esc(sel.branch) + " used " + sel.run.mode + " mode, so it is not drawn on the " + mode + " workflow.</p>" : "";

    var legend = '<p class="pp-legend" aria-hidden="true">' +
      leg("ok", run ? "Succeeded" : "Verified or trusted") + leg("attention", "Draft") + (run ? leg("failed", "Failed") + leg("busy", "Running") + leg("off", "Skipped or pending") : leg("off", "At start")) +
      leg("ghost", "Not in " + mode + " mode") + "</p>";

    var head = '<div class="pp-wf-head micro" aria-hidden="true"><span class="pp-lane-labels"><span>stackfork</span><span class="pp-lane-docker" data-active="' + (counts.docker ? 1 : 0) + '">Docker</span><span class="pp-lane-host" data-active="' + (counts.host ? 1 : 0) + '">Host</span></span><span>Step</span><span>Command</span><span>Plan</span><span class="pp-run-col">' + (run ? "Run #" + run.id : "") + "</span></div>";

    var draftCallout = mode === "hybrid" ? '<div class="callout callout-attention"><span class="callout-glyph" aria-hidden="true">&#9650;</span><div class="callout-body"><p class="callout-title">seed-host is a draft</p><p class="callout-detail">A Start in hybrid mode skips it, so a new hybrid environment has an empty database. Verify it once with your agent to record it.</p></div><div class="callout-actions"><button type="button" class="btn">Verify with your agent</button></div></div>' : "";

    return section("workflow", "Startup workflow", "Entry: <code class=\"mono\">App → " + esc(D.project.entry[mode]) + "</code>",
      seg + makeDefault + overlay,
      '<p class="pp-wf-summary">' + summary + "</p>" + overlayNote +
      '<div class="pp-wf" data-overlay="' + (run ? 1 : 0) + '">' + head +
      '<ol class="pp-wf-list" role="list" aria-label="Startup steps in ' + mode + ' mode">' + rows + "</ol></div>" + legend + draftCallout);
  }
  function leg(kind, label) {
    return '<span><svg width="16" height="16" viewBox="-8 -8 16 16" focusable="false">' + node(kind, 0).replace('y="50%"', 'y="0"') + "</svg>" + esc(label) + "</span>";
  }

  // ---------- resources ----------
  function meter(text, ratio, tone, label, title) {
    return '<span class="pp-meter" data-label="' + label + '"' + (title ? ' title="' + esc(title) + '"' : "") + '><span class="mono' + (tone ? " pp-tone-" + tone : "") + '">' + text + "</span>" +
      (ratio == null ? '<span class="pp-bar pp-bar-empty"></span>' : '<svg class="pp-bar" width="56" height="4" viewBox="0 0 56 4" aria-hidden="true" focusable="false"' + (tone ? ' data-tone="' + tone + '"' : "") + '><rect class="pp-bar-track" width="56" height="4" rx="2"/><rect class="pp-bar-fill" width="' + Math.max(2, Math.min(56, Math.round(ratio * 56))) + '" height="4" rx="2"/></svg>') + "</span>";
  }
  function memTone(r) { return r >= 0.95 ? "failed" : r >= 0.85 ? "attention" : ""; }
  function resources() {
    var tCpu = 0, tMem = 0, tDisk = 0, nC = 0, nH = 0;
    var rows = D.resources.map(function (g) {
      var c = checkout(g.checkout), cpu = 0, mem = 0, disk = 0;
      var svc = g.services.map(function (s) {
        cpu += s.cpu || 0; mem += s.mem || 0; disk += s.disk || 0;
        if (!g.stopped) { if (s.where === "host") nH++; else nC++; }
        var r = s.limit ? s.mem / s.limit : null, tone = r ? memTone(r) : "";
        var where = s.where === "host"
          ? '<span>Host app</span><span class="mono">pid ' + s.pid + " · up " + s.up + "</span>"
          : "<span>Docker</span><span class=\"mono\">" + esc(s.image) + "</span>";
        var memText = s.mem == null ? "—" : mb(s.mem) + (s.limit ? " / " + mb(s.limit) : "");
        return '<div class="pp-res-row" role="row"><span class="pp-res-name" role="cell"><span class="service-logo" data-kind="' + s.kind + '" aria-hidden="true"></span><b>' + esc(s.name) + "</b>" +
          (s.note ? '<span class="small muted">' + esc(s.note) + "</span>" : "") + "</span>" +
          '<span class="pp-res-where" role="cell">' + where + "</span>" +
          '<span role="cell">' + meter(pct(s.cpu), s.cpu == null ? null : s.cpu / 100, "", "CPU", s.note ? "High CPU while building is expected" : "") + "</span>" +
          '<span role="cell">' + meter(memText, r, tone, "Mem", r ? Math.round(r * 100) + " % of the container limit" : s.where === "host" ? "Resident memory of the process tree" : "No memory limit set") + "</span>" +
          '<span role="cell">' + meter(s.where === "host" ? '<span class="pp-dim">—</span>' : s.disk ? mb(s.disk) : '<span class="pp-dim">0</span>', null, "", "Disk",
            s.where === "host" ? "Host apps write to the checkout; not measured" : s.vol ? "Volume " + s.vol : "No volume") + "</span></div>";
      }).join("");
      tCpu += cpu; tMem += mem; tDisk += disk;
      var groupWhere = g.stopped ? "<span>Stopped · volumes kept</span>" : "<span>" + c.mode + "</span>";
      return '<div class="pp-res-row pp-res-group lane-' + D.project.lane + '" role="row"><span class="pp-res-name" role="rowheader"><span class="branch-pill">' + esc(c.branch) + "</span>" + stateChip(c.state) + "</span>" +
        '<span class="pp-res-where" role="cell">' + groupWhere + "</span>" +
        '<span role="cell">' + meter(g.stopped ? "—" : pct(cpu), null, "", "CPU") + "</span>" +
        '<span role="cell">' + meter(g.stopped ? "—" : mb(mem), null, "", "Mem") + "</span>" +
        '<span role="cell">' + meter(mb(disk), null, "", "Disk") + "</span></div>" + svc;
    }).join("");
    var total = '<div class="pp-res-row pp-res-total" role="row"><span role="rowheader">All checkouts</span><span class="pp-res-where" role="cell"><span>' + nC + " containers · " + nH + ' host apps</span></span>' +
      '<span role="cell">' + meter(pct(tCpu), null, "", "CPU") + '</span><span role="cell">' + meter(mb(tMem), null, "", "Mem") + '</span><span role="cell">' + meter(mb(tDisk), null, "", "Disk") + "</span></div>";
    var m = D.project.machine;
    var summary = '<p class="pp-res-summary">acme-web uses <strong>' + (tCpu / 100).toFixed(1) + " cores</strong> of " + m.cores + ", <strong>" + mb(tMem) + "</strong> of " + m.memGB +
      " GB memory and <strong>" + mb(tDisk + D.images.mb) + "</strong> of disk (" + mb(tDisk) + " volumes, " + mb(D.images.mb) + " images).</p>";
    var head = '<div class="pp-res-head micro" aria-hidden="true"><span>Service</span><span>Runs in</span><span class="num">CPU</span><span class="num">Memory</span><span class="num">Disk</span></div>';
    var callout = '<div class="callout callout-attention"><span class="callout-glyph" aria-hidden="true">&#9650;</span><div class="callout-body"><p class="callout-title">app on main uses 85 % of its memory limit</p><p class="callout-detail">1.7 GB of 2 GB. Above the limit, Docker stops the container. Raise <code class="mono">mem_limit</code> in the Compose file or restart app.</p></div></div>';
    return section("resources", "Resources", "Sampled 4 s ago · every 10 s while this page is open",
      '<span class="small muted">CPU: 100 % = one core</span>',
      summary + callout + '<div role="table" aria-label="Resources by checkout and service">' + head + rows + total + "</div>" +
      '<p class="pp-res-foot">Images are shared by every checkout and counted once: ' + esc(D.images.list) + ".</p>");
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
      viewProject(m[1]);
    } else viewRepos();
  }

  // ---------- interactions ----------
  // The mode and the selected checkout live in the URL, so a link reopens
  // the same view (for example a failed run to share or to come back to).
  function syncUrl() {
    var h = "#project/acme-web?mode=" + state.mode + (state.sel ? "&checkout=" + state.sel : "");
    if (location.hash !== h) history.replaceState(null, "", h);
  }
  function rerender(flashFrom) {
    syncUrl();
    var y = window.scrollY, focusKey = document.activeElement && document.activeElement.getAttribute("data-focus-key");
    route();
    window.scrollTo(0, y);
    if (flashFrom) {
      main.querySelectorAll(".pp-wf-row").forEach(function (r) {
        if (flashFrom[r.dataset.step] !== r.dataset.active) r.dataset.flash = "1";
      });
    }
    if (focusKey) { var el = main.querySelector('[data-focus-key="' + focusKey + '"]'); if (el) el.focus(); }
  }
  function activeMap() {
    var m = {};
    main.querySelectorAll(".pp-wf-row").forEach(function (r) { m[r.dataset.step] = r.dataset.active; });
    return m;
  }
  function setMode(mode, focusSel) {
    if (mode === state.mode) return;
    var before = activeMap();
    state.mode = mode; state.log = false;
    rerender(before);
    if (focusSel) { var el = main.querySelector(focusSel); if (el) el.focus(); }
  }

  main.addEventListener("change", function (e) {
    if (e.target.name === "wf-mode") setMode(e.target.value, 'input[name="wf-mode"][value="' + e.target.value + '"]');
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
      state.sel = key; state.log = c.run.outcome === "failed";
      if (c.run.mode !== state.mode) { state.mode = c.run.mode; }
      rerender(before);
      var b = main.querySelector('[data-select="' + key + '"]'); if (b) b.focus();
      return;
    }
    if (t.matches("[data-mode-to]")) { setMode(t.getAttribute("data-mode-to"), 'input[name="wf-mode"]:checked'); return; }
    if (t.matches("[data-clear]")) { state.sel = null; state.log = false; rerender(); var s = main.querySelector('input[name="wf-mode"]:checked'); if (s) s.focus(); return; }
    if (t.matches("[data-make-default]")) { D.project.defaultMode = state.mode; rerender(); var d = main.querySelector('input[name="wf-mode"]:checked'); if (d) d.focus(); return; }
    if (t.matches("[data-log]")) {
      state.log = !state.log;
      var row = document.getElementById(t.getAttribute("aria-controls"));
      if (row) row.hidden = !state.log;
      t.setAttribute("aria-expanded", String(state.log));
      t.textContent = state.log ? "Hide log" : "Show log";
      return;
    }
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
    if (open) { open.hidden = true; open.previousElementSibling.setAttribute("aria-expanded", "false"); open.previousElementSibling.focus(); }
  });
  window.addEventListener("hashchange", function () { route(); main.focus({ preventScroll: true }); window.scrollTo(0, 0); });

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
