// Project page resources charts (owner E): window.sfProjectResources.render(el, resources, opts).
// resources: the Resources JSON of docs/design/project-page/api.md.
// opts: {range: "15m"|"1h"|"6h", onRange(range), kindToggle: "checkout"|"kind"|false, name, now}.
// Hand-rolled SVG, no inline styles (CSP): geometry goes through attributes or
// CSSOM, never a style attribute in the markup.
(function (root) {
  "use strict";
  var NB = " ", MB = 1048576, GB = 1073741824;
  var SPAN = { "15m": 900, "1h": 3600, "6h": 21600 };
  var RANGE_LABEL = { "15m": "15 min", "1h": "1 h", "6h": "6 h" };
  var RANGE_LONG = { "15m": "15 minutes", "1h": "hour", "6h": "6 hours" };
  var KINDS = ["database", "app", "host-app", "utility"];
  var KIND_LABEL = { database: "database", app: "app", "host-app": "host app", utility: "utility" };
  var MAX_SLOTS = 4, SVC_ATTENTION = 0.85, SVC_FAILED = 0.95;

  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function fmtBytes(b) {
    if (b == null) return "—";
    return b < GB ? Math.round(b / MB) + NB + "MB" : (b / GB).toFixed(1) + NB + "GB";
  }
  function fmtPct(v) { return v == null ? "—" : v.toFixed(1) + NB + "%"; }
  function fmtGBWhole(b) { return Math.round(b / GB) + NB + "GB"; }
  function ago(s) {
    s = Math.max(0, Math.round(s));
    if (s < 30) return "now";
    if (s < 3600) return Math.round(s / 60) + " min ago";
    var h = Math.floor(s / 3600), m = Math.round((s % 3600) / 60);
    return h + " h" + (m ? " " + m + " min" : "") + " ago";
  }
  function dur(s) {
    s = Math.round(s);
    if (s < 60) return s + " s";
    if (s < 3600) return Math.round(s / 60) + " min";
    var h = Math.floor(s / 3600), m = Math.round((s % 3600) / 60);
    return h + " h" + (m ? " " + m + " min" : "");
  }
  function niceMax(max, step) { return Math.max(step, Math.ceil(max / step) * step); }
  // Docker reports the VM total for a container with no limit: that is "no limit".
  function limitOf(s, machine) {
    var l = s.mem_limit_bytes || 0;
    return l && !(machine && machine.memory_bytes && l >= machine.memory_bytes) ? l : 0;
  }
  function tone(r) { return r >= SVC_FAILED ? "failed" : r >= SVC_ATTENTION ? "attention" : ""; }

  // ---------- model ----------
  // Folds the raw series onto one shared time grid. A stopped period has no
  // samples; it stays null (a gap), never zero. Samples further apart than
  // `thr` are not joined.
  function onGrid(pts, m, grid, thr) {
    var out = new Array(grid.length), j = 0, i, g, a, b;
    for (i = 0; i < grid.length; i++) {
      g = grid[i];
      while (j + 1 < pts.length && pts[j + 1][0] <= g) j++;
      a = pts[j];
      if (!a || a[0] > g) { out[i] = null; continue; }
      if (a[0] === g) { out[i] = a[m]; continue; }
      b = pts[j + 1];
      out[i] = b && b[0] - a[0] <= thr ? a[m] + (b[m] - a[m]) * (g - a[0]) / (b[0] - a[0]) : null;
    }
    return out;
  }
  function sum(lists, n) {
    var out = new Array(n), i, k, s, any;
    for (i = 0; i < n; i++) {
      s = 0; any = false;
      for (k = 0; k < lists.length; k++) if (lists[k][i] != null) { s += lists[k][i]; any = true; }
      out[i] = any ? s : null;
    }
    return out;
  }
  // main first, then by name: a checkout keeps its colour while others come and go.
  function checkoutOrder(res) {
    var seen = {}, list = [];
    [].concat(res.series || [], res.services || [], res.disk || []).forEach(function (x) {
      if (x && x.checkout != null && !seen[x.checkout]) { seen[x.checkout] = 1; list.push(x.checkout); }
    });
    list.sort(function (a, b) { return a === "main" ? -1 : b === "main" ? 1 : a < b ? -1 : a > b ? 1 : 0; });
    return list;
  }
  function slotOf(order, c) { var i = order.indexOf(c); return i < 0 || i >= MAX_SLOTS ? 0 : i + 1; }

  function buildModel(res, o) {
    o = o || {};
    var range = o.range || res.range || "15m", span = SPAN[range] || 900;
    var interval = res.interval_s || 10, thr = Math.max(90, 3 * interval);
    var end = o.now || Date.now() / 1000;
    var all = res.series || [], first = Infinity, last = -Infinity;
    all.forEach(function (s) { (s.points || []).forEach(function (p) { if (p[0] < first) first = p[0]; if (p[0] > last) last = p[0]; }); });
    var has = last > -Infinity;
    var start = Math.max(end - span, has ? first : end - span);
    if (end - start < 6 * interval) start = end - 6 * interval;
    var short = has && start > end - span + thr;
    var order = checkoutOrder(res);
    var gridSet = {}, items = all.map(function (s) {
      var pts = (s.points || []).filter(function (p) { return p[0] >= start && p[0] <= end; }).sort(function (a, b) { return a[0] - b[0]; });
      pts.forEach(function (p) { gridSet[p[0]] = 1; });
      return { s: s, pts: pts };
    });
    var grid = Object.keys(gridSet).map(Number).sort(function (a, b) { return a - b; }), n = grid.length;
    items.forEach(function (it) { it.cpu = onGrid(it.pts, 1, grid, thr); it.mem = onGrid(it.pts, 2, grid, thr); });

    function group(keyOf, names, slots, metric) {
      return names.map(function (name, k) {
        var members = items.filter(function (it) { return keyOf(it.s) === name; }).map(function (it) { return it[metric]; });
        return { name: name, label: slots.label ? slots.label(name) : name, slot: slots.slot(name, k), values: sum(members, n) };
      });
    }
    // checkouts beyond four fold into one grey "Other" group
    var shown = order.slice(0, MAX_SLOTS), rest = order.slice(MAX_SLOTS);
    function byCheckout(metric) {
      var gs = group(function (s) { return s.checkout; }, shown, { slot: function (c) { return slotOf(order, c); } }, metric);
      if (rest.length) {
        var members = items.filter(function (it) { return rest.indexOf(it.s.checkout) >= 0; }).map(function (it) { return it[metric]; });
        gs.push({ name: "Other", label: "Other", slot: 0, values: sum(members, n) });
      }
      return gs;
    }
    var m = {
      range: range, span: span, start: start, end: end, grid: grid, n: n, interval: interval, thr: thr, short: short,
      last: has ? last : null, order: order, items: items,
      cpu: byCheckout("cpu"),
      mem: { checkout: byCheckout("mem"), kind: group(function (s) { return s.kind; }, KINDS, { slot: function (k, i) { return i + 1; }, label: function (k) { return KIND_LABEL[k]; } }, "mem") },
    };
    // kinds with no service at all stay out of the legend
    m.mem.kind = m.mem.kind.filter(function (g) { return g.values.some(function (v) { return v != null; }); });
    return m;
  }
  function currentOf(m, g) {
    for (var i = m.n - 1; i >= 0; i--) {
      if (g.values[i] != null) return m.grid[i] >= m.end - m.thr ? g.values[i] : null;
    }
    return null;
  }

  // ---------- charts ----------
  var L = 46, R = 8, T = 8, B = 22;
  function chartSvg(m, groups, stacked, W) {
    W = Math.max(240, W || 560);
    var H = W < 360 ? 150 : 180, pw = W - L - R, ph = H - T - B, n = m.n, i;
    var tops = [], layers = [], maxV = 0;
    for (i = 0; i < n; i++) tops.push(0);
    groups.forEach(function (g) {
      var lo = tops.slice(), hi = g.values.map(function (v, k) { return v == null ? null : stacked ? tops[k] + v : v; });
      if (stacked) g.values.forEach(function (v, k) { if (v != null) tops[k] += v; });
      hi.forEach(function (v) { if (v != null && v > maxV) maxV = v; });
      layers.push({ g: g, lo: lo, hi: hi });
    });
    var step = stacked ? GB : 100, ymax = niceMax(maxV, step), span = m.end - m.start;
    var X = function (k) { return L + (m.grid[k] - m.start) / span * pw; };
    var Y = function (v) { return T + ph - v / ymax * ph; };
    var svg = '<svg class="rs-svg" width="' + W + '" height="' + H + '" viewBox="0 0 ' + W + " " + H + '" aria-hidden="true" focusable="false">';
    for (var v = 0; v <= ymax + 1e-6; v += step) {
      svg += '<line class="rs-grid" x1="' + L + '" x2="' + (L + pw) + '" y1="' + Y(v).toFixed(1) + '" y2="' + Y(v).toFixed(1) + '"/>' +
        '<text class="rs-axis" x="' + (L - 6) + '" y="' + (Y(v) + 4).toFixed(1) + '" text-anchor="end">' + (stacked ? Math.round(v / GB) + NB + "GB" : v + NB + "%") + "</text>";
    }
    [0, 0.5, 1].forEach(function (f, k) {
      svg += '<text class="rs-axis" x="' + (L + f * pw).toFixed(1) + '" y="' + (H - 6) + '" text-anchor="' + (k === 0 ? "start" : k === 2 ? "end" : "middle") + '">' +
        (f === 1 ? "now" : ago(span * (1 - f))) + "</text>";
    });
    layers.forEach(function (l) {
      var runs = [], cur = null;
      l.hi.forEach(function (val, k) {
        if (val == null) { cur = null; return; }
        if (!cur) { cur = []; runs.push(cur); }
        cur.push(k);
      });
      runs.forEach(function (r) {
        var cls = " rs-s" + l.g.slot;
        if (r.length === 1) { // an isolated sample: a dot, a path would be invisible
          svg += '<circle class="rs-dot' + cls + '" cx="' + X(r[0]).toFixed(1) + '" cy="' + Y(l.hi[r[0]]).toFixed(1) + '" r="3"/>';
          return;
        }
        var top = r.map(function (k) { return X(k).toFixed(1) + " " + Y(l.hi[k]).toFixed(1); });
        if (stacked) {
          var bot = r.slice().reverse().map(function (k) { return X(k).toFixed(1) + " " + Y(l.lo[k]).toFixed(1); });
          svg += '<path class="rs-area' + cls + '" d="M' + top.join("L") + "L" + bot.join("L") + 'Z"/>';
        }
        svg += '<path class="rs-line' + cls + '" d="M' + top.join("L") + '"/>';
      });
    });
    svg += '<line class="rs-cross" x1="0" x2="0" y1="' + T + '" y2="' + (T + ph) + '" hidden="hidden"/></svg>';
    return { svg: svg, geo: { W: W, X: X, n: n } };
  }

  function legend(m, groups, kind, fmt) {
    return groups.map(function (g) {
      var cur = currentOf(m, g);
      return '<li><span class="rs-key rs-key-' + kind + " rs-s" + g.slot + '" aria-hidden="true"></span><span class="rs-legend-name">' + esc(g.label) +
        '</span><span class="rs-legend-val">' + (cur == null ? "stopped" : fmt(cur)) + "</span></li>";
    }).join("");
  }
  function empty(m) { return m.n === 0; }

  function chartCard(st, id, title, sub, tools, legendHtml, groups, stacked) {
    var m = st.m, body;
    delete st.charts[id];
    if (empty(m)) body = '<p class="rs-empty" role="status">Collecting samples… (first point in 10 s)</p>';
    else {
      var c = chartSvg(m, groups, stacked, st.widths[id]);
      st.charts[id] = c.geo;
      body = '<div class="rs-chart" data-chart="' + id + '" data-fk="chart:' + id + '" tabindex="0" role="group" aria-roledescription="chart" aria-describedby="rs-live-' + id + '" aria-label="' +
        esc(title + " over the last " + RANGE_LONG[m.range] + ". Use the arrow keys to read values; the table below has every value.") + '">' + c.svg + '<div class="rs-tip" hidden="hidden"></div></div>' +
        '<p class="rs-sr" id="rs-live-' + id + '" aria-live="polite"></p>';
    }
    return '<figure class="rs-card" aria-labelledby="rs-t-' + id + '"><div class="rs-card-head"><figcaption><h3 class="rs-title" id="rs-t-' + id + '">' + title + '</h3><p class="rs-sub">' + sub + "</p></figcaption>" + (tools || "") + "</div>" +
      (legendHtml ? '<ul class="rs-legend" aria-label="Legend">' + legendHtml + "</ul>" : "") + body + "</figure>";
  }

  // ---------- table view ----------
  function nearestIdx(m, t) {
    var lo = 0, hi = m.n - 1;
    while (lo < hi) { var mid = (lo + hi) >> 1; if (m.grid[mid] < t) lo = mid + 1; else hi = mid; }
    if (lo > 0 && Math.abs(m.grid[lo - 1] - t) <= Math.abs(m.grid[lo] - t)) lo--;
    return lo;
  }
  function dataTable(m, memGroups, open) {
    var rows = [], k, t, i, span = m.end - m.start;
    for (k = 1; k <= 12; k++) {
      t = m.start + span * k / 12; i = nearestIdx(m, t);
      rows.push({ t: t, i: Math.abs(m.grid[i] - t) <= 2 * m.thr ? i : -1 });
    }
    var cell = function (g, r, f) { return "<td>" + (r.i < 0 || g.values[r.i] == null ? "stopped" : f(g.values[r.i])) + "</td>"; };
    var head = '<tr><th scope="col">Time</th>' + memGroups.map(function (g) { return '<th scope="col">' + esc(g.label) + " memory</th>"; }).join("") +
      m.cpu.map(function (g) { return '<th scope="col">' + esc(g.label) + " CPU</th>"; }).join("") + "</tr>";
    var body = rows.map(function (r) {
      return '<tr><th scope="row">' + ago(m.end - r.t) + "</th>" + memGroups.map(function (g) { return cell(g, r, fmtBytes); }).join("") +
        m.cpu.map(function (g) { return cell(g, r, fmtPct); }).join("") + "</tr>";
    }).join("");
    return '<details class="rs-details" data-table="1"' + (open ? " open" : "") + '><summary data-fk="table">Show chart data as a table</summary>' +
      '<div class="rs-table-scroll" tabindex="0" role="region" aria-label="Chart data"><table class="rs-table"><caption class="rs-sr">Memory and CPU at twelve points of the last ' + RANGE_LONG[m.range] + "</caption><thead>" + head + "</thead><tbody>" + body + "</tbody></table></div></details>";
  }

  // ---------- disk ----------
  function diskRows(res, order) {
    var imgBytes = {}, users = {}, images = res.images || [];
    images.forEach(function (im) { imgBytes[im.name] = im.bytes; });
    (res.services || []).forEach(function (s) { if (s.image && imgBytes[s.image] != null) (users[s.image] = users[s.image] || {})[s.checkout] = 1; });
    var vol = {};
    (res.disk || []).forEach(function (d) { vol[d.checkout] = d.volumes_bytes || 0; });
    var rows = order.filter(function (c) { return vol[c] != null; }).map(function (c) {
      var img = 0, seen = {};
      (res.services || []).forEach(function (s) {
        if (s.checkout === c && users[s.image] && !seen[s.image]) { seen[s.image] = 1; img += imgBytes[s.image] / Object.keys(users[s.image]).length; }
      });
      return { c: c, vol: vol[c], img: img, slot: slotOf(order, c) };
    });
    var max = Math.max.apply(null, rows.map(function (r) { return r.vol + r.img; }).concat([1]));
    return rows.map(function (r) {
      var label = r.c + ": " + fmtBytes(r.vol) + " volumes and " + fmtBytes(r.img) + " share of images";
      return '<div class="rs-disk-row" tabindex="0" aria-label="' + esc(label) + '" title="' + esc(label) + '"><span class="rs-disk-name mono">' + esc(r.c) + "</span>" +
        '<span class="rs-disk-track"><span class="rs-disk-bar" aria-hidden="true"><span class="rs-disk-vol rs-s' + r.slot + '" data-w="' + (r.vol / max * 100).toFixed(2) + '"></span>' +
        (r.img > 0 ? '<span class="rs-disk-img" data-w="' + (r.img / max * 100).toFixed(2) + '"></span>' : "") + "</span>" +
        '<span class="rs-disk-val mono">' + fmtBytes(r.vol) + (r.img > 0 ? ' <span class="rs-dim">+ ' + fmtBytes(r.img) + "</span>" : "") + "</span></span></div>";
    }).join("");
  }

  // ---------- services table ----------
  function sparkline(m, item, metric, ymax, running) {
    var w = 72, h = 20;
    var pts = item ? item.pts : [];
    if (!pts.length) return '<svg class="rs-spark" width="72" height="20" aria-hidden="true" focusable="false"><line class="rs-spark-empty" x1="0" x2="72" y1="19" y2="19"/></svg>';
    var vals = pts.map(function (p) { return p[metric]; });
    var max = ymax || Math.max.apply(null, vals.concat([metric === 1 ? 5 : 64 * MB]));
    var span = m.end - m.start, d = "", prev = null, lx = 0, ly = 0;
    pts.forEach(function (p) {
      var x = 1 + (p[0] - m.start) / span * (w - 4), y = h - 2 - Math.min(1, p[metric] / max) * (h - 4);
      var joined = prev != null && p[0] - prev <= m.thr;
      d += (joined ? "L" : "M") + x.toFixed(1) + " " + y.toFixed(1); prev = p[0]; lx = x; ly = y;
    });
    var dot = running && m.end - prev <= m.thr ? '<circle class="rs-spark-dot" cx="' + lx.toFixed(1) + '" cy="' + ly.toFixed(1) + '" r="2.5"/>' : "";
    return '<svg class="rs-spark" width="72" height="20" viewBox="0 0 72 20" aria-hidden="true" focusable="false"><path class="rs-spark-line" d="' + d + '"/>' + dot + "</svg>";
  }
  function servicesTable(res, m) {
    var order = m.order, machine = res.machine || {};
    var svcs = res.services || [];
    var find = function (s) {
      for (var i = 0; i < m.items.length; i++) {
        var q = m.items[i].s;
        if (q.checkout === s.checkout && q.service === s.service && q.where === s.where) return m.items[i];
      }
      return null;
    };
    var diskBy = {}; (res.disk || []).forEach(function (d) { diskBy[d.checkout] = d.volumes_bytes || 0; });
    var nC = 0, nH = 0;
    var rows = order.map(function (c) {
      var list = svcs.filter(function (s) { return s.checkout === c; });
      var run = list.filter(function (s) { return s.state !== "stopped"; });
      var cpu = 0, mem = 0;
      run.forEach(function (s) { cpu += s.cpu_pct || 0; mem += s.mem_bytes || 0; if (s.where === "host") nH++; else nC++; });
      var group = '<div class="rs-row rs-group" role="row"><span class="rs-name" role="rowheader"><span class="rs-key rs-key-area rs-s' + slotOf(order, c) + '" aria-hidden="true"></span><b>' + esc(c) + "</b></span>" +
        '<span class="rs-where" role="cell">' + (run.length ? run.length + (run.length === 1 ? " service" : " services") : "Stopped · volumes kept") + "</span>" +
        '<span class="rs-cell" role="cell" data-label="CPU"><span class="mono rs-val">' + (run.length ? fmtPct(cpu) : "—") + "</span></span>" +
        '<span class="rs-cell" role="cell" data-label="Memory"><span class="mono rs-val">' + (run.length ? fmtBytes(mem) : "—") + "</span></span>" +
        '<span class="rs-cell" role="cell" data-label="Disk"><span class="mono">' + fmtBytes(diskBy[c] || 0) + "</span></span></div>";
      return group + list.map(function (s) {
        var it = find(s), running = s.state !== "stopped";
        var lim = limitOf(s, machine), r = running && lim ? s.mem_bytes / lim : null, tn = r == null ? "" : tone(r);
        var where = s.where === "host" ? "<span>Host app</span><span class=\"mono\">" + [s.pid ? "pid " + s.pid : "", s.uptime_s ? "up " + dur(s.uptime_s) : ""].filter(Boolean).join(" · ") + "</span>" :
          "<span>Docker</span><span class=\"mono\">" + esc(s.image) + "</span>";
        var memVal = !running ? "—" : fmtBytes(s.mem_bytes) + (lim ? " / " + fmtBytes(lim) : "");
        var extra = !running ? "" : lim ?
          '<svg class="rs-bar" width="40" height="4" viewBox="0 0 40 4" aria-hidden="true" focusable="false"' + (tn ? ' data-tone="' + tn + '"' : "") + '><rect class="rs-bar-track" width="40" height="4" rx="2"/><rect class="rs-bar-fill" width="' + Math.max(2, Math.round(Math.min(1, r) * 40)) + '" height="4" rx="2"/></svg>' :
          (machine.memory_bytes ? '<span class="rs-share" title="Share of the ' + fmtGBWhole(machine.memory_bytes) + ' on this machine">' + (s.mem_bytes / machine.memory_bytes * 100).toFixed(1) + NB + "% RAM</span>" : "");
        return '<div class="rs-row" role="row"><span class="rs-name" role="cell"><b>' + esc(s.service) + "</b>" + (running ? "" : '<span class="rs-dim">stopped</span>') + "</span>" +
          '<span class="rs-where" role="cell">' + where + "</span>" +
          '<span class="rs-cell" role="cell" data-label="CPU">' + sparkline(m, it, 1, 0, running) + '<span class="mono rs-val">' + (running ? fmtPct(s.cpu_pct) : "—") + "</span></span>" +
          '<span class="rs-cell" role="cell" data-label="Memory"' + (r != null ? ' title="' + Math.round(r * 100) + NB + '% of the container limit"' : "") + ">" +
          sparkline(m, it, 2, lim, running) + '<span class="mono rs-val' + (tn ? " rs-tone-" + tn : "") + '">' + memVal + "</span>" + extra + "</span>" +
          '<span class="rs-cell" role="cell" data-label="Disk">' + (s.where === "host" ? '<span class="rs-dim" title="Host apps write to the checkout; not measured">—</span>' : '<span class="mono">' + (s.disk_bytes ? fmtBytes(s.disk_bytes) : '<span class="rs-dim" title="Volumes are counted per checkout">—</span>') + "</span>") + "</span></div>";
      }).join("");
    }).join("");
    var t = res.totals || {};
    var total = '<div class="rs-row rs-total" role="row"><span role="rowheader">All checkouts</span><span class="rs-where" role="cell">' + nC + (nC === 1 ? " container" : " containers") + " · " + nH + (nH === 1 ? " host app" : " host apps") + "</span>" +
      '<span class="rs-cell" role="cell" data-label="CPU"><span class="mono rs-val">' + fmtPct(t.cpu_pct || 0) + '</span></span><span class="rs-cell" role="cell" data-label="Memory"><span class="mono rs-val">' + fmtBytes(t.mem_bytes || 0) +
      '</span></span><span class="rs-cell" role="cell" data-label="Disk"><span class="mono">' + fmtBytes(t.disk_bytes || 0) + "</span></span></div>";
    var head = '<div class="rs-head micro" role="row"><span role="columnheader">Service</span><span role="columnheader">Runs in</span><span class="rs-num" role="columnheader">CPU · ' + RANGE_LABEL[m.range] +
      '</span><span class="rs-num" role="columnheader">Memory</span><span class="rs-num" role="columnheader">Disk</span></div>';
    return '<div role="table" aria-label="Resources by checkout and service">' + head + rows + total + "</div>";
  }

  function callouts(res, m) {
    return (res.services || []).map(function (s) {
      var lim = limitOf(s, res.machine);
      if (s.state === "stopped" || !lim) return "";
      var r = s.mem_bytes / lim, tn = tone(r);
      if (!tn) return "";
      var it = null;
      m.items.forEach(function (x) { if (x.s.checkout === s.checkout && x.s.service === s.service && x.s.where === s.where) it = x; });
      var trend = "";
      if (it && it.pts.length > 1 && it.pts[0][2] < s.mem_bytes * 0.9) trend = " Up from " + fmtBytes(it.pts[0][2]) + " " + ago(m.end - it.pts[0][0]) + ".";
      return '<div class="callout callout-' + tn + '"><span class="callout-glyph" aria-hidden="true">' + (tn === "failed" ? "&#10005;" : "&#9650;") + '</span><div class="callout-body"><p class="callout-title">' +
        esc(s.service) + " on " + esc(s.checkout) + " uses " + Math.round(r * 100) + NB + "% of its memory limit</p><p class=\"callout-detail\">" + fmtBytes(s.mem_bytes) + " of " + fmtBytes(lim) + "." + trend +
        " Above the limit, Docker stops the container. Raise <code class=\"mono\">mem_limit</code> in the Compose file or restart " + esc(s.service) + ".</p></div></div>";
    }).join("");
  }

  function summary(res, o, m) {
    var t = res.totals || {}, mc = res.machine || {}, name = esc(o.name || "This project");
    var running = (res.services || []).some(function (s) { return s.state !== "stopped"; });
    if (!running && !(res.services || []).length && m.n === 0) return '<p class="rs-summary">No samples yet.</p>';
    if (!running) return '<p class="rs-summary">Nothing runs. Volumes keep ' + fmtBytes(t.volumes_bytes || 0) + ".</p>";
    return '<p class="rs-summary">' + name + " uses <strong>" + ((t.cpu_pct || 0) / 100).toFixed(1) + NB + "cores</strong> of " + (mc.cores || "?") + ", <strong>" + fmtBytes(t.mem_bytes || 0) + "</strong> of " +
      fmtGBWhole(mc.memory_bytes || 0) + " memory and <strong>" + fmtBytes(t.disk_bytes || 0) + "</strong> of disk (" + fmtBytes(t.volumes_bytes || 0) + " volumes, " + fmtBytes(t.images_bytes || 0) + " images).</p>";
  }

  // ---------- page ----------
  var states = typeof WeakMap === "function" ? new WeakMap() : null;
  function stateFor(el) {
    var st = states.get(el);
    if (!st) { st = { by: null, tableOpen: false, hover: null, widths: {}, charts: {}, rendering: false }; states.set(el, st); }
    return st;
  }

  function html(st) {
    var res = st.res, o = st.opts, m = buildModel(res, o);
    st.m = m;
    var by = st.by, memGroups = m.mem[by];
    var range = '<fieldset class="segmented rs-range"><legend class="rs-sr">Time range</legend>' + ["15m", "1h", "6h"].map(function (r) {
      return '<label><input type="radio" name="rs-range" value="' + r + '" data-fk="range:' + r + '"' + (r === m.range ? " checked" : "") + "><span>" + RANGE_LABEL[r] + "</span></label>";
    }).join("") + "</fieldset>";
    var since = m.short ? '<span class="rs-since">since dashboard start (' + dur(m.end - m.start) + ")</span>" : "";
    var lastAt = res.sampled_at ? Date.parse(res.sampled_at) / 1000 : m.last;
    var sampled = lastAt != null && !isNaN(lastAt) ? "Sampled " + dur(Math.max(0, m.end - lastAt)) + " ago · every " + m.interval + " s while this page is open, every 60 s otherwise" : "Collecting samples…";
    var toggle = o.kindToggle === false ? "" : '<fieldset class="segmented rs-mini"><legend class="rs-sr">Stack memory by</legend>' + [["checkout", "Checkout"], ["kind", "Kind"]].map(function (x) {
      return '<label><input type="radio" name="rs-by" value="' + x[0] + '" data-fk="by:' + x[0] + '"' + (x[0] === by ? " checked" : "") + "><span>" + x[1] + "</span></label>";
    }).join("") + "</fieldset>";
    var memSub = "Stacked by " + (by === "kind" ? "service kind" : "checkout");
    var imgTotal = (res.totals || {}).images_bytes;
    var disk = '<figure class="rs-card rs-disk-card" aria-labelledby="rs-t-disk"><div class="rs-card-head"><figcaption><h3 class="rs-title" id="rs-t-disk">Disk</h3><p class="rs-sub">Now, per checkout · volumes and a share of the images</p></figcaption></div>' +
      '<ul class="rs-legend" aria-label="Legend"><li><span class="rs-key rs-key-area rs-s0" aria-hidden="true"></span><span class="rs-legend-name">Volumes (checkout colour)</span></li><li><span class="rs-key rs-key-img" aria-hidden="true"></span><span class="rs-legend-name">Share of images</span></li></ul>' +
      '<div class="rs-disk">' + diskRows(res, m.order) + "</div>" +
      '<p class="rs-foot">Images are counted once in total (' + fmtBytes(imgTotal || 0) + "); a checkout's share splits each image between the checkouts that use it.</p></figure>";
    var stale = res.stats_error ? '<span class="rs-sampled rs-stale">Stats unavailable: ' + esc(res.stats_error) + ". Last values stay.</span>" : "";
    return '<div class="rs-bar-top' + (res.stats_error ? " rs-stale-on" : "") + '"><span class="rs-sampled">' + (stale || esc(sampled)) + '</span><span class="rs-bar-right">' + since + range + "</span></div>" +
      summary(res, o, m) +
      '<div class="rs-charts">' +
      chartCard(st, "mem", "Memory", memSub, toggle, empty(m) ? "" : legend(m, memGroups, "area", fmtBytes), memGroups, true) +
      chartCard(st, "cpu", "CPU", "Per checkout · % of one core", "", empty(m) ? "" : legend(m, m.cpu, "line", fmtPct), m.cpu, false) +
      disk + "</div>" +
      (empty(m) ? "" : dataTable(m, memGroups, st.tableOpen)) + callouts(res, m) + servicesTable(res, m) +
      '<p class="rs-foot">CPU is % of one core. Memory without a limit shows its share of this machine. History stays in memory for 6 h and starts over when the dashboard restarts.</p>';
  }

  // The crosshair belongs to the user, not to one render: it is kept as a time
  // (st.hover) and drawn again after every update or resize.
  function chartEl(el, id) { return el.querySelector ? el.querySelector('[data-chart="' + id + '"]') : null; }
  function groupsFor(st, id) { return id === "mem" ? st.m.mem[st.by] : st.m.cpu; }
  function showAt(el, st, id, i, announce) {
    var box = chartEl(el, id), m = st.m, geo = st.charts[id];
    if (!box || !geo || !m.n) return;
    i = Math.max(0, Math.min(m.n - 1, i));
    var x = geo.X(i), cross = box.querySelector(".rs-cross"), tip = box.querySelector(".rs-tip"), stacked = id === "mem";
    cross.setAttribute("x1", x); cross.setAttribute("x2", x); cross.removeAttribute("hidden");
    while (tip.firstChild) tip.removeChild(tip.firstChild);
    var doc = el.ownerDocument, mk = function (tag, cls, text) { var e = doc.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; };
    var head = mk("p", "rs-tip-time", ago(m.end - m.grid[i]));
    tip.appendChild(head);
    var total = 0, parts = [];
    groupsFor(st, id).forEach(function (g) {
      var v = g.values[i], row = mk("p", "rs-tip-row"), txt = v == null ? "stopped" : (stacked ? fmtBytes(v) : fmtPct(v));
      if (v != null) total += v;
      row.appendChild(mk("span", "rs-key rs-key-line rs-s" + g.slot));
      row.appendChild(mk("strong", "", txt));
      row.appendChild(mk("span", "", g.label));
      tip.appendChild(row); parts.push(g.label + " " + txt);
    });
    if (stacked) {
      var tr = mk("p", "rs-tip-row rs-tip-total"); tr.appendChild(mk("strong", "", fmtBytes(total))); tr.appendChild(mk("span", "", "total")); tip.appendChild(tr);
    }
    tip.removeAttribute("hidden");
    var left = x + 12; if (left + 200 > geo.W) left = x - 212;
    tip.style.left = Math.max(0, left) + "px";
    st.hover = { id: id, t: m.grid[i] };
    if (announce) { var live = el.querySelector("#rs-live-" + id); if (live) live.textContent = head.textContent + ": " + parts.join(", "); }
  }
  function hideAt(el, st, id) {
    var box = chartEl(el, id);
    if (!box) return;
    box.querySelector(".rs-cross").setAttribute("hidden", "hidden");
    box.querySelector(".rs-tip").setAttribute("hidden", "hidden");
  }
  function measure(el, st) {
    // charts were drawn at the last known width; redraw those whose box differs
    ["mem", "cpu"].forEach(function (id) {
      var box = chartEl(el, id);
      if (!box) return;
      var w = Math.round(box.clientWidth || 0);
      if (w >= 120 && Math.abs(w - (st.widths[id] || 0)) > 1) {
        st.widths[id] = w;
        var c = chartSvg(st.m, groupsFor(st, id), id === "mem", w);
        box.querySelector("svg").outerHTML = c.svg;
        st.charts[id] = c.geo;
      }
    });
  }
  function afterDraw(el, st) {
    var m = st.m;
    if (el.querySelectorAll) {
      el.querySelectorAll("[data-w]").forEach(function (e) { e.style.width = e.getAttribute("data-w") + "%"; });
    }
    measure(el, st);
    if (st.hover && m.n) showAt(el, st, st.hover.id, nearestIdx(m, st.hover.t), false);
  }

  function bind(el, st) {
    var doc = el.ownerDocument;
    var idOf = function (t) { var b = t && t.closest ? t.closest("[data-chart]") : null; return b; };
    el.addEventListener("pointermove", function (e) {
      var b = idOf(e.target); if (!b || !st.m || !st.m.n) return;
      var id = b.getAttribute("data-chart"), geo = st.charts[id]; if (!geo) return;
      var x = e.clientX - b.getBoundingClientRect().left, best = 0, bd = Infinity;
      for (var i = 0; i < geo.n; i++) { var d = Math.abs(geo.X(i) - x); if (d < bd) { bd = d; best = i; } }
      if (st.hover && st.hover.id !== id) hideAt(el, st, st.hover.id);
      showAt(el, st, id, best, false);
    });
    el.addEventListener("pointerout", function (e) {
      var b = idOf(e.target); if (!b || (e.relatedTarget && b.contains(e.relatedTarget))) return;
      if (doc.activeElement !== b) { hideAt(el, st, b.getAttribute("data-chart")); st.hover = null; }
    });
    el.addEventListener("focusin", function (e) {
      var b = idOf(e.target); if (!b || st.rendering || !st.m.n) return;
      var id = b.getAttribute("data-chart");
      showAt(el, st, id, st.hover && st.hover.id === id ? nearestIdx(st.m, st.hover.t) : st.m.n - 1, true);
    });
    el.addEventListener("focusout", function (e) {
      var b = idOf(e.target); if (!b || st.rendering) return;
      hideAt(el, st, b.getAttribute("data-chart")); st.hover = null;
    });
    el.addEventListener("keydown", function (e) {
      var b = idOf(e.target); if (!b || e.target !== b || !st.m.n) return;
      var id = b.getAttribute("data-chart"), m = st.m;
      var i = st.hover && st.hover.id === id ? nearestIdx(m, st.hover.t) : m.n - 1, jump = Math.max(1, Math.round(m.n / 12));
      if (e.key === "Escape") { hideAt(el, st, id); st.hover = null; return; }
      var k = { ArrowLeft: i - 1, ArrowRight: i + 1, PageUp: i - jump, PageDown: i + jump, Home: 0, End: m.n - 1 }[e.key];
      if (k == null) return;
      e.preventDefault(); showAt(el, st, id, k, true);
    });
    el.addEventListener("change", function (e) {
      var t = e.target; if (!t || t.type !== "radio") return;
      if (t.name === "rs-by") { st.by = t.value; redraw(el, st); }
      else if (t.name === "rs-range" && st.opts.onRange) st.opts.onRange(t.value);
    });
    el.addEventListener("toggle", function (e) { if (e.target && e.target.getAttribute && e.target.getAttribute("data-table")) st.tableOpen = !!e.target.open; }, true);
    if (typeof ResizeObserver === "function") {
      var ro = new ResizeObserver(function () {
        if (el.isConnected === false) { ro.disconnect(); st.ro = null; return; }
        if (st.m) { measure(el, st); if (st.hover && st.m.n) showAt(el, st, st.hover.id, nearestIdx(st.m, st.hover.t), false); }
      });
      ro.observe(el); st.ro = ro;
    }
  }

  function redraw(el, st) {
    var doc = el.ownerDocument, ae = doc && doc.activeElement, fk = ae && ae.getAttribute && el.contains && el.contains(ae) ? ae.getAttribute("data-fk") : null;
    st.rendering = true;
    el.innerHTML = html(st);
    afterDraw(el, st);
    if (fk && el.querySelector) { var n = el.querySelector('[data-fk="' + fk + '"]'); if (n && n.focus) n.focus({ preventScroll: true }); }
    st.rendering = false;
  }

  function render(el, res, opts) {
    opts = opts || {};
    var st = stateFor(el);
    st.res = res || {}; st.opts = opts;
    if (st.by == null) st.by = opts.kindToggle === "kind" ? "kind" : "checkout";
    if (opts.kindToggle === false) st.by = "checkout";
    if (el.classList) el.classList.add("rs-root");
    if (!st.bound) { st.bound = true; if (el.addEventListener) bind(el, st); }
    redraw(el, st);
  }

  root.sfProjectResources = { render: render, _model: buildModel, _fmt: { bytes: fmtBytes, pct: fmtPct }, _esc: esc };
})(typeof window !== "undefined" ? window : globalThis);
