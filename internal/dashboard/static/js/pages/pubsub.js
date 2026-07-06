// Pub/Sub: subscribe to channels and glob patterns from the browser (one
// SSE stream) and publish messages back through the workbench pipeline.
import { sse, postJSON, decodeServerStr } from "../api.js";
import { el, fmtTime, hexPreview } from "../fmt.js";
import { toast } from "../components/toast.js";

const FEED_MAX = 500;

let closeStream = null;

export async function mount(view) {
  const chanIn = el("input", { type: "text", placeholder: "channels, comma separated", style: "flex:1; min-width:160px" });
  const patIn = el("input", { type: "text", placeholder: "patterns, e.g. news:*", style: "flex:1; min-width:160px" });
  const subBtn = el("button", { class: "primary", onclick: toggleSubscribe }, "subscribe");
  const feed = el("div", { class: "log-surface", style: "height: 46vh", id: "ps-feed" },
    el("div", { class: "faint" }, "subscribe to channels or patterns to see messages here"));

  const pubChan = el("input", { type: "text", placeholder: "channel", style: "width:180px" });
  const pubMsg = el("input", { type: "text", placeholder: "message", style: "flex:1" });

  view.append(
    el("div", { class: "page-head" },
      el("h1", {}, "pub/sub"),
      el("span", { class: "hint" }, "live message feed over one SSE stream"),
      el("span", { class: "spacer" }),
      el("span", { class: "badge", id: "ps-state" }, "idle"),
    ),
    el("div", { class: "panel", style: "margin-bottom:12px" },
      el("h2", {}, "subscriptions"),
      el("div", { class: "field-row" }, chanIn, patIn, subBtn),
    ),
    feed,
    el("div", { class: "panel", style: "margin-top:12px" },
      el("h2", {}, "publish"),
      el("div", { class: "field-row" },
        pubChan, pubMsg,
        el("button", {
          class: "primary",
          onclick: async () => {
            if (!pubChan.value) return toast.error("channel required");
            try {
              const cmd = ["PUBLISH", pubChan.value, pubMsg.value].map(quoteArg).join(" ");
              const out = await postJSON("/api/cli", { command: cmd });
              const n = out.reply && out.reply.value;
              toast(`delivered to ${n ?? 0} subscriber${n === 1 ? "" : "s"}`);
            } catch (e) {
              toast.error(e.message);
            }
          },
        }, "publish"),
      ),
    ),
  );

  let subscribed = false;

  function setState(label, live) {
    const b = document.getElementById("ps-state");
    if (!b) return;
    b.textContent = label;
    b.className = "badge" + (live ? " live" : "");
  }

  function toggleSubscribe() {
    if (subscribed) {
      stop();
      return;
    }
    const channels = chanIn.value.trim();
    const patterns = patIn.value.trim();
    if (!channels && !patterns) return toast.error("give at least one channel or pattern");
    const params = new URLSearchParams();
    if (channels) params.set("channels", channels);
    if (patterns) params.set("patterns", patterns);

    feed.innerHTML = "";
    closeStream = sse("/api/stream/pubsub?" + params, {
      msg: (m) => {
        const chan = decodeServerStr(m.channel);
        const payload = decodeServerStr(m.payload);
        const row = el("div", { style: "display:flex; gap:10px" },
          el("span", { class: "faint", style: "flex:none" }, fmtTime(m.t)),
          el("span", { class: "accent", style: "flex:none" }, chan.text),
          m.pattern ? el("span", { class: "muted", style: "flex:none" }, "(" + decodeServerStr(m.pattern).text + ")") : null,
          el("span", { style: "word-break:break-all" },
            payload.binary ? "0x" + hexPreview(payload.text, 32) : payload.text),
        );
        const atBottom = feed.scrollTop + feed.clientHeight >= feed.scrollHeight - 40;
        feed.appendChild(row);
        while (feed.childElementCount > FEED_MAX) feed.firstElementChild.remove();
        if (atBottom) feed.scrollTop = feed.scrollHeight;
      },
    }, (state) => setState(state === "open" ? "subscribed" : "reconnecting…", state === "open"));

    subscribed = true;
    subBtn.textContent = "unsubscribe";
    subBtn.classList.remove("primary");
    subBtn.classList.add("danger");
  }

  function stop() {
    if (closeStream) { closeStream(); closeStream = null; }
    subscribed = false;
    subBtn.textContent = "subscribe";
    subBtn.classList.add("primary");
    subBtn.classList.remove("danger");
    setState("idle", false);
  }
}

export function unmount() {
  if (closeStream) { closeStream(); closeStream = null; }
}

// quoteArg wraps a value in double quotes with workbench-tokenizer escaping.
function quoteArg(s) {
  return '"' + s.replaceAll("\\", "\\\\").replaceAll('"', '\\"') + '"';
}
