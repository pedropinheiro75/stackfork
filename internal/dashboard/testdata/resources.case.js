// Runs static/project-resources.js against testdata/resources.fixture.json in a Node vm.
// usage: node resources.case.js <project-resources.js> <fixture.json>
const fs = require("fs"), vm = require("vm"), assert = require("assert");
const [, , js, fixture] = process.argv;
const ctx = { console, Date, Math, Object, Array, WeakMap, Number, String };
ctx.window = ctx; vm.createContext(ctx);
vm.runInContext(fs.readFileSync(js, "utf8"), ctx);
const api = ctx.sfProjectResources;
assert.ok(api && typeof api.render === "function", "window.sfProjectResources.render missing");
const base = JSON.parse(fs.readFileSync(fixture, "utf8"));
const NOW = 1700000004;
const clone = () => JSON.parse(JSON.stringify(base));

function fakeEl() {
  return { innerHTML: "", addEventListener() {}, querySelector() { return null; }, querySelectorAll() { return []; }, contains() { return false; }, classList: { add() {} }, ownerDocument: { activeElement: null } };
}
function draw(res, opts) { const el = fakeEl(); api.render(el, res, Object.assign({ range: "15m", now: NOW, name: "acme-web" }, opts)); return el.innerHTML; }
function card(html, id) { const i = html.indexOf('data-chart="' + id + '"'); assert.ok(i > 0, "chart " + id + " missing"); return html.slice(i, html.indexOf("</svg>", i)); }
const count = (s, re) => (s.match(re) || []).length;

// series paths: one line per run of samples; a stopped period splits a checkout's line
let html = draw(clone());
const cpu = card(html, "cpu"), mem = card(html, "mem");
assert.strictEqual(count(cpu, /class="rs-line rs-s\d"/g), 3, "cpu: main 1 run + feature 2 runs");
assert.strictEqual(count(mem, /class="rs-line rs-s\d"/g), 3, "memory by checkout: lines");
assert.strictEqual(count(mem, /class="rs-area rs-s\d"/g), 3, "memory by checkout: areas");

// the gap breaks the path and never drops to zero
const featureCpu = [...cpu.matchAll(/<path class="rs-line rs-s2" d="([^"]+)"/g)].map((m) => m[1]);
assert.strictEqual(featureCpu.length, 2, "feature cpu is two paths");
const baseline = 8 + (180 - 8 - 22); // T + plot height at the default 560 px width
for (const d of featureCpu) for (const p of d.slice(1).split("L")) assert.ok(parseFloat(p.split(" ")[1]) < baseline - 1, "a point sits on the baseline (zero) in " + p);
const x = (d, last) => parseFloat((last ? d.split("L").pop() : d.slice(1).split("L")[0]).split(" ")[0]);
assert.ok(x(featureCpu[1], false) - x(featureCpu[0], true) > 30, "gap is wider than one sample step");

// kind toggle: stacked by kind, utility has no service so it stays out
html = draw(clone(), { kindToggle: "kind" });
assert.strictEqual(count(card(html, "mem"), /class="rs-line rs-s\d"/g), 3, "kind view: one run each (a gap needs every member stopped)");
assert.ok(html.includes("host app") && html.includes("Stacked by service kind") && !html.includes(">utility<"));
html = draw(clone(), { kindToggle: false });
assert.ok(!html.includes('name="rs-by"'), "toggle hidden");

// attention at 86 %, failed at 96 %
html = draw(clone());
assert.ok(/data-tone="attention"/.test(html) && !/data-tone="failed"/.test(html), "86 % is attention");
assert.ok(html.includes("callout-attention") && html.includes("app on main uses 86 % of its memory limit"), "attention callout");
assert.ok(html.includes("rs-tone-attention"));
const hot = clone(); hot.services[0].mem_bytes = Math.round(hot.services[0].mem_limit_bytes * 0.96);
html = draw(hot);
assert.ok(/data-tone="failed"/.test(html) && html.includes("callout-failed"), "96 % is failed");
const cool = clone(); cool.services[0].mem_bytes = Math.round(cool.services[0].mem_limit_bytes * 0.5);
html = draw(cool);
assert.ok(!html.includes("callout-attention") && !html.includes("callout-failed") && !html.includes("data-tone="), "50 % is fine");

// table view
html = draw(clone());
assert.ok(html.includes("Show chart data as a table"));
const tbody = html.slice(html.indexOf("<tbody>"), html.indexOf("</tbody>"));
assert.strictEqual(count(tbody, /<tr>/g), 12, "twelve rows");
assert.ok(tbody.includes("stopped"), "the stopped period reads stopped");
assert.ok(tbody.includes("now"), "last row is now");

// units: non-breaking space before every unit, MB under 1 GB, GB with one decimal
assert.ok(html.includes("1.7 GB"), "GB one decimal");
assert.ok(/\d MB/.test(html) && /\d %/.test(html));
const text = html.replace(/<[^>]*>/g, "");
assert.ok(!/\d (MB|GB)\b/.test(text), "plain space before a unit: " + (text.match(/.{10}\d (MB|GB)\b.{5}/) || [""])[0]);
const sentence = text.match(/acme-web uses.*?memory/)[0];
assert.strictEqual(sentence, "acme-web uses " + (base.totals.cpu_pct / 100).toFixed(1) + "\u00a0cores of 10, " + api._fmt.bytes(base.totals.mem_bytes) + " of 32\u00a0GB memory");
assert.strictEqual(api._fmt.bytes(512 * 1048576), "512 MB");
assert.strictEqual(api._fmt.bytes(1073741824 * 3.55), "3.5 GB".replace("3.5", (3.55).toFixed(1)));
assert.strictEqual(api._fmt.pct(6.25), "6.3 %".replace("6.3", (6.25).toFixed(1)));

// hostile names never become markup
const bad = clone(), evil = '<img src=x onerror=alert(1)>', evilCo = '"><script>alert(2)</script>';
bad.series.forEach((s) => { if (s.service === "db") s.service = evil; if (s.checkout === "feature/invoices") s.checkout = evilCo; });
bad.services.forEach((s) => { if (s.service === "db") s.service = evil; if (s.checkout === "feature/invoices") s.checkout = evilCo; s.image = s.image && "<b>" + s.image; });
bad.disk[1].checkout = evilCo;
html = draw(bad, { name: "<i>x</i>" });
assert.ok(!html.includes("<img") && !html.includes("<script") && !html.includes("<i>x") && !html.includes("<b>acme"), "unescaped markup");
assert.ok(html.includes("&lt;img src=x onerror=alert(1)&gt;") && html.includes("&lt;script&gt;"));
assert.ok(!/ on\w+=/.test(html.replace(/&[a-z]+;/g, "")) || !/<[^>]* on\w+=/.test(html), "event handler attribute");

// empty state: no samples yet. Charts say so, the disk chart still shows.
const none = clone(); none.series.forEach((s) => { s.points = []; }); none.services.forEach((s) => { s.state = "starting"; });
html = draw(none);
assert.strictEqual(count(html, /Collecting samples… \(first point in 10 s\)/g), 2, "empty state in both time charts");
assert.ok(html.includes("rs-disk-row") && !html.includes("Show chart data as a table"));
assert.ok(!html.includes("<path"), "no paths without samples");

// nothing runs
const off = clone(); off.series.forEach((s) => { s.points = []; }); off.services.forEach((s) => { s.state = "stopped"; });
assert.ok(draw(off).includes("Nothing runs. Volumes keep 1.4 GB."), "stopped summary");

// a limit equal to the machine total is no limit; stats_error shows a stale state; sampled_at drives the age
html = draw(clone());
assert.ok(!/422\u00a0MB \//.test(html.replace(/<[^>]+>/g, "")) && html.includes("% RAM"), "VM-total limit is no limit");
assert.ok(html.includes("Sampled 4 s ago"), "sampled_at");
const err = clone(); err.stats_error = "docker stats timed out";
html = draw(err);
assert.ok(html.includes("Stats unavailable: docker stats timed out") && html.includes("rs-stale-on"), "stats_error");

// no inline style attribute may reach the markup (CSP)
assert.ok(!/ style=/.test(draw(clone())), "inline style in markup");
console.log("ok");
