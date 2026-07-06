// Overview: stat tiles + live sparklines fed by the stats SSE stream,
// backfilled from /api/stats/history.
import { fetchJSON, sse } from "../api.js";
import { el, fmtBytes, fmtNum, fmtDuration } from "../fmt.js";
import { sparkline } from "../components/sparkline.js";

const MAXPTS = 300;

let closeStream = null;

function tile(id, label, sub) {
  return el("div", { class: "tile" },
    el("div", { class: "label" }, label),
    el("div", { class: "value", id: "tile-" + id }, "–"),
    sub ? el("div", { class: "sub", id: "sub-" + id }, "") : null,
  );
}

function chartPanel(title, chart, valueId) {
  return el("div", { class: "panel chart-panel" },
    el("span", { class: "chart-value mono-num", id: valueId }, "–"),
    el("h2", {}, title),
    chart.node,
  );
}

export async function mount(view) {
  view.append(
    el("div", { class: "page-head" },
      el("h1", {}, "overview"),
      el("span", { class: "hint" }, "live · 1s resolution · last 5 minutes"),
    ),
    el("div", { class: "tiles", id: "tiles" },
      tile("keys", "keys"),
      tile("mem", "memory"),
      tile("clients", "clients", true),
      tile("cps", "commands/sec", true),
      tile("ratio", "cache hit ratio", true),
      tile("uptime", "uptime"),
    ),
  );

  const charts = {
    cps: sparkline({ format: (v) => fmtNum(v) + " cmd/s" }),
    mem: sparkline({ color: "var(--type-hash)", format: fmtBytes }),
    keys: sparkline({ color: "var(--type-list)", format: fmtNum }),
    clients: sparkline({ color: "var(--type-set)", format: String }),
  };
  view.append(
    el("div", { class: "charts" },
      chartPanel("commands / sec", charts.cps, "cv-cps"),
      chartPanel("memory", charts.mem, "cv-mem"),
      chartPanel("keys", charts.keys, "cv-keys"),
      chartPanel("clients", charts.clients, "cv-clients"),
    ),
  );

  // Series buffers. commands/sec is derived from the counter's deltas.
  const series = { cps: [], mem: [], keys: [], clients: [] };
  let lastSample = null;

  function pushSample(s) {
    if (lastSample) {
      const dt = (s.t - lastSample.t) / 1000;
      const cps = dt > 0 ? Math.max(0, (s.commands_total - lastSample.commands_total) / dt) : 0;
      series.cps.push(Math.round(cps));
    }
    lastSample = s;
    series.mem.push(s.memory_bytes);
    series.keys.push(s.keys);
    series.clients.push(s.clients);
    for (const k of Object.keys(series)) {
      if (series[k].length > MAXPTS) series[k].splice(0, series[k].length - MAXPTS);
    }
  }

  function redraw() {
    charts.cps.render(series.cps);
    charts.mem.render(series.mem);
    charts.keys.render(series.keys);
    charts.clients.render(series.clients);
    const last = (a) => a.length ? a[a.length - 1] : 0;
    setText("cv-cps", fmtNum(last(series.cps)));
    setText("cv-mem", fmtBytes(last(series.mem)));
    setText("cv-keys", fmtNum(last(series.keys)));
    setText("cv-clients", String(last(series.clients)));
  }

  function setText(id, s) {
    const n = document.getElementById(id);
    if (n) n.textContent = s;
  }

  function updateTiles(s) {
    setText("tile-keys", fmtNum(s.keys));
    setText("tile-mem", fmtBytes(s.memory_bytes));
    setText("tile-clients", String(s.connected_clients));
    setText("sub-clients", fmtNum(s.total_connections) + " total");
    setText("tile-ratio", (s.cache_hit_ratio * 100).toFixed(1) + "%");
    setText("sub-ratio", `${fmtNum(s.cache_hits)} hits / ${fmtNum(s.cache_misses)} misses`);
    setText("tile-uptime", fmtDuration(s.uptime_seconds));
    const cps = series.cps.length ? series.cps[series.cps.length - 1] : 0;
    setText("tile-cps", fmtNum(cps));
    setText("sub-cps", fmtNum(s.commands) + " total");
  }

  // Backfill history, then subscribe to the live stream.
  try {
    const hist = await fetchJSON("/api/stats/history");
    for (const s of hist.samples || []) {
      pushSample({
        t: s.t, commands_total: s.commands_total, memory_bytes: s.memory_bytes,
        keys: s.keys, clients: s.clients,
      });
    }
    redraw();
  } catch { /* fresh server; charts fill from the stream */ }

  try {
    const stats = await fetchJSON("/api/stats");
    updateTiles(stats);
  } catch { /* tiles update on first tick */ }

  closeStream = sse("/api/stream/stats", {
    tick: (s) => {
      pushSample({
        t: s.t, commands_total: s.commands, memory_bytes: s.memory_bytes,
        keys: s.keys, clients: s.connected_clients,
      });
      redraw();
      updateTiles(s);
    },
  });
}

export function unmount() {
  if (closeStream) { closeStream(); closeStream = null; }
}
