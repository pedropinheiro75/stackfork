// The run callout is not a click target: nothing inside it carries data-run
// except a Retry that runs a command, so the document's [data-run] handler
// never sees a click on the callout body, View log or a Start Retry.
module.exports = function (ex, t) {
  const { assert } = t;
  const runAttrs = (html) => [...html.matchAll(/ data-run="([^"]*)"/g)].map((m) => m[1]);
  const inst = (lr) => ({ project: "sf-a", root: "/src/a", last_run: Object.assign({ id: 4, outcome: "failed", failed_step: "app", tail: "boom" }, lr) });
  const start = ex.runCalloutHtml(inst({ retry: "stackfork --root /src/a start-env" }), "a");
  assert.match(start, /^<div class="callout callout-failed" data-run-outcome="failed">/);
  assert.deepEqual(runAttrs(start), [], "a Start Retry and View log post nothing");
  const cmd = ex.runCalloutHtml(inst({ outcome: "interrupted", retry: "stackfork --instance sf-a up" }), "a");
  assert.match(cmd, /data-run-outcome="interrupted"/);
  assert.deepEqual(runAttrs(cmd), ["stackfork --instance sf-a up"], "only the Retry command runs");
  assert.match(ex.runCalloutHtml(inst({ trigger: "resume", command: "up" }), "a"), /Resume failed at step/);
  assert.match(ex.runCalloutHtml(inst({ trigger: "cascade", command: "cascade" }), "a"), /Cascade stop failed/);
};
