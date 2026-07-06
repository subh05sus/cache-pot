// Modal dialogs. openModal returns a close function; confirmModal resolves
// true/false.
import { el } from "../fmt.js";

export function openModal(title, body, actions = []) {
  const backdrop = el("div", { class: "modal-backdrop" });
  const close = () => backdrop.remove();
  const actionRow = el("div", { class: "modal-actions" },
    ...actions.map(({ label, cls, onclick, autofocus }) => {
      const b = el("button", { class: cls || "" }, label);
      b.addEventListener("click", () => onclick(close));
      if (autofocus) setTimeout(() => b.focus(), 0);
      return b;
    }),
  );
  const box = el("div", { class: "modal", role: "dialog", "aria-label": title },
    el("h2", {}, title), body, actionRow);
  backdrop.appendChild(box);
  backdrop.addEventListener("mousedown", (e) => { if (e.target === backdrop) close(); });
  document.addEventListener("keydown", function esc(e) {
    if (e.key === "Escape") { close(); document.removeEventListener("keydown", esc); }
  });
  document.body.appendChild(backdrop);
  return close;
}

export function confirmModal(title, message, confirmLabel = "confirm") {
  return new Promise((resolve) => {
    openModal(title, el("p", { class: "muted" }, message), [
      { label: "cancel", cls: "ghost", onclick: (close) => { close(); resolve(false); } },
      { label: confirmLabel, cls: "danger", autofocus: true, onclick: (close) => { close(); resolve(true); } },
    ]);
  });
}
