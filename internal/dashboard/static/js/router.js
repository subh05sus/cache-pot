// Hash router. Pages are modules exporting { mount(el, ctx), unmount() }.
// unmount MUST close SSE streams and timers — navigating away is the only
// thing that stops a page's live feeds.

const routes = {};
let current = null;
let currentName = "";

export function register(name, page) {
  routes[name] = page;
}

export function currentPage() { return currentName; }

export function navigate(name) {
  location.hash = "#/" + name;
}

async function apply() {
  const name = (location.hash.replace(/^#\//, "") || "overview").split("?")[0];
  const page = routes[name] || routes.overview;
  const resolved = routes[name] ? name : "overview";
  if (current && current.unmount) {
    try { current.unmount(); } catch { /* page cleanup must never block nav */ }
  }
  const view = document.getElementById("view");
  view.innerHTML = "";
  view.scrollTop = 0;
  current = page;
  currentName = resolved;
  for (const a of document.querySelectorAll("#nav a")) {
    a.classList.toggle("active", a.dataset.page === resolved);
  }
  await page.mount(view);
}

export function startRouter() {
  window.addEventListener("hashchange", apply);
  apply();
}
