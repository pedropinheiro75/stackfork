// Loads static/app.js in a Node vm with a stub document, then runs a case file
// with the functions the script exports through window.__sfExport.
const fs = require("fs"), vm = require("vm"), assert = require("assert");
let ex = null;
const doc = {
  querySelector: () => ({ content: "tok" }), getElementById: () => null,
  querySelectorAll: () => [], documentElement: { dataset: {}, removeAttribute() {} },
  readyState: "loading", addEventListener() {}, activeElement: null,
};
const ctx = {
  window: { __sfExport: (e) => { ex = e; }, addEventListener() {} }, document: doc, console,
  setInterval() {}, setTimeout() {}, fetch() { return Promise.reject(new Error("no network")); },
  TextEncoder, location: { hash: "" }, navigator: {},
};
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[2], "utf8"), ctx);
assert.ok(ex, "app.js did not export");

// el builds a fake element; q maps a selector to what querySelector(All) returns.
function el(props, q) {
  const e = Object.assign({
    dataset: {}, className: "", title: "", hidden: false, textContent: "", attrs: {}, q: q || {},
    setAttribute(k, v) { this.attrs[k] = v; },
    removeAttribute(k) { delete this.attrs[k]; if (k === "title") this.title = ""; },
    querySelector(sel) { return (this.q[sel] || [])[0] || null; },
    querySelectorAll(sel) { return this.q[sel] || []; },
  }, props);
  return e;
}
// a case may return a promise (the ones that drive fetch)
Promise.resolve(require(process.argv[3])(ex, { el, doc, assert, ctx })).then(() => console.log("ok"), (e) => { console.error(e); process.exit(1); });
