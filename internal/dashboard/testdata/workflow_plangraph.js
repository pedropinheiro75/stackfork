// Renders a real /api/project body (argv[2]) through static/project-workflow.js.
const fs = require("fs"), vm = require("vm"), assert = require("assert"), path = require("path");
const win = {};
vm.runInNewContext(fs.readFileSync(path.join(__dirname, "../static/project-workflow.js"), "utf8"), { window: win, Date, setTimeout, navigator: {} });
const wf = win.sfProjectWorkflow;
const p = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const count = (s, re) => (s.match(re) || []).length;
const modes = p.modes;
for (const mode of modes) {
  const c = wf._canvas(p.workflow[mode], mode, modes, null);
  const html = c.html;
  if (process.env.SF_DUMP) fs.writeFileSync(process.env.SF_DUMP + "." + mode + ".html", html);
  const steps = p.workflow[mode].nodes.filter((n) => n.kind === "step");
  const live = steps.filter((n) => n.in_mode).map((n) => n.id).sort();
  assert.equal(JSON.stringify(Array.from(c.ids).sort()), JSON.stringify(live), mode + ": live cards are the in-mode steps");
  for (const n of steps) assert.equal(count(html, new RegExp('class="pw-node" data-node="' + n.id + '"', "g")), 1, mode + ": " + n.id + " is drawn once");
  assert.equal(count(html, /pw-start/g), 1, mode + ": one Start");
  assert.equal(count(html, /pw-terminal pw-ready/g), 2, mode + ": one Ready per mode branch");
  assert.equal(count(html, /pw-decision">(<svg.*?<\/svg>)?New data volume\?/g), 2, mode + ": the seed decision once per branch");
  assert.equal(count(html, /pw-split pw-parallel/g), 1, mode + ": host apps start together once");
  assert.equal(count(html, /pw-mode-branch" data-on="1"/g), 1, mode + ": one active branch");
}
// the parallel group holds api and app, and the active hybrid Ready follows it
const hy = wf._canvas(p.workflow.hybrid, "hybrid", modes, null).html;
const par = hy.slice(hy.indexOf("pw-split pw-parallel"));
assert.ok(par.indexOf('data-node="api"') > 0 && par.indexOf('data-node="app"') > 0);
// a failed hybrid run: stops at api, Ready is not reached
const run = { id: 7, outcome: "failed", failed_step: "api", steps: { prepare: { state: "succeeded" }, "up-deps": { state: "succeeded" }, "seed-host": { state: "succeeded" }, "migrate-host": { state: "succeeded" }, api: { state: "failed" } } };
const fr = wf._canvas(p.workflow.hybrid, "hybrid", modes, run).html;
assert.ok(fr.includes("not reached: run #7 stopped at api"));
console.log("ok");
