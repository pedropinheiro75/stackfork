// Runs static/project-workflow.js against testdata/workflow.fixture.json with
// a stub element (no DOM library). Prints "ok" when every check passes.
const fs = require("fs"), vm = require("vm"), assert = require("assert"), path = require("path");
const win = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, "../static/project-workflow.js"), "utf8"), { window: win, Date, setTimeout, navigator: {} });
const wf = win.sfProjectWorkflow;
const fx = JSON.parse(fs.readFileSync(path.join(__dirname, "workflow.fixture.json"), "utf8"));

function stubEl() {
  const regions = {}, writes = {}, handlers = {};
  const mk = (n) => regions[n] || (regions[n] = {
    hidden: false, scrollTop: 0, scrollLeft: 0, _h: "",
    get innerHTML() { return this._h; }, set innerHTML(v) { this._h = v; writes[n] = (writes[n] || 0) + 1; },
    querySelector() { return null; }, querySelectorAll() { return []; },
  });
  const body = { dataset: {} }, toggle = { setAttribute() {} };
  return {
    regions, writes, handlers, body,
    set innerHTML(v) {}, contains: () => false, addEventListener(t, f) { handlers[t] = f; },
    querySelector(sel) {
      const m = /data-region="(\w+)"/.exec(sel);
      if (m) return mk(m[1]);
      return sel === ".pw-body" ? body : sel === "[data-toggle-palette]" ? toggle : null;
    },
    querySelectorAll() { return []; },
  };
}
// a card/button target for a delegated handler: closest(sel) answers only for its own selector
const target = (sel, dataset = {}, attrs = {}) => ({ tabIndex: 0, dataset, getAttribute: (k) => attrs[k], closest: (s) => (s === sel ? target(sel, dataset, attrs) : null) });
const liveIds = (html) => [...html.matchAll(/<button[^>]*data-node="([^"]*)"[^>]*>/g)].filter((m) => !m[0].includes('tabindex="-1"')).map((m) => m[1]);
const count = (s, re) => (s.match(re) || []).length;

// -- container, no overlay: 5 live cards, 5 dimmed hybrid cards, both mode branches
let el = stubEl(), sel = [], modes = [];
const base = { mode: "container", onSelect: (id) => sel.push(id), onMode: (m) => modes.push(m), onRetry: () => {} };
wf.render(el, fx, base);
let html = el.regions.canvas.innerHTML;
assert.deepStrictEqual(liveIds(html), ["secrets", "prepare", "up", "seed", "migrate"]);
assert.equal(count(html, /class="pw-node"/g), 10, "container 5 + hybrid 5 cards");
assert.equal(count(html, /tabindex="-1" aria-hidden="true"/g), 5, "inactive branch cards are dimmed and out of the tab order");
assert.match(html, /pw-mode-branch" data-on="0" data-mode="hybrid" data-mode-to="hybrid"/, "dimmed branch is clickable");
assert.match(html, /pw-mode-branch" data-on="1" data-mode="container"/);
assert.ok(html.includes("New data volume?") && html.includes(">yes<") && html.includes(">no<"));
assert.ok(html.includes("skip") && html.includes("pw-branch-skip"), "the no branch is a skip note");
assert.ok(!html.includes('data-state="failed"'));
assert.ok(html.includes("nginx:80/"));
assert.equal(el.regions.panel.hidden, true, "panel closed by default");
assert.equal(el.regions.palette.hidden, true, "palette closed by default");
assert.ok(!html.includes("pw-edge-plus"), "no + on connectors");

// -- a click on a dimmed branch asks for that mode
el.handlers.click({ target: target("[data-mode-to]", {}, { "data-mode-to": "hybrid" }), detail: 1 });
assert.deepStrictEqual(modes, ["hybrid"]);

// -- hybrid with the failed run of "login"
el = stubEl(); sel = []; modes = [];
const hy = { mode: "hybrid", checkout: "login", onSelect: (id) => sel.push(id), onMode: (m) => modes.push(m), onRetry: (id) => modes.push("retry:" + id) };
wf.render(el, fx, hy);
html = el.regions.canvas.innerHTML;
assert.deepStrictEqual(liveIds(html), ["secrets", "prepare", "up-deps", "seed-host", "migrate-host", "api", "app"], "focus order = flow order");
assert.match(html, /data-node="migrate-host"[^>]*data-state="failed"/);
assert.match(html, /data-node="api"[^>]*data-state="pending"/);
assert.ok(!/ data-run=/.test(html), "data-run is the document's run-a-command hook; a node must not carry it");
assert.ok(html.includes("not reached: run #38 stopped at migrate-host"));
assert.match(html, /pw-ready" data-on="0"/);
assert.ok(html.includes("Skipped") && html.includes("already prepared"));
assert.ok(html.includes("start together"));
assert.ok(count(html, /pw-edge[^"]*" data-reached="0"/g) >= 3, "the rest of the path is dashed");
assert.ok(el.regions.drafts.innerHTML.includes("seed-host is a draft"));
assert.ok(el.regions.list.innerHTML.includes("migrate-host") && !el.regions.list.innerHTML.includes("&gt;up&lt;"));
// run of the other mode is not drawn on this plan
const wrong = stubEl(); wf.render(wrong, fx, { mode: "container", checkout: "login" });
assert.ok(!wrong.regions.canvas.innerHTML.includes("data-state=\"failed\""), "no overlay when the run used the other mode");

// -- keyboard
const order = liveIds(html);
assert.equal(wf._nav(order, "secrets", "ArrowDown"), "prepare");
assert.equal(wf._nav(order, "secrets", "ArrowUp"), "secrets");
assert.equal(wf._nav(order, "api", "ArrowRight"), "app");
assert.equal(wf._nav(order, "api", "End"), "app");
assert.equal(wf._nav(order, "api", "Home"), "secrets");
assert.equal(wf._nav(order, "api", "x"), null);

// -- panel: opens on click, shows the failure, log, retry, 3 runs, expanded command, learn command
el.handlers.click({ target: target("[data-node]", { node: "migrate-host" }), detail: 1 });
assert.deepStrictEqual(sel, ["migrate-host"]);
let p = el.regions.panel.innerHTML;
assert.equal(el.regions.panel.hidden, false);
assert.equal(el.body.dataset.panel, "1");
assert.ok(p.includes("As stackfork runs it") && p.includes("cd . &amp;&amp; npm run db:migrate"));
assert.ok(p.includes("Failed in run #38") && p.includes("exit 1") && p.includes("connection refused"));
assert.match(p, /data-retry="migrate-host"/);
assert.equal(count(p, /<li><span class="mono">#/g), 3, "last 3 runs only");
assert.ok(p.includes("stackfork learn record step --id migrate-host --kind exec --once schema --on host --cmd "));
assert.ok(p.includes("this machine") || p.includes("This machine"));
const oneOff = JSON.parse(JSON.stringify(fx));
const task = oneOff.workflow.container.nodes.find((n) => n.id === "up");
task.on = "one-off"; task.step_kind = "exec"; task.where = "docker"; task.service = "app";
task.command_raw = "bun install"; task.command = "bun install in app in a one-off container";
const temporary = stubEl(); wf.render(temporary, oneOff, { mode: "container", node: "up" });
assert.ok(temporary.regions.panel.innerHTML.includes("--on one-off --service app --cmd &#39;bun install&#39;"));
el.handlers.click({ target: target("[data-retry]", {}, { "data-retry": "migrate-host" }), detail: 1 });
assert.ok(modes.includes("retry:migrate-host"), "Retry calls onRetry, no POST");

// -- hostile strings are escaped everywhere (command in card, panel, list, learn command)
const raw = `<"'&>`;
for (const [name, out] of [["canvas", html], ["panel", p], ["list", el.regions.list.innerHTML]]) {
  assert.ok(!out.includes(raw), name + " has an unescaped hostile string");
}
assert.ok(html.includes("&lt;&quot;&#39;&amp;&gt;"), "escaped in the card");
assert.ok(p.includes("&lt;&quot;&#39;&amp;&gt;") || p.includes("&lt;&quot;"), "escaped in the panel");
const evil = JSON.parse(JSON.stringify(fx));
const ev = evil.workflow.container.nodes.find((n) => n.id === "up");
ev.title = '<img src=x onerror=1>"\''; ev.id = "up"; ev.command = "a'b"; ev.subtitle = "<b>";
const e2 = stubEl(); wf.render(e2, evil, { mode: "container", node: "up" });
assert.ok(!/<img|<b>/.test(e2.regions.canvas.innerHTML + e2.regions.panel.innerHTML + e2.regions.list.innerHTML));
assert.ok(e2.regions.panel.innerHTML.includes("&#39;a&#39;\\&#39;&#39;b&#39;"), "single quote in --cmd is shell quoted, then escaped");

// -- a poll with the same data writes nothing and keeps the panel open
const w0 = JSON.stringify(el.writes);
wf.render(el, JSON.parse(JSON.stringify(fx)), hy);
assert.equal(JSON.stringify(el.writes), w0, "identical data: no region is rewritten");
assert.equal(el.regions.panel.hidden, false);
assert.equal(el.regions.panel.innerHTML, p, "panel survives the poll");
// changed data: only the changed region is rewritten
const fx2 = JSON.parse(JSON.stringify(fx));
fx2.checkouts[1].last_run.steps["migrate-host"].ms = 2500;
wf.render(el, fx2, hy);
assert.ok(el.regions.panel.innerHTML.includes("2.5"), "panel updates in place");

// -- Escape closes the panel and tells the page
el.handlers.keydown({ key: "Escape", target: target("[data-node]", { node: "migrate-host" }), preventDefault() {} });
assert.equal(el.regions.panel.hidden, true);
assert.equal(sel[sel.length - 1], null);

// -- palette: toggle, read only
el.handlers.click({ target: target("[data-toggle-palette]"), detail: 1 });
assert.equal(el.regions.palette.hidden, false);
assert.ok(el.regions.palette.innerHTML.includes("Read only"));

// -- no workflow for the mode
const none = stubEl(); wf.render(none, { modes: ["container"], workflow: {} }, { mode: "container" });
assert.ok(none.regions.canvas.innerHTML.includes("No startup workflow"));
console.log("ok");
