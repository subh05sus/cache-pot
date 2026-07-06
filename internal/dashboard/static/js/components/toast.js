// Toast notifications: toast("saved"), toast.error("boom").
import { el } from "../fmt.js";

function show(msg, cls) {
  const host = document.getElementById("toasts");
  const t = el("div", { class: "toast" + (cls ? " " + cls : "") }, msg);
  host.appendChild(t);
  setTimeout(() => t.remove(), 3800);
}

export function toast(msg) { show(msg, ""); }
toast.error = (msg) => show(msg, "error");
