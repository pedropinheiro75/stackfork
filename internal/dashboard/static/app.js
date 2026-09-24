// stackfork dashboard client (design.md D3). Plain JS, no framework: polls
// /api/instances every 3s while the tab is visible and re-renders the card
// list, keeping the filter/running-only state across refreshes. Every POST
// carries the per-process token from the page's own <meta> tag and runs
// through /api/actions (design.md D4/D5).

(function () {
  "use strict";

  var token = document.querySelector('meta[name="stackfork-token"]').content;
  var state = { inventory: null, filter: "", runningOnly: false, stale: false };

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

  function groupByCheckout(instances) {
    var byRoot = {};
    var order = [];
    instances.forEach(function (inst) {
      if (!byRoot[inst.root]) {
        byRoot[inst.root] = [];
        order.push(inst.root);
      }
      byRoot[inst.root].push(inst);
    });
    order.sort();
    return order.map(function (root) { return { root: root, instances: byRoot[root] }; });
  }

  function matchesFilter(inst) {
    if (state.runningOnly && inst.state !== "running" && inst.state !== "partial") return false;
    if (!state.filter) return true;
    var needle = state.filter.toLowerCase();
    var haystack = (inst.project + " " + inst.branch + " " + inst.root).toLowerCase();
    return haystack.indexOf(needle) !== -1;
  }

  function endpointHtml(ep) {
    if (ep.scheme === "http" || ep.scheme === "https") {
      return '<a class="endpoint" href="' + escapeHtml(ep.url) + '" target="_blank" rel="noopener">' +
        escapeHtml(ep.url) + "</a>";
    }
    return '<span class="endpoint copyable" data-copy="' + escapeHtml(ep.url) + '">' + escapeHtml(ep.url) + "</span>";
  }

  function serviceHtml(svc) {
    var endpoints = (svc.endpoints || []).map(endpointHtml).join(" ");
    var health = svc.health ? " (" + escapeHtml(svc.health) + ")" : "";
    return '<li class="service service-' + escapeHtml(svc.state) + '">' +
      '<span class="service-name">' + escapeHtml(svc.name) + "</span> " +
      '<span class="service-state">' + escapeHtml(svc.state) + health + "</span> " +
      endpoints + "</li>";
  }

  function cardHtml(inst) {
    var services = (inst.services || []).map(serviceHtml).join("");
    var warnings = (inst.warnings || []).map(function (w) {
      return '<li class="warning">' + escapeHtml(w) + "</li>";
    }).join("");
    var knowledge = inst.knowledge
      ? '<span class="knowledge knowledge-' + escapeHtml(inst.knowledge.status) + '">knowledge: ' +
        escapeHtml(inst.knowledge.status) + "</span>"
      : "";
    var legacy = inst.legacy ? '<span class="legacy-tag">legacy</span>' : "";
    return (
      '<article class="card" data-project="' + escapeHtml(inst.project) + '" data-state="' + escapeHtml(inst.state) + '">' +
      '<header class="card-header">' +
      '<span class="hostname">' + escapeHtml(inst.hostname) + "</span>" +
      '<span class="branch">' + escapeHtml(inst.branch) + "</span>" +
      '<span class="state state-' + escapeHtml(inst.state) + '">' + escapeHtml(inst.state) + "</span>" +
      legacy + "</header>" +
      (services ? '<ul class="services">' + services + "</ul>" : "") +
      knowledge +
      (warnings ? '<ul class="warnings">' + warnings + "</ul>" : "") +
      '<div class="actions">' +
      '<button class="action" data-action="up" data-project="' + escapeHtml(inst.project) + '">Up</button>' +
      '<button class="action" data-action="down" data-project="' + escapeHtml(inst.project) + '">Down</button>' +
      "</div></article>"
    );
  }

  function render() {
    var inv = state.inventory;
    var main = document.getElementById("instances");
    if (!inv) return;

    var groups = groupByCheckout(inv.instances || []);
    var html = groups.map(function (g) {
      var cards = g.instances.filter(matchesFilter).map(cardHtml).join("");
      if (!cards) return "";
      return '<section class="checkout" data-root="' + escapeHtml(g.root) + '">' +
        '<h2 class="checkout-root">' + escapeHtml(g.root) + "</h2>" +
        '<div class="checkout-cards">' + cards + "</div></section>";
    }).join("");
    main.innerHTML = html;
    main.classList.toggle("stale", state.stale);

    var running = (inv.instances || []).filter(function (i) {
      return i.state === "running" || i.state === "partial";
    }).length;
    document.getElementById("counts").textContent = running + " / " + (inv.instances || []).length + " running";

    var dockerStatus = document.getElementById("docker-status");
    dockerStatus.textContent = "Docker: " + (inv.docker.reachable ? "ok" : "unreachable");
    dockerStatus.className = inv.docker.reachable ? "ok" : "down";

    var proxyStatus = document.getElementById("proxy-status");
    proxyStatus.textContent = "Proxy: " + (inv.proxy.running ? "running on " + inv.proxy.port : "stopped");
    proxyStatus.className = inv.proxy.running ? "ok" : "down";

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

  function wireControls() {
    document.getElementById("filter").addEventListener("input", function (e) {
      state.filter = e.target.value;
      render();
    });
    document.getElementById("running-only").addEventListener("change", function (e) {
      state.runningOnly = e.target.checked;
      render();
    });
  }

  function wireActions() {
    document.getElementById("instances").addEventListener("click", function (e) {
      var btn = e.target.closest(".action");
      if (btn) {
        runAction(btn.dataset.action, btn.dataset.project, btn.dataset.service);
        return;
      }
      var copy = e.target.closest(".copyable");
      if (copy && navigator.clipboard) {
        navigator.clipboard.writeText(copy.dataset.copy);
        log("copied " + copy.dataset.copy);
      }
    });
  }

  function start() {
    wireControls();
    wireActions();
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
