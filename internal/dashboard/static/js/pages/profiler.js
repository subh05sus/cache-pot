// Profiler: live MONITOR-style command stream over SSE. Events arrive in
// batched frames; the page keeps a capped ring and renders only the newest
// rows, so a benchmark-grade firehose stays smooth. Pause freezes rendering
// client-side (the ring keeps filling); a drop badge reports server-side
// sampling.
import { sse, decodeServerStr } from "../api.js";
import { el, hexPreview } from "../fmt.js";

const RING_MAX = 2000;
const RENDER_MAX = 400; // rows in the DOM

let closeStream = null;
let ring = [];
let paused = false;
let filterText = "";
let dropped = 0;
let renderQueued = false;

export async function mount(view) {
  ring = [];
  paused = false;
  filterText = "";
  dropped = 0;

  const pauseBtn = el("button", { onclick: togglePause }, "⏸ pause");
  const filterIn = el("input", {
    type: "text", placeholder: "filter (command, key, addr)", style: "width:240px",
    oninput: () => { filterText = filterIn.value.toLowerCase(); scheduleRender(); },
  });

  view.append(
    el("div", { class: "page-head" },
      el("h1", {}, "profiler"),
      el("span", { class: "hint" }, "every command the server dispatches, live"),
      el("span", { class: "spacer" }),
      el("span", { class: "badge live", id: "prof-rate" }, "0 evt/s"),
      el("span", { class: "badge drop", id: "prof-drop", style: "display:none" }, ""),
    ),
    el("div", { class: "field-row", style: "margin-bottom:10px" },
      pauseBtn,
      filterIn,
      el("button", { class: "ghost", onclick: () => { ring = []; scheduleRender(); } }, "clear"),
      el("span", { class: "muted", id: "prof-count" }, ""),
    ),
    el("div", { class: "log-surface", style: "height: 64vh", id: "prof-log" },
      el("div", { class: "faint" }, "waiting for commands… run something in the workbench or via redis-cli"),
    ),
  );

  function togglePause() {
    paused = !paused;
    pauseBtn.textContent = paused ? "▶ resume" : "⏸ pause";
    if (!paused) scheduleRender();
  }

  // events/sec meter
  let evCount = 0;
  const rateTimer = setInterval(() => {
    const badge = document.getElementById("prof-rate");
    if (badge) badge.textContent = evCount + " evt/s";
    evCount = 0;
  }, 1000);

  closeStream = sse("/api/stream/monitor", {
    cmd: (frame) => {
      dropped = frame.dropped || 0;
      for (const ev of frame.events) {
        ring.push(ev);
        evCount++;
      }
      if (ring.length > RING_MAX) ring.splice(0, ring.length - RING_MAX);
      if (!paused) scheduleRender();
    },
  });

  const origClose = closeStream;
  closeStream = () => { origClose(); clearInterval(rateTimer); };
}

export function unmount() {
  if (closeStream) { closeStream(); closeStream = null; }
  ring = [];
}

function scheduleRender() {
  if (renderQueued) return;
  renderQueued = true;
  requestAnimationFrame(() => {
    renderQueued = false;
    render();
  });
}

function fmtArgs(args) {
  return args.map((a) => {
    const dec = decodeServerStr(a);
    return dec.binary ? "0x" + hexPreview(dec.text, 8).replaceAll(" ", "") : dec.text;
  });
}

function render() {
  const log = document.getElementById("prof-log");
  if (!log) return;

  const dropBadge = document.getElementById("prof-drop");
  if (dropBadge) {
    dropBadge.style.display = dropped > 0 ? "" : "none";
    dropBadge.textContent = dropped + " dropped";
  }

  let rows = ring;
  if (filterText) {
    rows = ring.filter((ev) =>
      ev.addr.toLowerCase().includes(filterText) ||
      fmtArgs(ev.args).join(" ").toLowerCase().includes(filterText));
  }
  const countEl = document.getElementById("prof-count");
  if (countEl) countEl.textContent = rows.length + " buffered";

  const atBottom = log.scrollTop + log.clientHeight >= log.scrollHeight - 40;
  log.innerHTML = "";
  const slice = rows.slice(-RENDER_MAX);
  if (!slice.length) {
    log.appendChild(el("div", { class: "faint" }, "no events" + (filterText ? " match the filter" : " yet")));
    return;
  }
  const frag = document.createDocumentFragment();
  for (const ev of slice) {
    const t = new Date(ev.t / 1000);
    const ts = t.toLocaleTimeString(undefined, { hour12: false }) + "." + String(ev.t % 1000000).padStart(6, "0").slice(0, 3);
    const args = fmtArgs(ev.args);
    frag.appendChild(el("div", { style: "display:flex; gap:10px" },
      el("span", { class: "faint", style: "flex:none" }, ts),
      el("span", { class: "muted", style: "flex:none; min-width:130px" }, `[${ev.addr}#${ev.client_id}]`),
      el("span", { style: "word-break:break-all" },
        el("span", { class: "accent" }, args[0] + " "),
        args.slice(1).join(" ")),
    ));
  }
  log.appendChild(frag);
  if (atBottom) log.scrollTop = log.scrollHeight;
}
