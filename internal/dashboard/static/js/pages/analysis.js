// Analysis: on-demand memory report — bytes by type, by key-prefix
// namespace, TTL distribution, and the largest keys. Bars are horizontal
// with direct labels; type bars wear the validated categorical palette,
// magnitude-only bars stay single-hue.
import { postJSON, decodeServerStr } from "../api.js";
import { el, fmtBytes, fmtNum, hexPreview } from "../fmt.js";
import { toast } from "../components/toast.js";

const TYPE_COLORS = {
  string: "var(--type-string)", hash: "var(--type-hash)", list: "var(--type-list)",
  set: "var(--type-set)", zset: "var(--type-zset)", vector: "var(--type-vector)",
};

export async function mount(view) {
  const delimIn = el("input", { type: "text", value: ":", style: "width:50px", maxlength: "3" });
  const topNIn = el("input", { type: "number", value: "25", style: "width:70px", min: "5", max: "100" });
  const runBtn = el("button", { class: "primary", onclick: run }, "▶ run analysis");
  const results = el("div", { id: "an-results" },
    el("div", { class: "empty" }, "run an analysis to profile keyspace memory"));

  view.append(
    el("div", { class: "page-head" },
      el("h1", {}, "analysis"),
      el("span", { class: "hint" }, "sizes are heuristics — reliable for ranking, approximate in absolute terms"),
    ),
    el("div", { class: "panel", style: "margin-bottom:12px" },
      el("div", { class: "field-row" },
        el("label", {}, "namespace delimiter"), delimIn,
        el("label", {}, "top keys"), topNIn,
        runBtn,
        el("span", { class: "muted", id: "an-meta" }, ""),
      ),
    ),
    results,
  );

  async function run() {
    runBtn.disabled = true;
    let rep;
    try {
      rep = await postJSON("/api/analysis", {
        delimiter: delimIn.value || ":",
        top_n: parseInt(topNIn.value, 10) || 25,
      });
    } catch (e) {
      toast.error(e.message);
      runBtn.disabled = false;
      return;
    }
    runBtn.disabled = false;
    document.getElementById("an-meta").textContent =
      `${fmtNum(rep.total_keys)} keys · ${fmtBytes(rep.total_bytes)} · ${rep.elapsed_ms}ms`;
    renderReport(rep);
  }

  function bars(entries, colorOf) {
    const max = Math.max(...entries.map((e) => e.bytes), 1);
    return el("div", {}, entries.map((e) =>
      el("div", { class: "bar-row" },
        el("span", { style: "overflow:hidden; text-overflow:ellipsis; white-space:nowrap", title: e.label }, e.label),
        el("div", { class: "bar-track" },
          el("div", {
            class: "bar-fill",
            style: `width:${(e.bytes / max * 100).toFixed(1)}%; background:${colorOf(e)}`,
          })),
        el("span", { class: "bar-num" }, fmtBytes(e.bytes) + " · " + fmtNum(e.keys)),
      )));
  }

  function renderReport(rep) {
    results.innerHTML = "";

    const typeEntries = Object.entries(rep.by_type || {})
      .map(([t, agg]) => ({ label: t, bytes: agg.bytes, keys: agg.keys, type: t }))
      .sort((a, b) => b.bytes - a.bytes);

    const prefixEntries = (rep.by_prefix || []).slice(0, 20)
      .map((p) => ({ label: p.prefix, bytes: p.bytes, keys: p.keys }));

    const ttl = rep.ttl || {};
    const ttlEntries = [
      { label: "no ttl", bytes: ttl.no_ttl },
      { label: "< 1 min", bytes: ttl.under_1m },
      { label: "< 10 min", bytes: ttl.under_10m },
      { label: "< 1 hour", bytes: ttl.under_1h },
      { label: "< 1 day", bytes: ttl.under_1d },
      { label: "≥ 1 day", bytes: ttl.over_1d },
    ];
    const ttlMax = Math.max(...ttlEntries.map((e) => e.bytes || 0), 1);

    results.append(
      el("div", { class: "charts", style: "margin-bottom:12px" },
        el("div", { class: "panel" },
          el("h2", {}, "memory by type"),
          typeEntries.length
            ? bars(typeEntries, (e) => TYPE_COLORS[e.type] || "var(--faint)")
            : el("div", { class: "empty" }, "empty keyspace"),
        ),
        el("div", { class: "panel" },
          el("h2", {}, "memory by namespace (" + (delimIn.value || ":") + ")"),
          prefixEntries.length
            ? bars(prefixEntries, () => "var(--accent-strong)")
            : el("div", { class: "empty" }, "empty keyspace"),
        ),
      ),
      el("div", { class: "charts", style: "margin-bottom:12px" },
        el("div", { class: "panel" },
          el("h2", {}, "keys by ttl (count)"),
          el("div", {}, ttlEntries.map((e) =>
            el("div", { class: "bar-row" },
              el("span", {}, e.label),
              el("div", { class: "bar-track" },
                el("div", {
                  class: "bar-fill",
                  style: `width:${((e.bytes || 0) / ttlMax * 100).toFixed(1)}%; background:var(--accent-strong)`,
                })),
              el("span", { class: "bar-num" }, fmtNum(e.bytes || 0)),
            ))),
        ),
        el("div", { class: "panel", style: "max-height:420px; overflow-y:auto" },
          el("h2", {}, "largest keys"),
          el("table", { class: "data" },
            el("thead", {}, el("tr", {},
              el("th", {}, "key"), el("th", {}, "type"), el("th", { class: "num" }, "size"))),
            el("tbody", {}, (rep.top_keys || []).map((k) => {
              const dec = decodeServerStr(k.key);
              return el("tr", {},
                el("td", { style: "word-break:break-all" },
                  dec.binary ? el("span", {}, el("span", { class: "badge bin" }, "bin"), " " + hexPreview(dec.text, 16)) : dec.text),
                el("td", {}, el("span", { class: "badge type-badge type-" + k.type }, k.type)),
                el("td", { class: "num" }, fmtBytes(k.bytes)),
              );
            })),
          ),
        ),
      ),
    );
  }

  // auto-run once on mount so the page is never blank
  await run();
}

export function unmount() {}
