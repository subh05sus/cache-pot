// SlowLog: table of commands that exceeded the threshold, with controls for
// the threshold and retention, and a reset button.
import { fetchJSON, putJSON, del, decodeServerStr } from "../api.js";
import { el, fmtMicros, fmtTime } from "../fmt.js";
import { toast } from "../components/toast.js";

let timer = null;

export async function mount(view) {
  const thresholdIn = el("input", { type: "number", style: "width:110px", step: "1000" });
  const maxLenIn = el("input", { type: "number", style: "width:80px", min: "0" });

  view.append(
    el("div", { class: "page-head" },
      el("h1", {}, "slowlog"),
      el("span", { class: "hint" }, "commands slower than the threshold; -1 disables logging"),
      el("span", { class: "spacer" }),
      el("button", { class: "danger", onclick: reset }, "reset log"),
    ),
    el("div", { class: "panel", style: "margin-bottom:12px" },
      el("div", { class: "field-row" },
        el("label", {}, "slower than (µs)"), thresholdIn,
        el("label", {}, "keep"), maxLenIn,
        el("button", { class: "primary", onclick: applyConfig }, "apply"),
        el("span", { class: "muted", id: "sl-count" }, ""),
      ),
    ),
    el("div", { class: "panel", style: "max-height: 62vh; overflow-y:auto" },
      el("table", { class: "data" },
        el("thead", {}, el("tr", {},
          el("th", {}, "id"),
          el("th", {}, "when"),
          el("th", { class: "num" }, "duration"),
          el("th", {}, "command"),
          el("th", {}, "client"),
        )),
        el("tbody", { id: "sl-rows" }),
      ),
      el("div", { id: "sl-empty" }),
    ),
  );

  async function refresh() {
    let out;
    try {
      out = await fetchJSON("/api/slowlog?count=256");
    } catch (e) {
      toast.error(e.message);
      return;
    }
    const rows = document.getElementById("sl-rows");
    const emptyHost = document.getElementById("sl-empty");
    if (!rows) return;
    if (document.activeElement !== thresholdIn) thresholdIn.value = out.threshold_us;
    if (document.activeElement !== maxLenIn) maxLenIn.value = out.max_len;
    document.getElementById("sl-count").textContent = out.entries.length + " entries";

    rows.innerHTML = "";
    emptyHost.innerHTML = "";
    if (!out.entries.length) {
      emptyHost.appendChild(el("div", { class: "empty" }, "nothing slow yet — lower the threshold to log more"));
      return;
    }
    for (const e of out.entries) {
      const cmd = e.args.map((a) => {
        const dec = decodeServerStr(a);
        return dec.binary ? "(bin)" : dec.text;
      }).join(" ");
      rows.appendChild(el("tr", {},
        el("td", { class: "muted" }, String(e.id)),
        el("td", {}, fmtTime(e.t)),
        el("td", { class: "num", style: "color: var(--warn)" }, fmtMicros(e.dur_us)),
        el("td", { style: "word-break:break-all" }, cmd),
        el("td", { class: "muted" }, e.client || e.addr),
      ));
    }
  }

  async function applyConfig() {
    try {
      await putJSON("/api/config/slowlog", {
        threshold_us: parseInt(thresholdIn.value, 10),
        max_len: parseInt(maxLenIn.value, 10),
      });
      toast("slowlog config applied");
      refresh();
    } catch (e) {
      toast.error(e.message);
    }
  }

  async function reset() {
    try {
      await del("/api/slowlog");
      toast("slowlog cleared");
      refresh();
    } catch (e) {
      toast.error(e.message);
    }
  }

  await refresh();
  timer = setInterval(refresh, 3000);
}

export function unmount() {
  clearInterval(timer);
  timer = null;
}
