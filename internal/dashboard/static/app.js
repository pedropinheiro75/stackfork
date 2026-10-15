// stackfork dashboard client (design.md D3/D3a). Plain JS, no framework:
// polls /api/instances every 3s while the tab is visible and re-renders the
// stack list, keeping filter/collapse/panel state across refreshes. Every
// POST carries the per-process token from the page's own <meta> tag and
// runs through /api/actions (design.md D4/D5).

(function () {
  "use strict";

  var token = document.querySelector('meta[name="stackfork-token"]').content;

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
  var openMenus = {};

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

  function cliCommand(warning) {
    var m = /`([^`]+)`/.exec(warning);
    return m ? m[1] : null;
  }

  function commandsHtml(inst) {
    var cmds = ["stackfork down --instance " + inst.project + " --volumes"];
    (inst.warnings || []).forEach(function (w) {
      var cmd = cliCommand(w);
      if (cmd && cmds.indexOf(cmd) === -1) cmds.push(cmd);
    });
    var html = cmds.map(function (c) {
      return '<code class="copyable cli-command" data-copy="' + escapeHtml(c) + '" title="click to copy">' + escapeHtml(c) + "</code>";
    }).join("");
    if (inst.env_block) {
      html += '<code class="copyable cli-command env-block" data-copy="' + escapeHtml(inst.env_block) +
        '" data-copy-kind="env_block" data-copy-project="' + escapeHtml(inst.project) + '" title="click to copy">Copy .env for host app</code>';
    }
    return html;
  }

  function appChipHtml(app) {
    if (app.url) {
      var stoppedClass = app.running ? "" : " stopped";
      var title = app.url + (app.direct ? " (direct: " + app.direct + ")" : "");
      return '<a class="app-chip' + stoppedClass + '" href="' + escapeHtml(app.url) + '" target="_blank" rel="noopener" title="' + escapeHtml(title) + '">' +
        escapeHtml(app.label) + "</a>";
    }
    var hint = app.hint ? ": " + escapeHtml(app.hint) : "";
    return '<span class="app-chip stopped">' + escapeHtml(app.label) + hint + "</span>";
  }

  function endpointHtml(ep, running, optional) {
    if (!ep.known) {
      return '<span class="endpoint-pending">container ' + ep.container + ' &mdash; assigned on start</span>';
    }
    var stoppedClass = running ? "" : " stopped";
    var stoppedNote = running ? "" : (optional ? " (not started)" : " (stopped)");
    var proxiedNote = ep.proxied ? " (proxy)" : "";
    if (ep.scheme === "http" || ep.scheme === "https") {
      return '<a class="endpoint-link' + stoppedClass + '" href="' + escapeHtml(ep.url) + '" target="_blank" rel="noopener" title="' + escapeHtml(ep.url) + '">' +
        escapeHtml(ep.url) + stoppedNote + proxiedNote + "</a>";
    }
    return '<span class="endpoint-copy copyable' + stoppedClass + '" data-copy="' + escapeHtml(ep.url) + '" title="click to copy">' +
      escapeHtml(ep.url) + stoppedNote + "</span>";
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

  // dotClass must match dashboard/dot.go's dotClass.
  function dotClass(svc) {
    if (svc.state === "running") {
      if (svc.health === "unhealthy") return "unhealthy";
      if (svc.health === "health: starting") return "starting";
    }
    return svc.state;
  }

  function dotTitle(svc) {
    if (svc.state === "optional") return "not started (optional)";
    if (svc.state === "not-started") return "not started";
    if (svc.state === "waiting") return "waiting for " + (svc.waiting_for || []).join(", ") + " to become healthy";
    if (svc.state === "running" && svc.health === "unhealthy") return "running, unhealthy";
    if (svc.state === "running" && svc.health === "health: starting") return "running, health check starting";
    return svc.state;
  }

  function oneOffHtml(inst) {
    if (!inst.oneoff_running && !inst.oneoff_leftover) return "";
    var h = '<div class="oneoff-bar" data-oneoff="1">';
    if (inst.oneoff_running) h += "<span>" + inst.oneoff_running + " one-off task(s) running</span>";
    if (inst.oneoff_leftover) {
      h += "<span>" + inst.oneoff_leftover + " leftover one-off container(s)";
      if (inst.oneoff_cleanup) h += ': <code class="copyable cli-command" data-copy="' + escapeHtml(inst.oneoff_cleanup) + '" title="click to copy">' + escapeHtml(inst.oneoff_cleanup) + "</code>";
      h += "</span>";
    }
    return h + "</div>";
  }

  function serviceHtml(inst, svc) {
    var key = inst.project + "/" + svc.name;
    var running = svc.state === "running";
    var endpoints = (svc.endpoints || []).map(function (e) { return endpointHtml(e, running, svc.state === "optional" || svc.state === "not-started" || svc.state === "waiting"); }).join("");
    if (svc.connection_url) {
      endpoints += '<span class="endpoint-copy copyable connection-url" data-copy="' + escapeHtml(svc.connection_url) +
        '" data-copy-kind="database_url" data-copy-project="' + escapeHtml(inst.project) + '" data-copy-service="' + escapeHtml(svc.name) +
        '" title="click to copy the connection URL (' + escapeHtml(svc.connection_url) + ')">connection URL</span>';
    }
    (svc.use_commands || []).forEach(function (c) {
      endpoints += '<span class="endpoint-copy copyable use-command" data-copy="' + escapeHtml(c.value) +
        '" data-copy-kind="cli" data-copy-project="' + escapeHtml(inst.project) + '" data-copy-service="' + escapeHtml(svc.name) +
        '" title="click to copy: ' + escapeHtml(c.value) + '">' + escapeHtml(c.label) + "</span>";
    });
    var image = svc.state === "optional" ? '<span class="service-image">not started (optional)</span>'
      : svc.image ? '<span class="service-image">' + escapeHtml(svc.image) + "</span>" : "";
    var settingsOpen = !!openSettings[key];
    return (
      '<div class="service-row" data-service="' + escapeHtml(svc.name) + '">' +
      '<div class="service-summary" data-toggle-settings data-key="' + escapeHtml(key) + '">' +
      '<span class="service-logo" data-kind="' + escapeHtml(svc.kind || "") + '"></span>' +
      '<span class="status-dot ' + escapeHtml(dotClass(svc)) + '" title="' + escapeHtml(dotTitle(svc)) + '"></span>' +
      '<div class="service-text"><span class="service-name">' + escapeHtml(svc.name) + "</span>" + image + "</div>" +
      '<div class="service-endpoints">' + endpoints + "</div>" +
      '<div class="service-icons">' +
      (svc.state === "running"
        ? '<button class="icon-btn" data-action="service-stop" data-project="' + escapeHtml(inst.project) + '" data-service="' + escapeHtml(svc.name) + '" title="Stop">&#9632;</button>'
        : '<button class="icon-btn" data-action="service-start" data-project="' + escapeHtml(inst.project) + '" data-service="' + escapeHtml(svc.name) + '" title="Start">&#9654;</button>') +
      '<button class="icon-btn" data-action="restart" data-project="' + escapeHtml(inst.project) + '" data-service="' + escapeHtml(svc.name) + '" title="Restart">&#8635;</button>' +
      '<button class="icon-btn logs-button" data-project="' + escapeHtml(inst.project) + '" data-service="' + escapeHtml(svc.name) + '" title="Logs">&#9776;</button>' +
      "</div></div>" +
      '<div class="service-settings"' + (settingsOpen ? "" : " hidden") + ">" + settingsHtml(svc, inst.files) + "</div>" +
      "</div>"
    );
  }

  function stackHtml(inst) {
    var p = escapeHtml(inst.project);
    var running = inst.state === "running" || inst.state === "partial";
    var collapsed = isCollapsed(inst.project, !running);
    var services = (inst.services || []).map(function (s) { return serviceHtml(inst, s); }).join("");
    var legacy = inst.legacy ? '<span class="legacy-tag">legacy</span>' : "";
    var knowledge = inst.knowledge ? '<span class="knowledge knowledge-' + escapeHtml(inst.knowledge.status) + '">' + escapeHtml(inst.knowledge.status) + "</span>" : "";
    var warnings = (inst.warnings || []).map(function (w) { return '<li class="warning">' + escapeHtml(w) + "</li>"; }).join("");
    var seedBar = inst.seed_command
      ? '<div class="seed-bar" data-seed="failed">The database seed failed &mdash; retry with <code class="copyable cli-command" data-copy="' + escapeHtml(inst.seed_command) +
        '" title="click to copy">' + escapeHtml(inst.seed_command) + '</code> (or, to run it again on a populated database, <code class="copyable cli-command" data-copy="' +
        escapeHtml(inst.seed_force) + '" title="click to copy">' + escapeHtml(inst.seed_force) + "</code>)</div>"
      : "";
    var menuOpen = !!openMenus[inst.project];
    var colorIndex = stackColorIndex(inst.project);
    var appStrip = (inst.apps || []).map(appChipHtml).join("");
    return (
      '<article class="stack" data-project="' + p + '" data-state="' + escapeHtml(inst.state) + '" data-collapsed="' + (collapsed ? "1" : "0") + '">' +
      '<header class="stack-header" data-toggle-stack data-project="' + p + '">' +
      '<span class="stack-icon stack-color-' + colorIndex + '">' + escapeHtml((projectSlug(inst.project || "?").charAt(0) || "?").toUpperCase()) + "</span>" +
      '<div class="stack-title"><span class="stack-name" title="' + escapeHtml(inst.hostname) + '">' + escapeHtml(inst.hostname) + "</span>" +
      '<span class="stack-path" title="' + escapeHtml(inst.root) + '">' + escapeHtml(inst.root) + "</span></div>" +
      '<span class="branch-tag" title="' + escapeHtml(inst.branch) + '">' + escapeHtml(inst.branch) + "</span>" +
      legacy + knowledge +
      '<span class="state-badge state-' + escapeHtml(inst.state) + '">' + escapeHtml(inst.state) + "</span>" +
      '<div class="stack-actions">' +
      (inst.state !== "running" ? '<button class="icon-btn" data-action="up" data-project="' + p + '" title="Up">&#9654;</button>' : "") +
      (inst.state === "running" || inst.state === "partial" ? '<button class="icon-btn" data-action="down" data-project="' + p + '" title="Down (keeps volumes)">&#9632;</button>' : "") +
      '<button class="icon-btn stack-menu-toggle" data-project="' + p + '" title="More">&#8942;</button>' +
      "</div></header>" +
      (appStrip ? '<div class="app-strip">' + appStrip + "</div>" : "") +
      '<div class="stack-menu"' + (menuOpen ? "" : " hidden") + ">" + commandsHtml(inst) + "</div>" +
      (warnings ? '<ul class="warnings">' + warnings + "</ul>" : "") + oneOffHtml(inst) + seedBar +
      '<div class="stack-body">' + services + "</div></article>"
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

  function render() {
    var inv = state.inventory;
    if (!inv) return;
    var instances = (inv.instances || []).filter(matchesFilter);
    var running = instances.filter(function (i) { return i.state === "running" || i.state === "partial"; });
    var stopped = instances.filter(function (i) { return i.state !== "running" && i.state !== "partial"; });

    var runningSection = document.querySelector('[data-section="running"]');
    var stoppedSection = document.querySelector('[data-section="stopped"]');
    runningSection.querySelector(".stack-list").innerHTML = running.map(stackHtml).join("");
    runningSection.querySelector(".section-count").textContent = running.length;
    stoppedSection.querySelector(".stack-list").innerHTML = stopped.map(stackHtml).join("");
    stoppedSection.querySelector(".section-count").textContent = stopped.length;
    stoppedSection.hidden = state.runningOnly;

    document.getElementById("instances").classList.toggle("stale", state.stale);

    var runningCount = (inv.instances || []).filter(function (i) {
      return i.state === "running" || i.state === "partial";
    }).length;
    document.getElementById("counts").textContent = runningCount + " / " + (inv.instances || []).length + " running";

    var dockerStatus = document.getElementById("docker-status");
    dockerStatus.textContent = "Docker: " + (inv.docker.reachable ? "ok" : "unreachable");
    dockerStatus.className = inv.docker.reachable ? "ok" : "down";

    var proxyStatus = document.getElementById("proxy-status");
    proxyStatus.textContent = "Proxy: " + (inv.proxy.running ? "running on " + inv.proxy.port : "stopped");
    proxyStatus.className = inv.proxy.running ? "ok" : "down";
    document.getElementById("proxy-start").hidden = inv.proxy.running;

    document.getElementById("docker-banner").hidden = inv.docker.reachable;
    var proxyBanner = document.getElementById("proxy-banner");
    proxyBanner.textContent = inv.proxy.hint || "";
    proxyBanner.hidden = !inv.proxy.hint;
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
    // Outside #instances (header-level, design.md D3b §5), so it needs its
    // own listener rather than the delegated one in wireActions.
    document.getElementById("proxy-start").addEventListener("click", function () {
      runAction("proxy-up", "");
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
        document.getElementById("disk-banner").hidden = !d.full;
        document.getElementById("disk-text").textContent = d.text || "";
        document.getElementById("cleanup-disk").textContent = d.text || "";
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
    var panel = document.getElementById("cleanup-panel");
    var toggle = document.getElementById("cleanup-toggle");
    toggle.addEventListener("click", function () {
      panel.hidden = !panel.hidden;
      toggle.setAttribute("aria-expanded", String(!panel.hidden));
      if (!panel.hidden) { loadCleanup(); loadDisk(); }
    });
    panel.addEventListener("click", function (e) {
      var copy = e.target.closest(".copyable");
      if (copy) copyText(copy.dataset.copy, copy.textContent.trim(), copy);
    });
  }

  function wireActions() {
    document.getElementById("instances").addEventListener("click", function (e) {
      var toggleStack = e.target.closest("[data-toggle-stack]");
      if (toggleStack && !e.target.closest(".stack-actions")) {
        var article = toggleStack.closest(".stack");
        var collapsed = article.dataset.collapsed !== "1";
        article.dataset.collapsed = collapsed ? "1" : "0";
        setCollapsed(toggleStack.dataset.project, collapsed);
        return;
      }
      var menuToggle = e.target.closest(".stack-menu-toggle");
      if (menuToggle) {
        var menu = menuToggle.closest(".stack").querySelector(".stack-menu");
        var open = menu.hasAttribute("hidden");
        if (open) menu.removeAttribute("hidden");
        else menu.setAttribute("hidden", "");
        openMenus[menuToggle.dataset.project] = open;
        return;
      }
      var toggleSettings = e.target.closest("[data-toggle-settings]");
      if (toggleSettings && !e.target.closest(".service-icons") && !e.target.closest(".endpoint-link") && !e.target.closest(".endpoint-copy")) {
        var settings = toggleSettings.closest(".service-row").querySelector(".service-settings");
        var openNow = settings.hasAttribute("hidden");
        if (openNow) settings.removeAttribute("hidden");
        else settings.setAttribute("hidden", "");
        openSettings[toggleSettings.dataset.key] = openNow;
        return;
      }
      var btn = e.target.closest(".icon-btn[data-action]");
      if (btn) {
        runAction(btn.dataset.action, btn.dataset.project, btn.dataset.service);
        return;
      }
      var logsBtn = e.target.closest(".logs-button");
      if (logsBtn) {
        showLogs(logsBtn.dataset.project, logsBtn.dataset.service);
        return;
      }
      var copy = e.target.closest(".copyable");
      if (copy) {
        // Anything actually carrying the placeholder routes through
        // /api/copy for the real value (design.md D3b); the value never
        // gets logged or rendered, only written to the clipboard.
        if (copy.dataset.copyKind && copy.dataset.copy.indexOf("<password>") !== -1) {
          fetchCopyValue(copy.dataset.copyKind, copy.dataset.copyProject, copy.dataset.copyService)
            .then(function (value) {
              copyText(value, copy.textContent.trim(), copy);
            })
            .catch(function (err) {
              log("copy failed: " + err.message);
            });
        } else {
          copyText(copy.dataset.copy, copy.textContent.trim(), copy);
        }
      }
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
      if (el.tagName === "BUTTON") {
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

  function setTab(tab) {
    reposState.tab = tab;
    try { localStorage.setItem("stackfork:tab", tab); } catch (e) { /* ignore */ }
    document.querySelectorAll(".tab").forEach(function (b) {
      b.setAttribute("aria-selected", String(b.dataset.tab === tab));
    });
    document.getElementById("instances").hidden = tab !== "instances";
    document.getElementById("controls").hidden = tab !== "instances";
    document.getElementById("repos-view").hidden = tab !== "repos";
    if (tab === "repos") loadRepos();
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
        var meta = document.getElementById("repos-meta");
        document.getElementById("scan-state").textContent = meta && meta.dataset.scanning === "true" ? "scanning..." : "";
        if (meta && meta.dataset.configured === "false" && !reposState.autoShownSettings) {
          reposState.autoShownSettings = true;
          toggleSettings(true);
        }
      })
      .catch(function () { /* keep what is shown */ });
  }

  function toggleSettings(show) {
    var panel = document.getElementById("settings-panel");
    var btn = document.getElementById("settings-toggle");
    panel.hidden = show === undefined ? !panel.hidden : !show;
    btn.setAttribute("aria-expanded", String(!panel.hidden));
    if (!panel.hidden) loadSettings();
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
      .then(function () { btn.disabled = false; loadRepos(); poll(); });
  }

  function wireRepos() {
    document.querySelectorAll(".tab").forEach(function (b) {
      b.addEventListener("click", function () { setTab(b.dataset.tab); });
    });
    document.getElementById("repo-filter").addEventListener("input", function (e) {
      reposState.filter = e.target.value; applyRepoFilters();
    });
    document.getElementById("ready-only").addEventListener("change", function (e) {
      reposState.readyOnly = e.target.checked; applyRepoFilters();
    });
    document.getElementById("rescan").addEventListener("click", function () { postConfig("rescan", "").then(function () { setTimeout(loadRepos, 800); }); });
    document.getElementById("settings-toggle").addEventListener("click", function () { toggleSettings(); });
    document.getElementById("repos-list").addEventListener("click", function (e) {
      var inst = e.target.closest(".instance-link");
      if (inst) {
        e.preventDefault();
        setTab("instances");
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
    var panel = document.getElementById("settings-panel");
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
    var saved = "instances";
    try { saved = localStorage.getItem("stackfork:tab") || "instances"; } catch (e) { /* ignore */ }
    setTab(saved === "repos" ? "repos" : "instances");
    setInterval(function () {
      if (document.visibilityState === "visible" && reposState.tab === "repos" && document.getElementById("settings-panel").hidden) loadRepos();
    }, 3000);
  }

  function start() {
    wireControls();
    wireActions();
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
