module.exports = function (ex, t) {
  const { assert } = t;
  const run = { steps: [
    { id: "prepare", state: "succeeded", log: "9/prepare.log" },
    { id: "up-deps", state: "skipped", reason: "already prepared" },
    { id: "seed", state: "failed", log: "9/seed.log" },
    { id: "app", state: "pending" },
  ] };
  assert.equal(ex.pickStep(run, ""), "seed", "the failed step opens first");
  assert.equal(ex.pickStep(run, "prepare"), "prepare", "a named step wins");
  assert.equal(ex.pickStep({ steps: [{ id: "a", state: "succeeded", log: "1/a.log" }, { id: "b", state: "succeeded", log: "1/b.log" }] }, ""), "b", "else the last step with a log");
  assert.equal(ex.pickStep({ steps: [{ id: "a", state: "pending" }] }, ""), "a");
  const html = ex.stepsHtml(run, "seed");
  assert.match(html, /data-step="seed"[^>]*aria-pressed="true"/);
  assert.match(html, /data-step="prepare"[^>]*aria-pressed="false"/);
  for (const w of ["Succeeded", "Skipped", "Failed", "Pending"]) assert.ok(html.includes(w), w);
  const interrupted = ex.stepsHtml({ steps: [{ id: "up", state: "failed", reason: "interrupted" }] }, "up");
  assert.ok(interrupted.includes("Interrupted"));
};
