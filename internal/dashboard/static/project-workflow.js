// Project page workflow canvas (owner D): window.sfProjectWorkflow.render(el, project, opts).
//
//   render(el, project, {mode, checkout, node, onSelect(id|null), onMode(mode), onRetry(id)})
//
// Draws project.workflow[mode] (the Graph in docs/design/project-page/api.md)
// as a node flow, overlays the last run of the selected checkout, and opens a
// side panel for one step. Re-rendering is region by region and only when a
// region's markup changed, so the 5 s poll keeps focus, scroll and the panel.
(function () {
  "use strict";

  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function chip(cls, glyph, label, title) {
    return '<span class="chip chip-' + cls + '"' + (title ? ' title="' + esc(title) + '"' : "") +
      '><span class="chip-glyph" aria-hidden="true">' + glyph + "</span>" + esc(label) + "</span>";
  }
  var PLAN = {
    "at-start": ["off", "○", "at start", "stackfork resolves the secrets before anything else runs"],
    verified: ["ok", "●", "verified", "Recorded and verified by learn"],
    trusted: ["ok", "●", "trusted", "Runs on this machine; you trusted it"],
    untrusted: ["attention", "▲", "untrusted", "Runs on this machine; trust it first"],
    draft: ["attention", "▲", "draft", "Proposed by learn, not recorded; a Start skips it"],
    derived: ["off", "○", "derived", "Derived from the repo files; not recorded yet"]
  };
  var RUN = {
    succeeded: ["ok", "●", "Succeeded"], failed: ["failed", "⊘", "Failed"], skipped: ["off", "○", "Skipped"],
    running: ["busy", "◔", "Running"], pending: ["off", "○", "Pending"]
  };
  var ICON = {
    stackfork: '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><rect x="2" y="2" width="12" height="12" rx="3" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M10 6.2c-.4-.6-1.1-.9-2-.9-1.1 0-1.9.6-1.9 1.4 0 1.9 4 1 4 2.9 0 .9-.9 1.5-2.1 1.5-.9 0-1.7-.4-2.1-1" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>',
    docker: '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><path d="M8 1.8 13.5 5v6L8 14.2 2.5 11V5z M2.5 5 8 8.2 13.5 5 M8 8.2v6" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/></svg>',
    host: '<svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false"><rect x="3" y="3" width="10" height="7" rx="1" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M1.5 13h13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>',
    decision: '<svg viewBox="0 0 12 12" width="12" height="12" aria-hidden="true" focusable="false"><path d="M6 1 11 6 6 11 1 6z" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"/></svg>'
  };
  var PALETTE = [
    ["stackfork", "secrets", "Preflight. Resolves secret names before anything runs."],
    ["stackfork", "prepare", "Writes the instance: ports, env, Compose overrides."],
    ["docker", "compose-up", "Starts the services, or some of them in hybrid."],
    ["docker", "exec", "Runs a command in a service, or on: host. once: schema or data."],
    ["host", "host app", "A dev server on this machine, with its port in env."],
    ["stackfork", "note", "Text the run prints. Runs nothing."]
  ];

  function whereOf(n) { return n.where === "host" ? "host" : n.where === "docker" ? "docker" : "stackfork"; }
  function whereText(n) {
    var w = whereOf(n);
    return w === "stackfork" ? "stackfork" : w === "host" ? "this machine" : "Docker" + (n.service ? " · " + n.service : "");
  }
  function dur(ms) {
    if (ms == null || ms === "") return "";
    ms = Number(ms);
    if (!isFinite(ms)) return "";
    if (ms < 1000) return Math.round(ms) + " ms";
    var s = ms / 1000;
    if (s < 60) return s.toFixed(1).replace(/\.0$/, "") + " s";
    return Math.floor(s / 60) + " m " + Math.round(s % 60) + " s";
  }
  function ago(iso) {
    var t = Date.parse(iso);
    if (!isFinite(t)) return "";
    var s = Math.max(0, (Date.now() - t) / 1000);
    if (s < 60) return "just now";
    if (s < 3600) return Math.round(s / 60) + " min ago";
    if (s < 86400) return Math.round(s / 3600) + " h ago";
    return Math.round(s / 86400) + " d ago";
  }
  function shq(s) { return "'" + String(s).replace(/'/g, "'\\''") + "'"; }

  // ---- graph helpers -------------------------------------------------------
  function index(graph) {
    var g = { nodes: {}, out: {}, inn: {}, group: {} };
    (graph.nodes || []).forEach(function (n) { g.nodes[n.id] = n; });
    (graph.edges || []).forEach(function (e) {
      (g.out[e.from] = g.out[e.from] || []).push(e);
      g.inn[e.to] = (g.inn[e.to] || 0) + 1;
    });
    (graph.parallel || []).forEach(function (grp) { grp.forEach(function (id) { g.group[id] = grp; }); });
    return g;
  }
  // Ids along the first-edge path from id (parallel groups included), for join search.
  function pathOf(g, id) {
    var seen = {}, list = [];
    while (id && !seen[id]) {
      seen[id] = 1; list.push(id);
      if (g.group[id]) g.group[id].forEach(function (m) { if (!seen[m]) { seen[m] = 1; list.push(m); } });
      var o = g.out[id]; id = o && o[0] ? o[0].to : null;
    }
    return list;
  }
  function findJoin(g, edges) {
    if (edges.length < 2) return null;
    var paths = edges.map(function (e) { return pathOf(g, e.to); });
    var rest = paths.slice(1).map(function (p) { var s = {}; p.forEach(function (id) { s[id] = 1; }); return s; });
    for (var i = 0; i < paths[0].length; i++) {
      var id = paths[0][i];
      if (rest.every(function (s) { return s[id]; })) return id;
    }
    return null;
  }

  // ---- canvas --------------------------------------------------------------
  function edge(reached, label, cls) {
    return '<span class="pw-edge' + (cls ? " " + cls : "") + '" data-reached="' + (reached ? 1 : 0) + '">' +
      (label ? '<span class="pw-edge-label">' + label + "</span>" : "") + "</span>";
  }
  function runBadge(rs) {
    if (!rs) return "";
    var c = RUN[rs.state] || RUN.pending, t = dur(rs.ms);
    return '<span class="pw-node-run">' + chip(c[0], c[1], c[2] + (t ? " " + t : ""), rs.reason) +
      (rs.reason && rs.state !== "succeeded" ? '<span class="pw-node-reason">' + esc(rs.reason) + "</span>" : "") + "</span>";
  }
  function subOf(n) {
    var parts = [n.subtitle, n.once ? "once " + n.once : "", whereText(n)].filter(Boolean);
    return parts.join(" · ");
  }

  // canvas builds the flow. ctx: {g, mode, modes, run, ids[]}; returns {html, ids}.
  function canvas(graph, mode, modes, run) {
    var g = index(graph), seen = {}, ids = [];
    var steps = run && run.steps ? run.steps : null;
    function rstate(id) { return steps ? steps[id] || null : null; }
    function reachedStep(id) {
      if (!run) return true;
      var r = rstate(id);
      return !!r && r.state !== "pending";
    }

    function card(n, active) {
      var live = active && n.in_mode !== false;
      var rs = live && run ? rstate(n.id) : null;
      var p = PLAN[n.plan_state] || PLAN.derived;
      var sub = subOf(n), word = rs ? (RUN[rs.state] || RUN.pending)[2] : p[2];
      if (live) ids.push(n.id);
      var cmd = n.command_expanded || n.command || "";
      return '<button type="button" class="pw-node" data-node="' + esc(n.id) + '" data-where="' + whereOf(n) + '" data-run="' + (rs ? esc(rs.state) : "") + '"' +
        (live ? ' data-fk="n:' + esc(n.id) + '"' : ' tabindex="-1" aria-hidden="true"') + ' aria-pressed="false"' +
        ' aria-label="' + esc(n.id + ", " + sub + ", " + word + ". Open details") + '">' +
        '<span class="pw-node-head"><span class="pw-node-icon">' + ICON[whereOf(n)] + '</span><span class="pw-node-id"><span class="pw-node-title">' + esc(n.title || n.id) +
        '</span><span class="pw-node-sub">' + esc(sub) + "</span></span>" + chip(p[0], p[1], p[2], p[3]) + "</span>" +
        '<code class="pw-node-cmd" title="' + esc(cmd) + '">' + esc(n.command || "") + "</code>" +
        (rs ? runBadge(rs) : n.skip_reason ? '<span class="pw-node-reason">' + esc(n.skip_reason) + "</span>" : "") + "</button>";
    }

    // walk emits the chain starting at id until a stop id; returns html and
    // whether the path is still reached (overlay) after it.
    function walk(id, stop, active, reached, first) {
      var html = "", cur = id, started = !first;
      while (cur && !stop[cur] && !seen[cur]) {
        var n = g.nodes[cur];
        if (!n) break;
        seen[cur] = 1;
        if (n.kind === "terminal") {
          var isStart = !g.inn[n.id];
          if (isStart) {
            html += '<span class="pw-terminal pw-start">' + (run ? esc("Start · run #" + run.id + (run.started_at ? " · " + ago(run.started_at) : "")) : "Start") + "</span>";
          } else {
            if (started) html += edge(active && reached && (!run || run.outcome !== "failed"));
            var ok = active && reached && (!run || run.outcome === "succeeded");
            html += '<span class="pw-terminal pw-ready" data-on="' + (ok ? 1 : 0) + '"' + (n.entry_url ? ' title="' + esc(n.entry_url) + '"' : "") + ">" +
              (ok && run ? '<span class="chip-glyph pw-ok" aria-hidden="true">●</span>' : "") + "Ready" + (n.entry_url ? " · App → " + esc(n.entry_url) : "") + "</span>";
            if (active && run && run.outcome === "failed") {
              html += '<span class="pw-skip-note">not reached: run #' + esc(run.id) + " stopped at " + esc(run.failed_step || "a step") + "</span>";
            }
          }
          started = true;
          var so = isStart ? g.out[n.id] : null;
          cur = so && so[0] ? so[0].to : null;
          continue;
        }
        if (n.kind === "decision") {
          if (started) html += edge(active && reached);
          started = true;
          var edges = g.out[n.id] || [], isMode = modes.length && edges.length > 0 && edges.every(function (e) { return modes.indexOf(e.label) >= 0; });
          var join = isMode ? null : findJoin(g, edges);
          var stopHere = {}; if (join) stopHere[join] = 1;
          html += '<span class="pw-decision">' + ICON.decision + esc(n.label || n.id) + "</span>";
          var afterJoin = join ? reachedStep(join) : false;
          var branches = edges.map(function (e, bi) {
            var on, label, skip = e.to === join, lbl = e.label || (n.branches && n.branches[bi]) || "";
            var taken;
            if (isMode) {
              on = active && lbl === mode;
              label = '<button type="button" class="pw-branch-btn" data-fk="m:' + esc(lbl) + '" data-mode-to="' + esc(lbl) + '" aria-pressed="' + (lbl === mode) + '">' + esc(lbl) + "</button>";
              var sub = walk(e.to, stopHere, on, on && reached, true);
              return '<div class="pw-branch pw-mode-branch" data-on="' + (on ? 1 : 0) + '" data-mode="' + esc(lbl) + '"' + (on ? "" : ' data-mode-to="' + esc(lbl) + '"') + ">" +
                edge(on && reached, label, "pw-edge-tall") + '<span class="pw-branch-more">' + esc(lbl) + " mode</span>" + sub.html + "</div>";
            }
            // plain decision: a branch is taken when the run went through it
            var others = edges.filter(function (o) { return o !== e && o.to !== join; });
            if (!run) taken = true;
            else if (skip) taken = afterJoin && !others.some(function (o) { return reachedStarted(o.to); });
            else taken = reachedStarted(e.to);
            on = active && reached && taken;
            var body;
            if (skip) {
              var other = edges.filter(function (o) { return o !== e && o.to !== join; })[0];
              body = '<span class="pw-skip-note">skip' + (other ? " " + esc(other.to) : "") + (n.skip_reason ? '<span class="pw-dim"> · ' + esc(n.skip_reason) + "</span>" : "") + "</span>";
              return '<div class="pw-branch pw-branch-skip" data-on="' + (on ? 1 : 0) + '">' + edge(on, esc(lbl), "pw-edge-tall") + body + edge(on, "", "pw-edge-grow") + "</div>";
            }
            var w = walk(e.to, stopHere, active, on, true);
            return '<div class="pw-branch" data-on="' + (on ? 1 : 0) + '">' + edge(on, esc(lbl), "pw-edge-tall") + w.html + edge(on, "", "pw-edge-grow") + "</div>";
          });
          html += '<div class="pw-split' + (isMode ? " pw-modes" : "") + '" role="group" aria-label="' + esc(n.label || n.id) + '">' + branches.join("") + "</div>";
          if (join) {
            reached = active && reached && (!run || afterJoin);
            cur = join; continue;
          }
          cur = null; break;
        }
        // step
        var grp = g.group[n.id];
        if (grp) {
          html += (started ? edge(active && reached, "start together") : "") ;
          started = true;
          var rr = grp.map(function (m) { return active && reached && reachedStep(m); });
          html += '<div class="pw-split pw-parallel" role="group" aria-label="Host apps that start together">' + grp.map(function (m, j) {
            seen[m] = 1;
            return '<div class="pw-branch" data-on="1">' + edge(rr[j], "", "pw-edge-short") + card(g.nodes[m], active) + edge(rr[j], "", "pw-edge-grow") + "</div>";
          }).join("") + "</div>";
          reached = rr.every(Boolean);
          // continue after the group: the successor of a member that leaves it
          cur = null;
          grp.forEach(function (m) { (g.out[m] || []).forEach(function (e) { if (!cur && !g.group[e.to]) cur = e.to; }); });
          continue;
        }
        var r = active && reached && reachedStep(n.id);
        if (started) html += edge(active && r);
        html += card(n, active);
        started = true; reached = r;
        var o = g.out[n.id];
        cur = o && o[0] ? o[0].to : null;
      }
      return { html: html, reached: reached };
    }
    function reachedStarted(id) {
      var r = rstate(id);
      return !!r && (r.state === "succeeded" || r.state === "failed" || r.state === "running");
    }

    var startId = (graph.nodes || []).filter(function (n) { return !g.inn[n.id]; })[0];
    var out = walk(startId ? startId.id : null, {}, true, true, true);
    return { html: '<div class="pw-flow">' + out.html + "</div>", ids: ids };
  }

  // ---- panel ---------------------------------------------------------------
  function recordCmd(n) {
    var c = "stackfork learn record step --id " + n.id + " --kind " + (n.step_kind || "exec");
    if (n.once) c += " --once " + n.once;
    if (n.where === "host") c += " --on host";
    if (n.where === "docker" && n.service) c += " --service " + n.service;
    return c + " --cmd " + shq(n.command || "");
  }
  function panel(n, rs, run, canRetry) {
    var p = PLAN[n.plan_state] || PLAN.derived, w = whereOf(n);
    var whereLong = w === "stackfork" ? "stackfork itself, before any project code" : w === "host" ? "This machine, in the checkout" : "Docker" + (n.service ? ", in the " + n.service + " service" : "");
    var expanded = n.command_expanded || "";
    var log = rs && (rs.log_tail || rs.log);
    var fail = rs && rs.state === "failed" ?
      '<div class="callout callout-failed"><span class="callout-glyph" aria-hidden="true">⊘</span><div class="callout-body"><p class="callout-title">Failed in run #' + esc(run.id) + (rs.reason ? ": " + esc(rs.reason) : "") + "</p>" +
      (log ? '<pre class="mono-log" tabindex="0" aria-label="Log of ' + esc(n.id) + '">' + esc(log) + "</pre>" : "") + "</div>" +
      (canRetry ? '<div class="callout-actions"><button type="button" class="btn" data-fk="retry" data-retry="' + esc(n.id) + '">Retry</button></div>' : "") + "</div>" : "";
    var runs = n.runs || n.recent_runs || [];
    var history = runs.length ? '<h4 class="pw-panel-h">' + (runs.length === 1 ? "Last run" : "Last " + Math.min(3, runs.length) + " runs") + ' of this step</h4><ol class="pw-panel-runs">' + runs.slice(0, 3).map(function (r) {
      var c = RUN[r.state] || RUN.pending;
      return '<li><span class="mono">#' + esc(r.id) + "</span>" + chip(c[0], c[1], c[2]) + '<span class="mono pw-dim">' + esc(dur(r.ms)) + '</span><span class="pw-dim">' + esc(r.started_at ? ago(r.started_at) : r.reason || "") + "</span></li>";
    }).join("") + "</ol>" : "";
    var rec = recordCmd(n);
    return '<div class="pw-panel-head"><span class="pw-node-icon" data-where="' + w + '">' + ICON[w] + '</span><h3 class="pw-panel-title" tabindex="-1" data-fk="panel-title"><span class="mono">' + esc(n.title || n.id) +
      '</span><span class="pw-node-sub">' + esc(subOf(n)) + "</span></h3>" +
      '<button type="button" class="btn btn-ghost btn-icon" data-fk="close" data-close-panel aria-label="Close step details">&#10005;</button></div>' +
      '<dl class="pw-panel-dl"><dt>Runs on</dt><dd>' + esc(whereLong) + "</dd><dt>Plan</dt><dd>" + chip(p[0], p[1], p[2]) + ' <span class="pw-dim">' + esc(p[3]) + "</span></dd>" +
      (n.skip_reason ? "<dt>Skipped</dt><dd>" + esc(n.skip_reason) + "</dd>" : "") +
      (rs && run ? "<dt>Run #" + esc(run.id) + "</dt><dd>" + chip((RUN[rs.state] || RUN.pending)[0], (RUN[rs.state] || RUN.pending)[1], (RUN[rs.state] || RUN.pending)[2] + (rs.ms ? " " + dur(rs.ms) : "")) +
        (rs.reason ? ' <span class="pw-dim">' + esc(rs.reason) + "</span>" : "") + "</dd>" : "") + "</dl>" +
      '<h4 class="pw-panel-h">Command</h4><pre class="pw-panel-cmd mono">' + esc(n.command || "") + "</pre>" +
      (expanded ? '<h4 class="pw-panel-h">As stackfork runs it</h4><pre class="pw-panel-cmd mono">' + esc(expanded) + "</pre>" : "") +
      fail + history +
      '<h4 class="pw-panel-h">Change this step</h4><p class="pw-panel-note">Steps are recorded by your agent with <code class="mono">stackfork learn</code>. You review and trust them; this page does not edit them.</p>' +
      '<p class="command-row pw-panel-record"><code class="mono">' + esc(rec) + '</code><button type="button" class="btn btn-ghost" data-fk="copy" data-copy="' + esc(rec) + '">Copy</button></p>';
  }

  function paletteHTML() {
    return '<p class="pw-palette-note">Read only. Your agent records steps with <code class="mono">stackfork learn</code>; you trust them.</p><ul>' +
      PALETTE.map(function (k) {
        return '<li><span class="pw-node-icon" data-where="' + k[0] + '">' + ICON[k[0]] + '</span><span><b class="mono">' + esc(k[1]) + "</b><span>" + esc(k[2]) + "</span></span></li>";
      }).join("") + "</ul>";
  }

  function listHTML(graph, ids, run) {
    var by = {}; (graph.nodes || []).forEach(function (n) { by[n.id] = n; });
    return ids.map(function (id) {
      var n = by[id], rs = run && run.steps ? run.steps[id] : null;
      return '<li><code class="mono">' + esc(id) + "</code> · " + esc(whereText(n)) + " · " + esc(n.command || "") + " · plan: " + esc((PLAN[n.plan_state] || PLAN.derived)[2]) +
        (rs ? " · run: " + esc((RUN[rs.state] || RUN.pending)[2] + (rs.ms ? " " + dur(rs.ms) : "") + (rs.reason ? ", " + rs.reason : "")) : "") + "</li>";
    }).join("");
  }

  function summaryHTML(graph, ids) {
    var by = {}, c = { stackfork: 0, docker: 0, host: 0 };
    (graph.nodes || []).forEach(function (n) { by[n.id] = n; });
    ids.forEach(function (id) { c[whereOf(by[id])]++; });
    return c.stackfork + " by stackfork, " + c.docker + " in Docker, " + c.host + " on this machine.";
  }

  function draftsHTML(graph, ids) {
    var by = {}; (graph.nodes || []).forEach(function (n) { by[n.id] = n; });
    var d = ids.filter(function (id) { return by[id].plan_state === "draft"; });
    if (!d.length) return "";
    return '<div class="callout callout-attention"><span class="callout-glyph" aria-hidden="true">▲</span><div class="callout-body"><p class="callout-title">' +
      esc(d.join(", ")) + (d.length === 1 ? " is a draft" : " are drafts") + '</p><p class="callout-detail">A Start skips a draft step. Verify it once with your agent to record it.</p></div></div>';
  }

  // Arrow-key target among the focusable cards (pure, so the test can use it).
  function navTarget(ids, cur, key) {
    var i = ids.indexOf(cur);
    if (i < 0) return ids[0] || null;
    var k = { ArrowDown: i + 1, ArrowRight: i + 1, ArrowUp: i - 1, ArrowLeft: i - 1, Home: 0, End: ids.length - 1 }[key];
    return k == null ? null : ids[Math.max(0, Math.min(ids.length - 1, k))];
  }

  // ---- render --------------------------------------------------------------
  var STATE = typeof WeakMap === "function" ? new WeakMap() : null;
  var SKELETON =
    '<div class="pw-bar"><p class="pw-summary" data-region="summary"></p>' +
    '<button type="button" class="btn btn-ghost" data-fk="palette" data-toggle-palette aria-expanded="false" aria-controls="pw-palette">Step kinds</button></div>' +
    '<div class="pw-body" data-palette="0" data-panel="0">' +
    '<aside class="pw-palette" id="pw-palette" aria-label="Step kinds" data-region="palette" hidden></aside>' +
    '<div class="pw-canvas" role="group" aria-label="Startup flow. Use the arrow keys to move between steps." data-region="canvas"></div>' +
    '<aside class="pw-panel" aria-label="Step details" data-region="panel" hidden></aside></div>' +
    '<details class="pw-disclosure"><summary>Steps as a list</summary><ol class="pw-steps-list" data-region="list"></ol></details>' +
    '<div data-region="drafts"></div>';

  function region(el, name) { return el.querySelector('[data-region="' + name + '"]'); }
  function setRegion(st, name, html) {
    if (st.r[name] === html) return false;
    st.r[name] = html;
    var n = region(st.el, name);
    if (n) n.innerHTML = html;
    return true;
  }
  function focusKey(el) {
    var a = typeof document !== "undefined" ? document.activeElement : null;
    if (!a || !el.contains || !el.contains(a) || !a.closest) return null;
    var n = a.closest("[data-fk]");
    return n && n.dataset ? n.dataset.fk : null;
  }
  function restoreFocus(el, key) {
    if (!key) return;
    var all = el.querySelectorAll("[data-fk]");
    for (var i = 0; i < all.length; i++) {
      if (all[i].dataset.fk === key) { if (document.activeElement !== all[i]) all[i].focus({ preventScroll: true }); return; }
    }
  }

  function currentRun(project, opts, mode) {
    var key = opts.checkout && typeof opts.checkout === "object" ? opts.checkout.key : opts.checkout;
    var co = opts.checkout && typeof opts.checkout === "object" && opts.checkout.last_run ? opts.checkout :
      (project.checkouts || []).filter(function (c) { return c.key === key; })[0];
    var run = co && co.last_run;
    return run && run.mode === mode ? run : null; // never draw a run on a plan it did not follow
  }

  function paint(st) {
    var el = st.el, sel = st.sel, p = region(el, "panel"), body = el.querySelector(".pw-body");
    var cards = el.querySelectorAll(".pw-node");
    for (var i = 0; i < cards.length; i++) {
      cards[i].setAttribute("aria-pressed", String(!!sel && cards[i].dataset.node === sel && cards[i].tabIndex !== -1));
    }
    var open = !!(sel && st.nodes[sel]);
    if (p) {
      var top = p.scrollTop;
      var changed = setRegion(st, "panel", open ? panel(st.nodes[sel], st.runs && st.runs[sel], st.run, typeof st.opts.onRetry === "function") : "");
      p.hidden = !open;
      if (!changed) p.scrollTop = top;
    }
    if (body) { body.dataset.panel = open ? "1" : "0"; body.dataset.palette = st.palette ? "1" : "0"; }
    var pal = region(el, "palette");
    if (pal) pal.hidden = !st.palette;
    var tg = el.querySelector("[data-toggle-palette]");
    if (tg) tg.setAttribute("aria-expanded", String(!!st.palette));
  }

  function select(st, id, move) {
    if (id === st.sel) return;
    st.sel = id;
    paint(st);
    if (st.opts.onSelect) st.opts.onSelect(id);
    if (move) { var t = region(st.el, "panel"); t = t && t.querySelector("[data-fk='panel-title']"); if (t) t.focus({ preventScroll: true }); }
  }

  function init(el) {
    var st = { el: el, r: {}, sel: null, palette: false, opts: {}, nodes: {}, ids: [], mode: null };
    el.innerHTML = SKELETON;
    el.addEventListener("click", function (e) {
      var t = e.target, c;
      if (!t || !t.closest) return;
      if ((c = t.closest("[data-close-panel]"))) { close(st); return; }
      if ((c = t.closest("[data-retry]"))) { if (st.opts.onRetry) st.opts.onRetry(c.getAttribute("data-retry")); return; }
      if ((c = t.closest("[data-copy]"))) { copy(c); return; }
      if ((c = t.closest("[data-toggle-palette]"))) { st.palette = !st.palette; paint(st); return; }
      if ((c = t.closest("[data-mode-to]"))) { if (st.opts.onMode) st.opts.onMode(c.getAttribute("data-mode-to")); return; }
      if ((c = t.closest("[data-node]"))) {
        if (c.tabIndex === -1) return; // a dimmed card sits in a branch that has its own mode button
        select(st, c.dataset.node, e.detail === 0); // keyboard Enter or Space moves into the panel
        if (e.detail === 0 && st.sel === c.dataset.node) { var h = region(el, "panel"); h = h && h.querySelector("[data-fk='panel-title']"); if (h) h.focus({ preventScroll: true }); }
      }
    });
    el.addEventListener("focusin", function (e) {
      var c = e.target && e.target.closest && e.target.closest("[data-node]");
      if (c && c.tabIndex !== -1) select(st, c.dataset.node, false);
    });
    el.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && st.sel) { e.preventDefault(); close(st); return; }
      var c = e.target && e.target.closest && e.target.closest("[data-node]");
      if (!c || c.tabIndex === -1) return;
      var to = navTarget(st.ids, c.dataset.node, e.key);
      if (to == null) return;
      e.preventDefault();
      var all = el.querySelectorAll("[data-fk]");
      for (var i = 0; i < all.length; i++) {
        if (all[i].dataset.fk === "n:" + to) { all[i].focus(); if (all[i].scrollIntoView) all[i].scrollIntoView({ block: "nearest", inline: "nearest" }); return; }
      }
    });
    return st;
  }
  function close(st) {
    var id = st.sel;
    if (!id) return;
    st.sel = null;
    paint(st);
    if (st.opts.onSelect) st.opts.onSelect(null);
    var all = st.el.querySelectorAll("[data-fk]");
    for (var i = 0; i < all.length; i++) if (all[i].dataset.fk === "n:" + id) { all[i].focus({ preventScroll: true }); return; }
  }
  function copy(btn) {
    var text = btn.getAttribute("data-copy");
    var done = function () { btn.textContent = "Copied"; setTimeout(function () { btn.textContent = "Copy"; }, 1500); };
    if (typeof navigator !== "undefined" && navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text).then(done, function () {});
  }

  function render(el, project, opts) {
    opts = opts || {};
    var st = STATE.get(el);
    if (!st) { st = init(el); STATE.set(el, st); }
    st.opts = opts;
    var mode = opts.mode || project.default_mode || (project.modes || ["container"])[0];
    var graph = project.workflow && project.workflow[mode];
    var fk = focusKey(el), cv = region(el, "canvas"), sx = cv ? cv.scrollLeft : 0, sy = cv ? cv.scrollTop : 0;
    if (!graph) {
      st.nodes = {}; st.ids = []; st.sel = null;
      setRegion(st, "canvas", '<p class="pw-empty">No startup workflow is recorded for ' + esc(mode) + " mode.</p>");
      setRegion(st, "summary", ""); setRegion(st, "list", ""); setRegion(st, "drafts", "");
      paint(st);
      return;
    }
    var run = currentRun(project, opts, mode), modes = project.modes || ["container", "hybrid"];
    var c = canvas(graph, mode, modes, run);
    st.nodes = {}; (graph.nodes || []).forEach(function (n) { if (n.kind === "step") st.nodes[n.id] = n; });
    st.ids = c.ids; st.run = run; st.runs = run && run.steps ? run.steps : null;
    if ("node" in opts) st.sel = opts.node || null; // controlled selection, when the page keeps it in the URL
    if (st.sel && c.ids.indexOf(st.sel) < 0) st.sel = null;
    var changed = setRegion(st, "canvas", c.html);
    setRegion(st, "summary", summaryHTML(graph, c.ids));
    setRegion(st, "list", listHTML(graph, c.ids, run));
    setRegion(st, "drafts", draftsHTML(graph, c.ids));
    setRegion(st, "palette", paletteHTML());
    paint(st);
    if (changed) {
      var cv2 = region(el, "canvas");
      if (cv2) {
        if (st.mode === mode || st.mode == null) { cv2.scrollLeft = sx; cv2.scrollTop = sy; }
        else { var b = cv2.querySelector && cv2.querySelector('.pw-mode-branch[data-on="1"]'); if (b && b.scrollIntoView) b.scrollIntoView({ inline: "center", block: "nearest" }); }
      }
      restoreFocus(el, fk);
    }
    st.mode = mode;
  }

  window.sfProjectWorkflow = { render: render, _canvas: canvas, _panel: panel, _nav: navTarget, _esc: esc };
})();
