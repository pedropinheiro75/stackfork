// Reset environment: it is a streamed repo action like Start, its failed
// callout retries with the Reset action, and its retry command is the CLI's.
module.exports = function (ex, t) {
  const { assert } = t;
  const inst = (lr) => ({ project: "sf-a", root: "/src/a/.worktrees/x", last_run: Object.assign({ id: 4, command: "reset", outcome: "failed", failed_step: "up", tail: "boom" }, lr) });
  const html = ex.runCalloutHtml(inst({ retry: "stackfork --root /src/a/.worktrees/x reset --yes" }), "a");
  assert.match(html, /Reset failed at step <code class="mono">up<\/code>/);
  assert.match(html, /data-repo-action="reset" data-repo="\/src\/a" data-path="\/src\/a\/\.worktrees\/x"/);
  assert.doesNotMatch(html, / data-run="/, "the retry is a repo action, not a posted command");
  assert.equal(ex.isStreamAction("start"), true);
  assert.equal(ex.isStreamAction("reset"), true);
  assert.equal(ex.isStreamAction("fetch"), false);
  assert.equal(ex.retryCommand({ repoAction: "reset", repo: "/src/a", path: "/src/a/.worktrees/x" }), "stackfork --root '/src/a/.worktrees/x' reset");
  const again = ex.runCalloutHtml(inst({ retry: "stackfork --root /src/a/.worktrees/x reset --yes --from-main" }), "a");
  assert.match(again, /data-repo-action="reset" data-from-main="1" data-repo="\/src\/a"/);
  assert.doesNotMatch(html, /data-from-main/, "a plain reset retries as a plain reset");
  assert.equal(ex.retryCommand({ repoAction: "reset", fromMain: "1", repo: "/src/a", path: "/src/a/.worktrees/x" }), "stackfork --root '/src/a/.worktrees/x' reset --from-main");
  const chip = ex.startingChip({ last_run: { command: "reset", outcome: "running", step: "down", step_n: 3, step_total: 8 } });
  assert.equal(chip.label, "Resetting…");
};
