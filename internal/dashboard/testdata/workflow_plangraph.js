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
// the seed decision of the hybrid branch: yes/no only, the group is after the split, not in it
function splitOf(html, label) {
  const start = html.indexOf('<div class="pw-split' + (label === "Mode?" ? " pw-modes" : " pw-bypass") + '" role="group" aria-label="' + label + '">', label === "Mode?" ? 0 : html.indexOf("pw-mode-branch\" data-on=\"0\"") > 0 ? html.lastIndexOf('data-mode="hybrid"') : 0);
  assert.ok(start >= 0, label + " split exists");
  let depth = 0;
  for (const m of html.slice(start).matchAll(/<div\b|<\/div>/g)) {
    depth += m[0] === "</div>" ? -1 : 1;
    if (depth === 0) return html.slice(start, start + m.index + 6);
  }
  throw new Error("unbalanced");
}
const seedSplit = splitOf(hy, "New data volume?");
assert.equal(JSON.stringify(Array.from(seedSplit.matchAll(/pw-edge-label">(\w+)</g), (m) => m[1])), '["yes","no"]', "branch labels are yes and no");
assert.ok(seedSplit.includes("seed-host"), "it is the hybrid branch decision");
assert.ok(seedSplit.indexOf("pw-branch-skip") > seedSplit.indexOf("seed-host"), "the real branch is the spine, the skip branch comes last");
assert.ok(!seedSplit.includes("pw-parallel"), "the parallel group is not inside the decision's split");
assert.ok(hy.indexOf("pw-parallel") > hy.indexOf(seedSplit) + seedSplit.length, "the group follows the decision");
const modeSplit = splitOf(hy, "Mode?");
assert.equal(JSON.stringify(Array.from(modeSplit.matchAll(/data-mode-to="(\w+)"|data-mode="(\w+)"/g), (m) => m[1] || m[2]).filter((v, i, a) => a.indexOf(v) === i)), '["container","hybrid"]');
console.log("ok");
