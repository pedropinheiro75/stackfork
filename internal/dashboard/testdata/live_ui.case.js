// Live mode on the page (add-live-mode D14): the toast rules for live runs,
// the Live switch keeping focus across a render, and the calls to the live endpoint.
module.exports = function (ex, t) {
  const { el, doc, assert, ctx } = t;
  const inst = (project, lr) => ({ project, app: "acme-web", branch: "feature-login", root: "/src/acme-web/.worktrees/feature-login", last_run: lr });
  const run = (id, outcome, step) => ({ id, command: "live", trigger: "live", outcome, failed_step: step });

  // --- toasts: none for a success, one for a failure, none for the runs the page opened with
  const said = [];
  const say = (tone, text) => said.push([tone, text]);
  ex.noteLiveRuns([inst("p1", run(5, "failed", "rebuild"))], say);
  assert.deepEqual(said, [], "a failure that was there when the page opened only shows on its card");
  ex.noteLiveRuns([inst("p1", run(5, "failed", "rebuild"))], say);
  assert.deepEqual(said, []);
  ex.noteLiveRuns([inst("p1", run(6, "running"))], say);
  ex.noteLiveRuns([inst("p1", run(6, "succeeded"))], say);
  assert.deepEqual(said, [], "a succeeded live run says nothing");
  ex.noteLiveRuns([inst("p1", run(7, "running"))], say);
  ex.noteLiveRuns([inst("p1", run(7, "failed", "migrate"))], say);
  assert.deepEqual(said, [["failed", "acme-web live mode paused: migrate failed"]]);
  ex.noteLiveRuns([inst("p1", run(7, "failed", "migrate"))], say);
  assert.equal(said.length, 1, "one toast for one failed run");
  ex.noteLiveRuns([inst("p1", { id: 8, command: "start-env", trigger: "dashboard", outcome: "failed", failed_step: "up" })], say);
  assert.equal(said.length, 1, "only live runs are announced here");
  ex.noteLiveRuns([inst("p1", run(9, "interrupted"))], say);
  assert.equal(said[1][1], "acme-web live mode paused: run 9 interrupted");

  // --- the Live switch keeps focus across a render
  const key = "/src/acme-web/live";
  const oldSwitch = el({ dataset: { key }, closest: (s) => (s === "#repos-list" ? {} : null) });
  const newSwitch = el({ dataset: { key }, closest: () => null, focus() { this.focused = true; } });
  let written = "";
  const list = el({}, { ".repo": [], ".repo-error": [] });
  Object.defineProperty(list, "innerHTML", { set(v) { written = v; }, get() { return written; } });
  const fakes = {
    "repos-list": list,
    "repos-meta": el({ dataset: { configured: "true", scanning: "false", count: "1" } }),
    "rail-count-repos": el({}), "scan-state": el({}), "activity-log": el({}),
  };
  doc.getElementById = (id) => fakes[id] || null;
  doc.querySelector = (sel) => (sel.indexOf("aria-haspopup") >= 0 ? null : { content: "tok" });
  doc.querySelectorAll = (sel) => (sel === "#repos-list [data-key]" ? [newSwitch] : []);
  doc.activeElement = oldSwitch;
  const posts = [];
  ctx.fetch = (url, opts) => {
    if (opts && opts.method === "POST") {
      posts.push([url, JSON.parse(opts.body)]);
      return Promise.resolve({ status: 200, text: () => Promise.resolve(JSON.stringify({ ok: true, message: "live mode on for acme-web" })) });
    }
    if (url === "/api/instances") return new Promise(() => {}); // the poll after a call is not under test
    return Promise.resolve({ ok: true, text: () => Promise.resolve('<button role="switch" aria-checked="true" data-key="' + key + '">'), json: () => Promise.resolve({ instances: [] }) });
  };
  return ex.loadRepos().then(() => {
    assert.match(written, /role="switch"/);
    assert.equal(newSwitch.focused, true, "the new switch takes the focus the old one had");

    // --- a switch click posts the op for its repo; a second click while it is in flight does nothing
    const btn = el({ dataset: { key, liveOp: "on", repo: "/src/acme-web" }, disabled: false });
    const first = ex.liveOp(btn);
    ex.liveOp(btn);
    assert.equal(posts.length, 1, "one call in flight per button");
    assert.deepEqual(posts[0], ["/api/live", { repo: "/src/acme-web", op: "on" }]);
    assert.equal(btn.disabled, true);
    return first.then(() => {
      assert.equal(btn.disabled, false, "enabled again once the call ends");
      const clean = el({ dataset: { key: "/w/cleanup/clean", liveOp: "clean", repo: "/src/acme-web", path: "/w" }, disabled: false });
      return ex.liveOp(clean).then(() => assert.deepEqual(posts[posts.length - 1][1], { repo: "/src/acme-web", op: "clean", path: "/w" }));
    });
  });
};
