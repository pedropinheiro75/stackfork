// stackfork dashboard client (design.md D3/D3a). Plain JS, no framework:
// polls /api/instances every 3s while the tab is visible and re-renders the
// stack list, keeping filter/collapse/panel state across refreshes. Every
// POST carries the per-process token from the page's own <meta> tag and
// runs through /api/actions (design.md D4/D5).

(function () {
  "use strict";

  var token = document.querySelector('meta[name="stackfork-token"]').content;
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
      return '<a class="app-chip' + stoppedClass + '" href="' + escapeHtml(app.url) + '" target="_blank" rel="noopener" title="' + escapeHtml(app.url) + '">' +
        escapeHtml(app.label) + "</a>";
    }
    var hint = app.hint ? ": " + escapeHtml(app.hint) : "";
    return '<span class="app-chip stopped">' + escapeHtml(app.label) + hint + "</span>";
  }

  function endpointHtml(ep, running) {
    if (!ep.known) {
      return '<span class="endpoint-pending">container ' + ep.container + ' &mdash; assigned on start</span>';
    }
    var stoppedClass = running ? "" : " stopped";
    var stoppedNote = running ? "" : " (stopped)";
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

  function serviceHtml(inst, svc) {
    var key = inst.project + "/" + svc.name;
    var running = svc.state === "running";
    var endpoints = (svc.endpoints || []).map(function (e) { return endpointHtml(e, running); }).join("");
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
    var image = svc.image ? '<span class="service-image">' + escapeHtml(svc.image) + "</span>" : "";
    var settingsOpen = !!openSettings[key];
    return (
      '<div class="service-row" data-service="' + escapeHtml(svc.name) + '">' +
      '<div class="service-summary" data-toggle-settings data-key="' + escapeHtml(key) + '">' +
      '<span class="service-logo" data-kind="' + escapeHtml(svc.kind || "") + '"></span>' +
      '<span class="status-dot ' + escapeHtml(svc.state) + '"></span>' +
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
      (warnings ? '<ul class="warnings">' + warnings + "</ul>" : "") +
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
    return fetch("/api/actions", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-Stackfork-Token": token },
      body: JSON.stringify({ action: action, project: project, service: service || "" }),
    })
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
    return fetch("/api/copy", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-Stackfork-Token": token },
      body: JSON.stringify({ project: project, service: service || "", kind: kind }),
    }).then(function (r) {
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
              if (navigator.clipboard) navigator.clipboard.writeText(value);
              log("copied " + copy.textContent.trim());
            })
            .catch(function (err) {
              log("copy failed: " + err.message);
            });
        } else if (navigator.clipboard) {
          navigator.clipboard.writeText(copy.dataset.copy);
          log("copied " + copy.textContent.trim());
        }
      }
    });
  }

  function wireLogsPanel() {
    document.getElementById("logs-close").addEventListener("click", function () {
      document.getElementById("logs-panel").hidden = true;
    });
  }

  function start() {
    wireControls();
    wireActions();
    wireLogsPanel();
    poll();
    setInterval(function () {
      if (document.visibilityState === "visible") poll();
    }, 3000);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
