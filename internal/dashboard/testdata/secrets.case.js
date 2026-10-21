// A Check or Set value in flight keeps its busy state on the new button after
// every Repos render, and the log gate is recognised only by its header.
module.exports = function (ex, t) {
  const { el, doc, assert } = t;
  const btn = (key, text, cls) => el({ dataset: { key }, disabled: false, textContent: text, classList: { contains: (c) => (cls || []).includes(c) } });

  let check = btn("/r/a/secrets/check", "Check");
  let setv = btn("/r/a/secret/NPM_TOKEN/menu/set", "Set value…", ["menu-item"]);
  doc.querySelectorAll = (sel) => (sel === "#repos-list [data-key]" ? [check, setv] : []);
  ex.reposState.secrets["/r/a/secrets/check"] = "Checking…";
  ex.reposState.secrets["/r/a/secret/NPM_TOKEN/menu/set"] = "Set value…";
  ex.paintSecrets();
  assert.equal(check.attrs["aria-disabled"], "true");
  assert.equal(check.textContent, "Checking…");
  assert.equal(setv.attrs["aria-disabled"], "true");
  assert.equal(setv.textContent, "Set value…", "a menu item keeps its label");

  check = btn("/r/a/secrets/check", "Check"); // a poll renders the section again
  setv = btn("/r/a/secret/NPM_TOKEN/menu/set", "Set value…", ["menu-item"]);
  ex.paintSecrets();
  assert.equal(check.attrs["aria-disabled"], "true", "the new Check button is still busy");
  assert.equal(check.textContent, "Checking…");
  assert.equal(setv.attrs["aria-disabled"], "true");

  delete ex.reposState.secrets["/r/a/secrets/check"];
  check = btn("/r/a/secrets/check", "Check");
  ex.paintSecrets();
  assert.equal(check.attrs["aria-disabled"], undefined, "a finished Check leaves the button alone");

  const resp = (status, header) => ({ status, headers: { get: (k) => (k === "X-Stackfork-Needs" ? header : null) } });
  assert.equal(ex.needsSecretCheck(resp(409, "secret-check")), true);
  assert.equal(ex.needsSecretCheck(resp(409, null)), false, "a 409 without the header is not the gate");
  assert.equal(ex.needsSecretCheck(resp(200, "secret-check")), false);
};
