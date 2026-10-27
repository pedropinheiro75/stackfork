// Project page (static/project.js): hash routing and the markup it renders from
// testdata/project.fixture.json, in its own Node context with a stub document.
const fs = require("fs"), path = require("path"), vm = require("vm");

module.exports = function (_ex, t) {
  const { assert } = t;
  const fixture = JSON.parse(fs.readFileSync(path.join(__dirname, "project.fixture.json"), "utf8"));

  // --- a stub page: parts are found by data-part in whatever innerHTML was last written
  const parts = {};
  const title = { className: "", attrs: {}, innerHTML: "", setAttribute(k, v) { this.attrs[k] = v; }, removeAttribute(k) { delete this.attrs[k]; } };
  const body = {
    _html: "",
    set innerHTML(h) {
      this._html = h;
      Object.keys(parts).forEach((k) => delete parts[k]);
      for (const m of h.matchAll(/data-part="([^"]+)"/g)) parts[m[1]] = { innerHTML: "", textContent: "", dataset: {}, contains: () => false, querySelector: () => null };
    },
    get innerHTML() { return this._html; },
    querySelector(sel) {
      const m = /^\[data-part="([^"]+)"\]$/.exec(sel);
      if (m) return parts[m[1]] || null;
      return null;
    },
  };
  const doc = {
    readyState: "complete", activeElement: null, visibilityState: "visible", title: "",
    getElementById: (id) => (id === "project-body" ? body : id === "view-title" ? title : null),
    addEventListener() {},
  };
  const fetched = [];
  const win = {};
  let exported = null;
  win.__sfProjectExport = (e) => { exported = e; };
  const ctx = {
    window: win, document: doc, console, URLSearchParams, location: { hash: "" }, history: { replaceState(_a, _b, h) { ctx.location.hash = h; } },
    setInterval() { return 1; }, clearInterval() {}, setTimeout() {}, Date,
    fetch(url) {
      fetched.push(url);
      const ok = (j) => Promise.resolve({ ok: true, status: 200, text: () => Promise.resolve(JSON.stringify(j)) });
      if (url.indexOf("/api/project/resources") === 0) return ok({ range: "1h", series: [], services: [] });
      if (url.indexOf("name=ghost") >= 0) return Promise.resolve({ ok: false, status: 404, text: () => Promise.resolve('{"error":"no repo"}') });
      return ok(fixture);
    },
  };
  vm.createContext(ctx);
  vm.runInContext(fs.readFileSync(path.join(__dirname, "..", "static", "project.js"), "utf8"), ctx);
  assert.ok(exported, "project.js did not export for the test");
  const P = exported.api;

  // --- routing
  const r = P.parseHash("#project/acme-web?mode=hybrid&checkout=fix%2Flogin&range=6h");
  assert.equal(r.name, "acme-web");
  assert.equal(r.q.get("mode"), "hybrid");
  assert.equal(r.q.get("checkout"), "fix/login");
  assert.equal(P.parseHash("#project/my%20repo").name, "my repo");
  assert.equal(P.parseHash("#repos"), null);
  assert.equal(P.hashFor({ name: "my repo", mode: "hybrid", checkout: "fix/login", range: "6h", path: "" }), "#project/my%20repo?mode=hybrid&checkout=fix%2Flogin&range=6h");
  assert.equal(P.hashFor({ name: "a", mode: "", checkout: "", range: "1h", path: "" }), "#project/a?checkout=none", "no checkout is written as none");

  // --- the states that need no data
  assert.match(exported.skeleton("billing-api"), /Loading billing-api…/);
  assert.match(exported.notFound("old-site"), /No repository named old-site[\s\S]*Back to Repos[\s\S]*Rescan/);
  assert.doesNotMatch(exported.notFound("<img src=x>"), /<img/, "the name is escaped");

  // --- markup from the fixture
  exported.setState({ name: "acme-web", path: "", data: fixture, status: "ready", error: "", mode: "", checkout: null, range: "1h", collapsed: {}, branchesOpen: false });
  assert.equal(exported.selectedCheckout().key, "feature/invoices", "the newest finished run is selected, not the one still running");
  assert.equal(exported.viewMode(), "hybrid", "the selected run was hybrid");

  const head = exported.header(fixture);
  assert.match(head, /<h1 class="pp-title">acme-web<\/h1>/);
  assert.match(head, /2 running/);
  assert.match(head, /1 starting/);
  assert.match(head, /role="switch"[^>]*aria-checked="true"[^>]*data-live-op="off"/);
  assert.match(head, /Start&hellip;/);
  assert.match(head, /data-repo-action="start"[^>]*data-path="[^"]*fix-login"/, "a stopped checkout can be started from the menu");
  assert.match(head, /data-branch="backup\/old-checkout"/, "a branch without a worktree can be started from the menu");
  assert.doesNotMatch(head, /data-path="\/work\/clients\/acme\/acme-web"/, "a running checkout is not offered");

  const co = exported.checkoutsBody(fixture);
  assert.equal((co.match(/class="pp-co-row /g) || []).length, 5);
  assert.match(co, /<a class="btn" href="http:\/\/acme-web-9b21e07d4c3a\.localhost"[^>]*>Open App<\/a>/);
  assert.match(co, /data-repo-action="start"[^>]*>Retry<\/button>/, "a failed run turns Start into Retry");
  assert.match(co, /data-logs data-project="sf-acme-web-search"/, "a starting checkout offers Logs");
  assert.match(co, /No runs yet/);
  assert.match(co, /Failed at migrate/);
  assert.match(co, /data-select="feature\/invoices"[^>]*aria-pressed="true"/);
  assert.match(co, /data-select="fix\/login"[^>]*aria-pressed="false"/);
  assert.match(co, /2 branches not checked out/);
  assert.match(co, /data-stop data-project="sf-acme-web-main"/, "a running checkout can be stopped from its menu");

  const wf = exported.workflowTools(fixture);
  assert.match(wf, /name="wf-mode" value="hybrid" checked/);
  assert.match(wf, /Container <span class="pp-default-tag">default<\/span>/);
  assert.match(wf, /data-make-default/, "hybrid is not the default, so Make default shows");
  assert.match(wf, /<option value="feature\/invoices" selected>/);
  exported.getState().mode = "container";
  assert.doesNotMatch(exported.workflowTools(fixture), /data-make-default/);
  assert.match(exported.workflowTools(fixture), /data-mode-to="hybrid"/, "a run in the other mode offers Show hybrid");
  exported.getState().mode = "";

  const cfg = exported.configBody(fixture);
  assert.match(cfg, /data-secret-add data-repo="\/work\/clients\/acme\/acme-web"/);
  assert.match(cfg, /<span class="reload-chip" title="Edits are picked up live\. No restart needed\.">hot reload<\/span><span class="small muted">inferred/);
  assert.match(cfg, /Resolves/);
  assert.match(cfg, /Missing/);
  assert.match(cfg, /data-env-reset data-project="sf-acme-web-invoices" data-count="2"/);
  assert.match(cfg, /••••••/, "a masked value shows bullets, never the value");
  assert.match(cfg, /<option value="suggest" selected>/);
  assert.doesNotMatch(cfg, /data-cleanup-for="[^"]*"[^>]*disabled/, "cleanup is editable while Live is on");
  const off = JSON.parse(JSON.stringify(fixture));
  off.live = { on: false, state: "off", reason: "" };
  assert.match(exported.configBody(off), /data-cleanup-for="[^"]*"[^>]*disabled/);

  // --- hostile text is escaped
  const evil = JSON.parse(JSON.stringify(fixture));
  evil.name = "<b>x</b>";
  evil.checkouts[0].branch = '"><img src=x onerror=1>';
  const html = exported.header(evil) + exported.checkoutsBody(evil);
  assert.doesNotMatch(html, /<img src=x|<b>x/);

  // --- not set up: the setup callout, no workflow or resources sections
  const bare = JSON.parse(JSON.stringify(fixture));
  bare.knowledge = { state: "none" };
  const pageHtml = exported.page(bare);
  assert.match(pageHtml, /data-setup-callout/);
  assert.doesNotMatch(pageHtml, /id="sec-workflow"|id="sec-resources"/);
  assert.match(exported.page(fixture), /id="sec-workflow"[\s\S]*id="sec-resources"[\s\S]*id="sec-config"/);
  assert.match(exported.header(bare), /Not set up/);

  // --- enter(): fetch, render the parts, call D and E
  const calls = { wf: [], res: [] };
  win.sfProjectWorkflow = { render(el, p, o) { calls.wf.push({ name: p.name, mode: o.mode, checkout: o.checkout, onSelect: typeof o.onSelect }); } };
  win.sfProjectResources = { render(el, r, o) { calls.res.push({ range: o.range, onRange: typeof o.onRange, series: r.series.length }); } };
  exported.setState(null);
  ctx.location.hash = "#project/acme-web?range=15m";
  P.enter();
  return new Promise((resolve) => setImmediate(resolve)).then(() => new Promise((resolve) => setImmediate(resolve))).then(() => {
    assert.ok(fetched.some((u) => u === "/api/project?name=acme-web"), "fetches the project: " + fetched.join(" "));
    assert.ok(fetched.some((u) => u === "/api/project/resources?name=acme-web&range=15m"), "fetches the resources for the range in the URL");
    assert.match(parts.header.innerHTML, /acme-web/);
    assert.match(parts.checkouts.innerHTML, /pp-co-row/);
    assert.match(parts["workflow-tools"].innerHTML, /wf-mode/);
    assert.deepEqual(calls.wf[0], { name: "acme-web", mode: "hybrid", checkout: "feature/invoices", onSelect: "function" });
    assert.deepEqual(calls.res[0], { range: "15m", onRange: "function", series: 0 });
    assert.match(title.innerHTML, /<a href="#repos">Repos<\/a>[\s\S]*aria-current="page">acme-web</);
    assert.equal(title.attrs.role, "navigation");

    // an unknown project is the not-found page
    ctx.location.hash = "#project/ghost";
    P.enter();
    return new Promise((resolve) => setImmediate(resolve)).then(() => new Promise((resolve) => setImmediate(resolve)));
  }).then(() => {
    assert.match(body.innerHTML, /No repository named ghost/);
    P.leave();
    assert.equal(title.attrs.role, undefined, "leaving gives the toolbar title back");

    // selectors quote a Windows path, and relPath reads either separator
    assert.equal(exported.cssq('C:\\work\\a"b'), '"C:\\\\work\\\\a\\"b"');
    assert.equal(exported.relPath("C:\\repo", "C:\\repo\\.worktrees\\x"), ".worktrees\\x");
    assert.equal(exported.relPath("/r", "/r/.worktrees/x"), ".worktrees/x");
  });
};
