// Browser: cursor-paginated key list (flat or tree view) with pattern +
// type filtering on the left; key detail panel with per-type editors, TTL,
// rename, and delete on the right. All writes go through the server's
// Execute pipeline, so they show up in the profiler and the AOF.
import { fetchJSON, postJSON, patchJSON, del, decodeServerStr, encodeClientStr } from "../api.js";
import { el, fmtBytes, fmtTTL, hexPreview } from "../fmt.js";
import { toast } from "../components/toast.js";
import { openModal, confirmModal } from "../components/modal.js";
import { buildTree } from "../components/tree.js";

const TYPES = ["string", "hash", "list", "set", "zset", "vector"];
const PAGE = 200;

let state = null;

export async function mount(view) {
  state = {
    match: "*",
    typeFilter: "",
    treeView: false,
    keys: [],          // {name, display, binary, type, ttl}
    cursor: "",
    done: false,
    selected: null,    // raw key name
    valueStart: 0,
    previewBytes: 16384,
    debounce: null,
  };

  const searchBox = el("input", {
    type: "text", value: "*", placeholder: "match pattern, e.g. user:*",
    style: "flex:1; min-width:160px",
    oninput: () => {
      clearTimeout(state.debounce);
      state.debounce = setTimeout(() => {
        state.match = searchBox.value || "*";
        reloadKeys();
      }, 250);
    },
  });

  const chips = el("div", { class: "chips" },
    ...TYPES.map((t) => {
      const c = el("button", { class: "chip", dataset: { type: t } }, t);
      c.addEventListener("click", () => {
        state.typeFilter = state.typeFilter === t ? "" : t;
        for (const other of chips.querySelectorAll(".chip")) {
          other.classList.toggle("on", other.dataset.type === state.typeFilter);
        }
        reloadKeys();
      });
      return c;
    }),
  );

  const viewToggle = el("button", { class: "ghost", title: "toggle tree view" }, "⌥ tree");
  viewToggle.addEventListener("click", () => {
    state.treeView = !state.treeView;
    viewToggle.textContent = state.treeView ? "≡ flat" : "⌥ tree";
    renderKeyList();
  });

  view.append(
    el("div", { class: "page-head" },
      el("h1", {}, "browser"),
      el("span", { class: "hint", id: "key-count" }, ""),
      el("span", { class: "spacer" }),
      el("button", { class: "primary", onclick: () => addKeyModal() }, "+ new key"),
    ),
    el("div", { class: "split" },
      el("div", { class: "panel" },
        el("div", { class: "field-row", style: "margin-bottom:10px" }, searchBox, viewToggle),
        el("div", { style: "margin-bottom:10px" }, chips),
        el("div", { id: "key-list", style: "max-height: 60vh; overflow-y: auto" }),
        el("div", { style: "margin-top:10px; display:flex; gap:8px" },
          el("button", { id: "load-more", onclick: () => loadPage() }, "load more"),
          el("button", { class: "ghost", onclick: () => reloadKeys() }, "↻ refresh"),
        ),
      ),
      el("div", { class: "panel", id: "detail" },
        el("div", { class: "empty" }, "select a key"),
      ),
    ),
  );

  await reloadKeys();
}

export function unmount() {
  if (state) clearTimeout(state.debounce);
  state = null;
}

/* ---- key list -------------------------------------------------------- */

async function reloadKeys() {
  state.keys = [];
  state.cursor = "";
  state.done = false;
  await loadPage();
}

async function loadPage() {
  if (state.done) return;
  const params = new URLSearchParams({ match: state.match, count: String(PAGE) });
  if (state.typeFilter) params.set("type", state.typeFilter);
  if (state.cursor) params.set("cursor", state.cursor);
  let out;
  try {
    out = await fetchJSON("/api/keys?" + params);
  } catch (e) {
    toast.error("list keys: " + e.message);
    return;
  }
  if (!state) return; // page unmounted while awaiting
  for (const k of out.keys) {
    const dec = decodeServerStr(k.key);
    state.keys.push({
      name: dec.text, display: dec.binary ? "(binary) " + hexPreview(dec.text, 12) : dec.text,
      binary: dec.binary, type: k.type, ttl: k.ttl_ms,
    });
  }
  state.cursor = out.cursor;
  state.done = out.done;
  renderKeyList();
}

function typeBadge(t) {
  return el("span", { class: "badge type-badge type-" + t }, t);
}

function keyRow(k) {
  const row = el("div", { class: "key-row" },
    typeBadge(k.type),
    el("span", { class: "key-name", title: k.display }, k.display),
    k.binary ? el("span", { class: "badge bin" }, "bin") : null,
    el("span", { class: "ttl" }, k.ttl === -1 ? "" : fmtTTL(k.ttl)),
  );
  return row;
}

function renderKeyList() {
  const host = document.getElementById("key-list");
  if (!host) return;
  host.innerHTML = "";
  document.getElementById("key-count").textContent =
    state.keys.length + (state.done ? " keys" : "+ keys");
  document.getElementById("load-more").disabled = state.done;

  if (!state.keys.length) {
    host.appendChild(el("div", { class: "empty" }, "no keys match"));
    return;
  }
  if (state.treeView) {
    host.appendChild(buildTree(state.keys, ":", selectKey, state.selected, keyRow));
    return;
  }
  const sorted = [...state.keys].sort((a, b) => (a.display < b.display ? -1 : 1));
  for (const k of sorted) {
    const row = keyRow(k);
    if (k.name === state.selected) row.classList.add("selected");
    row.addEventListener("click", () => selectKey(k.name));
    host.appendChild(row);
  }
}

/* ---- detail panel ----------------------------------------------------- */

async function selectKey(name) {
  state.selected = name;
  state.valueStart = 0;
  state.previewBytes = 16384;
  renderKeyList();
  await renderDetail();
}

async function renderDetail() {
  const host = document.getElementById("detail");
  if (!host || !state.selected) return;
  let d;
  try {
    d = await fetchJSON("/api/key?" + new URLSearchParams({
      key: state.selected,
      start: String(state.valueStart),
      count: "100",
      preview: String(state.previewBytes),
    }));
  } catch (e) {
    host.innerHTML = "";
    host.appendChild(el("div", { class: "empty" }, "key vanished: " + e.message));
    return;
  }
  if (!state) return;

  const keyDec = decodeServerStr(d.key);
  host.innerHTML = "";
  host.append(
    el("div", { style: "display:flex; align-items:center; gap:10px; flex-wrap:wrap; margin-bottom:12px" },
      typeBadge(d.type),
      el("strong", { style: "word-break:break-all" }, keyDec.binary ? hexPreview(keyDec.text, 24) : keyDec.text),
      keyDec.binary ? el("span", { class: "badge bin" }, "binary name") : null,
      el("span", { class: "spacer", style: "flex:1" }),
      el("button", { class: "ghost", onclick: () => renameModal() }, "rename"),
      el("button", { class: "danger", onclick: () => deleteKey() }, "delete"),
    ),
    el("div", { class: "field-row", style: "margin-bottom:14px" },
      el("span", { class: "badge" }, fmtBytes(d.size_bytes)),
      el("span", { class: "badge", title: "time to live" }, "ttl " + fmtTTL(d.ttl_ms)),
      ttlEditor(d),
    ),
    valueEditor(d),
  );
}

function ttlEditor(d) {
  const secs = el("input", { type: "number", placeholder: "secs", style: "width:90px", min: "1" });
  return el("span", { class: "field-row" },
    secs,
    el("button", {
      onclick: async () => {
        const n = parseInt(secs.value, 10);
        if (!n || n <= 0) return toast.error("enter TTL seconds");
        await patch({ op: "set_ttl", ttl_ms: n * 1000 }, "TTL set");
      },
    }, "set ttl"),
    d.ttl_ms > 0 ? el("button", { class: "ghost", onclick: () => patch({ op: "persist" }, "TTL removed") }, "persist") : null,
  );
}

async function patch(fields, okMsg) {
  try {
    await patchJSON("/api/key", { key: encodeClientStr(state.selected), ...fields });
    if (okMsg) toast(okMsg);
    if (fields.op === "rename") {
      const nk = fields.newkey;
      state.selected = typeof nk === "string" ? nk : decodeServerStr(nk).text;
    }
    await reloadKeys();
    await renderDetail();
  } catch (e) {
    toast.error(e.message);
  }
}

function renameModal() {
  const input = el("input", { type: "text", value: state.selected, style: "width:100%" });
  openModal("rename key", el("div", {}, input), [
    { label: "cancel", cls: "ghost", onclick: (close) => close() },
    {
      label: "rename", cls: "primary", onclick: async (close) => {
        if (!input.value || input.value === state.selected) return close();
        close();
        await patch({ op: "rename", newkey: encodeClientStr(input.value) }, "renamed");
      },
    },
  ]);
  setTimeout(() => input.select(), 0);
}

async function deleteKey() {
  const ok = await confirmModal("delete key", `Delete "${state.selected}"? This cannot be undone.`, "delete");
  if (!ok) return;
  try {
    await del("/api/key?" + new URLSearchParams({ key: state.selected }));
    toast("deleted");
    state.selected = null;
    const detail = document.getElementById("detail");
    detail.innerHTML = "";
    detail.appendChild(el("div", { class: "empty" }, "select a key"));
    await reloadKeys();
  } catch (e) {
    toast.error(e.message);
  }
}

/* ---- per-type value editors ------------------------------------------- */

function pager(total, shown) {
  if (total <= 100 && state.valueStart === 0) return null;
  return el("div", { class: "field-row", style: "margin-top:8px" },
    el("button", {
      class: "ghost", disabled: state.valueStart === 0 ? "" : null,
      onclick: () => { state.valueStart = Math.max(0, state.valueStart - 100); renderDetail(); },
    }, "‹ prev"),
    el("span", { class: "muted" }, `${state.valueStart}–${state.valueStart + shown} of ${total}`),
    el("button", {
      class: "ghost", disabled: state.valueStart + 100 >= total ? "" : null,
      onclick: () => { state.valueStart += 100; renderDetail(); },
    }, "next ›"),
  );
}

function valueEditor(d) {
  const v = d.value;
  switch (d.type) {
    case "string": return stringEditor(v);
    case "hash": return hashEditor(v);
    case "list": return listEditor(v);
    case "set": return setEditor(v);
    case "zset": return zsetEditor(v);
    case "vector":
      return el("div", { class: "muted" },
        `vector collection · ${v.count} vectors · dim ${v.dim} — inspect via workbench (VSEARCH)`);
    default: return el("div", {});
  }
}

function stringEditor(v) {
  const dec = decodeServerStr(v.data);
  if (dec.binary) {
    return el("div", {},
      el("div", { class: "field-row", style: "margin-bottom:8px" },
        el("span", { class: "badge bin" }, "binary value"),
        el("span", { class: "muted" }, `${v.total_len} bytes — editing disabled`)),
      el("pre", { class: "log-surface", style: "max-height:220px; white-space:pre-wrap; word-break:break-all" },
        hexPreview(dec.text, 512)),
    );
  }
  const ta = el("textarea", { rows: "8" });
  ta.value = dec.text;
  const parts = [];
  if (v.truncated) {
    parts.push(el("div", { class: "field-row", style: "margin-bottom:8px" },
      el("span", { class: "badge drop" }, "truncated"),
      el("span", { class: "muted" }, `showing ${dec.text.length} of ${v.total_len} bytes`),
      state.previewBytes < 4 * 1024 * 1024
        ? el("button", { class: "ghost", onclick: () => { state.previewBytes = Math.min(state.previewBytes * 16, 4 * 1024 * 1024); renderDetail(); } }, "load more")
        : el("span", { class: "muted" }, "too large to display fully — use GETRANGE in the workbench"),
    ));
  }
  parts.push(ta);
  parts.push(el("div", { class: "modal-actions" },
    el("button", {
      class: "primary",
      disabled: v.truncated ? "" : null,
      title: v.truncated ? "cannot save a truncated view" : "",
      onclick: () => patch({ op: "set_string", value: encodeClientStr(ta.value) }, "saved"),
    }, "save value"),
  ));
  return el("div", {}, parts);
}

function kvTable(headers, rows) {
  return el("table", { class: "data" },
    el("thead", {}, el("tr", {}, ...headers.map((h) => el("th", h.num ? { class: "num" } : {}, h.label ?? h)))),
    el("tbody", {}, ...rows),
  );
}

function cellText(serverStr) {
  const dec = decodeServerStr(serverStr);
  if (dec.binary) {
    return el("span", {}, el("span", { class: "badge bin" }, "bin"), " " + hexPreview(dec.text, 16));
  }
  return el("span", { style: "word-break:break-all" }, dec.text);
}

function hashEditor(v) {
  const rows = v.fields.map(([f, val]) => {
    const fDec = decodeServerStr(f);
    return el("tr", {},
      el("td", {}, cellText(f)),
      el("td", {}, cellText(val)),
      el("td", { style: "width:1%" },
        el("button", { class: "ghost", title: "delete field", onclick: () => patch({ op: "hdel", field: encodeClientStr(fDec.text) }, "field removed") }, "✕")),
    );
  });
  const fIn = el("input", { type: "text", placeholder: "field" });
  const vIn = el("input", { type: "text", placeholder: "value", style: "flex:1" });
  return el("div", {},
    kvTable(["field", "value", ""], rows),
    pager(v.total, v.fields.length),
    el("div", { class: "field-row", style: "margin-top:10px" },
      fIn, vIn,
      el("button", { onclick: () => fIn.value && patch({ op: "hset", field: encodeClientStr(fIn.value), value: encodeClientStr(vIn.value) }, "field set") }, "hset"),
    ),
  );
}

function listEditor(v) {
  const rows = v.items.map((item, i) =>
    el("tr", {},
      el("td", { class: "num muted", style: "width:1%" }, String(state.valueStart + i)),
      el("td", {}, cellText(item)),
    ));
  const input = el("input", { type: "text", placeholder: "value", style: "flex:1" });
  return el("div", {},
    kvTable([{ label: "#", num: true }, "value"], rows),
    pager(v.total, v.items.length),
    el("div", { class: "field-row", style: "margin-top:10px" },
      input,
      el("button", { onclick: () => patch({ op: "lpush", value: encodeClientStr(input.value) }, "pushed") }, "lpush"),
      el("button", { onclick: () => patch({ op: "rpush", value: encodeClientStr(input.value) }, "pushed") }, "rpush"),
      el("button", { class: "ghost", onclick: () => patch({ op: "lpop" }, "popped") }, "lpop"),
      el("button", { class: "ghost", onclick: () => patch({ op: "rpop" }, "popped") }, "rpop"),
    ),
  );
}

function setEditor(v) {
  const rows = v.members.map((m) => {
    const dec = decodeServerStr(m);
    return el("tr", {},
      el("td", {}, cellText(m)),
      el("td", { style: "width:1%" },
        el("button", { class: "ghost", title: "remove member", onclick: () => patch({ op: "srem", member: encodeClientStr(dec.text) }, "member removed") }, "✕")),
    );
  });
  const input = el("input", { type: "text", placeholder: "member", style: "flex:1" });
  return el("div", {},
    kvTable(["member", ""], rows),
    pager(v.total, v.members.length),
    el("div", { class: "field-row", style: "margin-top:10px" },
      input,
      el("button", { onclick: () => input.value && patch({ op: "sadd", member: encodeClientStr(input.value) }, "member added") }, "sadd"),
    ),
  );
}

function zsetEditor(v) {
  const rows = v.members.map(([m, score]) => {
    const dec = decodeServerStr(m);
    return el("tr", {},
      el("td", {}, cellText(m)),
      el("td", { class: "num" }, String(score)),
      el("td", { style: "width:1%" },
        el("button", { class: "ghost", title: "remove member", onclick: () => patch({ op: "zrem", member: encodeClientStr(dec.text) }, "member removed") }, "✕")),
    );
  });
  const mIn = el("input", { type: "text", placeholder: "member", style: "flex:1" });
  const sIn = el("input", { type: "number", placeholder: "score", style: "width:110px", step: "any" });
  return el("div", {},
    kvTable(["member", { label: "score", num: true }, ""], rows),
    pager(v.total, v.members.length),
    el("div", { class: "field-row", style: "margin-top:10px" },
      mIn, sIn,
      el("button", {
        onclick: () => mIn.value && patch({ op: "zadd", member: encodeClientStr(mIn.value), score: parseFloat(sIn.value) || 0 }, "member set"),
      }, "zadd"),
    ),
  );
}

/* ---- add key modal ----------------------------------------------------- */

function addKeyModal() {
  const keyIn = el("input", { type: "text", placeholder: "key name", style: "width:100%" });
  const typeSel = el("select", {},
    ...["string", "hash", "list", "set", "zset"].map((t) => el("option", { value: t }, t)));
  const ttlIn = el("input", { type: "number", placeholder: "ttl seconds (optional)", style: "width:100%", min: "1" });
  const valueHost = el("div", { style: "margin-top:10px" });

  let getValue = () => null;

  function renderValueInput() {
    valueHost.innerHTML = "";
    const t = typeSel.value;
    if (t === "string") {
      const ta = el("textarea", { rows: "4", placeholder: "value" });
      valueHost.appendChild(ta);
      getValue = () => encodeClientStr(ta.value);
    } else if (t === "hash") {
      const ta = el("textarea", { rows: "4", placeholder: "one field=value per line" });
      valueHost.appendChild(ta);
      getValue = () => ta.value.split("\n").filter(Boolean).map((line) => {
        const i = line.indexOf("=");
        return i < 0 ? [line, ""] : [line.slice(0, i), line.slice(i + 1)];
      });
    } else if (t === "zset") {
      const ta = el("textarea", { rows: "4", placeholder: "one score member per line, e.g. 1.5 alice" });
      valueHost.appendChild(ta);
      getValue = () => ta.value.split("\n").filter(Boolean).map((line) => {
        const i = line.indexOf(" ");
        return [line.slice(i + 1), parseFloat(line.slice(0, i)) || 0];
      });
    } else {
      const ta = el("textarea", { rows: "4", placeholder: "one element per line" });
      valueHost.appendChild(ta);
      getValue = () => ta.value.split("\n").filter(Boolean);
    }
  }
  typeSel.addEventListener("change", renderValueInput);
  renderValueInput();

  openModal("new key",
    el("div", {},
      el("div", { class: "field-row", style: "margin-bottom:10px" }, keyIn),
      el("div", { class: "field-row", style: "margin-bottom:10px" },
        el("label", {}, "type"), typeSel,
        el("label", {}, "ttl"), ttlIn),
      valueHost,
    ),
    [
      { label: "cancel", cls: "ghost", onclick: (close) => close() },
      {
        label: "create", cls: "primary", onclick: async (close) => {
          if (!keyIn.value) return toast.error("key name required");
          const body = {
            key: encodeClientStr(keyIn.value),
            type: typeSel.value,
            value: getValue(),
          };
          const secs = parseInt(ttlIn.value, 10);
          if (secs > 0) body.ttl_ms = secs * 1000;
          try {
            await postJSON("/api/key", body);
            toast("created " + keyIn.value);
            close();
            await reloadKeys();
            await selectKey(keyIn.value);
          } catch (e) {
            toast.error(e.message);
          }
        },
      },
    ]);
  setTimeout(() => keyIn.focus(), 0);
}
