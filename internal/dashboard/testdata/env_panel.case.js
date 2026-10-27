// The Environment panel (add-env-overrides D7): which names render as a
// toggle, masked rows, the build note, and what Save sends.
module.exports = function (ex, t) {
  const { assert } = t;

  for (const [n, v] of [
    ["ENABLE_GUEST_ACCESS", "true"], ["ENABLE_X", "false"], ["SIGNUP_ENABLED", "true"], ["FEATURE_BETA", "false"],
    ["VITE_ENABLE_MOCKS", "true"], ["ENABLE_X", ""], ["ENABLE_X", "1"], ["FOO", "true"], ["BAR", "false"],
  ]) assert.equal(ex.isFlagLike(n, v), true, n + "=" + v + " is a toggle");
  for (const [n, v] of [
    ["LOG_LEVEL", "debug"], ["PORT", "3000"], ["ENABLE_X", "beta"], ["ENABLE_X", null], ["FEATURE_FLAGS", "a,b"], ["API_URL", ""],
  ]) assert.equal(ex.isFlagLike(n, v), false, n + "=" + v + " is a text field");
  assert.equal(ex.flagNext("true"), "false");
  assert.equal(ex.flagNext("false"), "true");
  assert.equal(ex.flagNext("1"), "0", "a toggle keeps the vocabulary the app already uses");
  assert.equal(ex.flagNext(""), "true");

  const svc = {
    name: "web", kind: "container", build: true, vars: [
      { name: "ENABLE_GUEST_ACCESS", value: "true", masked: false, origin: "project", has_default: true, default: "true" },
      { name: "LOG_LEVEL", value: "debug", masked: false, origin: "override", has_default: true, default: "info" },
      { name: "NPM_TOKEN", value: null, masked: true, origin: "vault", has_default: true, default: null },
      { name: "VITE_API_URL", value: "http://x", masked: false, origin: "project", has_default: true, default: "http://x" },
      { name: "ONLY_MINE", value: "1", masked: false, origin: "override", has_default: false, default: null },
      { name: "DB_PASSWORD", value: null, masked: true, origin: "env_file", has_default: true, default: null },
    ],
  };
  const rows = ex.envRowsOf(svc);
  const html = rows.map((r, i) => ex.envRowHtml(r, i, svc));
  assert.match(html[0], /<input type="checkbox" role="switch" class="env-switch" data-env-toggle="0" aria-label="ENABLE_GUEST_ACCESS" checked>/);
  assert.match(html[0], /env-origin[^>]*>project</);
  assert.doesNotMatch(html[0], /data-env-remove/, "a project name is overridden, never deleted");
  assert.match(html[1], /<input type="text" class="env-value mono" data-env-value="1" value="debug"/);
  assert.match(html[1], /data-env-remove="1"[^>]*>Restore default</);
  assert.match(html[4], /data-env-remove="4"[^>]*>Remove</);

  assert.match(html[2], /env-masked[^>]*>••••</);
  assert.match(html[5], /env-masked[^>]*>••••</);
  assert.doesNotMatch(html[2] + html[5], /<input|value=|null/, "a masked row has no field and no value");
  assert.match(html[2], /env-origin[^>]*>vault</);

  assert.match(html[3], /applies at next build/);
  assert.equal(html.filter((h) => /applies at next build/.test(h)).length, 1, "only VITE_* names");
  const noBuild = Object.assign({}, svc, { build: false });
  assert.doesNotMatch(ex.envRowHtml(rows[3], 3, noBuild), /applies at next build/, "an image-only service has no build");

  // Save sends only what changed
  assert.deepEqual(ex.envChanges(rows), { set: {}, unset: [] });
  ex.envEdit(rows, 0, "false");
  assert.equal(rows[0].origin, "override");
  ex.envEdit(rows, 3, "http://y");
  assert.deepEqual(ex.envChanges(rows), { set: { ENABLE_GUEST_ACCESS: "false", VITE_API_URL: "http://y" }, unset: [] });
  ex.envEdit(rows, 3, "http://x");
  assert.equal(rows[3].origin, "project", "typing the project value back is no override");
  ex.envRestore(rows, 1);
  assert.equal(rows[1].value, "info");
  assert.equal(rows[1].origin, "project");
  ex.envRestore(rows, 4);
  assert.equal(rows[4].gone, true);
  assert.deepEqual(ex.envChanges(rows), { set: { ENABLE_GUEST_ACCESS: "false" }, unset: ["LOG_LEVEL", "ONLY_MINE"] });
  assert.equal(ex.envRowHtml(rows[4], 4, svc), "", "a removed row is not drawn");

  // adding
  assert.equal(ex.envAdd(rows, "ENABLE_NEW", "true"), "");
  assert.deepEqual(ex.envChanges(rows).set.ENABLE_NEW, "true");
  assert.match(ex.envAdd(rows, "1BAD", "x"), /name/i);
  assert.match(ex.envAdd(rows, "API_TOKEN", "x"), /Settings → Secrets/, "a secret-like name points to Settings → Secrets");
  assert.match(ex.envAdd(rows, "npm_password", "x"), /Settings → Secrets/);
  assert.match(ex.envAdd(rows, "LOG_LEVEL", "x"), /already/);
  assert.equal(rows.some((r) => r.name === "API_TOKEN"), false);

  // hostile names and values are escaped in the markup (never raw `<"'&`)
  const evil = { name: "web", kind: "container", build: false, vars: [
    { name: "A<\"'&", value: "<\"'&", masked: false, origin: "override", has_default: false, default: null },
    { name: "B", value: "<img src=x onerror=alert(1)>", masked: false, origin: "project", has_default: true, default: "<\"'&" },
  ] };
  const erows = ex.envRowsOf(evil);
  const ehtml = erows.map((r, i) => ex.envRowHtml(r, i, evil)).join("");
  assert.doesNotMatch(ehtml, /<img|value="<|aria-label="A<|<"/, "no raw markup from a name or value");
  assert.match(ehtml, /&lt;/);
  assert.match(ehtml, /&quot;|&#34;/);
  assert.match(ehtml, /&#39;|&#x27;/);
  assert.match(ehtml, /&amp;/);

  // the overrides list: only overrides, per service; a masked value never prints
  const list = ex.envOverridesHtml([svc, evil, { name: "db", vars: [{ name: "X", value: "1", masked: false, origin: "project" }] }], "p1");
  assert.match(list, /data-env-open data-project="p1" data-service="web"/, "a button per service opens its Environment dialog");
  assert.match(list, /LOG_LEVEL<\/span><span class="env-ov-val mono">debug</);
  assert.match(list, /ONLY_MINE/);
  assert.doesNotMatch(list, /ENABLE_GUEST_ACCESS|NPM_TOKEN|DB_PASSWORD|data-service="db"/, "project, vault and unset names are not listed");
  assert.doesNotMatch(list, /<img|A<"/, "escaped");
  const masked = ex.envOverridesHtml([{ name: "web", vars: [{ name: "API_TOKEN", value: null, masked: true, origin: "override" }, { name: "SECRET_X", value: "leak", masked: true, origin: "override" }] }], "p1");
  assert.match(masked, /env-masked[^>]*>••••</);
  assert.doesNotMatch(masked, /leak|null/, "a masked override shows bullets, never a value");
  assert.match(ex.envOverridesHtml([], "p1"), /No overrides are set/);
};
