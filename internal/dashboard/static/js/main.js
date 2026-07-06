// Boot: register pages, start the router, wire global shortcuts.
import { register, startRouter, navigate } from "./router.js";
import * as overview from "./pages/overview.js";
import * as browser from "./pages/browser.js";
import * as workbench from "./pages/workbench.js";
import * as profiler from "./pages/profiler.js";
import * as slowlog from "./pages/slowlog.js";
import * as pubsub from "./pages/pubsub.js";
import * as analysis from "./pages/analysis.js";
import * as clients from "./pages/clients.js";

register("overview", overview);
register("browser", browser);
register("workbench", workbench);
register("profiler", profiler);
register("slowlog", slowlog);
register("pubsub", pubsub);
register("analysis", analysis);
register("clients", clients);

// g then a letter jumps between pages (vim-style), unless typing in a field.
const jumps = {
  o: "overview", b: "browser", w: "workbench", p: "profiler",
  s: "slowlog", u: "pubsub", a: "analysis", c: "clients",
};
let pendingG = false;
document.addEventListener("keydown", (e) => {
  const t = e.target;
  if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return;
  if (e.key === "g" && !e.ctrlKey && !e.metaKey && !e.altKey) {
    pendingG = true;
    setTimeout(() => { pendingG = false; }, 800);
    return;
  }
  if (pendingG && jumps[e.key]) {
    navigate(jumps[e.key]);
    pendingG = false;
  }
});

// Global connection indicator + version, independent of the current page.
async function heartbeat() {
  const dot = document.getElementById("conn-dot");
  const label = document.getElementById("conn-label");
  const version = document.getElementById("version");
  try {
    const res = await fetch("/api/stats");
    const s = await res.json();
    if (dot) dot.className = "status-dot live";
    if (label) label.textContent = "connected";
    if (version && s.version) version.textContent = "v" + s.version;
  } catch {
    if (dot) dot.className = "status-dot down";
    if (label) label.textContent = "unreachable";
  }
}
heartbeat();
setInterval(heartbeat, 5000);

startRouter();
