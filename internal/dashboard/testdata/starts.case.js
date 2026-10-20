// Starts in flight: each one removes only its own progress text when it ends,
// and a card's Retry stays disabled while its Start runs, whatever re-renders.
module.exports = function (ex, t) {
  const { el, doc, assert } = t;
  const removed = [];
  function pair(key) {
    const progress = el({ classList: { contains: (c) => c === "row-progress" }, remove() { removed.push(key); } });
    return { progress, btn: el({ dataset: { key }, previousElementSibling: progress, closest: () => null }) };
  }
  const a = pair("/r/a"), b = pair("/r/b");
  doc.querySelectorAll = (sel) => (sel === "#repos-list [data-key]" ? [a.btn, b.btn] : []);
  ex.reposState.starts["/r/a"] = { inMenu: false, repo: "/r/a", text: "x" };
  ex.reposState.starts["/r/b"] = { inMenu: false, repo: "/r/b", text: "y" };
  assert.equal(ex.finishStart("/r/a"), a.btn);
  assert.deepEqual(removed, ["/r/a"], "the finished Start removes its own text only");
  assert.ok(ex.reposState.starts["/r/b"], "the other Start is still in flight");
  ex.finishStart("/r/b");
  assert.deepEqual(removed, ["/r/a", "/r/b"]);

  // the card's Retry is in the instances view and is replaced by every render
  const retry = () => el({ dataset: { key: "sf-a/run/retry" }, disabled: false, textContent: "Retry", previousElementSibling: null, closest: () => null, insertAdjacentElement() {} });
  doc.createElement = () => el({ classList: { contains: () => true } });
  let card = retry();
  doc.querySelectorAll = (sel) => (sel === "#view-instances [data-key]" ? [card] : []);
  ex.reposState.starts["sf-a/run/retry"] = { inMenu: false, repo: "/r/a", text: "starting" };
  ex.paintStarts();
  assert.equal(card.disabled, true);
  card = retry(); // a poll renders the card again
  ex.paintStarts();
  assert.equal(card.disabled, true, "the new Retry is disabled while its Start runs");
};
