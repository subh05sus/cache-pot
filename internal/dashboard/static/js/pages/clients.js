// Clients: live table of connections with a kill action.
import { fetchJSON, postJSON } from "../api.js";
import { el, fmtDuration } from "../fmt.js";
import { toast } from "../components/toast.js";
import { confirmModal } from "../components/modal.js";

let timer = null;

export async function mount(view) {
  view.append(
    el("div", { class: "page-head" },
      el("h1", {}, "clients"),
      el("span", { class: "hint", id: "cl-count" }, ""),
    ),
    el("div", { class: "panel" },
      el("table", { class: "data" },
        el("thead", {}, el("tr", {},
          el("th", {}, "id"),
          el("th", {}, "addr"),
          el("th", {}, "name"),
          el("th", {}, "kind"),
          el("th", { class: "num" }, "age"),
          el("th", { class: "num" }, "idle"),
          el("th", {}, "last cmd"),
          el("th", {}, "subs"),
          el("th", {}, ""),
        )),
        el("tbody", { id: "cl-rows" }),
      ),
      el("div", { id: "cl-empty" }),
    ),
  );

  async function refresh() {
    let out;
    try {
      out = await fetchJSON("/api/clients");
    } catch (e) {
      toast.error(e.message);
      return;
    }
    const rows = document.getElementById("cl-rows");
    if (!rows) return;
    document.getElementById("cl-count").textContent = out.clients.length + " connected";
    rows.innerHTML = "";
    document.getElementById("cl-empty").innerHTML = "";
    if (!out.clients.length) {
      document.getElementById("cl-empty").appendChild(
        el("div", { class: "empty" }, "no RESP clients connected — try redis-cli -p 6379"));
      return;
    }
    for (const c of out.clients) {
      rows.appendChild(el("tr", {},
        el("td", { class: "muted" }, String(c.id)),
        el("td", {}, c.addr),
        el("td", {}, c.name || "–"),
        el("td", {}, el("span", { class: "badge" }, c.kind), c.monitor ? el("span", { class: "badge live", style: "margin-left:4px" }, "monitor") : null),
        el("td", { class: "num" }, fmtDuration(c.age_secs)),
        el("td", { class: "num" }, fmtDuration(c.idle_secs)),
        el("td", { class: "muted" }, (c.last_cmd || "").toLowerCase()),
        el("td", { class: "num" }, String(c.subs)),
        el("td", {},
          el("button", {
            class: "danger", title: "close this connection",
            onclick: async () => {
              const ok = await confirmModal("kill connection", `Close connection #${c.id} (${c.addr})?`, "kill");
              if (!ok) return;
              try {
                const r = await postJSON("/api/clients/kill", { id: c.id });
                toast(r.killed ? "connection closed" : "already gone");
                refresh();
              } catch (e) {
                toast.error(e.message);
              }
            },
          }, "kill"),
        ),
      ));
    }
  }

  await refresh();
  timer = setInterval(refresh, 2000);
}

export function unmount() {
  clearInterval(timer);
  timer = null;
}
