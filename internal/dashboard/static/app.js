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

  function appChipHtml(app, i, project) {
    if (app.url && app.running) {
      var title = app.url + (app.direct ? " (direct: " + app.direct + ")" : "");
      return '<a class="app-chip" data-key="' + escapeHtml(project) + '/app' + i + '" href="' + escapeHtml(app.url) + '" target="_blank" rel="noopener" title="' + escapeHtml(title) + '">' + escapeHtml(app.label) + "</a>";
    }
    var hint = !app.url && app.hint ? ": " + escapeHtml(app.hint) : "";
    return '<span class="app-chip stopped" title="Starts with the stack">' + escapeHtml(app.label) + hint + "</span>";
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
    if (ep.scheme === "http" || ep.scheme === "https") {
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

  // The seed callout: Retry seed is the button; Mark as seeded and the
  // overwriting Re-seed live in the menu, each with its command in mono.
  function seedHtml(inst) {
    if (!inst.seed_command) return "";
    var k = inst.project;
    return '<div class="callout callout-failed" data-seed="failed"><span class="callout-glyph" aria-hidden="true">' + GLYPH.failed + "</span>" +
      '<div class="callout-body"><p class="callout-title">Database seed failed</p>' +
      '<p class="callout-detail">Retry runs it again. If the app already has its data, mark it as seeded instead.</p>' +
      (inst.seed_error ? '<pre class="mono-log" tabindex="0" data-key="' + escapeHtml(k) + '/seed/log" aria-label="Seed output">' + escapeHtml(inst.seed_error) + "</pre>" : "") + "</div>" +
      '<div class="callout-actions"><button type="button" class="btn" data-run="' + escapeHtml(inst.seed_command) + '" data-key="' + escapeHtml(k) +
      '/seed/retry" title="' + escapeHtml(inst.seed_command) + '">Retry seed</button>' +
      menuHtml(k + "/seed/menu", "More seed actions for " + displayName(k), "More seed actions",
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
      '/settings" aria-expanded="' + settingsOpen + '">' + escapeHtml(svc.name) + "</button>" + image + "</div></td>" +
      '<td class="cell-state">' + chipHtml(serviceChip(svc)) + "</td>" +
      '<td class="cell-endpoints">' + endpoints + "</td>" +
      '<td class="cell-actions"><div class="row-actions"><button type="button" class="btn btn-ghost logs-button mobile-hide"' + pa +
      ' data-key="' + escapeHtml(key) + '/logs">Logs</button>' +
      menuHtml(key + "/menu", "More actions for " + svc.name, "More actions",
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

  function stackHtml(inst) {
    var p = escapeHtml(inst.project);
    var name = displayName(inst.project);
    var live = isLive(inst);
    var collapsed = isCollapsed(inst.project, !live);
    var cmds = commandsOf(inst);
    var down = cmds[0];
    var app = null;
    (inst.apps || []).forEach(function (a) { if (!app && a.url && a.running) app = a; });

    var items = "";
    if (inst.state === "partial") items += menuItem(inst.project + "/menu/up", "Start stopped services", "", ' data-action="up" data-project="' + p + '"');
    if (live) {
      items += menuItem(inst.project + "/menu/restart", "Restart", "", ' data-action="restart" data-project="' + p + '"');
      items += menuItem(inst.project + "/menu/down", "Stop", "", ' data-action="down" data-project="' + p + '"', "mobile-only");
    }
    items += menuItem(inst.project + "/menu/path", "Copy path", "", ' data-copy="' + escapeHtml(inst.root) + '"');
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

    var chips = chipHtml(stateChip(inst.state)) + (inst.legacy ? '<span class="chip chip-off">legacy</span>' : "") +
      (inst.knowledge ? '<span class="chip chip-' + (inst.knowledge.status === "draft" || inst.knowledge.status === "stale" ? "attention" : "off") +
        '" title="Setup knowledge">' + escapeHtml(inst.knowledge.status) + "</span>" : "");
    var meta = '<div class="stack-meta mono"><span class="stack-path" title="' + escapeHtml(inst.root) + '">' + escapeHtml(inst.rel_root || inst.root) + "</span>" +
      (inst.hostname ? '<span class="stack-sep" aria-hidden="true"> · </span><span class="stack-host" title="' + escapeHtml(inst.hostname) + '">' + escapeHtml(inst.hostname) + "</span>" : "") + "</div>";
    var actions =
      (!live ? '<button type="button" class="btn" data-action="up" data-project="' + p + '" data-key="' + p + '/up">Start</button>' : "") +
      (live && app ? '<a class="btn mobile-hide" href="' + escapeHtml(app.url) + '" target="_blank" rel="noopener" data-key="' + p + '/open" title="' + escapeHtml(app.url) + '">Open App</a>' : "") +
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
    return (
      '<article class="stack lane-' + (stackColorIndex(inst.project) + 1) + '" data-project="' + p + '" data-state="' + escapeHtml(inst.state) + '" data-collapsed="' + (collapsed ? "1" : "0") + '">' +
      '<header class="stack-header">' +
      '<button type="button" class="btn btn-ghost btn-icon chevron" data-toggle-stack data-project="' + p + '" data-key="' + p + '/toggle" aria-expanded="' + !collapsed +
      '" aria-label="Details for ' + escapeHtml(name) + '"><span class="chevron-glyph" aria-hidden="true"></span></button>' +
      '<span class="avatar" aria-hidden="true">' + escapeHtml((projectSlug(inst.project || "?").charAt(0) || "?").toUpperCase()) + "</span>" +
      '<div class="stack-id"><h3 class="stack-name" title="' + p + '">' + escapeHtml(name) + "</h3>" +
      '<span class="branch-pill" title="' + escapeHtml(inst.branch) + '">⌥ ' + escapeHtml(inst.branch) + "</span>" + chips +
      '<div class="stack-actions">' + actions + "</div>" + meta + "</div></header>" +
      (appStrip ? '<div class="app-strip">' + appStrip + "</div>" : "") +
      seedHtml(inst) + warnings + oneOffHtml(inst) +
      '<div class="stack-body">' + table + "</div></article>"
    );
  }

  // projectSlug must match dashboard.go's projectSlug exactly: strip the
  // sf-/idv- instance prefix so every legacy instance doesn't show the same
  // "R" initial and color.
  function projectSlug(project) {
    if (project.indexOf("sf-") === 0) return project.slice(3);
    if (project.indexOf("idv-") === 0) return project.slice(3);
    return project;
  }

  // stackColorIndex must match dashboard.go's stackColorIndex exactly: a
  // plain byte sum mod the palette size, never a real hash, so the two
  // sides agree without sharing code.
  function stackColorIndex(project) {
    var slug = projectSlug(project);
    var sum = 0;
    for (var i = 0; i < slug.length; i++) sum += slug.charCodeAt(i);
    return sum % 8;
  }

  function matchesFilter(inst) {
    if (!state.filter) return true;
    var needle = state.filter.toLowerCase();
    var haystack = (inst.project + " " + inst.branch + " " + inst.root).toLowerCase();
    return haystack.indexOf(needle) !== -1;
  }

  // lastCards is the HTML last written to the lists. A poll that brings the
  // same data changes nothing in the DOM; one that differs remembers the
  // focused element, the open menu and the scroll position, writes, and
  // puts them back (the cards are replaced wholesale).
  var lastCards = null;

  function keyed(key) {
    var els = document.querySelectorAll("#view-instances [data-key]");
    for (var i = 0; i < els.length; i++) if (els[i].dataset.key === key) return els[i];
    return null;
  }

  function writeCards(running, stopped) {
    var runHtml = running.map(stackHtml).join(""), stopHtml = stopped.map(stackHtml).join("");
    var html = runHtml + "\u0000" + stopHtml;
    if (html === lastCards) return;
    lastCards = html;
    var active = document.activeElement;
    var focusKey = active && active.closest && active.closest("#view-instances") && active.dataset ? active.dataset.key : "";
    var menu = document.querySelector('#view-instances [aria-haspopup="menu"][aria-expanded="true"]');
    var menuKey = menu ? menu.dataset.key : "";
    var scrollY = window.scrollY;
    document.querySelector('[data-section="running"] .stack-list').innerHTML = runHtml;
    document.querySelector('[data-section="stopped"] .stack-list').innerHTML = stopHtml;
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
    var instances = all.filter(matchesFilter);
    var running = instances.filter(isLive);
    var stopped = instances.filter(function (i) { return !isLive(i); });

    writeCards(running, stopped);
    document.querySelector('[data-section="running"] .section-count').textContent = running.length;
    var stoppedSection = document.querySelector('[data-section="stopped"]');
    stoppedSection.querySelector(".section-count").textContent = stopped.length;
    stoppedSection.hidden = state.runningOnly;
    document.getElementById("instances-empty").hidden = all.length > 0;

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
        log(project + " " + action + ": " + (result.body.message || (result.ok ? "ok" : "failed")));
      })
      .catch(function (err) { log(project + " " + action + ": " + err.message); });
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

  function showLogs(project, service) {
    var panel = document.getElementById("logs-panel");
    var title = document.getElementById("logs-title");
    var body = document.getElementById("logs-body");
    title.textContent = project + (service ? " / " + service : "") + " (last 200 lines)";
    body.textContent = "loading...";
    panel.hidden = false;
    var url = "/api/logs?project=" + encodeURIComponent(project) + "&tail=200";
    if (service) url += "&service=" + encodeURIComponent(service);
    fetch(url, { credentials: "same-origin" })
      .then(function (r) { return r.text(); })
      .then(function (text) { body.textContent = text || "(no output)"; })
      .catch(function (err) { body.textContent = "error: " + err.message; });
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
  var cleanupLabels = [["stopped", "Stopped instances"], ["orphaned", "Orphaned (checkout or branch gone)"], ["all", "Everything, running included"]];

  function loadCleanup() {
    var list = document.getElementById("cleanup-list");
    fetch("/api/cleanup", { credentials: "same-origin" })
      .then(function (r) { if (!r.ok) throw new Error("unavailable"); return r.json(); })
      .then(function (data) {
        list.innerHTML = cleanupLabels.map(function (l) {
          var g = data[l[0]];
          return "<li><code class=\"copyable cli-command\" data-copy=\"" + escapeHtml(g.command) + "\" title=\"click to copy\">" +
            escapeHtml(g.command) + "</code> <span class=\"cleanup-count\">" + g.count + " instance(s)</span> " + escapeHtml(l[1]) + "</li>";
        }).join("");
      })
      .catch(function () { list.innerHTML = "<li>Docker is unreachable; run <code>stackfork prune</code> in a terminal.</li>"; });
  }

  // Docker disk: the banner shows from 85% used; the cleanup panel always
  // shows the figure. Measured server-side at most every 5 minutes.
  function loadDisk() {
    fetch("/api/disk", { credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        var banner = document.getElementById("disk-banner");
        banner.hidden = !d.full;
        // attention from 85 %, failed only from 95 % (DESIGN.md Banner strip).
        banner.className = "banner " + (d.percent >= 95 ? "banner-failed" : "banner-attention");
        document.getElementById("disk-text").textContent = d.text || "";
        document.getElementById("cleanup-disk").textContent = d.text || "";
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
      var btn = e.target.closest("button[data-action]");
      if (btn) {
        runAction(btn.dataset.action, btn.dataset.project, btn.dataset.service);
        return;
      }
      var logsBtn = e.target.closest(".logs-button");
      if (logsBtn) {
        showLogs(logsBtn.dataset.project, logsBtn.dataset.service);
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
    document.getElementById("logs-close").addEventListener("click", function () {
      document.getElementById("logs-panel").hidden = true;
    });
  }

  // --- Repos tab and Settings (add-repo-browser) ---------------------------
  // The tab's HTML is rendered by the server (html/template escapes every
  // repo and branch name); this code only swaps it in, filters it, and posts
  // the actions. Branch and path values travel in data-* attributes.

  var reposState = { tab: "instances", filter: "", readyOnly: false, autoShownSettings: false, errors: {} };

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
    document.querySelectorAll("#repos-list .repo-group").forEach(function (g) {
      g.hidden = !g.querySelector(".repo:not([hidden])");
    });
  }

  function loadRepos() {
    fetch("/fragments/repos", { credentials: "same-origin" })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.text(); })
      .then(function (html) {
        var list = document.getElementById("repos-list");
        var open = {};
        list.querySelectorAll("details.repo[open]").forEach(function (d) { open[d.dataset.key] = true; });
        list.innerHTML = html;
        list.querySelectorAll("details.repo").forEach(function (d) { if (open[d.dataset.key]) d.open = true; });
        applyRepoFilters();
        renderRepoErrors();
        document.getElementById("rail-count-repos").textContent = list.querySelectorAll(".repo").length || "";
        var meta = document.getElementById("repos-meta");
        document.getElementById("scan-state").textContent = meta && meta.dataset.scanning === "true" ? "scanning..." : "";
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
      .then(function (html) { document.getElementById("settings-body").innerHTML = html; });
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

  // A failed start / worktree-add stays on its repo row (not only in the
  // activity log) with the exact CLI command to retry it; it survives the
  // list refresh until dismissed or the action succeeds.
  function renderRepoErrors() {
    var list = document.getElementById("repos-list");
    list.querySelectorAll(".repo-error").forEach(function (e) { e.remove(); });
    list.querySelectorAll("details.repo").forEach(function (d) {
      var err = reposState.errors[d.dataset.key];
      if (!err) return;
      var box = document.createElement("div");
      box.className = "repo-error";
      box.setAttribute("role", "alert");
      box.dataset.repo = d.dataset.key;
      var head = document.createElement("strong");
      head.textContent = err.label + " failed";
      var msg = document.createElement("pre");
      msg.className = "repo-error-msg";
      msg.textContent = err.message;
      var retry = document.createElement("div");
      retry.appendChild(document.createTextNode("Retry in a terminal: "));
      var code = document.createElement("code");
      code.className = "copyable";
      code.dataset.copy = err.command;
      code.title = "click to copy";
      code.textContent = err.command;
      retry.appendChild(code);
      var dismiss = document.createElement("button");
      dismiss.type = "button";
      dismiss.className = "repo-btn";
      dismiss.dataset.dismissRepo = d.dataset.key;
      dismiss.textContent = "Dismiss";
      box.appendChild(head); box.appendChild(msg); box.appendChild(retry); box.appendChild(dismiss);
      var summary = d.querySelector("summary");
      summary.parentNode.insertBefore(box, summary.nextSibling);
      d.open = true;
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
    function fail(msg) {
      reposState.errors[d.repo] = { label: label, message: msg, command: retryCommand(d, created) };
    }
    btn.disabled = true;
    var btnText = btn.textContent;
    btn.textContent = d.repoAction === "start" ? "Starting\u2026" : btnText + "\u2026";
    // The latest progress line shows next to the button: a start can take
    // minutes (installs, health checks) and the log is at the page bottom.
    var progress = document.createElement("span");
    progress.className = "row-progress";
    progress.setAttribute("aria-live", "polite");
    progress.textContent = "starting\u2026";
    btn.insertAdjacentElement("beforebegin", progress);
    reposState.busy = (reposState.busy || 0) + 1;
    delete reposState.errors[d.repo];
    log(label + ": started");
    post("/api/actions", body)
      .then(function (r) {
        if ((r.headers.get("Content-Type") || "").indexOf("ndjson") === -1) {
          return r.json().then(function (b) {
            log(label + ": " + (b.message || (r.ok ? "ok" : "failed")));
            if (!r.ok || b.ok === false) fail(b.message || "refused (status " + r.status + ")");
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
            if (o.line.indexOf("created ") === 0) created = o.line.slice(8).split(" (copied")[0];
          } else {
            log(label + ": " + (o.ok ? "done - " : "failed - ") + o.message);
            if (!o.ok) fail(o.message);
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
      .catch(function (e) { log(label + ": " + e.message); fail(e.message); })
      .then(function () {
        btn.disabled = false;
        btn.textContent = btnText;
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
    document.getElementById("rescan").addEventListener("click", function () { postConfig("rescan", "").then(function () { setTimeout(loadRepos, 800); }); });
    document.getElementById("repos-list").addEventListener("click", function (e) {
      var inst = e.target.closest(".instance-link");
      if (inst) {
        e.preventDefault();
        setView("instances");
        var art = document.querySelector('.stack[data-project="' + inst.dataset.project.replace(/"/g, "") + '"]');
        if (art) { art.dataset.collapsed = "0"; art.scrollIntoView({ block: "center" }); }
        return;
      }
      var dis = e.target.closest("[data-dismiss-repo]");
      if (dis) { delete reposState.errors[dis.dataset.dismissRepo]; renderRepoErrors(); return; }
      var act = e.target.closest("[data-repo-action]");
      if (act) { runRepoAction(act); return; }
      var copy = e.target.closest(".copyable");
      if (copy) { e.preventDefault(); copyText(copy.dataset.copy, copy.dataset.label || copy.textContent.trim(), copy); }
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
      if (document.visibilityState === "visible" && reposState.tab === "repos" && !reposState.busy) loadRepos();
    }, 3000);
  }

  // Every command chip the page shows can also run from here (POST
  // /api/run). The server allowlists it and asks for a native macOS
  // confirmation before anything that deletes data. Chips with a
  // placeholder (PATH, CMD, <name>) stay copy-only.
  function runnable(cmd) {
    return /^(stackfork|git|docker) /.test(cmd) && !/[<>…]|\b[A-Z]{3,}\b/.test(cmd);
  }

  function decorateRunnables() {
    document.querySelectorAll("code.cli-command").forEach(function (el) {
      if (el.parentElement.classList.contains("cmd-row") || !runnable(el.dataset.copy || "")) return;
      var row = document.createElement("span");
      row.className = getComputedStyle(el).display === "block" ? "cmd-row block" : "cmd-row";
      el.insertAdjacentElement("beforebegin", row);
      row.appendChild(el);
      var btn = document.createElement("button");
      btn.type = "button";
      btn.className = "run-btn";
      btn.dataset.run = el.dataset.copy;
      btn.title = "run " + el.dataset.copy;
      btn.textContent = "\u25B6 Run";
      row.appendChild(btn);
    });
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
          document.getElementById("logs-title").textContent = cmd;
          document.getElementById("logs-body").textContent = msg;
          document.getElementById("logs-panel").hidden = false;
          log("run: " + (res.ok ? "done" : "failed") + " (output in the logs panel)");
        } else {
          log("run: " + msg);
        }
      })
      .catch(function (err) { log("run: " + err.message); })
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
  // agentRules), and stackfork opens it in a terminal with the prompt.
  function openLearn(btn) {
    var modal = document.getElementById("learn-modal");
    var agents = document.getElementById("learn-agents");
    var status = document.getElementById("learn-status");
    document.getElementById("learn-title").textContent = "Learn " + btn.dataset.name + " with your agent";
    document.getElementById("learn-prompt").textContent = btn.dataset.copy;
    agents.innerHTML = "loading\u2026";
    status.textContent = "";
    modal.showModal();
    fetch("/api/agents?repo=" + encodeURIComponent(btn.dataset.repo), { credentials: "same-origin" })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.json(); })
      .then(function (d) {
        agents.innerHTML = d.agents.map(function (a) {
          return '<button type="button" class="agent-btn" data-agent="' + escapeHtml(a.id) + '"' + (a.available && d.canOpen ? "" : " disabled") + ">" +
            '<strong>' + escapeHtml(a.name) + "</strong>" +
            '<span class="muted">' + escapeHtml(a.available ? (d.canOpen ? "opens in " + ({ warp: "Warp", iterm: "iTerm", terminal: "Terminal" }[d.terminal] || d.terminal) : "copy the prompt instead") : a.reason) + "</span></button>";
        }).join("");
        agents.querySelectorAll(".agent-btn").forEach(function (b) {
          b.addEventListener("click", function () {
            status.textContent = "opening\u2026";
            post("/api/learn", { repo: btn.dataset.repo, agent: b.dataset.agent })
              .then(function (r) { return r.json(); })
              .then(function (res) {
                status.textContent = res.message;
                log("learn: " + res.message);
                if (res.ok) setTimeout(function () { modal.close(); }, 1200);
              })
              .catch(function (e) { status.textContent = e.message; });
          });
        });
      })
      .catch(function (e) { agents.textContent = "could not list agents: " + e.message; });
  }

  function wireRun() {
    document.addEventListener("click", function (e) {
      var lb = e.target.closest(".learn-btn");
      if (lb) openLearn(lb);
    });
    document.getElementById("learn-copy").addEventListener("click", function (e) {
      copyText(document.getElementById("learn-prompt").textContent, "learn prompt", e.target);
    });
    document.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-run]");
      if (btn && !btn.disabled) runCommand(btn);
    });
    decorateRunnables();
    new MutationObserver(decorateRunnables).observe(document.body, { childList: true, subtree: true });
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

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
