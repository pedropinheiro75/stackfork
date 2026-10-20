// A fresh plan list as the server renders it: every row with its plan chip.
module.exports = function (ex, t) {
  const { el, doc, assert } = t;
  function repo(path) {
    const rows = ["prepare", "seed", "app"].map((id) => {
      const glyph = el({ textContent: "●", nextSibling: { nodeType: 3, nodeValue: "verified" } });
      const chip = el({ className: "chip chip-ok" }, { ".chip-glyph": [glyph] });
      const dur = el({ hidden: true });
      return el({ dataset: { step: id } }, { ".plan-state .chip": [chip], ".plan-dur": [dur] });
    });
    const lives = ["prepare", "seed", "app", ""].map((id) => el({ dataset: { liveFor: id } }));
    return el({ dataset: { key: path } }, { ".plan-row[data-step]": rows, "[data-live-for]": lives });
  }
  let page = repo("/r/acme-web");
  doc.querySelectorAll = (sel) => (sel === "#repos-list [data-key]" ? [page] : []);
  const chipOf = (r, i) => r.q[".plan-row[data-step]"][i].q[".plan-state .chip"][0];
  const label = (c) => c.q[".chip-glyph"][0].nextSibling.nodeValue;
  const live = (r, id) => r.q["[data-live-for]"].find((l) => l.dataset.liveFor === id).textContent;
  const P = "/r/acme-web";

  ex.onStreamObject(P, { run: { id: 7, project: "p", steps: [
    { id: "prepare", kind: "prepare", state: "pending" }, { id: "seed", kind: "exec", state: "pending" }, { id: "app", kind: "host", state: "pending" }] } });
  assert.equal(label(chipOf(page, 0)), "Pending");
  ex.onStreamObject(P, { step: { id: "prepare", state: "skipped", reason: "already prepared" } });
  assert.equal(label(chipOf(page, 0)), "Skipped");
  assert.equal(chipOf(page, 0).title, "already prepared");
  ex.onStreamObject(P, { step: { id: "seed", state: "running" } });
  ex.onStreamObject(P, { line: "seeding users", step: "seed" });
  assert.equal(label(chipOf(page, 1)), "Running");
  assert.equal(chipOf(page, 1).className, "chip chip-busy");
  assert.equal(live(page, "seed"), "seeding users");
  assert.equal(live(page, "app"), "");

  // a poll re-renders the fragment: new elements with the plan chips again
  page = repo(P);
  assert.equal(label(chipOf(page, 1)), "verified");
  ex.paintRuns();
  assert.equal(label(chipOf(page, 0)), "Skipped", "skipped row after render");
  assert.equal(label(chipOf(page, 1)), "Running", "running row after render");
  assert.equal(label(chipOf(page, 2)), "Pending", "pending row after render");
  assert.equal(live(page, "seed"), "seeding users", "current line after render");

  // painting twice changes nothing
  const before = JSON.stringify(page.q[".plan-row[data-step]"].map((r) => r.q[".plan-state .chip"][0].className));
  ex.paintRuns();
  assert.equal(JSON.stringify(page.q[".plan-row[data-step]"].map((r) => r.q[".plan-state .chip"][0].className)), before);

  ex.onStreamObject(P, { step: { id: "seed", state: "failed" } });
  assert.equal(label(chipOf(page, 1)), "Failed");
  assert.equal(chipOf(page, 1).className, "chip chip-failed");
  assert.equal(live(page, "seed"), "", "the line goes when the step ends");
  assert.match(page.q[".plan-row[data-step]"][1].q[".plan-dur"][0].textContent, /^\d+ ms$/);

  // a line with no plan row for its step goes under the list
  ex.onStreamObject(P, { step: { id: "implicit", state: "running" } });
  ex.onStreamObject(P, { line: "starting db", step: "implicit" });
  assert.equal(live(page, ""), "starting db", "no row for the step: the line goes under the list");
  assert.equal(ex.fmtDur(41000), "41 s");
  assert.equal(ex.fmtDur(65000), "1 m 5 s");
  assert.equal(ex.fmtDur(320), "320 ms");
};
