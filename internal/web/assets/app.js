// promtop web dashboard: renders the frames the Go server streams over
// server-sent events. Widget choice, ordering and value formatting happen on
// the server; this file lays cards out and draws charts.
"use strict";

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));

const state = {
  frame: null,
  opts: { all: false, filter: "" },
  paused: false,
  source: null,
  cards: new Map(), // id -> element
  detail: null,     // { id, data, series, line, plot, zoomed }
};

// ---------------------------------------------------------------- formatting
// Mirrors internal/view/format.go so axes and tooltips read like the cards.

function sig(v) {
  const a = Math.abs(v);
  const s = a >= 100 ? v.toFixed(0) : a >= 10 ? v.toFixed(1) : v.toFixed(2);
  return s.includes(".") ? s.replace(/0+$/, "").replace(/\.$/, "") : s;
}
function si(v) {
  const a = Math.abs(v);
  if (a === 0) return "0";
  if (a >= 1e12) return sig(v / 1e12) + "T";
  if (a >= 1e9) return sig(v / 1e9) + "G";
  if (a >= 1e6) return sig(v / 1e6) + "M";
  if (a >= 1e4) return sig(v / 1e3) + "k";
  if (a >= 1 && Number.isInteger(v)) return v.toFixed(0);
  if (a >= 0.001) return sig(v);
  return v.toPrecision(3).replace(/\.?0+e/, "e");
}
function iec(v) {
  const units = ["", "Ki", "Mi", "Gi", "Ti", "Pi"];
  let i = 0;
  while (Math.abs(v) >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return i === 0 ? v.toFixed(0) : sig(v) + units[i];
}
function percent(p) {
  const a = Math.abs(p);
  if (a === 0) return "0%";
  return (a < 10 ? p.toFixed(2) : a < 100 ? p.toFixed(1) : p.toFixed(0)) + "%";
}
function age(sec) {
  const s = Math.floor(sec);
  const pad = (n) => String(n).padStart(2, "0");
  if (s < 60) return s + "s";
  if (s < 3600) return Math.floor(s / 60) + "m" + pad(s % 60) + "s";
  if (s < 86400) return Math.floor(s / 3600) + "h" + pad(Math.floor((s % 3600) / 60)) + "m";
  return Math.floor(s / 86400) + "d" + pad(Math.floor((s % 86400) / 3600)) + "h";
}
function duration(sec) {
  const a = Math.abs(sec), neg = sec < 0 ? "-" : "";
  if (a === 0) return "0s";
  if (a < 1e-6) return neg + sig(a * 1e9) + "ns";
  if (a < 1e-3) return neg + sig(a * 1e6) + "µs";
  if (a < 1) return neg + sig(a * 1e3) + "ms";
  if (a < 60) return neg + sig(a) + "s";
  return neg + age(a);
}
function fmtValue(v, f, line) {
  if (v == null || Number.isNaN(v)) return "–";
  if (!Number.isFinite(v)) return v > 0 ? "+Inf" : "-Inf";
  if (line === "rate" && f.quantiled) return si(v) + "/s";
  const rate = f.rate;
  v *= f.scale || 1;
  switch (f.unit) {
    case "bytes": return iec(v) + "B" + (rate ? "/s" : "");
    case "seconds": return rate ? percent(v * 100) : duration(v);
    case "ratio": return percent(v * 100);
    case "percent": return percent(v);
    case "other": return si(v) + f.sym + (rate ? "/s" : "");
  }
  return si(v) + (rate ? "/s" : "");
}

// ---------------------------------------------------------------- charts

const cssVar = (el, name) => getComputedStyle(el).getPropertyValue(name).trim();

function scaleFor(vals, zeroBased) {
  let lo = Infinity, hi = -Infinity;
  for (const v of vals) if (v != null && Number.isFinite(v)) { lo = Math.min(lo, v); hi = Math.max(hi, v); }
  if (lo === Infinity) return [0, 1];
  if (zeroBased && lo >= 0 && lo < hi * 0.5) lo = 0;
  else if (hi !== lo) {
    const pad = (hi - lo) * 0.1;
    lo -= pad;
    if (zeroBased && lo < 0 && hi >= 0) lo = 0;
    hi += pad;
  }
  if (hi === lo) {
    if (hi === 0) return [0, 1];
    const d = Math.abs(hi) * 0.5;
    return [lo - d, hi + d];
  }
  return [lo, hi];
}

function prepCanvas(c) {
  const dpr = window.devicePixelRatio || 1;
  const w = c.clientWidth, h = c.clientHeight;
  if (!w || !h) return null;
  if (c.width !== Math.round(w * dpr) || c.height !== Math.round(h * dpr)) {
    c.width = Math.round(w * dpr);
    c.height = Math.round(h * dpr);
  }
  const ctx = c.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  return { ctx, w, h };
}

// Area chart: line with a fading fill; points fill the width from the right.
function drawArea(c, vals, color, zeroBased) {
  const p = prepCanvas(c);
  if (!p || !vals || !vals.length) return;
  const { ctx, w, h } = p;
  const [lo, hi] = scaleFor(vals, zeroBased);
  const n = Math.max(vals.length, 60);
  const step = w / (n - 1);
  const x0 = w - (vals.length - 1) * step;
  const y = (v) => h - 2 - ((v - lo) / (hi - lo)) * (h - 4);
  const grad = ctx.createLinearGradient(0, 0, 0, h);
  grad.addColorStop(0, color + "66");
  grad.addColorStop(1, color + "05");
  let seg = [];
  const flush = () => {
    if (seg.length > 1) {
      ctx.beginPath();
      seg.forEach(([px, py], i) => (i ? ctx.lineTo(px, py) : ctx.moveTo(px, py)));
      ctx.lineTo(seg[seg.length - 1][0], h);
      ctx.lineTo(seg[0][0], h);
      ctx.closePath();
      ctx.fillStyle = grad;
      ctx.fill();
      ctx.beginPath();
      seg.forEach(([px, py], i) => (i ? ctx.lineTo(px, py) : ctx.moveTo(px, py)));
      ctx.strokeStyle = color;
      ctx.lineWidth = 1.5;
      ctx.lineJoin = "round";
      ctx.stroke();
    } else if (seg.length === 1) {
      ctx.fillStyle = color;
      ctx.fillRect(seg[0][0] - 1.5, seg[0][1] - 1.5, 3, 3);
    }
    seg = [];
  };
  vals.forEach((v, i) => {
    if (v == null) return flush();
    seg.push([x0 + i * step, y(v)]);
  });
  flush();
}

// Mini bar chart for table rows, scaled to range or to its own values.
function drawBars(c, vals, color, range) {
  const p = prepCanvas(c);
  if (!p || !vals || !vals.length) return;
  const { ctx, w, h } = p;
  const [lo, hi] = range || scaleFor(vals, true);
  const bw = 3, gap = 1, n = Math.floor(w / (bw + gap));
  const data = vals.slice(-n);
  const x0 = w - data.length * (bw + gap);
  ctx.fillStyle = color;
  data.forEach((v, i) => {
    if (v == null) return;
    const f = Math.max(0, Math.min(1, (v - lo) / (hi - lo)));
    const bh = Math.max(1, f * h);
    ctx.globalAlpha = 0.45 + 0.55 * f;
    ctx.fillRect(x0 + i * (bw + gap), h - bh, bw, bh);
  });
  ctx.globalAlpha = 1;
}

// 0/1 history strip of a health flag.
function drawStrip(c, vals, ok, err) {
  const p = prepCanvas(c);
  if (!p || !vals) return;
  const { ctx, w, h } = p;
  const bw = 3, gap = 1, n = Math.floor(w / (bw + gap));
  const data = vals.slice(-n);
  const x0 = w - data.length * (bw + gap);
  data.forEach((v, i) => {
    if (v == null) return;
    ctx.fillStyle = v === 1 ? ok : err;
    const bh = v === 1 ? h : h * 0.35;
    ctx.fillRect(x0 + i * (bw + gap), h - bh, bw, bh);
  });
}

// ---------------------------------------------------------------- cards

function bar(frac, tone, cls = "") {
  const pct = Math.max(0, Math.min(1, frac ?? 0)) * 100;
  return `<div class="bar ${cls} tone-${tone || "ok"}"><i style="width:${pct.toFixed(1)}%"></i></div>`;
}

function more(c) {
  return c.more ? `<div class="more">+${c.more} more · click for all</div>` : "";
}

function cardBody(c) {
  const rows = c.rows || [];
  switch (c.view) {
    case "single":
      return `<div class="big">${esc(c.value || "–")}</div><div class="sub">${esc(c.sub)}</div><canvas class="area"></canvas>`;
    case "rows":
      return `<div class="rows">${rows.map((r) =>
        `<span class="l" title="${esc(r.label)}">${esc(r.label)}</span><span class="v">${esc(r.value)}</span><canvas class="spark"></canvas>`).join("")}</div>${more(c)}`;
    case "quantiles":
      return `<div class="rows">${rows.map((r) =>
        `<span class="l q">${esc(r.label)}</span><span class="v">${esc(r.value)}</span><canvas class="spark"></canvas>`).join("")}</div><div class="foot">${esc(c.footer)}</div>`;
    case "usage":
      if (!rows.length) {
        return `<div class="big" style="font-size:18px">${esc(c.value)}</div>${bar(c.frac, c.tone, "wide")}<canvas class="area" style="height:40px"></canvas>`;
      }
      return `<div class="usage">${rows.map((r) =>
        `<span class="l" title="${esc(r.label)}">${esc(r.label)}</span><span class="v tone-${r.tone}">${esc(r.value)}</span>${bar(r.frac, r.tone)}`).join("")}</div>${more(c)}`;
    case "state": {
      if (rows.length === 1 && c.spark) {
        const up = rows[0].up;
        return `<div class="updown tone-${up ? "ok" : "err"}">${up ? "● up" : "○ down"}</div><canvas class="strip"></canvas>`;
      }
      return `<div class="chips">${rows.map((r) => {
        const cls = r.tone === "accent" ? `set${r.up ? " on" : ""}` : r.up ? "" : "down";
        return `<span class="chip ${cls}">${esc(r.label)}</span>`;
      }).join("")}</div>`;
    }
    case "info":
      return `<div class="kv">${rows.map((r) => r.value !== undefined
        ? `<span class="k">${esc(r.label)}</span><span class="val" title="${esc(r.value)}">${esc(r.value)}</span>`
        : `<span class="val" style="grid-column:1/3">${esc(r.label)}</span>`).join("")}</div>${more(c)}`;
    case "time":
      if (rows.length === 1 && !c.more) {
        return `<div class="big">${esc(rows[0].value)}</div><div class="sub">${esc(rows[0].sub)}</div>`;
      }
      return `<div class="rows nospark">${rows.map((r) =>
        `<span class="l">${esc(r.label)}</span><span class="v">${esc(r.value)}</span>`).join("")}</div>${more(c)}`;
  }
  return "";
}

function drawCard(el, c) {
  const color = cssVar(el, "--k");
  const zero = c.kind === "rate";
  if (c.view === "single" || (c.view === "usage" && c.spark)) {
    const cv = el.querySelector("canvas.area");
    if (cv) drawArea(cv, c.spark, color, zero || c.view === "usage");
  }
  if (c.view === "rows" || c.view === "quantiles") {
    const cvs = el.querySelectorAll("canvas.spark");
    // Rows of one metric share a scale so they compare; groups do not.
    const shared = c.view === "quantiles" || !c.badge.startsWith("group")
      ? scaleFor(c.rows.flatMap((r) => r.spark || []), true) : null;
    c.rows.forEach((r, i) => { if (cvs[i]) drawBars(cvs[i], r.spark || [], color, shared); });
  }
  if (c.view === "state" && c.spark) {
    const cv = el.querySelector("canvas.strip");
    if (cv) drawStrip(cv, c.spark, cssVar(el, "--ok"), cssVar(el, "--err"));
  }
}

function renderCard(c) {
  let el = state.cards.get(c.id);
  if (!el) {
    el = document.createElement("article");
    el.addEventListener("click", () => openDetail(el.dataset.id));
    state.cards.set(c.id, el);
  }
  el.dataset.id = c.id;
  el.className = `card k-${c.kind}`;
  const head = c.head ? `<span class="head tone-${c.tone || "value"}">${esc(c.head)}</span>` : "";
  el.innerHTML = `<header><span class="title" title="${esc(c.id)}">${esc(c.title)}</span>${head}</header>${cardBody(c)}<footer>${esc(c.badge)}</footer>`;
  return el;
}

function render() {
  const fr = state.frame;
  if (!fr) return;
  $("target").textContent = fr.target || "";
  $("source").textContent = fr.source;
  const st = $("state");
  if (state.paused) { st.className = "state paused"; st.textContent = "❚❚ paused"; }
  else if (fr.err) { st.className = "state err"; st.textContent = "error"; }
  else { st.className = "state ok"; st.textContent = fr.interval + "s"; }
  const at = fr.at ? new Date(fr.at).toLocaleTimeString() : "";
  $("stats").textContent = `${fr.metrics} metrics · ${fr.series} series${fr.hidden && !state.opts.all ? ` · ${fr.hidden} hidden` : ""} · ${fr.took} · ${at}`;
  document.title = `promtop · ${fr.target || fr.source}`;

  const dl = $("names");
  if (dl.dataset.n !== String(fr.names?.length)) {
    dl.dataset.n = String(fr.names?.length);
    dl.innerHTML = (fr.names || []).map((n) => `<option value="${esc(n)}">`).join("");
  }

  const main = $("main");
  const frag = document.createDocumentFragment();
  const seen = new Set();
  if (fr.err) {
    const e = document.createElement("p");
    e.className = "error";
    e.textContent = "scrape failed: " + fr.err;
    frag.appendChild(e);
  }
  if (!fr.scrapes) {
    const e = document.createElement("p");
    e.className = "empty";
    e.textContent = "waiting for the first scrape…";
    frag.appendChild(e);
  }
  const todo = [];
  let cards = 0;
  for (const s of fr.sections || []) {
    const sec = document.createElement("section");
    sec.innerHTML = `<h2>${esc(s.title)}${s.hidden ? `<small>${s.hidden} hidden</small>` : ""}</h2>`;
    const grid = document.createElement("div");
    grid.className = "grid";
    for (const c of s.cards) {
      const el = renderCard(c);
      seen.add(c.id);
      grid.appendChild(el);
      todo.push([el, c]);
      cards++;
    }
    if (s.cards.length) sec.appendChild(grid);
    frag.appendChild(sec);
  }
  if (fr.scrapes && !cards && state.opts.filter) {
    const e = document.createElement("p");
    e.className = "empty";
    e.textContent = "no metrics match";
    frag.appendChild(e);
  }
  for (const id of state.cards.keys()) if (!seen.has(id)) state.cards.delete(id);
  main.replaceChildren(frag);
  for (const [el, c] of todo) drawCard(el, c);
}

// ---------------------------------------------------------------- data

function query() {
  const q = new URLSearchParams();
  if (state.opts.all) q.set("all", "1");
  if (state.opts.filter) q.set("filter", state.opts.filter);
  return q.toString();
}

// The URL fragment keeps the options and the open metric, so a link
// reopens the same view.
function updateHash() {
  const q = new URLSearchParams(query());
  if (state.detail) q.set("metric", state.detail.id);
  const h = q.toString();
  history.replaceState(null, "", h ? "#" + h : location.pathname);
}

function connect() {
  if (state.source) state.source.close();
  const q = query();
  updateHash();
  const src = new EventSource("api/events" + (q ? "?" + q : ""));
  src.onmessage = (ev) => {
    state.frame = JSON.parse(ev.data);
    if (state.paused) return;
    render();
    if (state.detail) loadDetail();
  };
  src.onerror = () => {
    $("state").className = "state err";
    $("state").textContent = "disconnected";
  };
  state.source = src;
}

// ---------------------------------------------------------------- details

function openDetail(id) {
  state.detail = { id, series: 0, line: 0, plot: null, zoomed: false };
  $("detail").hidden = false;
  document.body.style.overflow = "hidden";
  updateHash();
  loadDetail();
}

function closeDetail() {
  if (state.detail?.plot) state.detail.plot.destroy();
  state.detail = null;
  $("detail").hidden = true;
  document.body.style.overflow = "";
  $("d-chart").replaceChildren();
  updateHash();
}

async function loadDetail() {
  const d = state.detail;
  if (!d) return;
  const q = query();
  const res = await fetch(`api/panel?id=${encodeURIComponent(d.id)}${q ? "&" + q : ""}`);
  if (!res.ok || state.detail !== d) return;
  d.data = await res.json();
  renderDetail();
}

function renderDetail() {
  const d = state.detail, data = d.data;
  $("d-name").textContent = data.name;
  $("d-describe").textContent = data.describe;
  $("d-help").textContent = data.help || "";
  const series = data.series || [];
  d.series = Math.max(0, Math.min(d.series, series.length - 1));
  const s = series[d.series];
  const plotted = s && data.kind !== "info" && data.kind !== "time";

  $("d-select").innerHTML = s && series.length > 1
    ? `series <b>${d.series + 1}/${data.total}</b> · ${esc(s.labels || "{}")}`
    : s ? esc(s.labels) : "no data in the latest scrape";

  // Tabs: one line at a time; histograms and summaries overlay their quantiles.
  const quantiles = s ? s.lines.filter((l) => l !== "rate" && l !== "avg") : [];
  const tabs = plotted ? (quantiles.length > 1 ? ["quantiles", ...s.lines.filter((l) => !quantiles.includes(l))] : s.lines) : [];
  d.line = Math.max(0, Math.min(d.line, tabs.length - 1));
  $("d-tabs").innerHTML = tabs.length > 1
    ? tabs.map((t, i) => `<button type="button" data-i="${i}" class="${i === d.line ? "on" : ""}">${esc(t)}</button>`).join("")
    : "";
  $("d-tabs").querySelectorAll("button").forEach((b) => b.addEventListener("click", () => { d.line = +b.dataset.i; renderDetail(); }));

  if (plotted && tabs.length) drawDetailChart(data, s, tabs[d.line] === "quantiles" ? quantiles : [tabs[d.line]]);
  else { if (d.plot) d.plot.destroy(); d.plot = null; $("d-chart").replaceChildren(); }

  // Bucket distribution of the selected histogram series.
  const dist = $("d-dist");
  const b = (s?.buckets || []);
  const first = b.findIndex((x) => x.count > 0);
  if (data.kind === "histogram" && first >= 0) {
    let last = b.length - 1;
    while (b[last].count === 0) last--;
    const shown = b.slice(first, last + 1), maxC = Math.max(...shown.map((x) => x.count));
    dist.innerHTML = `<h3>Distribution · observations per bucket, last interval</h3><div class="rowsd">${shown.map((x) =>
      `<span class="le">${esc(x.le)}</span>${bar(x.count / maxC, "", "")}<span>${si(x.count)}</span>`).join("")}</div>`;
  } else {
    dist.innerHTML = data.kind === "histogram" ? `<h3>Distribution</h3><p class="note">no observations in the last interval</p>` : "";
  }

  // Series table.
  const cols = data.columns || [];
  const usage = data.kind === "usage";
  const head = `<tr><th>labels</th>${cols.map((c) => `<th>${esc(c)}</th>`).join("")}${usage ? "<th></th>" : ""}</tr>`;
  const body = series.map((x, i) => {
    let cells = x.cells.map((v, j) => {
      const tone = (usage && j === 2) || data.kind === "state" ? ` class="tone-${x.tone || (x.up === false ? "err" : x.up ? "ok" : "")}"` : "";
      return `<td${tone}>${esc(v)}</td>`;
    }).join("");
    if (usage) cells += `<td>${bar(x.frac, x.tone)}</td>`;
    return `<tr data-i="${i}" class="${i === d.series ? "sel" : ""}"><td>${esc(x.labels || x.label)}</td>${cells}</tr>`;
  }).join("");
  const note = data.total > series.length ? `<p class="note">showing ${series.length} of ${data.total} series</p>` : "";
  $("d-table").innerHTML = series.length > 1 || cols.length > 1
    ? `<h3>Series</h3><table><thead>${head}</thead><tbody>${body}</tbody></table>${note}` : "";
  $("d-table").querySelectorAll("tbody tr").forEach((tr) => tr.addEventListener("click", () => { d.series = +tr.dataset.i; renderDetail(); }));
}

function drawDetailChart(data, s, lines) {
  const d = state.detail;
  const box = $("d-chart");
  const n = Math.max(...lines.map((l) => (s.values[l] || []).length));
  const end = data.at / 1000;
  const xs = Array.from({ length: n }, (_, i) => end - (n - 1 - i) * data.interval);
  const ys = lines.map((l) => {
    const v = s.values[l] || [];
    return Array(n - v.length).fill(null).concat(v);
  });
  const el = document.querySelector(".sheet");
  const kindVar = { histogram: "--k-histogram", summary: "--k-summary", rate: "--k-rate", time: "--k-time", info: "--k-info" }[data.kind] || "--k-gauge";
  const palette = [cssVar(el, kindVar), cssVar(el, "--k-rate"), cssVar(el, "--k-summary"), cssVar(el, "--k-time"), cssVar(el, "--section")];
  const fmt = (v, line) => fmtValue(v, data.fmt, line);
  const zero = data.kind === "rate" || lines.includes("rate") || ys.flat().every((v) => v == null || v >= 0);
  const key = lines.join(",") + "|" + d.id + "|" + d.series;
  const width = box.clientWidth || 900;

  if (d.plot && d.plotKey === key) {
    d.plot.setData([xs, ...ys], !d.zoomed);
    return;
  }
  if (d.plot) d.plot.destroy();
  const grid = { stroke: cssVar(el, "--border"), width: 1 };
  const axis = { stroke: cssVar(el, "--dim"), grid, ticks: { stroke: cssVar(el, "--border") }, font: "12px system-ui" };
  const opts = {
    width, height: 320,
    cursor: { drag: { x: true, y: false } },
    legend: { live: true },
    scales: {
      x: { time: true },
      y: {
        range: (u, lo, hi) => {
          const [a, b] = scaleFor([lo, hi], zero);
          return [a, b];
        },
      },
    },
    axes: [axis, { ...axis, size: 70, values: (u, splits) => splits.map((v) => fmt(v, lines[0])) }],
    series: [
      { value: (u, v) => v == null ? "–" : new Date(v * 1000).toLocaleTimeString() },
      ...lines.map((l, i) => ({
        label: l, stroke: palette[i % palette.length], width: 2,
        fill: lines.length === 1 ? palette[0] + "22" : undefined,
        value: (u, v) => fmt(v, l), spanGaps: false, points: { show: false },
      })),
    ],
    hooks: { setSelect: [() => { d.zoomed = true; }] },
  };
  box.replaceChildren();
  d.plot = new uPlot(opts, [xs, ...ys], box);
  d.plotKey = key;
  d.zoomed = false;
  box.ondblclick = () => { d.zoomed = false; };
}

// ---------------------------------------------------------------- controls

function setFilter(v) {
  if (v === state.opts.filter) return;
  state.opts.filter = v;
  connect();
}

// initialOptions reads the URL fragment; without one, the server's defaults.
async function initialOptions(h) {
  if (location.hash.length > 1) return { all: h.get("all") === "1", filter: h.get("filter") || "" };
  try {
    const res = await fetch("api/config");
    if (res.ok) {
      const c = await res.json();
      return { all: !!c.all, filter: c.filter || "" };
    }
  } catch { /* offline or old server: plain defaults */ }
  return { all: false, filter: "" };
}

async function init() {
  const h = new URLSearchParams(location.hash.slice(1));
  const o = await initialOptions(h);
  state.opts.all = o.all;
  state.opts.filter = o.filter;
  $("all").checked = state.opts.all;
  $("filter").value = state.opts.filter;

  let t = 0;
  $("filter").addEventListener("input", (e) => { clearTimeout(t); t = setTimeout(() => setFilter(e.target.value.trim()), 200); });
  $("all").addEventListener("change", (e) => { state.opts.all = e.target.checked; connect(); });
  $("pause").addEventListener("click", togglePause);
  $("d-close").addEventListener("click", closeDetail);
  $("detail").addEventListener("click", (e) => { if (e.target === $("detail")) closeDetail(); });
  window.addEventListener("resize", () => {
    render();
    if (state.detail?.plot) state.detail.plot.setSize({ width: $("d-chart").clientWidth, height: 320 });
  });
  document.addEventListener("keydown", (e) => {
    const typing = e.target.tagName === "INPUT";
    if (e.key === "Escape") {
      if (state.detail) closeDetail();
      else if (typing) e.target.blur();
      else if (state.opts.filter) { $("filter").value = ""; setFilter(""); }
      return;
    }
    if (typing || e.ctrlKey || e.metaKey || e.altKey) return;
    const d = state.detail;
    if (d && d.data) {
      const keys = { ArrowDown: [1, 0], j: [1, 0], ArrowUp: [-1, 0], k: [-1, 0], ArrowRight: [0, 1], l: [0, 1], ArrowLeft: [0, -1], h: [0, -1] };
      if (keys[e.key]) {
        e.preventDefault();
        d.series = Math.max(0, d.series + keys[e.key][0]);
        d.line = Math.max(0, d.line + keys[e.key][1]);
        renderDetail();
        return;
      }
    }
    if (e.key === "/") { e.preventDefault(); $("filter").focus(); }
    else if (e.key === "a") { $("all").click(); }
    else if (e.key === "p" || e.key === " ") { e.preventDefault(); togglePause(); }
  });
  connect();
  if (h.get("metric")) openDetail(h.get("metric"));
}

function togglePause() {
  state.paused = !state.paused;
  $("pause").classList.toggle("on", state.paused);
  $("pause").textContent = state.paused ? "resume" : "pause";
  render();
}

init();
