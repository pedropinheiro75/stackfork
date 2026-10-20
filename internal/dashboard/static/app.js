// stackfork dashboard client (design.md D3/D3a). Plain JS, no framework:
// polls /api/instances every 3s while the tab is visible and re-renders the
// stack list, keeping filter/collapse/panel state across refreshes. Every
// POST carries the per-process token from the page's own <meta> tag and
// runs through /api/actions (design.md D4/D5).

(function () {
  "use strict";

  var token = document.querySelector('meta[name="stackfork-token"]').content;

  // setTheme applies "light" | "dark" | "system" and remembers it. "system"
  // removes the pin so prefers-color-scheme decides. localStorage can throw
  // (private mode, blocked site data), so every access is guarded.
  var THEME_KEY = "stackfork-theme";
  var THEME_NEXT = { system: "light", light: "dark", dark: "system" };
  function setTheme(theme) {
    if (theme !== "light" && theme !== "dark") theme = "system";
    if (theme === "system") document.documentElement.removeAttribute("data-theme");
    else document.documentElement.dataset.theme = theme;
    try {
      if (theme === "system") localStorage.removeItem(THEME_KEY);
      else localStorage.setItem(THEME_KEY, theme);
    } catch (e) { /* storage unavailable: the choice lasts for this page only */ }
    var btn = document.getElementById("theme-toggle");
    if (btn) {
      btn.dataset.themeMode = theme;
      btn.textContent = "Theme: " + theme;
      btn.setAttribute("aria-label", "Theme: " + theme + ". Switch to " + THEME_NEXT[theme]);
    }
  }
  window.setTheme = setTheme;
  (function initTheme() {
    var stored = "system";
    try { stored = localStorage.getItem(THEME_KEY) || "system"; } catch (e) { /* ignore */ }
    setTheme(stored);
    var btn = document.getElementById("theme-toggle");
    if (btn) btn.addEventListener("click", function () { setTheme(THEME_NEXT[btn.dataset.themeMode] || "light"); });
  })();

  // post sends a token-bearing POST. The token changes when the dashboard
  // restarts, so on a 403 it reads the current one from the page and
  // retries once: an open tab keeps working across restarts.
  function post(url, body) {
    function send() {
      return fetch(url, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json", "X-Stackfork-Token": token },
        body: JSON.stringify(body),
      });
    }
    return send().then(function (r) {
      if (r.status !== 403) return r;
      return fetch("/", { credentials: "same-origin" })
        .then(function (p) { return p.text(); })
        .then(function (html) {
          var m = html.match(/name="stackfork-token" content="([0-9a-f]+)"/);
          if (!m || m[1] === token) return r;
          token = m[1];
          return send();
        });
    });
  }
  var state = { inventory: null, filter: "", runningOnly: false, stale: false };
  // Ephemeral, session-only UI state that must survive a poll's full
  // re-render (design.md D3a: collapse state also persists across reloads,
  // via localStorage; these two don't need to).
  var openSettings = {};

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function log(msg) {
    var el = document.getElementById("activity-log");
    var line = new Date().toLocaleTimeString() + "  " + msg;
    el.textContent = line + "\n" + el.textContent;
  }

  // ActionToast: success goes to the polite status region and dismisses
  // itself; an error goes to the alert region and stays until dismissed.
  // At most three are stacked. The text is also logged by the callers.
  var TOAST_MS = 5000;
  function toast(tone, text) {
    var err = tone === "failed";
    var region = document.getElementById(err ? "toast-alert" : "toast-status");
    if (!region) return;
    var el = document.createElement("div");
    el.className = "toast toast-" + tone;
    el.innerHTML = '<span class="toast-glyph" aria-hidden="true">' + (GLYPH[tone] || GLYPH.ok) + '</span><span class="toast-text"></span>' +
      '<button type="button" class="btn btn-ghost btn-icon toast-close" aria-label="Dismiss"><svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true" focusable="false"><path d="M4 4l8 8M12 4l-8 8"/></svg></button>';
    el.querySelector(".toast-text").textContent = text;
    var timer = null;
    function dismiss() { clearTimeout(timer); el.remove(); }
    el.querySelector(".toast-close").addEventListener("click", dismiss);
    region.appendChild(el);
    var all = document.querySelectorAll("#toasts .toast");
    for (var i = 0; i < all.length - 3; i++) all[i].remove();
    if (!err) timer = setTimeout(dismiss, TOAST_MS);
  }

  // --- collapse persistence (design.md D3a: "remembered per stack in
  // localStorage, wrapped in try/catch") -------------------------------

  function collapsedKey(project) {
    return "stackfork:collapsed:" + project;
  }

  function isCollapsed(project, defaultCollapsed) {
    try {
      var v = localStorage.getItem(collapsedKey(project));
      if (v === "1") return true;
      if (v === "0") return false;
    } catch (e) {
      // ignore: localStorage unavailable (private window, blocked, ...)
    }
    return defaultCollapsed;
  }

  function setCollapsed(project, collapsed) {
    try {
      localStorage.setItem(collapsedKey(project), collapsed ? "1" : "0");
    } catch (e) {
      // ignore
    }
  }

  // A project (app) is expanded unless the user collapsed it.
  function projectCollapsedKey(app) {
    return "stackfork:project-collapsed:" + app;
  }
  function isProjectCollapsed(app) {
    try { return localStorage.getItem(projectCollapsedKey(app)) === "1"; } catch (e) { return false; }
  }
  function setProjectCollapsed(app, collapsed) {
    try { localStorage.setItem(projectCollapsedKey(app), collapsed ? "1" : "0"); } catch (e) { /* ignore */ }
  }

  // --- rendering ---------------------------------------------------------
  // Instance cards are rendered twice: by the Go template (first paint) and
  // here (every poll). Both emit the same markup, so a change to one is a
  // change to the other. Every focusable element carries a stable data-key
  // so a re-render can hand focus back (see render()).

  var GLYPH = { ok: "●", partial: "◐", attention: "▲", failed: "⊘", busy: "◔", off: "○" };

  function chipHtml(c) {
    return '<span class="chip chip-' + c.cls + '"' + (c.title ? ' title="' + escapeHtml(c.title) + '"' : "") +
      '><span class="chip-glyph" aria-hidden="true">' + c.glyph + "</span>" + escapeHtml(c.label) + "</span>";
  }

  // stateChip/serviceChip must match dashboard/dot.go. Stopped is off (grey):
  // failed is only for something broken.
  function stateChip(state) {
    var c = {
      running: ["ok", GLYPH.ok, "Running"], partial: ["attention", GLYPH.partial, "Partial"],
      stopped: ["off", GLYPH.off, "Stopped"], "missing-checkout": ["failed", GLYPH.failed, "Checkout missing"],
      "missing-branch": ["failed", GLYPH.failed, "Branch missing"],
    }[state] || ["attention", "?", "Unknown"];
    return { cls: c[0], glyph: c[1], label: c[2] };
  }

  function serviceChip(svc) {
    var r = { cls: "off", glyph: GLYPH.off, label: "", title: dotTitle(svc) };
    var health = svc.health;
    if (svc.state === "running" && health === "unhealthy") { r.cls = "attention"; r.glyph = GLYPH.attention; r.label = "Unhealthy"; }
    else if (svc.state === "running" && health === "health: starting") { r.cls = "busy"; r.glyph = GLYPH.busy; r.label = "Starting…"; }
    else if (svc.state === "running") { r.cls = "ok"; r.glyph = GLYPH.ok; r.label = "Running"; }
    else if (svc.state === "dead") { r.cls = "failed"; r.glyph = GLYPH.failed; r.label = "Dead"; }
    else if (svc.state === "restarting") { r.cls = "attention"; r.glyph = GLYPH.attention; r.label = "Restarting"; }
    else if (svc.state === "waiting") { r.cls = "busy"; r.glyph = GLYPH.busy; r.label = "Waiting"; }
    else if (svc.state === "not-started") r.label = "Not started";
    else if (svc.state === "optional") r.label = "Optional";
    else if (svc.state === "exited" || svc.state === "stopped") r.label = "Stopped";
    else if (!svc.state) r.label = "Unknown";
    else r.label = svc.state.charAt(0).toUpperCase() + svc.state.slice(1);
    return r;
  }

  function dotTitle(svc) {
    if (svc.state === "optional") return "not started (optional)";
    if (svc.state === "not-started") return "not started";
    if (svc.state === "waiting") return "waiting for " + (svc.waiting_for || []).join(", ") + " to become healthy";
    if (svc.state === "running" && svc.health === "unhealthy") return "running, unhealthy";
    if (svc.state === "running" && svc.health === "health: starting") return "running, health check starting";
    return svc.state || "";
  }

  function plural(n, one) { return n + " " + one + (n === 1 ? "" : "s"); }

  // displayName must match dot.go's displayName: the slug without its hash.
  function displayName(project) {
    return projectSlug(project).replace(/-[0-9a-f]{12}$/, "") || project;
  }

  function cliCommand(warning) {
    var m = /`([^`]+)`/.exec(warning);
    return m ? m[1] : null;
  }

  // commands: the down command first, then whatever the warnings name.
  function commandsOf(inst) {
    var cmds = ["stackfork down --instance " + inst.project + " --volumes"];
    (inst.warnings || []).forEach(function (w) {
      var cmd = cliCommand(w);
      if (cmd && cmds.indexOf(cmd) === -1) cmds.push(cmd);
    });
    return cmds;
  }

  // OverflowMenu markup. attrs is the item's own attribute string.
  function menuItem(key, label, cmd, attrs, cls) {
    return '<button type="button" role="menuitem" class="menu-item' + (cls ? " " + cls : "") + '" tabindex="-1" data-key="' + escapeHtml(key) + '"' +
      (attrs || "") + '><span class="menu-label">' + escapeHtml(label) + "</span>" +
      (cmd ? '<span class="menu-cmd mono">' + escapeHtml(cmd) + "</span>" : "") + "</button>";
  }

  function menuHtml(key, label, title, items) {
    return '<div class="menu-anchor"><button type="button" class="btn btn-ghost btn-icon" data-key="' + escapeHtml(key) +
      '" aria-haspopup="menu" aria-expanded="false" aria-label="' + escapeHtml(label) + '" title="' + escapeHtml(title) +
      '"><span aria-hidden="true">⋯</span></button><div class="menu" role="menu" hidden>' + items + "</div></div>";
  }

  // Only http(s) URLs become links (html/template's guarantee on first paint).
  function isWebUrl(u) { return /^https?:\/\//.test(u || ""); }

  // The knowledge-only host app chip never prints its hint inline: it sits in
  // the tooltip and in the version menu (hostHint). Must match the template.
  function appChipHtml(app, i, project) {
    if (isWebUrl(app.url) && app.running) {
      var title = app.url + (app.direct ? " (direct: " + app.direct + ")" : "");
      return '<a class="app-chip" data-key="' + escapeHtml(project) + '/app' + i + '" href="' + escapeHtml(app.url) + '" target="_blank" rel="noopener" title="' + escapeHtml(title) + '">' + escapeHtml(app.label) + "</a>";
    }
    var l = escapeHtml(app.label);
    if (app.url) return '<span class="app-chip stopped" title="Starts with the stack">' + l + "</span>";
    if (app.source === "knowledge") return '<span class="app-chip stopped host" title="' + escapeHtml(app.hint || "") + '">' + l + " · on host</span>";
    if (app.hint) return '<span class="app-chip stopped" title="Starts with the stack">' + l + " · " + escapeHtml(app.hint) + "</span>";
    return '<span class="app-chip stopped" title="Starts with the stack">' + l + "</span>";
  }

  function hostHint(apps) {
    var h = "";
    (apps || []).forEach(function (a) { if (!h && a.source === "knowledge" && a.hint) h = a.hint; });
    return h;
  }

  // endpointNote must match dot.go's endpointNote.
  function endpointNote(state, proxied) {
    var parts = [];
    if (state !== "running") parts.push(state === "optional" || state === "not-started" || state === "waiting" ? "(not started)" : "(stopped)");
    if (proxied) parts.push("(proxy)");
    return parts.join(" ");
  }

  function endpointHtml(ep, svcState, key) {
    if (!ep.known) {
      return '<span class="endpoint-pending" title="container port ' + escapeHtml(ep.container) + '">port assigned on start</span>';
    }
    var stopped = svcState === "running" ? "" : " stopped";
    if ((ep.scheme === "http" || ep.scheme === "https") && isWebUrl(ep.url)) {
      var note = endpointNote(svcState, ep.proxied);
      return '<a class="endpoint-link' + stopped + '" data-key="' + escapeHtml(key) + '" href="' + escapeHtml(ep.url) + '" target="_blank" rel="noopener" title="' + escapeHtml(ep.url) + '">' +
        '<span class="endpoint-url">' + escapeHtml(ep.url) + "</span>" + (note ? '<span class="endpoint-note">' + note + "</span>" : "") + "</a>";
    }
    var n = endpointNote(svcState, false);
    return '<button type="button" class="endpoint-copy copyable' + stopped + '" data-key="' + escapeHtml(key) + '" data-copy="' + escapeHtml(ep.url) + '" title="click to copy">' +
      escapeHtml(ep.url) + (n ? " " + n : "") + "</button>";
  }

  function settingRow(label, values) {
    if (!values || values.length === 0) return "";
    var html = values.map(function (v) { return '<span class="setting-value">' + v + "</span>"; }).join("");
    return '<div class="setting-row"><span class="setting-label">' + escapeHtml(label) + '</span><span class="setting-values">' + html + "</span></div>";
  }

  function settingsHtml(svc, files) {
    var out = "";
    out += settingRow("Ports", (svc.ports || []).map(function (p) {
      return escapeHtml(p.container + (p.host ? " → " + p.host : "") + "/" + p.protocol);
    }));
    out += settingRow("Volumes", (svc.volumes || []).map(function (v) {
      return escapeHtml(v.name + " → " + v.path + (v.read_only ? " (ro)" : ""));
    }));
    if (svc.healthcheck) {
      out += '<div class="setting-row"><span class="setting-label">Healthcheck</span><code class="setting-value">' + escapeHtml(svc.healthcheck) + "</code></div>";
    }
    out += settingRow("Depends on", (svc.depends_on || []).map(escapeHtml));
    if (svc.replicas) {
      out += '<div class="setting-row"><span class="setting-label">Replicas</span><span class="setting-value">' + svc.replicas + "</span></div>";
    }
    out += settingRow("Environment", (svc.env_keys || []).map(escapeHtml));
    out += settingRow("Source files", (files || []).map(escapeHtml));
    return out;
  }

  function oneOffHtml(inst) {
    if (!inst.oneoff_running && !inst.oneoff_leftover) return "";
    var h = '<div class="callout callout-attention" data-oneoff="1"><span class="callout-glyph" aria-hidden="true">' + GLYPH.attention + '</span><div class="callout-body">';
    if (inst.oneoff_running) h += '<p class="callout-detail">' + plural(inst.oneoff_running, "one-off task") + " running</p>";
    if (inst.oneoff_leftover) {
      h += '<p class="callout-detail">' + plural(inst.oneoff_leftover, "leftover one-off container") +
        (inst.oneoff_cleanup ? ' <span class="mono">' + escapeHtml(inst.oneoff_cleanup) + "</span>" : "") + "</p>";
    }
    h += "</div>";
    if (inst.oneoff_leftover && inst.oneoff_cleanup) {
      h += '<div class="callout-actions"><button type="button" class="btn" data-run="' + escapeHtml(inst.oneoff_cleanup) + '" data-key="' +
        escapeHtml(inst.project) + '/oneoff/clean" title="' + escapeHtml(inst.oneoff_cleanup) + '">Clean up</button></div>';
    }
    return h + "</div>";
  }

  // --- the last run on the card (design.md D14); must match runcard.go ---------

  // agoText must match dashboard.go's ago.
  function agoText(iso) {
    var d = Date.now() - Date.parse(iso);
    var h = d / 3600000;
    if (isNaN(d)) return "\u2014";
    if (d < 60000) return "just now";
    if (d < 3600000) return Math.floor(d / 60000) + " min ago";
    if (h < 24) return Math.floor(h) + " h ago";
    if (h < 7 * 24) return Math.floor(h / 24) + " d ago";
    if (h < 60 * 24) return Math.floor(h / (24 * 7)) + " w ago";
    if (h < 365 * 24) return Math.floor(h / (24 * 30)) + " mo ago";
    return Math.floor(h / (24 * 365)) + " y ago";
  }

  function isStartEnvRetry(cmd) { return /^stackfork --root .* start-env$/.test(cmd || ""); }

  // The Starting… chip replaces the state chip while a run is in progress,
  // whoever started it (a terminal included).
  function startingChip(inst) {
    var lr = inst.last_run;
    if (!lr || lr.outcome !== "running") return null;
    return { cls: "busy", glyph: GLYPH.busy, label: "Starting…", title: lr.step ? "step " + lr.step_n + " of " + lr.step_total + ": " + lr.step : "Start in progress" };
  }

  // A failed seed step is reported by the seed callout; the card never shows
  // two callouts for one failure.
  function seedCovers(inst) {
    var lr = inst.last_run;
    return !!(lr && lr.outcome === "failed" && inst.seed_command && lr.failed_step && inst.seed_run === lr.id && inst.seed_step === lr.failed_step);
  }

  function runLogAttrs(project, run, step) {
    return ' data-run-log data-project="' + escapeHtml(project) + '" data-run-id="' + run + '" data-step="' + escapeHtml(step || "") + '"';
  }

  function runCalloutHtml(inst, name) {
    var lr = inst.last_run, p = inst.project;
    if (!lr || (lr.outcome !== "failed" && lr.outcome !== "interrupted") || seedCovers(inst)) return "";
    var interrupted = lr.outcome === "interrupted", step = lr.failed_step || "";
    var title = (interrupted ? "Start interrupted" : "Start failed") + (step ? ' at step <code class="mono">' + escapeHtml(step) + "</code>" : "");
    var retry = "";
    if (isStartEnvRetry(lr.retry)) {
      retry = '<button type="button" class="btn" data-repo-action="start" data-repo="' + escapeHtml(repoRoot(inst.root)) + '" data-path="' + escapeHtml(inst.root) +
        '" data-key="' + escapeHtml(p) + '/run/retry" title="Start again">Retry</button>';
    } else if (lr.retry) {
      retry = '<button type="button" class="btn" data-run="' + escapeHtml(lr.retry) + '" data-key="' + escapeHtml(p) + '/run/retry" title="' + escapeHtml(lr.retry) + '">Retry</button>';
    }
    var more = step ? menuHtml(p + "/run/menu", "More run actions for " + name, "More run actions",
      menuItem(p + "/run/viewlog", "View log", "", runLogAttrs(p, lr.id, step))) : "";
    return '<div class="callout callout-' + (interrupted ? "attention" : "failed") + '" data-run="' + escapeHtml(lr.outcome) + '"><span class="callout-glyph" aria-hidden="true">' +
      (interrupted ? GLYPH.attention : GLYPH.failed) + '</span><div class="callout-body"><p class="callout-title">' + title + "</p>" +
      (lr.tail ? '<pre class="mono-log" tabindex="0" data-key="' + escapeHtml(p) + '/run/log" aria-label="Output of step ' + escapeHtml(step) + '">' + escapeHtml(lr.tail) + "</pre>" : "") +
      '</div><div class="callout-actions">' + retry + more + "</div></div>";
  }

  // The version menu's Last run item, for a run that succeeded.
  function lastRunItem(inst) {
    var lr = inst.last_run;
    if (!lr || lr.outcome !== "succeeded") return "";
    var text = "succeeded \u00B7 " + fmtDur(Date.parse(lr.ended_at) - Date.parse(lr.started_at)) + " \u00B7 " + agoText(lr.started_at);
    return menuItem(inst.project + "/menu/lastrun", "Last run", text, runLogAttrs(inst.project, lr.id, ""));
  }

  // The seed callout: Retry seed is the button; Mark as seeded and the
  // overwriting Re-seed live in the menu, each with its command in mono.
  function seedHtml(inst, name) {
    if (!inst.seed_command) return "";
    var k = inst.project;
    return '<div class="callout callout-failed" data-seed="failed"><span class="callout-glyph" aria-hidden="true">' + GLYPH.failed + "</span>" +
      '<div class="callout-body"><p class="callout-title">Database seed failed</p>' +
      '<p class="callout-detail">Retry runs it again. If the app already has its data, mark it as seeded instead.</p>' +
      (inst.seed_error ? '<pre class="mono-log" tabindex="0" data-key="' + escapeHtml(k) + '/seed/log" aria-label="Seed output">' + escapeHtml(inst.seed_error) + "</pre>" : "") + "</div>" +
      '<div class="callout-actions"><button type="button" class="btn" data-run="' + escapeHtml(inst.seed_command) + '" data-key="' + escapeHtml(k) +
      '/seed/retry" title="' + escapeHtml(inst.seed_command) + '">Retry seed</button>' +
      menuHtml(k + "/seed/menu", "More seed actions for " + name, "More seed actions",
        (inst.seed_run ? menuItem(k + "/seed/viewlog", "View log", "", runLogAttrs(k, inst.seed_run, inst.seed_step)) : "") +
        menuItem(k + "/seed/done", "Mark as seeded", inst.seed_done, ' data-run="' + escapeHtml(inst.seed_done) + '"') +
        '<hr class="menu-sep" role="separator">' +
        menuItem(k + "/seed/force", "Re-seed, overwriting data", inst.seed_force, ' data-run="' + escapeHtml(inst.seed_force) + '"', "menu-item-danger")) +
      "</div></div>";
  }

  function serviceHtml(inst, svc) {
    var p = inst.project;
    var key = p + "/" + svc.name;
    var running = svc.state === "running";
    var endpoints = (svc.endpoints || []).map(function (e, i) { return endpointHtml(e, svc.state, key + "/ep" + i); }).join("");
    if (svc.connection_url) {
      endpoints += '<button type="button" class="endpoint-copy copyable connection-url" data-key="' + escapeHtml(key) + '/conn" data-copy="' + escapeHtml(svc.connection_url) +
        '" data-copy-kind="database_url" data-copy-project="' + escapeHtml(p) + '" data-copy-service="' + escapeHtml(svc.name) +
        '" title="click to copy the connection URL (' + escapeHtml(svc.connection_url) + ')">connection URL</button>';
    }
    (svc.use_commands || []).forEach(function (c, i) {
      endpoints += '<button type="button" class="endpoint-copy copyable use-command" data-key="' + escapeHtml(key) + '/use' + i + '" data-copy="' + escapeHtml(c.value) +
        '" data-copy-kind="cli" data-copy-project="' + escapeHtml(p) + '" data-copy-service="' + escapeHtml(svc.name) +
        '" title="click to copy: ' + escapeHtml(c.value) + '">' + escapeHtml(c.label) + "</button>";
    });
    endpoints = endpoints ? '<div class="endpoints">' + endpoints + "</div>" : '<span class="endpoint-none">—</span>';
    var image = svc.state === "optional" ? '<span class="service-image mono">not started (optional)</span>'
      : svc.image ? '<span class="service-image mono" title="' + escapeHtml(svc.image) + '">' + escapeHtml(svc.image) + "</span>" : "";
    var settingsOpen = !!openSettings[key];
    var pa = ' data-project="' + escapeHtml(p) + '" data-service="' + escapeHtml(svc.name) + '"';
    return (
      '<tbody class="service-group"><tr class="service-row" data-service="' + escapeHtml(svc.name) + '">' +
      '<td class="cell-service"><span class="service-logo" data-kind="' + escapeHtml(svc.kind || "") + '"></span>' +
      '<div class="service-text"><button type="button" class="service-name" data-toggle-settings data-key="' + escapeHtml(key) +
      '/settings" aria-expanded="' + settingsOpen + '">' + escapeHtml(svc.label || svc.name) + "</button>" + image + "</div></td>" +
      '<td class="cell-state">' + chipHtml(serviceChip(svc)) + "</td>" +
      '<td class="cell-endpoints">' + endpoints + "</td>" +
      '<td class="cell-actions"><div class="row-actions"><button type="button" class="btn btn-ghost logs-button mobile-hide"' + pa +
      ' data-key="' + escapeHtml(key) + '/logs">Logs</button>' +
      menuHtml(key + "/menu", "More actions for " + (svc.label || svc.name), "More actions",
        (running
          ? menuItem(key + "/menu/stop", "Stop", "", ' data-action="service-stop"' + pa)
          : menuItem(key + "/menu/start", "Start", "", ' data-action="service-start"' + pa)) +
        menuItem(key + "/menu/restart", "Restart", "", ' data-action="restart"' + pa) +
        menuItem(key + "/menu/logs", "Logs", "", pa, "logs-button mobile-only")) +
      "</div></td></tr>" +
      '<tr class="service-settings"' + (settingsOpen ? "" : " hidden") + '><td colspan="4">' + settingsHtml(svc, inst.files) + "</td></tr></tbody>"
    );
  }

  function isLive(inst) { return inst.state === "running" || inst.state === "partial"; }

  function versionHtml(inst) {
    var p = escapeHtml(inst.project);
    var app = appName(inst);
    var name = app + " " + inst.branch;
    var hint = hostHint(inst.apps);
    var live = isLive(inst);
    var collapsed = isCollapsed(inst.project, !live);
    var cmds = commandsOf(inst);
    var down = cmds[0];
    var openApp = null;
    (inst.apps || []).forEach(function (a) { if (!openApp && isWebUrl(a.url) && a.running) openApp = a; });

    var items = "";
    if (inst.state === "partial") items += menuItem(inst.project + "/menu/up", "Start stopped services", "", ' data-action="up" data-project="' + p + '"');
    if (live) {
      items += menuItem(inst.project + "/menu/restart", "Restart", "", ' data-action="restart" data-project="' + p + '"');
      items += menuItem(inst.project + "/menu/down", "Stop", "", ' data-action="down" data-project="' + p + '"', "mobile-only");
    }
    items += lastRunItem(inst);
    items += menuItem(inst.project + "/menu/path", "Copy path", "", ' data-copy="' + escapeHtml(inst.root) + '"');
    if (hint) items += menuItem(inst.project + "/menu/host", "How to run the host app", hint, ' data-copy="' + escapeHtml(hint) + '" title="' + escapeHtml(hint) + '"');
    if (inst.env_block) {
      items += menuItem(inst.project + "/menu/env", "Copy .env for host app", "",
        ' data-copy="' + escapeHtml(inst.env_block) + '" data-copy-kind="env_block" data-copy-project="' + p + '"');
    }
    cmds.slice(1).forEach(function (c, i) {
      var run = runnable(c);
      items += menuItem(inst.project + "/menu/cmd" + i, run ? "Run command" : "Copy command", c, (run ? " data-run" : " data-copy") + '="' + escapeHtml(c) + '"');
    });
    items += menuItem(inst.project + "/menu/copydown", "Copy down command", down, ' data-copy="' + escapeHtml(down) + '"');
    items += '<hr class="menu-sep" role="separator">';
    items += menuItem(inst.project + "/menu/remove", "Remove instance and data", down, ' data-run="' + escapeHtml(down) + '"', "menu-item-danger");

    var chips = '<div class="stack-chips">' + chipHtml(startingChip(inst) || stateChip(inst.state)) + (inst.legacy ? '<span class="chip chip-off">legacy</span>' : "") +
      (inst.knowledge && (inst.knowledge.status === "draft" || inst.knowledge.status === "stale")
        ? '<span class="chip chip-attention" title="Setup knowledge: ' + escapeHtml(inst.knowledge.status) + '"><span class="chip-glyph" aria-hidden="true">' + GLYPH.attention + "</span>Setup unverified</span>" : "") + "</div>";
    var meta = '<div class="stack-meta mono"><span class="stack-path" title="' + escapeHtml(inst.root) + '">' + escapeHtml(inst.rel_root || inst.root) + "</span>" +
      (inst.hostname ? '<span class="stack-sep" aria-hidden="true"> · </span><span class="stack-host" title="' + escapeHtml(inst.hostname) + '">' + escapeHtml(inst.hostname) + "</span>" : "") + "</div>";
    var actions =
      (!live ? '<button type="button" class="btn" data-action="up" data-project="' + p + '" data-key="' + p + '/up">Start</button>' : "") +
      (live && openApp && isWebUrl(openApp.url) ? '<a class="btn" href="' + escapeHtml(openApp.url) + '" target="_blank" rel="noopener" data-key="' + p + '/open" title="' + escapeHtml(openApp.url) + '">Open App</a>' : "") +
      (live ? '<button type="button" class="btn btn-ghost mobile-hide" data-action="down" data-project="' + p + '" data-key="' + p + '/down" title="Stop (keeps volumes)">Stop</button>' : "") +
      menuHtml(inst.project + "/menu", "More actions for " + name, "More actions", items);

    var warnings = (inst.warnings || []).length
      ? '<div class="callout callout-attention" data-callout="warnings"><span class="callout-glyph" aria-hidden="true">' + GLYPH.attention + '</span><div class="callout-body"><ul class="warnings">' +
        inst.warnings.map(function (w) { return '<li class="warning">' + escapeHtml(w) + "</li>"; }).join("") + "</ul></div></div>"
      : "";
    var services = (inst.services || []).map(function (s) { return serviceHtml(inst, s); }).join("");
    var table = services
      ? '<table class="service-table"><caption class="sr-only">Services of ' + escapeHtml(name) + '</caption><colgroup><col class="col-service"><col class="col-state"><col class="col-endpoints"><col class="col-actions"></colgroup>' +
        '<thead><tr><th scope="col" class="micro">Service</th><th scope="col" class="micro">State</th><th scope="col" class="micro">Endpoints</th><th scope="col" class="micro">Actions</th></tr></thead>' + services + "</table>"
      : "";
    var appStrip = (inst.apps || []).map(function (a, i) { return appChipHtml(a, i, inst.project); }).join("");
    var id = domId(inst.project);
    return (
      '<article class="stack version lane-' + (stackColorIndex(app) + 1) + '" id="ver-' + id + '" data-project="' + p + '" data-state="' + escapeHtml(inst.state) + '" data-collapsed="' + (collapsed ? "1" : "0") + '">' +
      '<header class="stack-header">' +
      '<button type="button" class="btn btn-ghost btn-icon chevron" data-toggle-stack data-project="' + p + '" data-key="' + p + '/toggle" aria-expanded="' + !collapsed +
      '" aria-controls="body-' + id + '" aria-label="Details for ' + escapeHtml(name) + '"><span class="chevron-glyph" aria-hidden="true"></span></button>' +
      '<div class="stack-id"><h3 class="version-title" title="' + p + '"><span class="branch-pill" title="' + escapeHtml(inst.branch) + '">' + escapeHtml(inst.branch) + "</span></h3>" +
      chips + '<div class="stack-actions">' + actions + "</div>" + meta + "</div></header>" +
      (appStrip ? '<div class="app-strip">' + appStrip + "</div>" : "") +
      seedHtml(inst, name) + runCalloutHtml(inst, name) + warnings + oneOffHtml(inst) +
      '<div class="stack-body" id="body-' + id + '">' + table + "</div></article>"
    );
  }

  // projectSlug must match dashboard.go's projectSlug: strip the sf-/idv-
  // instance prefix.
  function projectSlug(project) {
    if (project.indexOf("sf-") === 0) return project.slice(3);
    if (project.indexOf("idv-") === 0) return project.slice(3);
    return project;
  }

  // appName must match dashboard.go's appName: the main checkout folder,
  // else the project's display name.
  function appName(inst) { return inst.app || displayName(inst.project); }

  // initials must match dashboard.go's initials: first letter of the first
  // two words (split on - _ . space and camelCase), or the first two letters
  // of a one-word name.
  function initials(name) {
    var words = [], cur = [], prev = "";
    Array.from(name).forEach(function (c) {
      if (/[-_.\s]/.test(c)) { if (cur.length) words.push(cur); cur = []; }
      else if (cur.length && c !== c.toLowerCase() && c === c.toUpperCase() && prev !== prev.toUpperCase()) { words.push(cur); cur = [c]; }
      else cur.push(c);
      prev = c;
    });
    if (cur.length) words.push(cur);
    if (!words.length) return "?";
    var out = words.length === 1 ? words[0].slice(0, 2) : [words[0][0], words[1][0]];
    return out.join("").toUpperCase();
  }

  // stackColorIndex must match dashboard.go's stackColorIndex exactly: a
  // plain byte sum of the app name mod the palette size, never a real hash,
  // so the two sides agree without sharing code. (UTF-8 bytes, like Go.)
  function stackColorIndex(app) {
    var bytes = new TextEncoder().encode(app);
    var sum = 0;
    for (var i = 0; i < bytes.length; i++) sum += bytes[i];
    return sum % 8;
  }

  function appOfProject(project) {
    var found = ((state.inventory || {}).instances || []).filter(function (i) { return i.project === project; })[0];
    return found ? appName(found) : displayName(project);
  }

  function domId(s) {
    return Array.from(String(s)).map(function (c) { return /[A-Za-z0-9_-]/.test(c) ? c : "-"; }).join("");
  }

  // repoRoot must match dashboard.go's repoRoot: the main checkout.
  function repoRoot(root) {
    var i = root.lastIndexOf("/.worktrees/");
    return i > 0 ? root.slice(0, i) : root;
  }

  // groupProjects must sort exactly like dashboard.go's buildProjects:
  // projects with a live version first, then the newest activity, then name;
  // inside a project live versions, the main checkout, then branch.
  function groupProjects(instances) {
    var by = {}, list = [];
    instances.forEach(function (inst) {
      var app = appName(inst);
      if (!Object.prototype.hasOwnProperty.call(by, app)) {
        by[app] = { app: app, root: repoRoot(inst.root), versions: [], live: false, last: 0 };
        list.push(by[app]);
      }
      var g = by[app];
      g.versions.push(inst);
      if (isLive(inst)) g.live = true;
      var ms = Date.parse(inst.used);
      if (ms > g.last) g.last = ms;
    });
    list.forEach(function (g) {
      g.versions.sort(function (a, b) {
        if (isLive(a) !== isLive(b)) return isLive(a) ? -1 : 1;
        if (!!a.is_main !== !!b.is_main) return a.is_main ? -1 : 1;
        if (a.branch !== b.branch) return a.branch < b.branch ? -1 : 1;
        return a.project < b.project ? -1 : a.project > b.project ? 1 : 0;
      });
    });
    list.sort(function (a, b) {
      if (a.live !== b.live) return a.live ? -1 : 1;
      if (a.last !== b.last) return b.last - a.last;
      return a.app < b.app ? -1 : a.app > b.app ? 1 : 0;
    });
    return list;
  }

  function versionMatches(inst, needle) {
    return (inst.project + " " + inst.branch + " " + inst.root + " " + (inst.rel_root || "") + " " + (inst.hostname || "")).toLowerCase().indexOf(needle) !== -1;
  }

  // Filter: an app-name match shows the whole project, otherwise only the
  // matching versions; Running only drops stopped versions first. Projects
  // left empty disappear; hidden is how many versions were dropped.
  function visibleProjects(instances) {
    var needle = state.filter.toLowerCase();
    var out = [];
    groupProjects(instances).forEach(function (g) {
      var appMatch = !!needle && g.app.toLowerCase().indexOf(needle) !== -1;
      var vs = g.versions.filter(function (v) {
        return (!state.runningOnly || isLive(v)) && (!needle || appMatch || versionMatches(v, needle));
      });
      if (!vs.length) return;
      out.push({ app: g.app, root: g.root, total: g.versions.length, hidden: g.versions.length - vs.length, all: g.versions, versions: vs });
    });
    return out;
  }

  function projectChips(all) {
    var running = all.filter(function (v) { return v.state === "running"; }).length;
    var partial = all.filter(function (v) { return v.state === "partial"; }).length;
    var chips = [];
    if (running) chips.push({ cls: "ok", glyph: GLYPH.ok, label: running + " running" });
    if (partial) chips.push({ cls: "attention", glyph: GLYPH.partial, label: partial + " partial" });
    if (!running && !partial) chips.push({ cls: "off", glyph: GLYPH.off, label: "Stopped" });
    return chips;
  }

  function projectHtml(g) {
    var app = g.app, id = domId(app), a = escapeHtml(app);
    var collapsed = isProjectCollapsed(app);
    var count = plural(g.total, "version") + (g.hidden ? " \u00B7 " + g.hidden + " hidden" : "");
    return (
      '<section class="project lane-' + (stackColorIndex(app) + 1) + '" id="proj-' + id + '" data-app="' + a + '" data-collapsed="' + (collapsed ? "1" : "0") + '" aria-labelledby="proj-' + id + '-h">' +
      '<header class="project-header">' +
      '<button type="button" class="btn btn-ghost btn-icon chevron" data-toggle-project data-app="' + a + '" data-key="' + a + '/toggle" aria-expanded="' + !collapsed +
      '" aria-controls="proj-' + id + '-body" aria-label="Versions of ' + a + '"><span class="chevron-glyph" aria-hidden="true"></span></button>' +
      '<span class="avatar" aria-hidden="true">' + escapeHtml(initials(app)) + "</span>" +
      '<div class="project-id"><h2 class="project-name" id="proj-' + id + '-h">' + a + "</h2>" +
      '<span class="project-count">' + count + "</span></div>" +
      '<div class="project-chips">' + projectChips(g.all).map(chipHtml).join("") + "</div>" +
      menuHtml(app + "/menu", "More actions for " + app, "More actions",
        menuItem(app + "/menu/start", "Start a branch…", "", ' data-project-action="start" data-repo="' + escapeHtml(g.root) + '"') +
        menuItem(app + "/menu/repos", "Show in Repos", "", ' data-project-action="repos" data-repo="' + escapeHtml(g.root) + '"') +
        menuItem(app + "/menu/path", "Copy repo path", "", ' data-copy="' + escapeHtml(g.root) + '"')) +
      "</header>" +
      '<div class="project-pills">' + g.versions.map(function (v) { return '<span class="branch-pill" title="' + escapeHtml(v.branch) + '">' + escapeHtml(v.branch) + "</span>"; }).join("") + "</div>" +
      '<div class="project-body" id="proj-' + id + '-body">' + g.versions.map(versionHtml).join("") + "</div></section>"
    );
  }

  // lastCards is the HTML last written to the list. A poll that brings the
  // same data changes nothing in the DOM; one that differs remembers the
  // focused element, the open menu and the scroll position, writes, and
  // puts them back (the cards are replaced wholesale).
  var lastCards = null;

  function keyedIn(scope, key) {
    var els = document.querySelectorAll(scope + " [data-key]");
    for (var i = 0; i < els.length; i++) if (els[i].dataset.key === key) return els[i];
    return null;
  }
  function keyed(key) { return keyedIn("#view-instances", key); }

  function writeProjects(groups) {
    var html = groups.map(projectHtml).join("");
    if (html === lastCards) return;
    lastCards = html;
    var active = document.activeElement;
    var focusKey = active && active.closest && active.closest("#view-instances") && active.dataset ? active.dataset.key : "";
    var menu = document.querySelector('#view-instances [aria-haspopup="menu"][aria-expanded="true"]');
    var menuKey = menu ? menu.dataset.key : "";
    var scrollY = window.scrollY;
    document.getElementById("projects").innerHTML = html;
    Object.keys(inFlight).forEach(function (k) {
      var el = keyed(k);
      if (el) markRunning(el);
    });
    if (menuKey) {
      var trigger = keyed(menuKey);
      if (trigger) openMenu(trigger, false);
    }
    if (focusKey) {
      var again = keyed(focusKey);
      if (again && again !== document.activeElement) again.focus({ preventScroll: true });
    }
    if (window.scrollY !== scrollY) window.scrollTo(0, scrollY);
  }

  function render() {
    var inv = state.inventory;
    if (!inv) return;
    var all = inv.instances || [];
    var groups = visibleProjects(all);

    writeProjects(groups);
    document.getElementById("instances-empty").hidden = all.length > 0;
    document.getElementById("instances-none").hidden = all.length === 0 || groups.length > 0;

    document.getElementById("view-instances").classList.toggle("stale", state.stale);

    document.getElementById("rail-count-instances").textContent = all.filter(isLive).length + "/" + all.length;

    setStatus("docker-status", inv.docker.reachable ? "ok" : "failed", "Docker", inv.docker.reachable ? "ok" : "unreachable");
    setStatus("proxy-status", inv.proxy.running ? "ok" : "off", "Proxy", inv.proxy.running ? ":" + inv.proxy.port : "stopped");
    document.getElementById("proxy-start").hidden = inv.proxy.running;

    document.getElementById("docker-banner").hidden = inv.docker.reachable;
    var proxyBanner = document.getElementById("proxy-banner");
    document.getElementById("proxy-banner-text").textContent = inv.proxy.hint || "";
    proxyBanner.hidden = !inv.proxy.hint || proxyHintDismissed === inv.proxy.hint;
  }

  // StatusPill: glyph + word, never colour alone. state is ok|busy|failed|off.
  var statusGlyphs = { ok: "\u25CF", busy: "\u25D4", failed: "\u25B2", off: "\u25CB" };
  var proxyHintDismissed = "";
  function setStatus(id, st, name, word) {
    var el = document.getElementById(id);
    el.dataset.state = st;
    el.className = "chip chip-" + st + " status-pill";
    el.querySelector(".status-glyph").textContent = statusGlyphs[st];
    el.querySelector(".status-text").textContent = name + " \u00B7 " + word;
  }

  function poll() {
    fetch("/api/instances", { credentials: "same-origin" })
      .then(function (r) {
        if (!r.ok) throw new Error("status " + r.status);
        return r.json();
      })
      .then(function (inv) {
        state.inventory = inv;
        state.stale = !inv.docker.reachable;
        render();
      })
      .catch(function () {
        // Keep the last known data, marked stale (design.md D6).
        state.stale = true;
        render();
      });
  }

  function runAction(action, project, service) {
    return post("/api/actions", { action: action, project: project, service: service || "" })
      .then(function (r) { return r.json().then(function (body) { return { ok: r.ok, body: body }; }); })
      .then(function (result) {
        var msg = project + " " + action + ": " + (result.body.message || (result.ok ? "ok" : "failed"));
        log(msg);
        toast(result.ok && result.body.ok !== false ? "ok" : "failed", msg);
      })
      .catch(function (err) { var msg = project + " " + action + ": " + err.message; log(msg); toast("failed", msg); });
  }

  // fetchCopyValue is D3b's one exception: a placeholder-bearing pill's
  // real value, resolved server-side and never logged or rendered - only
  // ever handed straight to the clipboard.
  function fetchCopyValue(kind, project, service) {
    return post("/api/copy", { project: project, service: service || "", kind: kind }).then(function (r) {
      if (!r.ok) throw new Error("copy failed");
      return r.json().then(function (body) { return body.value; });
    });
  }

  // LogDrawer: a right-side, non-modal dialog. Focus moves in on open and
  // returns to the opener on close; Escape closes it. The opener is found
  // again by data-key because a poll can replace the button meanwhile.
  var drawerOpener = null;

  function openerOf(el) {
    el = el || document.activeElement;
    return el && el !== document.body ? { node: el, key: el.dataset.key || "" } : null;
  }

  function focusBack(o) {
    if (!o) return;
    var el = o.node.isConnected ? o.node : o.key && (keyed(o.key) || keyedIn("#view-repos", o.key));
    // an item inside a closed menu cannot take focus: use the menu's button
    if ((!el || !el.offsetParent) && o.key) {
      var menuKey = o.key.replace(/\/menu\/[^/]+$/, "/menu");
      el = keyed(menuKey) || keyedIn("#view-repos", menuKey) || el;
    }
    if (el) el.focus();
  }

  function openDrawer(title, project, bodyText, opener) {
    var panel = document.getElementById("logs-panel");
    if (panel.hidden) drawerOpener = openerOf(opener);
    panel.className = "log-drawer" + (project ? " lane-" + (stackColorIndex(appOfProject(project)) + 1) : "");
    document.getElementById("logs-title").textContent = title;
    document.getElementById("logs-body").textContent = bodyText;
    document.getElementById("logs-run").hidden = true;
    stopRunFollow();
    panel.hidden = false;
    // after the click finishes: a closing menu would otherwise take focus back
    setTimeout(function () { if (!panel.hidden) panel.focus(); }, 0);
  }

  function closeDrawer() {
    var panel = document.getElementById("logs-panel");
    if (panel.hidden) return;
    panel.hidden = true;
    stopRunFollow();
    focusBack(drawerOpener);
    drawerOpener = null;
  }

  function showLogs(project, service, opener) {
    openDrawer(project + (service ? " / " + service : ""), project, "loading...", opener);
    var body = document.getElementById("logs-body");
    var url = "/api/logs?project=" + encodeURIComponent(project) + "&tail=200";
    if (service) url += "&service=" + encodeURIComponent(service);
    fetch(url, { credentials: "same-origin" })
      .then(function (r) { return r.text(); })
      .then(function (text) { body.textContent = text || "(no output)"; })
      .catch(function (err) { body.textContent = "error: " + err.message; });
  }

  // Run log source of the LogDrawer (design.md D14): the title reads
  // project / run N / step, the step buttons list the run's steps with their
  // chips, and Follow keeps the log at its end while the run is running.
  // Without a step the failed one opens, else the last step that has a log.
  var runLog = null; // {project, runId, step, run, timer}

  function stopRunFollow() {
    if (runLog && runLog.timer) clearTimeout(runLog.timer);
    runLog = null;
  }

  function pickStep(run, step) {
    if (step) return step;
    var withLog = (run.steps || []).filter(function (s) { return s.log; });
    var failed = withLog.filter(function (s) { return s.state === "failed"; })[0];
    return (failed || withLog[withLog.length - 1] || (run.steps || [])[0] || {}).id || "";
  }

  function stepsHtml(run, current) {
    return (run.steps || []).map(function (s) {
      var c = runChip(s.state === "failed" && s.reason === "interrupted" ? "interrupted" : s.state, s.reason);
      return '<button type="button" class="btn btn-ghost logs-step" data-step="' + escapeHtml(s.id) + '" data-key="logs/step/' + escapeHtml(s.id) +
        '" aria-pressed="' + (s.id === current) + '">' + chipHtml(c) + '<span class="mono">' + escapeHtml(s.id) + "</span></button>";
    }).join("");
  }

  function openRunLog(project, runId, step, opener) {
    openDrawer(project + " / run " + runId + (step ? " / " + step : ""), project, "loading...", opener);
    var st = runLog = { project: project, runId: runId, step: step || "", run: null, timer: null };
    document.getElementById("logs-run").hidden = false;
    document.getElementById("logs-steps").innerHTML = "";
    loadRunLog(st);
  }

  function loadRunLog(st) {
    var mine = st.seq = (st.seq || 0) + 1; // a newer load (step click) retires this one
    var body = document.getElementById("logs-body");
    var q = "project=" + encodeURIComponent(st.project) + "&id=" + st.runId;
    fetch("/api/runs?" + q, { credentials: "same-origin" })
      .then(function (r) { if (!r.ok) throw new Error("run " + st.runId + " is gone"); return r.json(); })
      .then(function (run) {
        if (runLog !== st || mine !== st.seq) return;
        st.run = run;
        st.step = pickStep(run, st.step);
        document.getElementById("logs-title").textContent = st.project + " / run " + st.runId + (st.step ? " / " + st.step : "");
        var steps = document.getElementById("logs-steps");
        var html = stepsHtml(run, st.step);
        if (steps.dataset.html !== html) {
          var hadFocus = steps.contains(document.activeElement) ? document.activeElement.dataset.key : "";
          steps.dataset.html = html;
          steps.innerHTML = html;
          if (hadFocus) { var again = keyedIn("#logs-steps", hadFocus); if (again) again.focus({ preventScroll: true }); }
        }
        var running = run.outcome === "running";
        var follow = document.getElementById("logs-follow");
        document.getElementById("logs-follow-label").hidden = !running;
        var cur = (run.steps || []).filter(function (s) { return s.id === st.step; })[0];
        if (!cur || !cur.log) {
          body.textContent = !cur ? "(this run has no steps)" : cur.state === "skipped" ? "(skipped" + (cur.reason ? ": " + cur.reason : "") + ")" : cur.state === "pending" ? "(not started)" : "(no output)";
          return;
        }
        return fetch("/api/runs/log?" + q + "&step=" + encodeURIComponent(st.step), { credentials: "same-origin" })
          .then(function (r) { if (!r.ok) throw new Error("no log for " + st.step); return r.text(); })
          .then(function (text) {
            if (runLog !== st || mine !== st.seq) return;
            text = text || "(no output)";
            if (body.textContent !== text) {
              body.textContent = text;
              if (running && follow.checked) body.scrollTop = body.scrollHeight;
            }
          });
      })
      .catch(function (err) { if (runLog === st && mine === st.seq) body.textContent = "error: " + err.message; })
      .then(function () {
        if (runLog !== st || mine !== st.seq) return;
        if (st.run && st.run.outcome === "running") st.timer = setTimeout(function () { loadRunLog(st); }, 1500);
      });
  }

  function wireControls() {
    document.getElementById("filter").addEventListener("input", function (e) {
      state.filter = e.target.value;
      render();
    });
    document.getElementById("running-only").addEventListener("change", function (e) {
      state.runningOnly = e.target.checked;
      render();
    });
    // Outside #view-instances (toolbar-level, design.md D3b §5), so it needs its
    // own listener rather than the delegated one in wireActions.
    document.getElementById("proxy-start").addEventListener("click", function () {
      runAction("proxy-up", "");
    });
    document.getElementById("proxy-banner-dismiss").addEventListener("click", function () {
      proxyHintDismissed = document.getElementById("proxy-banner-text").textContent;
      document.getElementById("proxy-banner").hidden = true;
    });
  }

  // Header "..." menu: "Clean up" shows the prune commands with dry-run
  // counts. Read-only by design: nothing here deletes, it only copies text.
  var cleanupLabels = [
    ["stopped", "Stopped instances", "Nothing running in them"],
    ["orphaned", "Orphaned instances", "Checkout or branch is gone"],
    ["all", "Everything", "Running instances included"]
  ];

  function loadCleanup() {
    var list = document.getElementById("cleanup-list");
    fetch("/api/cleanup", { credentials: "same-origin" })
      .then(function (r) { if (!r.ok) throw new Error("unavailable"); return r.json(); })
      .then(function (data) {
        list.innerHTML = cleanupLabels.map(function (l) {
          var g = data[l[0]], cmd = escapeHtml(g.command), key = "cleanup/" + l[0];
          return '<tr class="prune-row"><td class="prune-target"><span class="prune-name">' + l[1] + '</span><span class="muted">' + l[2] + "</span></td>" +
            '<td class="num prune-count" data-label="Instances">' + g.count + "</td>" +
            '<td class="prune-cmd"><span class="command-row"><code class="mono cmd-text" title="' + cmd + '">' + cmd + "</code>" +
            '<button type="button" class="btn btn-ghost copyable" data-key="' + key + '/copy" data-copy="' + cmd + '" aria-label="Copy ' + cmd + '">Copy</button></span></td>' +
            '<td class="prune-act"><button type="button" class="btn" data-key="' + key + '/run" data-run="' + cmd + '" title="Shows what would be removed first">Run</button></td></tr>';
        }).join("");
      })
      .catch(function () { list.innerHTML = '<tr><td colspan="4">Docker is unreachable; run <code>stackfork prune</code> in a terminal.</td></tr>'; });
  }

  // Docker disk: the banner shows from 85% used; the cleanup view always
  // shows the gauge. Measured server-side at most every 5 minutes.
  function loadDisk() {
    fetch("/api/disk", { credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        var banner = document.getElementById("disk-banner");
        banner.hidden = !d.full;
        // attention from 85 %, failed only from 95 % (DESIGN.md Banner strip).
        banner.className = "banner " + (d.percent >= 95 ? "banner-failed" : "banner-attention");
        document.getElementById("disk-text").textContent = d.text || "";
        var text = (d.text || "").replace(/^Docker disk:\s*/, "");
        document.getElementById("cleanup-disk").textContent = text || "Not measured yet";
        var meter = document.getElementById("disk-meter");
        var pct = d.percent || 0;
        meter.setAttribute("aria-valuenow", String(pct));
        meter.setAttribute("aria-valuetext", text || "not measured yet");
        meter.dataset.tone = pct >= 95 ? "failed" : pct >= 85 ? "attention" : "ok";
        meter.firstElementChild.style.width = pct + "%";
        var count = document.getElementById("rail-count-cleanup");
        count.textContent = d.percent ? d.percent + "%" : "";
        count.dataset.tone = d.full ? "attention" : "";
      })
      .catch(function () { /* keep the last state */ });
  }

  function freeSpace(btn) {
    var buttons = document.querySelectorAll(".free-space-btn");
    buttons.forEach(function (b) { b.disabled = true; });
    btn.textContent = "Freeing...";
    runAction("free-space", "docker").then(function () {
      buttons.forEach(function (b) { b.disabled = false; b.textContent = "Free Docker space"; });
      loadDisk();
    });
  }

  function wireCleanup() {
    document.addEventListener("click", function (e) {
      var btn = e.target.closest(".free-space-btn");
      if (btn && !btn.disabled) freeSpace(btn);
    });
    var panel = document.getElementById("view-cleanup");
    panel.addEventListener("click", function (e) {
      var copy = e.target.closest(".copyable");
      if (copy) copyText(copy.dataset.copy, copy.textContent.trim(), copy);
    });
  }

  function wireActions() {
    document.getElementById("view-instances").addEventListener("click", function (e) {
      var toggleStack = e.target.closest("[data-toggle-stack]");
      if (toggleStack) {
        var article = toggleStack.closest(".stack");
        var collapsed = article.dataset.collapsed !== "1";
        article.dataset.collapsed = collapsed ? "1" : "0";
        toggleStack.setAttribute("aria-expanded", String(!collapsed));
        setCollapsed(toggleStack.dataset.project, collapsed);
        return;
      }
      var toggleProject = e.target.closest("[data-toggle-project]");
      if (toggleProject) {
        var section = toggleProject.closest(".project");
        var pc = section.dataset.collapsed !== "1";
        section.dataset.collapsed = pc ? "1" : "0";
        toggleProject.setAttribute("aria-expanded", String(!pc));
        setProjectCollapsed(toggleProject.dataset.app, pc);
        return;
      }
      var projAct = e.target.closest("[data-project-action]");
      if (projAct) { showInRepos(projAct.dataset.repo, projAct.dataset.projectAction === "start"); return; }
      var toggleSettings = e.target.closest("[data-toggle-settings]");
      if (toggleSettings) {
        var settings = toggleSettings.closest("tbody").querySelector(".service-settings");
        var openNow = settings.hasAttribute("hidden");
        if (openNow) settings.removeAttribute("hidden");
        else settings.setAttribute("hidden", "");
        toggleSettings.setAttribute("aria-expanded", String(openNow));
        openSettings[toggleSettings.dataset.key.replace(/\/settings$/, "")] = openNow;
        return;
      }
      var runLog = e.target.closest("[data-run-log]");
      if (runLog) { openRunLog(runLog.dataset.project, Number(runLog.dataset.runId), runLog.dataset.step, runLog); return; }
      var startAgain = e.target.closest("button[data-repo-action]"); // Retry of a failed Start
      if (startAgain) { if (!startAgain.disabled) runRepoAction(startAgain); return; }
      var btn = e.target.closest("button[data-action]");
      if (btn) {
        runAction(btn.dataset.action, btn.dataset.project, btn.dataset.service);
        return;
      }
      var logsBtn = e.target.closest(".logs-button");
      if (logsBtn) {
        showLogs(logsBtn.dataset.project, logsBtn.dataset.service, logsBtn);
        return;
      }
      var copy = e.target.closest("[data-copy]");
      if (copy) {
        // Anything actually carrying the placeholder routes through
        // /api/copy for the real value (design.md D3b); the value never
        // gets logged or rendered, only written to the clipboard.
        var label = (copy.querySelector(".menu-label") || copy).textContent.trim();
        if (copy.dataset.copyKind && copy.dataset.copy.indexOf("<password>") !== -1) {
          fetchCopyValue(copy.dataset.copyKind, copy.dataset.copyProject, copy.dataset.copyService)
            .then(function (value) {
              copyText(value, label, copy);
            })
            .catch(function (err) {
              log("copy failed: " + err.message);
            });
        } else {
          copyText(copy.dataset.copy, label, copy);
        }
      }
    });
  }

  // --- OverflowMenu (DESIGN.md Components) ------------------------------
  // Any button[aria-haspopup="menu"] opens the role="menu" that follows it
  // (or the one named by aria-controls). Wired once, on document, so cards
  // and Repos rows both get it however often their HTML is replaced.

  function menuOf(trigger) {
    var id = trigger.getAttribute("aria-controls");
    return (id && document.getElementById(id)) || trigger.nextElementSibling;
  }

  function menuItems(menu) {
    return Array.prototype.filter.call(menu.querySelectorAll('[role="menuitem"]'), function (el) {
      return !el.disabled && el.offsetParent !== null; // mobile-only items are display:none on desktop
    });
  }

  function openMenu(trigger, focusFirst) {
    closeMenus(false, trigger);
    var menu = menuOf(trigger);
    if (!menu) return;
    menu.classList.remove("menu-up");
    menu.hidden = false;
    trigger.setAttribute("aria-expanded", "true");
    // Flip above the trigger when the menu would run off the bottom.
    var r = menu.getBoundingClientRect();
    var t = trigger.getBoundingClientRect();
    if (r.bottom > window.innerHeight && t.top > r.height) menu.classList.add("menu-up");
    if (focusFirst !== false) {
      var first = menuItems(menu)[0];
      if (first) first.focus();
    }
  }

  function closeMenus(returnFocus, except) {
    document.querySelectorAll('[aria-haspopup="menu"][aria-expanded="true"]').forEach(function (t) {
      if (t === except) return;
      var m = menuOf(t);
      if (m) m.hidden = true;
      t.setAttribute("aria-expanded", "false");
      if (returnFocus) t.focus();
    });
  }

  function wireMenus() {
    document.addEventListener("click", function (e) {
      var trigger = e.target.closest('[aria-haspopup="menu"]');
      if (trigger) {
        if (trigger.getAttribute("aria-expanded") === "true") closeMenus(true);
        else openMenu(trigger);
        return;
      }
      var item = e.target.closest('[role="menu"] [role="menuitem"]');
      // A copy item stays open so its "Copied" feedback can be read.
      if (item) { if (!item.hasAttribute("data-copy")) closeMenus(true); return; }
      if (!e.target.closest('[role="menu"]')) closeMenus(false);
    });
    document.addEventListener("keydown", function (e) {
      var open = document.querySelector('[aria-haspopup="menu"][aria-expanded="true"]');
      var onTrigger = e.target.closest && e.target.closest('[aria-haspopup="menu"]');
      if (!open) {
        if (onTrigger && e.key === "ArrowDown") { e.preventDefault(); openMenu(onTrigger); }
        return;
      }
      if (e.key === "Escape") { e.preventDefault(); closeMenus(true); return; }
      if (e.key === "Tab") { closeMenus(false); return; }
      var items = menuItems(menuOf(open));
      var i = items.indexOf(document.activeElement);
      var to = { ArrowDown: i + 1, ArrowUp: i < 0 ? items.length - 1 : i - 1, Home: 0, End: items.length - 1 }[e.key];
      if (to === undefined || !items.length) return;
      e.preventDefault();
      items[(to + items.length) % items.length].focus();
    });
  }

  function wireLogsPanel() {
    document.getElementById("logs-close").addEventListener("click", closeDrawer);
    document.getElementById("logs-steps").addEventListener("click", function (e) {
      var b = e.target.closest(".logs-step");
      if (!b || !runLog) return;
      if (runLog.timer) clearTimeout(runLog.timer);
      runLog.step = b.dataset.step;
      loadRunLog(runLog);
    });
    document.addEventListener("keydown", function (e) {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      if (document.querySelector('[aria-haspopup="menu"][aria-expanded="true"]') || document.querySelector("dialog[open]")) return;
      closeDrawer();
    });
  }

  // --- Repos tab and Settings (add-repo-browser) ---------------------------
  // The tab's HTML is rendered by the server (html/template escapes every
  // repo and branch name); this code only swaps it in, filters it, and posts
  // the actions. Branch and path values travel in data-* attributes.

  var reposState = { tab: "instances", filter: "", readyOnly: false, autoShownSettings: false, errors: {}, open: {}, sort: "activity", group: false,
    runs: {}, starts: {} };

  // copyText writes to the clipboard and says so on the clicked element
  // itself (a button shows "Copied", anything else flashes), since the
  // activity log is not visible on every tab.
  function copyText(text, label, el) {
    var done = function (ok) {
      log((ok ? "copied " : "copy failed: ") + label);
      if (!el) return;
      var label = el.classList.contains("menu-item") && el.querySelector(".menu-label");
      if (label) {
        if (!el.dataset.label) el.dataset.label = label.textContent;
        label.textContent = ok ? "Copied \u2713" : "Copy failed";
        clearTimeout(el._copyTimer);
        el._copyTimer = setTimeout(function () { label.textContent = el.dataset.label; }, 1500);
      } else if (el.tagName === "BUTTON" && !el.classList.contains("copyable")) {
        if (!el.dataset.label) el.dataset.label = el.textContent;
        el.textContent = ok ? "Copied \u2713 paste it into your agent" : "Copy failed";
        clearTimeout(el._copyTimer);
        el._copyTimer = setTimeout(function () { el.textContent = el.dataset.label; }, 2500);
      } else {
        el.classList.add(ok ? "just-copied" : "copy-failed");
        setTimeout(function () { el.classList.remove("just-copied", "copy-failed"); }, 1200);
      }
    };
    // Fallbacks when the async clipboard is refused (no permission, not
    // focused): the legacy copy command, then a dialog with the text selected.
    var fallback = function () {
      var ta = document.createElement("textarea");
      ta.value = text; ta.setAttribute("readonly", ""); ta.style.position = "fixed"; ta.style.opacity = "0";
      document.body.appendChild(ta); ta.select();
      var ok = false;
      try { ok = document.execCommand("copy"); } catch (e) { ok = false; }
      document.body.removeChild(ta);
      if (!ok) window.prompt("Copy this (Cmd+C), then paste it into your agent:", text);
      done(ok);
    };
    if (!navigator.clipboard) { fallback(); return; }
    navigator.clipboard.writeText(text).then(function () { done(true); }, fallback);
  }

  // Views, switched by the rail and mirrored in the URL hash (#repos, ...).
  var views = ["instances", "repos", "cleanup", "settings"];
  var viewTitles = { instances: "Instances", repos: "Repos", cleanup: "Clean up", settings: "Settings" };

  function viewFromHash() {
    var h = location.hash.replace(/^#/, "");
    return views.indexOf(h) >= 0 ? h : "instances";
  }

  function setView(view) {
    if (views.indexOf(view) < 0) view = "instances";
    reposState.tab = view;
    closeMenus(false);
    if (viewFromHash() !== view || !location.hash) {
      try { history.replaceState(null, "", "#" + view); } catch (e) { /* ignore */ }
    }
    document.querySelectorAll(".rail-item").forEach(function (b) {
      var on = b.dataset.view === view;
      if (on) b.setAttribute("aria-current", "page"); else b.removeAttribute("aria-current");
      b.tabIndex = on ? 0 : -1;
    });
    views.forEach(function (v) { document.getElementById("view-" + v).hidden = v !== view; });
    document.querySelectorAll(".toolbar-tools").forEach(function (t) { t.hidden = t.dataset.for !== view; });
    document.getElementById("view-title").textContent = viewTitles[view];
    if (view === "repos") loadRepos();
    if (view === "cleanup") { loadCleanup(); loadDisk(); }
    if (view === "settings") loadSettings();
  }

  function applyRepoFilters() {
    var needle = reposState.filter.toLowerCase();
    document.querySelectorAll("#repos-list .repo").forEach(function (el) {
      var hide = (needle && el.dataset.search.indexOf(needle) === -1) || (reposState.readyOnly && el.dataset.ready !== "1");
      el.hidden = !!hide;
    });
    document.querySelectorAll("#repos-list .repo-group-title").forEach(function (g) {
      var n = g.nextElementSibling, any = false;
      while (n && n.classList.contains("repo")) { if (!n.hidden) any = true; n = n.nextElementSibling; }
      g.hidden = !any;
    });
  }

  // Sort menu: Activity (the server's order: running first, then recent),
  // Name or Folder, optionally grouped by folder. Done on the rows already
  // in the page; the server's data-rank is the Activity order.
  function applyRepoView() {
    var rows = document.getElementById("repo-rows");
    if (!rows) return;
    rows.querySelectorAll(".repo-group-title").forEach(function (g) { g.remove(); });
    var items = Array.prototype.slice.call(rows.querySelectorAll(".repo"));
    var by = reposState.sort, group = reposState.group || by === "folder";
    function str(a, b) { return a < b ? -1 : a > b ? 1 : 0; }
    var want = items.slice().sort(function (x, y) {
      var c = 0;
      if (group) c = str(x.dataset.folder.toLowerCase(), y.dataset.folder.toLowerCase());
      if (!c && by === "activity") c = x.dataset.rank - y.dataset.rank;
      if (!c) c = str(x.dataset.name.toLowerCase(), y.dataset.name.toLowerCase()) || x.dataset.rank - y.dataset.rank;
      return c;
    });
    var same = want.every(function (el, i) { return el === items[i]; });
    if (!same) want.forEach(function (el) { rows.appendChild(el); });
    if (reposState.group) {
      var last = null;
      want.forEach(function (el) {
        if (el.dataset.folder === last) return;
        last = el.dataset.folder;
        var h = document.createElement("h3");
        h.className = "repo-group-title micro";
        h.textContent = last || "Scan root";
        rows.insertBefore(h, el);
      });
    }
  }

  // The project menu's way into Repos: switch view, find the repo row once
  // the fragment is there (it loads asynchronously) and open its panel.
  function showInRepos(path, start) {
    setView("repos");
    var tries = 0;
    (function find() {
      var row = keyedIn("#repos-list", path);
      if (row) {
        if (start) {
          setRepoOpen(row, true);
          var first = row.querySelector('.repo-panel [data-repo-action="start"]');
          if (first) { first.scrollIntoView({ block: "nearest" }); first.focus({ preventScroll: true }); return; }
        }
        row.scrollIntoView({ block: "center" });
        var t = row.querySelector(".repo-toggle");
        if (t) t.focus({ preventScroll: true });
        return;
      }
      if (++tries < 15) setTimeout(find, 200);
      else toast("failed", "This repo is not in Repos. Check the scan folders in Settings.");
    })();
  }

  function setRepoOpen(repo, open) {
    repo.dataset.open = open ? "1" : "0";
    repo.querySelector(".repo-panel").hidden = !open;
    repo.querySelector(".repo-toggle").setAttribute("aria-expanded", String(open));
    if (open) reposState.open[repo.dataset.key] = true; else delete reposState.open[repo.dataset.key];
  }

  // lastRepos is the fragment last written. A poll that brings the same HTML
  // changes nothing; one that differs remembers the focused element, the
  // open menu and the scroll position, writes, and puts them back.
  var lastRepos = null;

  function loadRepos() {
    fetch("/fragments/repos", { credentials: "same-origin" })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.text(); })
      .then(function (html) {
        var list = document.getElementById("repos-list");
        var meta = document.getElementById("repos-meta");
        if (html !== lastRepos) {
          lastRepos = html;
          var active = document.activeElement;
          var focusKey = active && active.closest && active.closest("#repos-list") && active.dataset ? active.dataset.key : "";
          var menu = document.querySelector('#repos-list [aria-haspopup="menu"][aria-expanded="true"]');
          var menuKey = menu ? menu.dataset.key : "";
          var scrollY = window.scrollY;
          list.innerHTML = html;
          list.querySelectorAll(".repo").forEach(function (d) { if (reposState.open[d.dataset.key]) setRepoOpen(d, true); });
          applyRepoView();
          applyRepoFilters();
          paintRuns(); // the live run and the Starts in flight survive every render
          paintStarts();
          if (menuKey) { var tr = keyedIn("#repos-list", menuKey); if (tr) openMenu(tr, false); }
          if (focusKey) {
            var again = keyedIn("#repos-list", focusKey);
            if (again && again !== document.activeElement) again.focus({ preventScroll: true });
          }
          if (window.scrollY !== scrollY) window.scrollTo(0, scrollY);
          meta = document.getElementById("repos-meta");
        }
        renderRepoErrors(); // errors change without the fragment changing (failed/dismissed action)
        document.getElementById("rail-count-repos").textContent = list.querySelectorAll(".repo").length || "";
        var scanning = meta && meta.dataset.scanning === "true";
        var n = meta ? Number(meta.dataset.count) : 0;
        document.getElementById("scan-state").textContent = scanning ? "◔ Scanning folders…" : n ? n + (n === 1 ? " repo" : " repos") : "";
        if (meta && meta.dataset.configured === "false" && !reposState.autoShownSettings) {
          reposState.autoShownSettings = true;
          setView("settings");
        }
      })
      .catch(function () { /* keep what is shown */ });
  }

  function loadSettings() {
    fetch("/fragments/settings", { credentials: "same-origin" })
      .then(function (r) { return r.text(); })
      .then(function (html) {
        var body = document.getElementById("settings-body");
        var hadFocus = body.contains(document.activeElement);
        body.innerHTML = html;
        // the form was replaced: put focus on the field to add the next folder
        if (hadFocus) { var f = body.querySelector('form[data-config-op="add-root"] input'); if (f) f.focus(); }
      });
  }

  function postConfig(op, value) {
    var err = document.getElementById("settings-error");
    err.textContent = "";
    return post("/api/config", { op: op, value: value })
      .then(function (r) { return r.json().then(function (b) { return { ok: r.ok, body: b }; }); })
      .then(function (res) {
        if (!res.ok) { err.textContent = res.body.message || "refused"; return; }
        log("settings: " + res.body.message);
        loadSettings();
        setTimeout(loadRepos, 600);
      })
      .catch(function (e) { err.textContent = e.message; });
  }

  function shq(v) { return "'" + String(v).replace(/'/g, "'\\''") + "'"; }

  // --- the live run in the plan list (design.md D13) -------------------------
  // reposState.runs[repoPath] holds the step states of the Start streaming
  // from this page. paintRuns writes them onto the plan rows by data-step, in
  // place (class and text of the chip), so a fragment render or a poll never
  // erases them, never replaces a focused element and never moves focus.

  var RUN_CHIP = {
    running: ["busy", GLYPH.busy, "Running"], succeeded: ["ok", GLYPH.ok, "Succeeded"], failed: ["failed", GLYPH.failed, "Failed"],
    skipped: ["off", GLYPH.off, "Skipped"], pending: ["off", GLYPH.off, "Pending"], interrupted: ["attention", GLYPH.attention, "Interrupted"],
  };

  function runChip(st, reason) {
    var c = RUN_CHIP[st] || RUN_CHIP.pending;
    return { cls: c[0], glyph: c[1], label: c[2], title: st === "skipped" && reason ? reason : "" };
  }

  // fmtDur must match the CLI's durText: 320 ms, 41 s, 1 m 5 s.
  function fmtDur(ms) {
    if (ms < 1000) return Math.round(ms) + " ms";
    var s = Math.round(ms / 1000);
    if (s < 60) return s + " s";
    var m = Math.floor(s / 60);
    return m + " m " + (s % 60) + " s";
  }

  function paintChip(chipEl, c) {
    chipEl.className = "chip chip-" + c.cls;
    if (c.title) chipEl.title = c.title; else chipEl.removeAttribute("title");
    var g = chipEl.querySelector(".chip-glyph");
    if (g.textContent !== c.glyph) g.textContent = c.glyph;
    var t = g.nextSibling;
    if (t && t.nodeType === 3) { if (t.nodeValue !== c.label) t.nodeValue = c.label; }
    else chipEl.appendChild(document.createTextNode(c.label));
  }

  function paintRuns() {
    Object.keys(reposState.runs).forEach(function (repo) {
      var run = reposState.runs[repo];
      var el = keyedIn("#repos-list", repo);
      if (!el) return;
      var shown = {};
      el.querySelectorAll(".plan-row[data-step]").forEach(function (row) {
        var st = run.steps[row.dataset.step];
        if (!st) return;
        shown[row.dataset.step] = true;
        var c = runChip(st.state, st.reason);
        var chipEl = row.querySelector(".plan-state .chip");
        var sig = st.state + "|" + (st.reason || "");
        if (chipEl && row.dataset.runSig !== sig) { paintChip(chipEl, c); row.dataset.runSig = sig; }
        var dur = row.querySelector(".plan-dur");
        if (dur) {
          var txt = st.ms !== undefined ? fmtDur(st.ms) : "";
          if (dur.textContent !== txt) dur.textContent = txt;
          dur.hidden = !txt;
        }
      });
      // the current line goes under the running step's row, else under the list
      var live = shown[run.lineStep] ? run.lineStep : "";
      el.querySelectorAll("[data-live-for]").forEach(function (l) {
        var want = l.dataset.liveFor === live && run.steps[run.lineStep] && run.steps[run.lineStep].state === "running" ? run.line : "";
        if (l.textContent !== want) l.textContent = want;
      });
      run.rowShown = !!shown[run.lineStep];
    });
  }

  // onStreamObject applies one NDJSON object of a Start to reposState.runs.
  function onStreamObject(repo, o) {
    var run = reposState.runs[repo];
    if (o.run && typeof o.run === "object") {
      run = reposState.runs[repo] = { id: o.run.id, project: o.run.project, steps: {}, line: "", lineStep: "" };
      (o.run.steps || []).forEach(function (s) { run.steps[s.id] = { state: s.state }; });
    } else if (o.step && typeof o.step === "object" && run) {
      var st = run.steps[o.step.id] || (run.steps[o.step.id] = {});
      st.state = o.step.state;
      if (o.step.reason) st.reason = o.step.reason; else delete st.reason;
      if (st.state === "running") { st.t0 = Date.now(); delete st.ms; }
      else if (st.state === "succeeded" || st.state === "failed") st.ms = st.t0 ? Date.now() - st.t0 : 0;
      if (st.state !== "running" && run.lineStep === o.step.id) { run.line = ""; run.lineStep = ""; }
    } else if (o.line !== undefined && run) {
      if (o.step) { run.lineStep = o.step; run.line = progressText(o.line); }
    }
    paintRuns();
  }

  // Starts in flight, by the Start button's data-key: a render replaces the
  // button and the floating progress text, so paintStarts puts them back.
  function paintStarts() {
    Object.keys(reposState.starts).forEach(function (key) {
      var b = keyedIn("#repos-list", key);
      if (!b) return;
      var s = reposState.starts[key];
      b.disabled = true;
      if (!s.inMenu && b.textContent !== "Starting\u2026") b.textContent = "Starting\u2026";
      var anchor = s.inMenu ? b.closest(".menu-anchor") : b;
      var p = anchor.previousElementSibling;
      if (!p || !p.classList.contains("row-progress")) {
        p = document.createElement("span");
        p.className = "row-progress";
        p.setAttribute("aria-live", "polite");
        anchor.insertAdjacentElement("beforebegin", p);
      }
      var run = reposState.runs[s.repo];
      // with a plan row showing the line, the floating text stays out
      var hide = !!(run && run.rowShown && run.lineStep);
      p.hidden = hide;
      if (p.textContent !== s.text) p.textContent = s.text;
    });
  }

  // A failed start / worktree-add stays on its repo row (not only in the
  // activity log) with the exact CLI command to retry it; it survives the
  // list refresh until dismissed or the action succeeds.
  function renderRepoErrors() {
    var list = document.getElementById("repos-list");
    list.querySelectorAll(".repo-error").forEach(function (e) { e.remove(); });
    list.querySelectorAll(".repo").forEach(function (d) {
      var err = reposState.errors[d.dataset.key];
      if (!err) return;
      var box = document.createElement("div");
      box.className = "repo-error callout callout-failed";
      box.setAttribute("role", "alert");
      box.dataset.repo = d.dataset.key;
      var glyph = document.createElement("span");
      glyph.className = "callout-glyph";
      glyph.setAttribute("aria-hidden", "true");
      glyph.textContent = "⊘";
      var body = document.createElement("div");
      body.className = "callout-body";
      var head = document.createElement("p");
      head.className = "callout-title";
      head.textContent = err.title || err.label + " failed";
      var detail = document.createElement("p");
      detail.className = "callout-detail";
      detail.textContent = err.label;
      var msg = document.createElement("pre");
      msg.className = "repo-error-msg mono-log";
      msg.tabIndex = 0;
      msg.setAttribute("aria-label", "Error output");
      msg.dataset.key = d.dataset.key + "/error/log";
      msg.textContent = err.message;
      var retry = document.createElement("div");
      retry.className = "command-row";
      retry.appendChild(document.createTextNode("Retry in a terminal: "));
      var code = document.createElement("code");
      code.className = "copyable mono";
      code.dataset.copy = err.command;
      code.title = "click to copy";
      code.textContent = err.command;
      retry.appendChild(code);
      body.appendChild(head); body.appendChild(detail); body.appendChild(msg); body.appendChild(retry);
      var actions = document.createElement("div");
      actions.className = "callout-actions";
      if (err.log) {
        var view = document.createElement("button");
        view.type = "button";
        view.className = "btn";
        view.dataset.runLog = "";
        view.dataset.project = err.log.project;
        view.dataset.runId = err.log.id;
        view.dataset.step = err.log.step;
        view.dataset.key = d.dataset.key + "/error/viewlog";
        view.textContent = "View log";
        actions.appendChild(view);
      }
      var dismiss = document.createElement("button");
      dismiss.type = "button";
      dismiss.className = "btn btn-ghost";
      dismiss.dataset.dismissRepo = d.dataset.key;
      dismiss.dataset.key = d.dataset.key + "/error/dismiss";
      dismiss.textContent = "Dismiss";
      actions.appendChild(dismiss);
      box.appendChild(glyph); box.appendChild(body); box.appendChild(actions);
      d.querySelector("[data-repo-errors]").appendChild(box);
      setRepoOpen(d, true);
    });
  }

  function retryCommand(d, created) {
    if (d.repoAction === "fetch") return "git -C " + shq(d.repo) + " fetch --prune";
    if (d.repoAction === "start" && d.path) return "stackfork start-env " + shq(d.path);
    if (created) return "stackfork start-env " + shq(created);
    return "stackfork worktree add " + shq(d.repo) + " " + shq(d.branch) + (d.repoAction === "start" ? " && stackfork start-env " + shq(d.repo + "/.worktrees/" + d.branch.replace(/\//g, "-")) : "");
  }

  // runRepoAction posts start / fetch / worktree-add. A start answers with
  // newline-delimited JSON so progress lines reach the activity log live.
  // progressText turns a compose line ("Container sf-x-1-web-1 Waiting")
  // into "waiting for web to be healthy"; other lines pass through.
  function progressText(line) {
    var m = line.match(/^\s*Container\s+\S+-([A-Za-z0-9_.]+)-\d+\s+(\w+)/);
    if (!m) return line.trim();
    var verbs = { Waiting: "waiting for " + m[1] + " to be healthy", Healthy: m[1] + " is healthy", Starting: "starting " + m[1],
      Started: m[1] + " started", Created: m[1] + " created", Creating: "creating " + m[1], Running: m[1] + " running" };
    return verbs[m[2]] || m[1] + " " + m[2].toLowerCase();
  }

  function runRepoAction(btn) {
    var d = btn.dataset;
    if (d.confirm && !window.confirm(d.confirm)) return;
    var body = { action: d.repoAction, repo: d.repo, path: d.path || "", branch: d.branch || "" };
    var name = d.repo.split("/").pop();
    var what = d.branch || (d.path && d.path !== d.repo ? d.path.split("/").pop() : "");
    var label = name + (what ? ":" + what : "") + " " + d.repoAction;
    var created = "";
    var runRef = null; // the run this Start recorded, for the callout's View log
    function fail(msg) {
      reposState.errors[d.repo] = { label: label, title: ({ start: "Start", "worktree-add": "Create worktree", fetch: "Fetch" }[d.repoAction] || "Action") + " failed", message: msg, command: retryCommand(d, created),
        log: runRef && runRef.step ? runRef : null };
    }
    // A menu item keeps its label (it is hidden once the menu closes); the
    // progress text goes next to the row's overflow button instead.
    var inMenu = btn.classList.contains("menu-item");
    var anchor = inMenu ? btn.closest(".menu-anchor") : btn;
    btn.disabled = true;
    var btnText = btn.textContent;
    if (!inMenu) btn.textContent = d.repoAction === "start" ? "Starting\u2026" : btnText + "\u2026";
    // The latest progress line shows next to the button: a start can take
    // minutes (installs, health checks) and the log is at the page bottom.
    // With a live plan row it moves under that row (paintRuns).
    var progress = document.createElement("span");
    progress.className = "row-progress";
    progress.setAttribute("aria-live", "polite");
    progress.textContent = "starting\u2026";
    anchor.insertAdjacentElement("beforebegin", progress);
    var startKey = btn.dataset.key || "";
    if (startKey) reposState.starts[startKey] = { inMenu: inMenu, repo: d.repo, text: progress.textContent };
    reposState.busy = (reposState.busy || 0) + 1;
    delete reposState.errors[d.repo];
    if (d.repoAction === "start" && reposState.runs[d.repo]) { // a new Start replaces the previous run's states
      delete reposState.runs[d.repo];
      lastRepos = null;
      loadRepos();
    }
    log(label + ": started");
    post("/api/actions", body)
      .then(function (r) {
        if ((r.headers.get("Content-Type") || "").indexOf("ndjson") === -1) {
          return r.json().then(function (b) {
            log(label + ": " + (b.message || (r.ok ? "ok" : "failed")));
            if (!r.ok || b.ok === false) { fail(b.message || "refused (status " + r.status + ")"); toast("failed", label + ": " + (b.message || "refused")); }
            else toast("ok", label + ": " + (b.message || "ok"));
          });
        }
        var reader = r.body.getReader();
        var dec = new TextDecoder();
        var buf = "";
        function handle(line) {
          if (!line.trim()) return;
          var o = JSON.parse(line);
          if (o.line !== undefined) {
            log(label + ": " + o.line);
            progress.textContent = progressText(o.line);
            if (reposState.starts[startKey]) reposState.starts[startKey].text = progress.textContent;
            if (o.line.indexOf("created ") === 0) created = o.line.slice(8).split(" (copied")[0];
            onStreamObject(d.repo, o);
            paintStarts();
          } else if (o.ok === undefined) { // run and step objects (design.md D12)
            if (o.run && typeof o.run === "object") runRef = { project: o.run.project, id: o.run.id, step: "" };
            onStreamObject(d.repo, o);
            paintStarts();
          } else {
            log(label + ": " + (o.ok ? "done - " : "failed - ") + o.message);
            if (runRef && o.failed_step) runRef.step = o.failed_step;
            if (!o.ok) fail(o.message);
            // the callout and the step log hold the detail; the toast names the step
            toast(o.ok ? "ok" : "failed", o.ok || !o.failed_step ? label + ": " + o.message : label + " failed at step " + o.failed_step);
          }
        }
        function pump() {
          return reader.read().then(function (res) {
            if (res.done) { handle(buf); return; }
            buf += dec.decode(res.value, { stream: true });
            var i;
            while ((i = buf.indexOf("\n")) >= 0) { handle(buf.slice(0, i)); buf = buf.slice(i + 1); }
            return pump();
          });
        }
        return pump();
      })
      .catch(function (e) { log(label + ": " + e.message); fail(e.message); toast("failed", label + ": " + e.message); })
      .then(function () {
        delete reposState.starts[startKey];
        var live = startKey && keyedIn("#repos-list", startKey);
        [btn, live].forEach(function (b) {
          if (!b) return;
          b.disabled = false;
          if (!inMenu) b.textContent = btnText;
        });
        if (!Object.keys(reposState.starts).length) document.querySelectorAll("#repos-list .row-progress").forEach(function (p) { p.remove(); });
        progress.remove();
        reposState.busy--;
        loadRepos();
        poll();
      });
  }

  function wireRepos() {
    var rail = document.querySelector("nav.rail");
    rail.addEventListener("click", function (e) {
      var b = e.target.closest(".rail-item");
      if (b) setView(b.dataset.view);
    });
    // Arrow keys move between items; Enter/Space select (native button click).
    rail.addEventListener("keydown", function (e) {
      var step = { ArrowDown: 1, ArrowRight: 1, ArrowUp: -1, ArrowLeft: -1 }[e.key];
      if (!step) return;
      var items = Array.prototype.slice.call(rail.querySelectorAll(".rail-item"));
      var i = items.indexOf(document.activeElement);
      if (i < 0) return;
      e.preventDefault();
      items[(i + step + items.length) % items.length].focus();
    });
    window.addEventListener("hashchange", function () { setView(viewFromHash()); });
    document.getElementById("repo-filter").addEventListener("input", function (e) {
      reposState.filter = e.target.value; applyRepoFilters();
    });
    document.getElementById("ready-only").addEventListener("change", function (e) {
      reposState.readyOnly = e.target.checked; applyRepoFilters();
    });
    function rescan() { postConfig("rescan", "").then(function () { setTimeout(loadRepos, 800); }); }
    document.getElementById("rescan").addEventListener("click", rescan);
    document.getElementById("repos-list").addEventListener("click", function (e) {
      var inst = e.target.closest(".instance-link");
      if (inst) {
        e.preventDefault();
        setView("instances");
        var art = document.querySelector('.stack[data-project="' + inst.dataset.project.replace(/"/g, "") + '"]');
        if (art) {
          // same path as the chevrons, so the next poll keeps both open
          var proj = art.closest(".project");
          if (proj) {
            proj.dataset.collapsed = "0";
            var pchev = proj.querySelector("[data-toggle-project]");
            if (pchev) { pchev.setAttribute("aria-expanded", "true"); setProjectCollapsed(pchev.dataset.app, false); }
          }
          art.dataset.collapsed = "0";
          var chev = art.querySelector("[data-toggle-stack]");
          if (chev) { chev.setAttribute("aria-expanded", "true"); setCollapsed(chev.dataset.project, false); }
          art.scrollIntoView({ block: "center" });
        }
        return;
      }
      var tog = e.target.closest("[data-toggle-repo]");
      if (tog) {
        var repo = tog.closest(".repo");
        var open = tog.hasAttribute("data-open-only") ? true : repo.dataset.open !== "1";
        setRepoOpen(repo, open);
        if (open && tog.hasAttribute("data-open-only")) {
          var first = repo.querySelector('.repo-panel [data-repo-action="start"]');
          if (first) { first.scrollIntoView({ block: "nearest" }); first.focus({ preventScroll: true }); }
        }
        return;
      }
      var goto = e.target.closest("[data-goto]");
      if (goto) { setView(goto.dataset.goto); return; }
      if (e.target.closest("[data-rescan]")) { rescan(); return; }
      var row = e.target.closest(".repo-row");
      if (row && !e.target.closest("button, a, .menu-anchor, .copyable")) { var rt = row.querySelector(".repo-toggle"); if (rt) rt.click(); return; }
      var rl = e.target.closest("[data-run-log]");
      if (rl) { openRunLog(rl.dataset.project, Number(rl.dataset.runId), rl.dataset.step, rl); return; }
      var dis = e.target.closest("[data-dismiss-repo]");
      if (dis) { delete reposState.errors[dis.dataset.dismissRepo]; renderRepoErrors(); return; }
      var act = e.target.closest("[data-repo-action]");
      if (act) { runRepoAction(act); return; }
      var copy = e.target.closest(".copyable, .menu-item[data-copy]");
      if (copy) {
        e.preventDefault();
        var lbl = copy.querySelector(".menu-label");
        copyText(copy.dataset.copy, copy.dataset.label || (lbl || copy).textContent.trim(), copy);
      }
    });
    document.getElementById("repos-list").addEventListener("change", function (e) {
      var sel = e.target.closest("select[data-mode-for]");
      if (!sel) return;
      post("/api/config", { op: "set-mode", value: sel.dataset.modeFor + "=" + sel.value })
        .then(function (r) { return r.json().then(function (b) { return { ok: r.ok, body: b }; }); })
        .then(function (res) {
          if (!res.ok) toast("failed", "mode: " + (res.body.message || "refused"));
          else log("mode: " + sel.value);
          loadRepos();
        })
        .catch(function (err) { toast("failed", "mode: " + err.message); loadRepos(); });
    });
    document.querySelectorAll('input[name="repo-sort"]').forEach(function (r) {
      r.addEventListener("change", function () { reposState.sort = r.value; applyRepoView(); applyRepoFilters(); });
    });
    document.getElementById("repo-group").addEventListener("change", function (e) {
      reposState.group = e.target.checked; applyRepoView(); applyRepoFilters();
    });
    var panel = document.getElementById("view-settings");
    panel.addEventListener("click", function (e) {
      var rm = e.target.closest("button[data-config-op]");
      if (rm) { postConfig(rm.dataset.configOp, rm.dataset.value); return; }
      var copy = e.target.closest(".copyable");
      if (copy) copyText(copy.dataset.copy, copy.textContent.trim(), copy);
    });
    panel.addEventListener("submit", function (e) {
      e.preventDefault();
      var form = e.target.closest("form[data-config-op]");
      if (form) postConfig(form.dataset.configOp, form.elements.value.value);
    });
    setView(viewFromHash());
    loadRepos(); // fills the rail count even when another view is open
    setInterval(function () {
      if (document.visibilityState === "visible" && reposState.tab === "repos") loadRepos();
    }, 3000);
  }

  // Every command chip the page shows can also run from here (POST
  // /api/run). The server allowlists it and asks for a native macOS
  // confirmation before anything that deletes data. Chips with a
  // placeholder (PATH, CMD, <name>) stay copy-only.
  function runnable(cmd) {
    return /^(stackfork|git|docker) /.test(cmd) && !/[<>…]|\b[A-Z]{3,}\b/.test(cmd);
  }

  // Commands still running, by the button's data-key. A card re-render
  // replaces the button, so writeCards re-applies this state to the new
  // one: a second click cannot start the same command twice.
  var inFlight = {};

  function markRunning(btn) {
    btn.disabled = true;
    if (!btn.classList.contains("menu-item")) btn.textContent = "Running\u2026";
  }

  function runCommand(btn) {
    var key = btn.dataset.key || "";
    if (key && inFlight[key]) return;
    var card = btn.closest("[data-project]");
    var project = card ? card.dataset.project : "";
    var cmd = btn.dataset.run;
    var label = btn.textContent;
    if (key) inFlight[key] = { label: label };
    markRunning(btn);
    log("run: " + cmd);
    post("/api/run", { command: cmd, project: project })
      .then(function (r) { return r.text().then(function (t) { try { return JSON.parse(t); } catch (e) { return { message: t }; } }); })
      .then(function (res) {
        var msg = res.message || "";
        if (msg.indexOf("\n") >= 0) {
          openDrawer(cmd, project, msg, btn);
          log("run: " + (res.ok ? "done" : "failed") + " (output in the logs panel)");
          toast(res.ok ? "ok" : "failed", cmd + ": " + (res.ok ? "done, output in the logs panel" : "failed, output in the logs panel"));
        } else {
          log("run: " + msg);
          toast(res.ok === false ? "failed" : "ok", cmd + ": " + (msg || (res.ok === false ? "failed" : "done")));
        }
      })
      .catch(function (err) { log("run: " + err.message); toast("failed", cmd + ": " + err.message); })
      .then(function () {
        if (key) delete inFlight[key];
        // the button may have been replaced by a re-render: find the live one
        var live = key ? keyed(key) : btn;
        [btn, live].forEach(function (el) {
          if (!el) return;
          el.disabled = false;
          if (!el.classList.contains("menu-item")) el.textContent = label;
        });
        poll();
        if (typeof loadRepos === "function" && reposState.tab === "repos") loadRepos();
      });
  }

  // Learn modal: pick an installed agent the folder allows (config
  // agentRules), then Start setup: stackfork opens it in a terminal with
  // the prompt. A disabled agent keeps its reason on screen.
  var learn = { repo: "", opener: null };

  function openLearn(btn) {
    var modal = document.getElementById("learn-modal");
    var agents = document.getElementById("learn-agents");
    var status = document.getElementById("learn-status");
    var start = document.getElementById("learn-start");
    learn.repo = btn.dataset.repo;
    learn.opener = openerOf(btn);
    document.getElementById("learn-title").textContent = "Set up " + btn.dataset.name + " with your agent";
    document.getElementById("learn-prompt").textContent = btn.dataset.prompt || btn.dataset.copy;
    agents.textContent = "Looking for agents\u2026";
    status.textContent = "";
    start.disabled = true;
    modal.showModal();
    fetch("/api/agents?repo=" + encodeURIComponent(btn.dataset.repo), { credentials: "same-origin" })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.json(); })
      .then(function (d) {
        agents.innerHTML = d.agents.map(function (a) {
          var usable = a.available && d.canOpen;
          var sub = a.available ? (d.canOpen ? "Opens in " + ({ warp: "Warp", iterm: "iTerm", terminal: "Terminal" }[d.terminal] || d.terminal) : "Copy the prompt instead") : a.reason;
          return '<label class="agent-opt"><input type="radio" name="learn-agent" value="' + escapeHtml(a.id) + '"' + (usable ? "" : " disabled") + ">" +
            '<span class="agent-text"><span class="agent-name">' + escapeHtml(a.name) + '</span><span class="agent-sub">' + escapeHtml(sub) + "</span></span></label>";
        }).join("");
        var first = agents.querySelector("input:not(:disabled)");
        if (first) {
          first.checked = true;
          start.disabled = false;
          if (document.activeElement === document.getElementById("learn-close")) first.focus();
        }
      })
      .catch(function (e) { agents.textContent = "Could not list agents: " + e.message; });
  }

  function wireLearn() {
    var modal = document.getElementById("learn-modal");
    var status = document.getElementById("learn-status");
    var start = document.getElementById("learn-start");
    document.getElementById("learn-close").addEventListener("click", function () { modal.close(); });
    document.getElementById("learn-cancel").addEventListener("click", function () { modal.close(); });
    modal.addEventListener("close", function () { focusBack(learn.opener); learn.opener = null; });
    start.addEventListener("click", function () {
      var pick = document.querySelector('#learn-agents input[name="learn-agent"]:checked');
      if (!pick) return;
      status.textContent = "Opening\u2026";
      start.disabled = true;
      post("/api/learn", { repo: learn.repo, agent: pick.value })
        .then(function (r) { return r.json(); })
        .then(function (res) {
          status.textContent = res.message;
          log("learn: " + res.message);
          if (res.ok) setTimeout(function () { if (modal.open) modal.close(); }, 1200);
          else start.disabled = false;
        })
        .catch(function (e) { status.textContent = e.message; start.disabled = false; });
    });
  }

  function wireRun() {
    document.addEventListener("click", function (e) {
      var lb = e.target.closest(".learn-btn");
      if (lb) openLearn(lb);
    });
    wireLearn();
    document.getElementById("learn-copy").addEventListener("click", function (e) {
      copyText(document.getElementById("learn-prompt").textContent, "learn prompt", e.target);
    });
    document.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-run]");
      if (btn && !btn.disabled) runCommand(btn);
    });
  }

  function start() {
    wireRun();
    wireControls();
    wireActions();
    wireMenus();
    wireCleanup();
    wireLogsPanel();
    wireRepos();
    loadDisk();
    poll();
    setInterval(function () {
      if (document.visibilityState === "visible") poll();
    }, 3000);
    setInterval(function () {
      if (document.visibilityState === "visible") loadDisk();
    }, 60000);
  }

  // The script tests (appscript_runs_test.go) load this file in Node and ask
  // for the functions they exercise; a browser never defines __sfExport.
  if (typeof window.__sfExport === "function") {
    window.__sfExport({ reposState: reposState, paintRuns: paintRuns, onStreamObject: onStreamObject, fmtDur: fmtDur, runChip: runChip, versionHtml: versionHtml, agoText: agoText, pickStep: pickStep, stepsHtml: stepsHtml });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
