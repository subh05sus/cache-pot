// Workbench: an in-browser CLI. Commands run in-process on the server via
// /api/cli; replies render with full RESP typing. History persists in
// localStorage; a help strip suggests syntax as you type.
import { postJSON, decodeServerStr } from "../api.js";
import { el, fmtMicros, hexPreview } from "../fmt.js";
import { COMMANDS } from "../commands.js";

const HISTORY_KEY = "cachepot.cli.history";
const HISTORY_MAX = 200;

let cleanup = null;

export async function mount(view) {
  const scrollback = el("div", { class: "log-surface", style: "height: 62vh", id: "wb-scroll" },
    el("div", { class: "faint" },
      "cache-pot workbench — type a command, ↑/↓ for history, ctrl+L to clear. ",
      "MONITOR and SUBSCRIBE live on their own pages."),
  );
  const input = el("input", {
    type: "text", style: "flex:1", placeholder: "SET greeting \"hello\"",
    autocomplete: "off", spellcheck: "false",
  });
  const helpStrip = el("div", { class: "faint", style: "min-height: 20px; font-size: 11px; padding: 2px 4px" });

  view.append(
    el("div", { class: "page-head" },
      el("h1", {}, "workbench"),
      el("span", { class: "hint" }, "commands execute in-process — they appear in the profiler and AOF"),
    ),
    scrollback,
    helpStrip,
    el("div", { class: "field-row", style: "margin-top:4px" },
      el("span", { class: "accent", style: "font-weight:600" }, "❯"),
      input,
      el("button", { class: "primary", onclick: run }, "run"),
    ),
  );

  let history = [];
  try { history = JSON.parse(localStorage.getItem(HISTORY_KEY)) || []; } catch { /* fresh */ }
  let histPos = history.length;
  let draft = "";

  function saveHistory(cmd) {
    if (history[history.length - 1] !== cmd) {
      history.push(cmd);
      if (history.length > HISTORY_MAX) history = history.slice(-HISTORY_MAX);
      localStorage.setItem(HISTORY_KEY, JSON.stringify(history));
    }
    histPos = history.length;
  }

  function updateHelp() {
    const word = (input.value.trim().split(/\s+/)[0] || "").toUpperCase();
    if (!word) { helpStrip.textContent = ""; return; }
    const exact = COMMANDS[word];
    if (exact) {
      helpStrip.textContent = exact[0] + " — " + exact[1];
      return;
    }
    const matches = Object.keys(COMMANDS).filter((c) => c.startsWith(word)).slice(0, 6);
    helpStrip.textContent = matches.length ? "… " + matches.join("  ") : "";
  }

  async function run() {
    const cmd = input.value.trim();
    if (!cmd) return;
    input.value = "";
    updateHelp();
    saveHistory(cmd);

    const echo = el("div", { style: "margin-top:8px" },
      el("span", { class: "accent" }, "❯ "),
      el("span", { class: "resp-bulk" }, cmd));
    scrollback.appendChild(echo);

    try {
      const out = await postJSON("/api/cli", { command: cmd });
      scrollback.appendChild(el("div", { style: "display:flex; gap:10px; align-items:baseline" },
        renderReply(out.reply),
        el("span", { class: "faint", style: "font-size:11px; flex:none" }, fmtMicros(out.elapsed_us)),
      ));
    } catch (e) {
      scrollback.appendChild(el("div", { class: "resp-error" }, "(dashboard) " + e.message));
    }
    scrollback.scrollTop = scrollback.scrollHeight;
    input.focus();
  }

  input.addEventListener("input", updateHelp);
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { run(); return; }
    if (e.key === "ArrowUp") {
      if (histPos === history.length) draft = input.value;
      if (histPos > 0) { histPos--; input.value = history[histPos]; }
      e.preventDefault();
    } else if (e.key === "ArrowDown") {
      if (histPos < history.length) {
        histPos++;
        input.value = histPos === history.length ? draft : history[histPos];
      }
      e.preventDefault();
    } else if (e.key === "l" && e.ctrlKey) {
      scrollback.innerHTML = "";
      e.preventDefault();
    }
  });

  setTimeout(() => input.focus(), 0);
  cleanup = () => {};
}

export function unmount() {
  if (cleanup) { cleanup(); cleanup = null; }
}

// renderReply turns the typed reply JSON into DOM, preserving RESP kinds.
function renderReply(reply, depth = 0) {
  switch (reply.type) {
    case "simple":
      return el("span", { class: "resp-simple" }, strOf(reply.value));
    case "error":
      return el("span", { class: "resp-error" }, "(error) " + reply.value);
    case "int":
      return el("span", { class: "resp-int" }, "(integer) " + reply.value);
    case "null":
      return el("span", { class: "resp-null" }, "(nil)");
    case "bulk": {
      const dec = decodeServerStr(reply.value);
      if (dec.binary) {
        return el("span", { class: "resp-bulk" },
          el("span", { class: "badge bin" }, "bin"), " " + hexPreview(dec.text, 48));
      }
      return el("span", { class: "resp-bulk" }, JSON.stringify(dec.text));
    }
    case "array": {
      if (!reply.value.length) return el("span", { class: "resp-null" }, "(empty array)");
      const box = el("div", depth > 0 ? { class: "resp-nest" } : {});
      reply.value.forEach((item, i) => {
        box.appendChild(el("div", {},
          el("span", { class: "resp-idx" }, `${i + 1}) `),
          renderReply(item, depth + 1),
        ));
      });
      return box;
    }
    default:
      return el("span", { class: "resp-null" }, "(?)");
  }
}

function strOf(v) {
  return typeof v === "string" ? v : decodeServerStr(v).text;
}
