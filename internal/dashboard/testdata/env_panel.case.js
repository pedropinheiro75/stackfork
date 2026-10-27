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
  // project rows are read-only context: project value, origin text, no field, no badge
  assert.match(html[0], /env-row-ro/);
  assert.doesNotMatch(html[0] + html[3], /<input|<textarea|env-reset/, "a project variable has no control");
  assert.match(html[0], /env-from[^>]*>project</);
  assert.match(html[3], /title="http:\/\/x"/, "the full value is in a tooltip");
  assert.doesNotMatch(html.join(""), />override<|env-origin|class="chip/i, "no OVERRIDE badge");

  // an override row: project value, your value, reset button
  assert.match(html[1], /env-proj[^>]*><span class="mono" title="info">info</);
  assert.match(html[1], /<textarea class="env-value mono" rows="1" data-env-value="1" title="debug"[^>]*>debug<\/textarea>/);
  assert.match(html[1], /data-env-undo="1" aria-label="Reset LOG_LEVEL to the project value"[^>]*>&#8634;</);
  assert.match(html[4], /not in project/);
  assert.match(html[4], /aria-label="Remove ONLY_MINE"/, "no project value: reset removes it");

  // a boolean shows the toggle, visible true/false text and the project value
  const bsvc = { name: "api", kind: "container", vars: [{ name: "ENABLE_X", value: "false", masked: false, origin: "override", has_default: true, default: "true" }] };
  const brows = ex.envRowsOf(bsvc);
  const bh = ex.envRowHtml(brows[0], 0, bsvc);
  assert.match(bh, /<input type="checkbox" role="switch" class="env-switch" data-env-toggle="0" aria-label="ENABLE_X"><span class="env-bool mono" aria-hidden="true">false</);
  assert.match(bh, /env-proj[^>]*><span class="mono" title="true">true</);
  ex.envEdit(brows, 0, "true");
  assert.match(ex.envRowHtml(brows[0], 0, bsvc), /checked><span class="env-bool mono" aria-hidden="true">true</);

  // masked rows: bullets, no field, no value
  assert.match(html[2], /env-masked[^>]*>••••</);
  assert.match(html[5], /env-masked[^>]*>••••</);
  assert.doesNotMatch(html[2] + html[5], /<input|<textarea|value=|null/, "a masked row has no field and no value");
  assert.match(html[2], /env-from[^>]*>vault</);
  const mo = ex.envRowsOf({ name: "web", vars: [{ name: "API_KEY", value: null, masked: true, origin: "override", has_default: true, default: null }] });
  const moh = ex.envRowHtml(mo[0], 0, svc);
  assert.match(moh, /env-proj[^>]*><span class="env-masked/, "a hidden project value shows bullets");
  assert.match(moh, /env-control[^>]*><span class="env-masked/);
  assert.doesNotMatch(moh, /<textarea|<input/);

  assert.match(ex.envRowHtml(ex.envRowsOf({ vars: [{ name: "VITE_A", value: "1", masked: false, origin: "override", has_default: true, default: "0" }] })[0], 0, svc), /applies at next build/);
  assert.doesNotMatch(ex.envRowHtml(rows[3], 3, svc), /applies at next build/, "only an editable VITE_* row carries the note");

  // overridden rows come first, the rest sit in a disclosure
  const all = ex.envRowsHtml(rows, svc, false);
  assert.ok(all.indexOf("LOG_LEVEL") < all.indexOf("<details"), "overrides first");
  assert.ok(all.indexOf("<details") < all.indexOf("ENABLE_GUEST_ACCESS"), "project variables inside the disclosure");
  assert.match(all, /Show 4 other variables/);
  assert.match(all, /Variable<\/span><span>Project value<\/span><span>Your value/);
  assert.doesNotMatch(all, /<details open/);
  assert.match(ex.envRowsHtml(rows, svc, true), /<details open/);
  assert.match(ex.envRowsHtml(ex.envRowsOf({ vars: [{ name: "A", value: "1", masked: false, origin: "project", has_default: true, default: "1" }, { name: "B", value: "2", masked: false, origin: "project", has_default: true, default: "2" }] }), svc, false), /No overrides yet[\s\S]*Show all 2 variables/);

  // Save sends only what changed
  assert.deepEqual(ex.envChanges(rows), { set: {}, unset: [] });
  ex.envEdit(rows, 1, "trace");
  assert.deepEqual(ex.envChanges(rows), { set: { LOG_LEVEL: "trace" }, unset: [] });
  ex.envEdit(rows, 1, "debug");
  assert.deepEqual(ex.envChanges(rows), { set: {}, unset: [] }, "typing the stored value back is no change");

  // reset is pending, undoable, and sent only as unset
  ex.envToggleReset(rows, 1);
  assert.equal(rows[1].reset, true);
  assert.deepEqual(ex.envChanges(rows), { set: {}, unset: ["LOG_LEVEL"] });
  const pend = ex.envRowHtml(rows[1], 1, svc);
  assert.match(pend, /env-pending/);
  assert.match(pend, /<s class="env-struck">[^]*debug[^]*<\/s>/, "your value is struck through");
  assert.match(pend, /will reset on save/);
  assert.match(pend, /data-env-undo="1" aria-label="Keep the override of LOG_LEVEL"[^>]*>Undo</);
  assert.doesNotMatch(pend, /<textarea/);
  ex.envToggleReset(rows, 1);
  assert.deepEqual(ex.envChanges(rows), { set: {}, unset: [] }, "undo restores the override");
  ex.envToggleReset(rows, 4);
  assert.match(ex.envRowHtml(rows[4], 4, svc), /will be removed on save/);
  assert.deepEqual(ex.envChanges(rows), { set: {}, unset: ["ONLY_MINE"] });
  ex.envToggleReset(rows, 4);

  // adding
  assert.equal(ex.envAdd(rows, "ENABLE_NEW", "true"), "");
  assert.deepEqual(ex.envChanges(rows).set.ENABLE_NEW, "true");
  assert.match(ex.envAdd(rows, "1BAD", "x"), /name/i);
  assert.match(ex.envAdd(rows, "API_TOKEN", "x"), /Settings → Secrets/, "a secret-like name points to Settings → Secrets");
  assert.match(ex.envAdd(rows, "npm_password", "x"), /Settings → Secrets/);
  // an existing name edits its row instead of duplicating it
  assert.equal(ex.envAdd(rows, "LOG_LEVEL", "x"), "");
  assert.equal(rows.filter((r) => r.name === "LOG_LEVEL").length, 1);
  assert.equal(rows[1].value, "x");
  assert.equal(ex.envAdd(rows, "VITE_API_URL", "http://z"), "");
  assert.equal(rows[3].origin, "override", "naming a project variable overrides it");
  assert.equal(ex.envChanges(rows).set.VITE_API_URL, "http://z");
  assert.match(ex.envAdd(rows, "DB_PASSWORD", "x"), /Settings → Secrets/);
  assert.match(ex.envAdd(rows, "NPM_TOKEN", "x"), /Settings → Secrets/);
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
