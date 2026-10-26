// Project page: shell, checkouts and configuration (docs/design/project-page/api.md, owner C).
// Hash route #project/NAME?mode=&checkout=&range=; app.js calls sfProject.enter() and leave()
// when the view changes and exposes its action helpers as window.sfDash. The Workflow canvas
// (sfProjectWorkflow, agent D) and the Resources charts (sfProjectResources, agent E) render
// into the containers this file owns. Vanilla JS: CSP allows no inline script or style.
(function () {
  "use strict";

  var RANGES = ["15m", "1h", "6h"];
  var st = null; // the open project: see open()
  var timers = [];
  var busy = 0; // actions in flight: their buttons must not be replaced by a repaint

  // ---------- helpers ----------
  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function plural(n, word) { return n + " " + word + (n === 1 ? "" : /(ch|sh|s|x)$/.test(word) ? "es" : "s"); }
  function chip(cls, glyph, label, title) {
    return '<span class="chip chip-' + cls + '"' + (title ? ' title="' + esc(title) + '"' : "") +
      '><span class="chip-glyph" aria-hidden="true">' + glyph + "</span>" + esc(label) + "</span>";
  }
  function ago(iso) {
    var t = Date.parse(iso);
    if (!t) return "";
    var s = Math.max(0, Math.round((Date.now() - t) / 1000));
    if (s < 45) return "just now";
    if (s < 3600) return Math.round(s / 60) + " min ago";
    if (s < 86400) return Math.round(s / 3600) + " h ago";
    return Math.round(s / 86400) + " d ago";
  }
  function dur(ms, clock) {
    var s = Math.round((ms || 0) / 1000);
    if (clock || s >= 3600) return Math.floor(s / 60) + ":" + ("0" + (s % 60)).slice(-2);
    if (s < 60) return (ms < 10000 ? (ms / 1000).toFixed(1) : s) + " s";
    return Math.floor(s / 60) + " min " + (s % 60) + " s";
  }
  function cap(s) { return s ? s.charAt(0).toUpperCase() + s.slice(1) : s; }
  function initials(name) {
    var w = String(name).split(/[-_.\s]+/).filter(Boolean);
    return ((w[0] || "?").charAt(0) + (w.length > 1 ? w[1].charAt(0) : "")).toUpperCase();
  }
  function lane(name) {
    var d = window.sfDash;
    return d && d.stackColorIndex ? d.stackColorIndex(name) + 1 : 1;
  }
  function relPath(root, p) {
    if (p === root) return "main checkout";
    return p.indexOf(root + "/") === 0 ? p.slice(root.length + 1) : p;
  }
  function isWeb(u) { return /^https?:\/\//.test(u || ""); }
  function byKey(list, key) { for (var i = 0; i < (list || []).length; i++) if (list[i].key === key) return list[i]; return null; }

  var STATE_CHIP = {
    running: ["ok", "\u25CF", "Running"], partial: ["attention", "\u25D1", "Partial"], starting: ["busy", "\u25D4", "Starting\u2026"],
    stopped: ["off", "\u25CB", "Stopped"], "no-stack": ["off", "\u25CB", "No stack"]
  };
  function stateChip(s) { var c = STATE_CHIP[s] || STATE_CHIP["no-stack"]; return chip(c[0], c[1], c[2]); }
  function runChip(r) {
    if (r.outcome === "succeeded") return chip("ok", "\u25CF", "Succeeded");
    if (r.outcome === "failed") return chip("failed", "\u2298", "Failed" + (r.failed_step ? " at " + r.failed_step : ""));
    if (r.outcome === "running") return chip("busy", "\u25D4", "Running " + dur(r.duration_ms, true));
    return chip("attention", "\u25B2", cap(r.outcome || "unknown"));
  }
  function secretChip(s) {
    var v = String(s.status || "").toLowerCase();
    if (!s.reference && v !== "shell only") return chip("failed", "\u2298", "Missing", "No reference on this machine; map it with stackfork secret map");
    if (v === "resolves") return chip("ok", "\u25CF", "Resolves");
    if (v === "missing") return chip("failed", "\u2298", "Missing");
    if (v === "locked" || v === "unavailable") return chip("attention", "\u25B2", cap(v));
    if (v === "shell only") return chip("off", "\u25CB", "Shell only", "The dashboard does not see your shell environment");
    if (v === "not checked" || v === "not-checked" || !v) return chip("off", "\u25CB", "Not checked", "Check reads it; 1Password may ask you to unlock it");
    return chip("failed", "\u2298", "Error", v);
  }

  // ---------- state ----------
  // parseHash("#project/acme-web?mode=hybrid&checkout=fix%2Flogin") -> {name, q}
  function parseHash(hash) {
    var h = String(hash || "").replace(/^#/, "");
    var m = /^project\/([^?]+)(?:\?(.*))?$/.exec(h);
    if (!m) return null;
    var name = m[1];
    try { name = decodeURIComponent(name); } catch (e) { /* keep the raw name */ }
    return { name: name, q: new URLSearchParams(m[2] || "") };
  }
  function hashFor(s) {
    var q = new URLSearchParams();
    if (s.mode) q.set("mode", s.mode);
    if (s.checkout !== null) q.set("checkout", s.checkout || "none");
    if (s.step) q.set("step", s.step);
    if (s.range && s.range !== "1h") q.set("range", s.range);
    if (s.path) q.set("path", s.path);
    var qs = q.toString();
    return "#project/" + encodeURIComponent(s.name) + (qs ? "?" + qs : "");
  }
  function syncHash() {
    if (!st) return;
    try { history.replaceState(null, "", hashFor(st)); } catch (e) { /* ignore */ }
  }
  function loadCollapsed() {
    try { return JSON.parse(localStorage.getItem("sf.project.collapsed") || "{}") || {}; } catch (e) { return {}; }
  }
  function saveCollapsed() {
    try { localStorage.setItem("sf.project.collapsed", JSON.stringify(st.collapsed)); } catch (e) { /* private mode */ }
  }
  function open(route) {
    var q = route.q;
    st = {
      name: route.name, path: q.get("path") || "", data: null, status: "loading", error: "",
      mode: q.get("mode") || "", checkout: q.has("checkout") ? (q.get("checkout") === "none" ? "" : q.get("checkout")) : null,
      range: RANGES.indexOf(q.get("range")) >= 0 ? q.get("range") : "1h", step: q.get("step") || "",
      collapsed: loadCollapsed(), branchesOpen: false, res: null, resError: "", resLoaded: false,
      inflight: false, resInflight: false, built: ""
    };
  }

  // The checkout whose last run the workflow shows: the URL's, else the newest finished run.
  function selectedCheckout() {
    var p = st.data;
    if (!p) return null;
    if (st.checkout !== null) return st.checkout ? byKey(p.checkouts, st.checkout) : null;
    var best = null;
    (p.checkouts || []).forEach(function (c) {
      var r = c.last_run;
      if (!r || r.outcome === "running") return;
      if (!best || Date.parse(r.started_at) > Date.parse(best.last_run.started_at)) best = c;
    });
    return best;
  }
  function viewMode() {
    var p = st.data;
    if (st.mode && p.modes.indexOf(st.mode) >= 0) return st.mode;
    var c = selectedCheckout();
    if (c && c.last_run && p.modes.indexOf(c.last_run.mode) >= 0) return c.last_run.mode;
    return p.default_mode;
  }
  function isSetUp(p) { return p.knowledge && p.knowledge.state !== "none"; }

  // ---------- sections ----------
  function section(id, title, sub, tools, body) {
    var collapsed = st.collapsed[id] ? "1" : "0";
    return '<section class="pp-section" id="sec-' + id + '" data-collapsed="' + collapsed + '" aria-labelledby="h-' + id + '">' +
      '<div class="pp-section-head">' +
      '<button type="button" class="btn btn-ghost btn-icon chevron" data-collapse="' + id + '" aria-expanded="' + (collapsed === "1" ? "false" : "true") +
      '" aria-controls="body-' + id + '" aria-label="Show or hide ' + esc(title) + '"><span class="chevron-glyph" aria-hidden="true"></span></button>' +
      '<h2 id="h-' + id + '">' + esc(title) + "</h2>" +
      '<span class="pp-section-sub" data-part="' + id + '-sub">' + (sub || "") + "</span>" +
      '<div class="pp-section-tools" data-part="' + id + '-tools">' + (tools || "") + "</div></div>" +
      '<div class="pp-section-body" id="body-' + id + '">' + body + "</div></section>";
  }

  // trigger: {cls, html, title}; the default is the ellipsis button.
  function menu(key, label, items, trigger) {
    var t = trigger || { cls: "btn btn-ghost btn-icon", html: '<span aria-hidden="true">&#8943;</span>', title: "More actions" };
    return '<div class="menu-anchor"><button type="button" class="' + t.cls + '" data-key="' + esc(key) + '" aria-haspopup="menu" aria-expanded="false" aria-label="' + esc(label) +
      '" title="' + esc(t.title) + '">' + t.html + '</button><div class="menu" role="menu" hidden>' + items + "</div></div>";
  }
  function menuItem(key, label, cmd, attrs, cls) {
    return '<button type="button" role="menuitem" class="menu-item' + (cls ? " " + cls : "") + '" tabindex="-1" data-key="' + esc(key) + '"' + (attrs || "") + '><span class="menu-label">' + esc(label) + "</span>" +
      (cmd ? '<span class="menu-cmd mono">' + esc(cmd) + "</span>" : "") + "</button>";
  }

  // The checkout's Start / Retry button, shared by the row and the Start... menu.
  function startAttrs(p, c) { return ' data-repo-action="start" data-repo="' + esc(p.path) + '" data-path="' + esc(c.path) + '"'; }

  function header(p) {
    var run = 0, starting = 0;
    (p.checkouts || []).forEach(function (c) { if (c.state === "running" || c.state === "partial") run++; else if (c.state === "starting") starting++; });
    var draft = 0, g = p.workflow && p.workflow[p.default_mode];
    if (g) (g.nodes || []).forEach(function (n) { if (n.kind === "step" && n.plan_state === "draft" && n.in_mode) draft++; });
    var chips = "";
    if (!isSetUp(p)) chips += chip("off", "\u25CB", "Not set up");
    else {
      if (run) chips += chip("ok", "\u25CF", run + " running");
      if (starting) chips += chip("busy", "\u25D4", starting + " starting");
      if (draft) chips += chip("attention", "\u25B2", plural(draft, "draft step"), "Learn proposed a step that is not recorded yet");
      if (p.knowledge.state === "draft") chips += chip("attention", "\u25B2", "Setup draft", "Setup knowledge is not verified yet");
      if (p.knowledge.state === "borrowed") chips += chip("off", "\u25CB", "Setup from " + (p.knowledge.from || "another worktree"));
    }
    var lv = p.live || { state: "off" }, on = lv.state !== "off";
    var live = '<button type="button" role="switch" class="switch" aria-checked="' + on + '" data-live-op="' + (on ? "off" : "on") + '" data-repo="' + esc(p.path) + '" data-key="' + esc(p.path) + '/live" aria-label="Live mode for ' + esc(p.name) +
      '"><span class="switch-track" aria-hidden="true"><span class="switch-thumb"></span></span><span class="switch-label">Live</span></button>';
    if (lv.state === "paused") {
      live += chip("attention", "\u25B2", "Paused", lv.reason) + '<button type="button" class="btn" data-live-op="resume" data-repo="' + esc(p.path) + '" data-key="' + esc(p.path) + '/live/resume">Resume</button>';
    }
    var startItems = "";
    if (isSetUp(p)) {
      (p.checkouts || []).forEach(function (c) {
        if (c.state === "stopped" || c.state === "no-stack") startItems += menuItem(p.path + "/start/" + c.key, c.branch, "", startAttrs(p, c));
      });
      (p.branches_not_checked_out || []).forEach(function (b) {
        startItems += menuItem(p.path + "/startbr/" + b.branch, b.branch, "new worktree", ' data-repo-action="start" data-repo="' + esc(p.path) + '" data-branch="' + esc(b.branch) +
          '" data-confirm="' + esc("Create a worktree for " + b.branch + " and start its environment?") + '"');
      });
    }
    var start = startItems ? menu(p.path + "/startmenu", "Start a checkout or branch of " + p.name, startItems,
      { cls: "btn", html: "Start&hellip;", title: "Pick a branch or worktree to start" }) : "";
    var more = menu(p.path + "/menu", "More actions for " + p.name,
      menuItem(p.path + "/menu/fetch", "Fetch", "git fetch --prune", ' data-repo-action="fetch" data-repo="' + esc(p.path) + '"') +
      menuItem(p.path + "/menu/path", "Copy path", p.path, ' data-copy="' + esc(p.path) + '"') +
      menuItem(p.path + "/menu/verify", "Verify setup with your agent", "", " data-learn") +
      menuItem(p.path + "/menu/rescan", "Rescan", "", " data-rescan"));
    return '<header class="pp-head lane-' + lane(p.name) + '"><span class="avatar" aria-hidden="true">' + esc(initials(p.name)) + "</span>" +
      '<div class="pp-head-id"><div class="pp-title-line"><h1 class="pp-title">' + esc(p.name) + '</h1><div class="pp-chips">' + chips + "</div></div>" +
      '<p class="pp-meta"><span class="mono" title="' + esc(p.path) + '">' + esc(p.path) + '</span><span class="dot" aria-hidden="true">\u00B7</span>' +
      '<span>default <code class="mono">' + esc(p.default_branch) + "</code></span></p></div>" +
      '<div class="pp-head-actions">' + live + start + more + "</div></header>";
  }

  function setupCallout(p) {
    return '<div class="callout" data-setup-callout><span class="callout-glyph" aria-hidden="true">&#9675;</span><div class="callout-body">' +
      '<p class="callout-title">stackfork has not verified how this project starts. Set it up once with your agent.</p>' +
      '<p class="callout-detail">The workflow, resources and secrets appear here after setup.</p></div>' +
      '<div class="callout-actions"><button type="button" class="btn btn-primary" data-learn data-key="' + esc(p.path) + '/setup">Set up with your agent</button></div></div>';
  }

  // ----- checkouts -----
  function abText(c) {
    if (!c.upstream) return "no upstream";
    if (!c.ahead && !c.behind) return "up to date";
    return c.ahead + " ahead \u00B7 " + c.behind + " behind";
  }
  function abBar(c) {
    if (!c.upstream) return '<span class="ab-none">no upstream</span>';
    if (!c.ahead && !c.behind) return '<span class="ab-none">up to date</span>';
    var t = c.ahead + c.behind, a = Math.max(c.ahead ? 4 : 0, Math.round(56 * c.ahead / t)), b = c.behind ? 56 - a : 0;
    return '<span class="ab"><svg class="ab-bar" width="56" height="4" viewBox="0 0 56 4" aria-hidden="true" focusable="false"><rect class="ab-track" width="56" height="4" rx="2"/>' +
      (a ? '<rect class="ab-ahead" width="' + a + '" height="4" rx="2"/>' : "") + (b ? '<rect class="ab-behind" x="' + a + '" width="' + b + '" height="4" rx="2"/>' : "") +
      '</svg><span class="mono ab-count" aria-hidden="true">' + c.ahead + " \u00B7 " + c.behind + '</span><span class="sr-only">' + abText(c) + " upstream</span></span>";
  }
  function rowAction(p, c) {
    var r = c.last_run, k = esc(p.path + "/co/" + c.key);
    if (c.state === "running" || c.state === "partial") {
      if (isWeb(c.open_url)) return '<a class="btn" href="' + esc(c.open_url) + '" target="_blank" rel="noopener" data-key="' + k + '/open" title="' + esc(c.open_url) + '">Open App</a>';
      return '<button type="button" class="btn" data-logs data-project="' + esc(c.project) + '" data-key="' + k + '/logs">Logs</button>';
    }
    if (c.state === "starting") return '<button type="button" class="btn" data-logs data-project="' + esc(c.project) + '" data-key="' + k + '/logs">Logs</button>';
    if (!isSetUp(p)) return '<button type="button" class="btn" disabled title="Set up this repo first">Start</button>';
    var retry = r && r.outcome === "failed";
    return '<button type="button" class="btn"' + startAttrs(p, c) + ' data-key="' + k + '/start" title="' + (retry ? "Start again after the failed run" : "Start this checkout") + '">' + (retry ? "Retry" : "Start") + "</button>";
  }
  function rowMenu(p, c) {
    var items = menuItem(p.path + "/co/" + c.key + "/menu/path", "Copy path", c.path === p.path ? p.path : relPath(p.path, c.path), ' data-copy="' + esc(c.path) + '"');
    if (c.project && (c.state === "running" || c.state === "partial" || c.state === "starting")) {
      items += menuItem(p.path + "/co/" + c.key + "/menu/stop", "Stop", "keeps volumes", ' data-stop data-project="' + esc(c.project) + '"');
    }
    if (c.project && isSetUp(p)) {
      items += menuItem(p.path + "/co/" + c.key + "/menu/reset", "Reset environment\u2026", "", ' data-repo-action="reset" data-repo="' + esc(p.path) + '" data-path="' + esc(c.path) +
        '" title="deletes this environment\'s volumes and starts it again from the code in the worktree; asks first"');
    }
    return menu(p.path + "/co/" + c.key + "/menu", "More actions for " + c.branch, items);
  }
  function checkoutRow(p, c, i, n, sel) {
    var r = c.last_run, selected = sel && sel.key === c.key;
    var lane_ = '<span class="wt-lane" aria-hidden="true"><svg width="24" height="100%" focusable="false"><rect class="lane-trunk" x="11" y="' + (i === 0 ? "50%" : "0") + '" width="2" height="' + (i === 0 || i === n - 1 ? "50%" : "100%") + '"/>' +
      (c.main ? "" : '<rect class="lane-stub" x="11" y="50%" width="9" height="2"/>') + '<circle class="lane-dot" cx="' + (c.main ? 12 : 20) + '" cy="50%" r="3"/></svg></span>';
    var runCell = r ? '<button type="button" class="pp-run-btn" data-select="' + esc(c.key) + '" data-key="' + esc(p.path + "/co/" + c.key + "/run") + '" aria-pressed="' + !!selected + '" aria-label="Show run ' + r.id + " of " + esc(c.branch) + ' in the workflow">' +
      runChip(r) + '<span class="mono">#' + r.id + '</span><span class="pp-run-when">' + esc(ago(r.started_at)) + "</span></button>" : '<span class="small muted">No runs yet</span>';
    return '<div class="pp-co-row lane-' + lane(p.name) + (c.main ? " wt-main" : "") + '" data-co="' + esc(c.key) + '" data-selected="' + !!selected + '">' + lane_ +
      '<span class="wt-branch"><span class="branch-pill" title="' + esc(c.branch) + '">' + esc(c.branch) + "</span>" +
      (c.main ? '<span class="chip chip-off">main checkout</span>' : "") + (c.changes ? chip("attention", "\u25CF", "changes", "Uncommitted changes") : "") + "</span>" +
      '<span class="pp-co-path mono" title="' + esc(c.path) + '">' + esc(relPath(p.path, c.path)) + "</span>" +
      '<span class="pp-co-ab">' + abBar(c) + "</span>" +
      '<span class="pp-co-mode"><span class="pp-mode" data-mode="' + esc(c.mode) + '">' + esc(c.mode) + "</span></span>" +
      '<span class="wt-state">' + stateChip(c.state) + "</span>" +
      '<span class="pp-co-run">' + runCell + "</span>" +
      '<span class="pp-co-actions">' + rowAction(p, c) + rowMenu(p, c) + "</span>" +
      '<span class="pp-co-meta small mono">' + esc(relPath(p.path, c.path)) + " \u00B7 " + esc(abText(c)) + " \u00B7 " + esc(c.mode) + "</span></div>";
  }
  function checkoutsBody(p) {
    var list = p.checkouts || [], sel = selectedCheckout();
    if (!list.length) return '<p class="small muted">No checkouts found.</p>';
    var head = '<div class="pp-co-head micro" aria-hidden="true"><span></span><span>Branch</span><span class="pp-co-path-col">Path</span><span class="pp-co-ab">Ahead / behind</span><span>Mode</span><span>State</span><span>Last run</span><span></span></div>';
    var rows = list.map(function (c, i) { return checkoutRow(p, c, i, list.length, sel); }).join("");
    var brs = p.branches_not_checked_out || [];
    var br = brs.length ? '<details class="pp-disclosure" data-branches' + (st.branchesOpen ? " open" : "") + "><summary>" + plural(brs.length, "branch") + ' not checked out</summary><div class="wt-table wt-branches">' +
      brs.map(function (b) {
        return '<div class="wt-row wt-branch-row"><span class="wt-lane" aria-hidden="true"><svg width="24" height="100%" focusable="false"><rect class="lane-trunk lane-dashed" x="11" y="0" width="2" height="100%"/><rect class="lane-stub lane-none lane-dashed" x="11" y="50%" width="9" height="2"/><circle class="lane-dot lane-none lane-hollow" cx="20" cy="50%" r="3"/></svg></span>' +
          '<span class="wt-branch"><span class="branch-pill branch-pill-plain">' + esc(b.branch) + '</span></span><span class="wt-path mono muted">not checked out</span><span class="wt-ab"><span class="ab-none">' + esc(b.upstream || "no upstream") + "</span></span>" +
          '<span class="wt-state">' + stateChip("no-stack") + '</span><span class="wt-actions">' +
          (isSetUp(p) ? '<button type="button" class="btn" data-repo-action="start" data-repo="' + esc(p.path) + '" data-branch="' + esc(b.branch) + '" data-key="' + esc(p.path + "/br/" + b.branch) +
            '/start" data-confirm="' + esc("Create a worktree for " + b.branch + " and start its environment?") + '">Start</button>' : '<button type="button" class="btn" disabled title="Set up this repo first">Start</button>') + "</span></div>";
      }).join("") + "</div></details>" : "";
    return '<div class="pp-co">' + head + rows + "</div>" + br;
  }
  function checkoutsSub(p) {
    var list = p.checkouts || [], n = 0;
    list.forEach(function (c) { if (c.state === "running" || c.state === "partial") n++; });
    return plural(list.length, "checkout") + " \u00B7 " + n + " running";
  }

  // ----- workflow bar -----
  function workflowTools(p) {
    var mode = viewMode(), sel = selectedCheckout();
    var seg = (p.modes || []).length < 2 ? "" : '<fieldset class="segmented"><legend class="sr-only">Show the workflow for</legend>' + (p.modes || []).map(function (m) {
      return '<label><input type="radio" name="wf-mode" value="' + esc(m) + '"' + (m === mode ? " checked" : "") + "><span>" + esc(cap(m)) +
        (m === p.default_mode ? ' <span class="pp-default-tag">default</span>' : "") + "</span></label>";
    }).join("") + "</fieldset>";
    var mk = mode !== p.default_mode && (p.modes || []).length > 1 ? '<button type="button" class="btn btn-ghost" data-make-default data-key="' + esc(p.path) + '/makedefault" title="Start uses this mode for new environments">Make default</button>' : "";
    var withRun = (p.checkouts || []).filter(function (c) { return c.last_run; });
    var overlay = "";
    if (withRun.length) {
      overlay = '<span class="pp-overlay-tag lane-' + lane(p.name) + '"><label class="small muted" for="pp-run-select">Last run of</label>' +
        '<select id="pp-run-select" data-run-select data-key="' + esc(p.path) + '/runselect"><option value="">none</option>' + withRun.map(function (c) {
          return '<option value="' + esc(c.key) + '"' + (sel && sel.key === c.key ? " selected" : "") + ">" + esc(c.branch) + "</option>";
        }).join("") + "</select>";
      if (sel && sel.last_run) {
        var r = sel.last_run;
        overlay += '<span class="mono">#' + r.id + "</span>" + runChip(r) + '<span class="small muted">' + esc(ago(r.started_at)) + (r.outcome === "running" ? "" : " \u00B7 " + esc(dur(r.duration_ms))) + "</span>" +
          (r.mode !== mode && p.modes.indexOf(r.mode) >= 0 ? '<button type="button" class="btn" data-mode-to="' + esc(r.mode) + '">Show ' + esc(r.mode) + "</button>" : "") +
          '<button type="button" class="btn btn-ghost" data-clear data-key="' + esc(p.path) + '/clear" aria-label="Hide the last run">Hide</button>';
      }
      overlay += "</span>";
    }
    return '<div class="pp-wf-bar">' + seg + mk + overlay + "</div>";
  }
  function workflowSub(p) {
    var g = p.workflow && p.workflow[viewMode()], url = "";
    if (g) (g.nodes || []).forEach(function (n) { if (n.id === "ready" && n.entry_url) url = n.entry_url; });
    var t = url || (p.entry && p.entry.target) || "";
    return t ? 'Entry: <code class="mono">App \u2192 ' + esc(t) + "</code>" : "";
  }

  // ----- configuration -----
  function configBody(p) {
    var c = p.config || {}, e = p.entry || { state: "none" }, secrets = c.secrets || [], envs = c.env_overrides || [];
    var entry = e.state === "none" ? '<span class="small muted">No web entry</span>' :
      '<span class="micro">App</span><span aria-hidden="true">&rarr;</span>' + (e.target ? "<code class=\"mono\">" + esc(e.target) + "</code>" : '<span class="small muted">no entry recorded</span>') +
      (e.state === "recorded" ? chip("ok", "\u25CF", "recorded") : chip("attention", "\u25CF", e.target ? "entry guessed" : "no entry"));
    var sec = secrets.length ? '<div class="secret-list" role="table" aria-label="Secrets of ' + esc(p.name) + '"><div class="secret-head micro" role="row"><span role="columnheader">Name</span><span role="columnheader">Reference</span><span role="columnheader">Status</span><span role="columnheader"><span class="sr-only">Actions</span></span></div>' +
      secrets.map(function (s) {
        return '<div class="secret-row" role="row"><span class="secret-name" role="cell"><span class="mono secret-id">' + esc(s.name) + "</span>" + (s.hint ? '<span class="small muted secret-hint">' + esc(s.hint) + "</span>" : "") +
          '</span><span class="secret-ref" role="cell">' + (s.reference ? "<code class=\"mono\"" + (s.source ? ' title="' + esc(s.source + (s.from ? ": " + s.from : "")) + '"' : "") + ">" + esc(s.reference) + "</code>" : '<span class="small muted">not mapped on this machine</span>') +
          '</span><span class="secret-state" role="cell">' + secretChip(s) + '</span><span class="secret-actions" role="cell"></span></div>';
      }).join("") + "</div>" : '<p class="small muted secrets-empty">No secrets. Add one to map a name this project needs to a keychain item or a 1Password reference.</p>';
    sec += '<p class="pp-actions-line"><button type="button" class="btn" data-secret-add data-repo="' + esc(p.path) + '" data-name="' + esc(p.name) + '" data-key="' + esc(p.path) + '/secrets/add">Add secret</button>' +
      (secrets.length ? ' <button type="button" class="btn btn-ghost" data-secrets-check data-repo="' + esc(p.path) + '" data-key="' + esc(p.path) + '/secrets/check" title="Read every reference once; 1Password may ask you to unlock it">Check</button>' : "") + "</p>";
    var env = envs.length ? '<div class="pp-env lane-' + lane(p.name) + '">' + envs.map(function (o, i) {
      var co = byKey(p.checkouts, o.checkout), n = envs.filter(function (x) { return x.checkout === o.checkout; }).length;
      return '<div class="pp-env-row"><code class="mono">' + esc(o.name) + '</code><code class="mono">' + (o.masked ? "\u2022\u2022\u2022\u2022\u2022\u2022" : esc(o.value)) + '</code><span class="pp-env-where small muted">' + esc(o.service) +
        ' on <span class="branch-pill">' + esc(co ? co.branch : o.checkout) + "</span></span>" +
        (co && co.project ? '<button type="button" class="btn btn-ghost" data-env-reset data-project="' + esc(co.project) + '" data-count="' + n + '" data-key="' + esc(p.path + "/env/" + i) + '" aria-label="Reset ' + plural(n, "env override") + " of " + esc(co.branch) + '" title="Removes all ' + plural(n, "override") + ' of this checkout">Reset</button>' : "<span></span>") + "</div>";
    }).join("") + "</div>" : '<p class="small muted">No env overrides. Every service uses the project values.</p>';
    var live = p.live && p.live.state !== "off", cur = c.branch_cleanup || "off";
    var cleanup = '<div><span class="live-cleanup"><label class="plan-mode"><span class="small muted">When a PR merges</span> <select data-cleanup-for="' + esc(p.path) + '" data-key="' + esc(p.path) + '/cleanup" aria-label="Branch cleanup for ' + esc(p.name) + '"' + (live ? "" : " disabled") + ">" +
      ["off", "suggest", "auto"].map(function (m) { return '<option value="' + m + '"' + (m === cur ? " selected" : "") + ">" + m + "</option>"; }).join("") + "</select></label></span>" +
      (live ? "" : '<p class="pp-note pp-cleanup-note">Turn Live on to change this.</p>') + "</div>";
    return '<div class="pp-config"><div><h3>Entry</h3><p>Where Open App goes.</p></div><div class="entry-line" data-entry="' + esc(e.state) + '">' + entry + "</div>" +
      "<div><h3>Secrets</h3><p>Names and status only. A value never reaches this page.</p></div><div>" + sec + "</div>" +
      "<div><h3>Env overrides</h3><p>Replace the project value until you reset them.</p></div><div>" + env + "</div>" +
      "<div><h3>Branch cleanup</h3><p>Used while Live is on.</p></div>" + cleanup + "</div>";
  }

  // ----- page -----
  function skeleton(name) {
    return '<div class="pp-page" aria-busy="true"><p class="muted" role="status">Loading ' + esc(name) + '\u2026</p><div class="pp-skel"></div><div class="pp-skel pp-skel-lg"></div><div class="pp-skel pp-skel-lg"></div></div>';
  }
  function notFound(name) {
    return '<div class="pp-empty"><h2>No repository named ' + esc(name) + '</h2><p>It may have moved or been removed from the scanned folders.</p><p><a class="btn" href="#repos">Back to Repos</a> <button type="button" class="btn btn-ghost" data-rescan>Rescan</button></p></div>';
  }
  function failed(name, msg) {
    return '<div class="pp-empty"><h2>Could not load ' + esc(name) + '</h2><p>' + esc(msg) + '</p><p><button type="button" class="btn" data-retry-load>Try again</button> <a class="btn btn-ghost" href="#repos">Back to Repos</a></p></div>';
  }
  // page(p) is the static frame; the parts below it are repainted by paint().
  function page(p) {
    var setUp = isSetUp(p);
    var s = '<div class="pp-page" data-name="' + esc(p.name) + '"><div data-part="header"></div><div data-part="errorbar"></div>';
    if (!setUp) s += setupCallout(p);
    s += section("checkouts", "Checkouts", "", '<span class="small muted">Select a last run to show it on the workflow</span>', '<div data-part="checkouts"></div>');
    if (setUp) {
      s += section("workflow", "Startup workflow", "", "", '<div id="pp-workflow" data-part="workflow"></div>');
      s += section("resources", "Resources", "", "", '<div class="pp-res-wrap"><p class="pp-note" data-part="res-note" role="status"></p><div id="pp-resources" data-part="resources"><p class="small muted">Loading resources\u2026</p></div></div>');
    }
    s += section("config", "Configuration", "Entry, secrets, env overrides", "", '<div data-part="config"></div>');
    return s + "</div>";
  }

  var body = null;
  function part(name) { return body && body.querySelector('[data-part="' + name + '"]'); }
  // set swaps in new markup only when it changed and no menu inside is open, so a poll
  // never closes a menu, drops focus or restarts a button's label.
  function set(name, html, hold) {
    var el = part(name);
    if (!el || el.__html === html) return;
    if (hold && busy) return;
    if (el.querySelector('[aria-haspopup="menu"][aria-expanded="true"]')) return;
    var a = document.activeElement, fk = a && el.contains(a) && a.dataset ? a.dataset.key : "";
    el.innerHTML = html;
    el.__html = html;
    if (fk) {
      var again = el.querySelector('[data-key="' + fk.replace(/"/g, '\\"') + '"]');
      if (again && again.focus) again.focus({ preventScroll: true });
    }
  }
  function paintCrumbs() {
    var t = document.getElementById("view-title");
    if (!t || !st) return;
    t.className = "toolbar-title pp-crumbs";
    t.setAttribute("role", "navigation");
    t.setAttribute("aria-label", "Breadcrumb");
    t.innerHTML = '<a href="#repos">Repos</a><span class="pp-crumb-sep" aria-hidden="true">/</span><span aria-current="page">' + esc(st.name) + "</span>";
    document.title = st.name + " \u00B7 Repos \u00B7 stackfork";
  }
  function paintWorkflow() {
    var el = part("workflow"), p = st.data;
    if (!el) return;
    var wf = window.sfProjectWorkflow;
    if (!wf || !wf.render) { el.textContent = "The workflow view is not available."; return; }
    var sel = selectedCheckout();
    wf.render(el, p, { mode: viewMode(), checkout: sel ? sel.key : "", node: st.step || null,
      onSelect: function (id) { st.step = id || ""; syncHash(); },
      onMode: function (m) { setMode(m); },
      onRetry: function () { retrySelected(); } });
  }
  function paintResources() {
    var el = part("resources");
    if (!el || !st.res) return;
    var re = window.sfProjectResources;
    if (!re || !re.render) { el.textContent = "The resources view is not available."; return; }
    re.render(el, st.res, { range: st.range, name: st.name, onRange: function (r) { setRange(r); } });
  }
  function paint() {
    if (!body || !st) return;
    paintCrumbs();
    if (st.status === "loading") { body.innerHTML = skeleton(st.name); st.built = ""; return; }
    if (st.status === "notfound") { body.innerHTML = notFound(st.name); st.built = ""; return; }
    if (!st.data) { body.innerHTML = failed(st.name, st.error || "The server did not answer."); st.built = ""; return; }
    var p = st.data;
    if (st.built !== p.name + "|" + isSetUp(p)) { // new project or its set-up state changed: rebuild the frame
      body.innerHTML = page(p);
      st.built = p.name + "|" + isSetUp(p);
      st.resLoaded = false;
    }
    set("header", header(p), true);
    set("errorbar", st.error ? '<p class="pp-note pp-note-attention" role="status">Showing the last data. ' + esc(st.error) + "</p>" : "");
    set("checkouts", checkoutsBody(p), true);
    set("checkouts-sub", checkoutsSub(p));
    set("config", configBody(p), true);
    if (isSetUp(p)) {
      set("workflow-tools", workflowTools(p));
      set("workflow-sub", workflowSub(p));
      paintWorkflow();
      if (st.res) paintResources();
      set("res-note", st.resError ? "Stats unavailable: " + st.resError : "");
    }
  }

  // ---------- data ----------
  function queryName() { return "name=" + encodeURIComponent(st.name) + (st.path ? "&path=" + encodeURIComponent(st.path) : ""); }
  function getJSON(url) {
    return fetch(url, { credentials: "same-origin" }).then(function (r) {
      return r.text().then(function (t) {
        var j = null;
        try { j = JSON.parse(t); } catch (e) { /* not JSON */ }
        if (!r.ok) { var err = new Error((j && j.error) || (t && t.trim().slice(0, 120)) || "status " + r.status); err.status = r.status; throw err; }
        return j;
      });
    });
  }
  function load() {
    var mine = st;
    if (!mine || mine.inflight) return;
    mine.inflight = true;
    getJSON("/api/project?" + queryName()).then(function (p) {
      if (st !== mine) return;
      mine.data = p; mine.status = "ready"; mine.error = "";
    }, function (e) {
      if (st !== mine) return;
      if (e.status === 404) { mine.status = "notfound"; mine.data = null; return; }
      mine.error = e.message;
      if (!mine.data) mine.status = "error";
    }).then(function () {
      if (st !== mine) return;
      mine.inflight = false;
      paint();
      if (mine.data && isSetUp(mine.data) && !mine.resLoaded) loadResources();
    });
  }
  function loadResources() {
    var mine = st;
    if (!mine || mine.resInflight || !mine.data || !isSetUp(mine.data) || mine.collapsed.resources) return;
    mine.resInflight = true;
    var range = mine.range;
    getJSON("/api/project/resources?" + queryName() + "&range=" + range).then(function (r) {
      if (st !== mine || mine.range !== range) return;
      mine.res = r; mine.resError = ""; mine.resLoaded = true;
    }, function (e) {
      if (st !== mine) return;
      mine.resError = e.message; mine.resLoaded = true;
    }).then(function () {
      if (st !== mine) return;
      mine.resInflight = false;
      if (mine.res) paintResources();
      set("res-note", mine.resError ? "Stats unavailable: " + mine.resError : "");
    });
  }
  function visible() { return document.visibilityState === "visible"; }

  // ---------- interactions ----------
  // toggle: a second click on the same run hides the overlay (the run buttons; the select and the canvas do not toggle).
  function selectCheckout(key, toggle) {
    var p = st.data, c = key ? byKey(p.checkouts, key) : null;
    if (c && toggle && selectedCheckout() && selectedCheckout().key === c.key) { st.checkout = ""; c = null; }
    else st.checkout = c ? c.key : "";
    if (c && c.last_run && p.modes.indexOf(c.last_run.mode) >= 0) st.mode = c.last_run.mode;
    syncHash();
    paint();
  }
  // Retry from the step panel: start the selected checkout again, like the row's Retry.
  function retrySelected() {
    var c = selectedCheckout(), btn = c && body && body.querySelector('[data-repo-action="start"][data-path="' + c.path.replace(/"/g, "") + '"]');
    if (btn && window.sfDash) track(window.sfDash.runRepoAction(btn));
  }
  function setMode(m) { st.mode = m; syncHash(); paint(); }
  function setRange(r) {
    if (RANGES.indexOf(r) < 0 || r === st.range) return;
    st.range = r; st.res = null; st.resLoaded = false;
    syncHash();
    loadResources();
  }
  function toast(tone, text) { if (window.sfDash) window.sfDash.toast(tone, text); }
  function track(promise) {
    busy++;
    return Promise.resolve(promise).then(function () {}, function () {}).then(function () { busy--; if (st) load(); });
  }
  function makeDefault() {
    var p = st.data, mode = viewMode();
    window.sfDash.post("/api/config", { op: "set-mode", value: p.path + "=" + mode })
      .then(function (r) { return r.json().then(function (b) { return { ok: r.ok, body: b }; }); })
      .then(function (res) {
        if (!res.ok) toast("failed", "mode: " + (res.body.message || "refused"));
        else window.sfDash.log("mode: " + mode);
      })
      .catch(function (e) { toast("failed", "mode: " + e.message); })
      .then(function () { load(); });
  }
  function learn(btn) {
    var p = st.data;
    btn.dataset.repo = p.path; btn.dataset.name = p.name;
    fetch("/api/agents?repo=" + encodeURIComponent(p.path), { credentials: "same-origin" })
      .then(function (r) { return r.ok ? r.json() : {}; }, function () { return {}; })
      .then(function (d) {
        btn.dataset.prompt = d.prompt || "Learn how " + p.name + " starts and record it with stackfork learn.";
        window.sfDash.openLearn(btn);
      });
  }

  function onClick(e) {
    var t = e.target, d = window.sfDash;
    if (!st || !d) return;
    var el;
    if ((el = t.closest("[data-collapse]"))) {
      var id = el.dataset.collapse, sec = body.querySelector("#sec-" + id);
      st.collapsed[id] = !st.collapsed[id];
      sec.dataset.collapsed = st.collapsed[id] ? "1" : "0";
      el.setAttribute("aria-expanded", st.collapsed[id] ? "false" : "true");
      saveCollapsed();
      if (!st.collapsed[id] && id === "resources") loadResources();
      return;
    }
    if ((el = t.closest("[data-retry-load]"))) { st.status = "loading"; paint(); load(); return; }
    if ((el = t.closest("[data-select]"))) { selectCheckout(el.dataset.select, true); return; }
    if ((el = t.closest("[data-mode-to]"))) { setMode(el.dataset.modeTo); return; }
    if ((el = t.closest("[data-clear]"))) { selectCheckout(""); return; }
    if ((el = t.closest("[data-make-default]"))) { makeDefault(); return; }
    if ((el = t.closest("[data-repo-action]"))) {
      if (el.disabled) return;
      track(d.runRepoAction(el));
      return;
    }
    if ((el = t.closest("[data-logs]"))) { d.showLogs(el.dataset.project, "", el); return; }
    if ((el = t.closest("[data-stop]"))) { track(d.runAction("down", el.dataset.project)); return; }
    if ((el = t.closest("[data-secrets-check]"))) { track(d.checkSecrets(el)); return; }
    if ((el = t.closest("[data-learn]"))) { learn(el); return; }
    if ((el = t.closest("[data-rescan]"))) {
      d.post("/api/config", { op: "rescan", value: "" }).then(function () { setTimeout(load, 800); });
      return;
    }
    if ((el = t.closest("[data-copy]"))) {
      var lbl = el.querySelector(".menu-label");
      d.copyText(el.dataset.copy, (lbl || el).textContent.trim(), el);
      return;
    }
    if ((el = t.closest(".pp-co-row")) && !t.closest("button, a, .menu-anchor, summary, input, select")) selectCheckout(el.dataset.co, true);
  }
  function onChange(e) {
    var t = e.target;
    if (!st || !st.data) return;
    if (t.matches('input[name="wf-mode"]')) { setMode(t.value); return; }
    if (t.matches("select[data-run-select]")) { selectCheckout(t.value); return; }
    if (t.matches("select[data-cleanup-for]") && window.sfDash) track(window.sfDash.postCleanup(t));
  }

  // ---------- lifecycle ----------
  function stop() { timers.forEach(clearInterval); timers = []; }
  function enter() {
    var route = parseHash(location.hash);
    body = document.getElementById("project-body");
    if (!route || !body) return;
    var same = st && st.name === route.name && st.path === (route.q.get("path") || "");
    if (!same) { open(route); paint(); }
    else { // the URL changed inside the same project: take its mode, checkout and range
      if (route.q.get("mode")) st.mode = route.q.get("mode");
      st.step = route.q.get("step") || "";
      if (route.q.has("checkout")) st.checkout = route.q.get("checkout") === "none" ? "" : route.q.get("checkout");
      if (RANGES.indexOf(route.q.get("range")) >= 0 && route.q.get("range") !== st.range) { st.range = route.q.get("range"); st.res = null; st.resLoaded = false; }
      paint();
    }
    stop();
    load();
    timers.push(setInterval(function () { if (visible() && !busy) load(); }, 5000));
    timers.push(setInterval(function () { if (visible()) loadResources(); }, 10000));
  }
  function leave() {
    stop();
    var t = document.getElementById("view-title");
    if (t) { t.removeAttribute("role"); t.removeAttribute("aria-label"); t.className = "toolbar-title"; }
    document.title = "stackfork";
  }

  function wire() {
    var main = document.getElementById("view-project");
    if (!main) return;
    main.addEventListener("click", onClick);
    main.addEventListener("change", onChange);
    main.addEventListener("toggle", function (e) { if (e.target.matches && e.target.matches("details[data-branches]") && st) st.branchesOpen = e.target.open; }, true);
  }
  if (typeof document !== "undefined" && document.readyState === "loading") document.addEventListener("DOMContentLoaded", wire);
  else if (typeof document !== "undefined") wire();

  var api = { enter: enter, leave: leave, parseHash: parseHash, hashFor: hashFor, render: function (p, s) { st = s || st; return page(p); },
    parts: { header: header, checkouts: checkoutsBody, workflowTools: workflowTools, config: configBody } };
  window.sfProject = api;
  if (typeof window.__sfProjectExport === "function") {
    window.__sfProjectExport({ api: api, open: open, setState: function (s) { st = s; }, getState: function () { return st; }, selectedCheckout: selectedCheckout, viewMode: viewMode, page: page, header: header,
      checkoutsBody: checkoutsBody, workflowTools: workflowTools, configBody: configBody, skeleton: skeleton, notFound: notFound });
  }
})();
