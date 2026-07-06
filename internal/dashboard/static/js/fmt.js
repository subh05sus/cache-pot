// Formatting helpers shared by every page.

export function fmtBytes(b) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = Number(b) || 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return v.toFixed(i ? 1 : 0) + " " + units[i];
}

export function fmtNum(n) {
  n = Number(n) || 0;
  if (n >= 1e9) return (n / 1e9).toFixed(1) + "B";
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e4) return (n / 1e3).toFixed(1) + "k";
  return String(n);
}

export function fmtDuration(secs) {
  secs = Math.floor(Number(secs) || 0);
  const d = Math.floor(secs / 86400), h = Math.floor((secs % 86400) / 3600),
        m = Math.floor((secs % 3600) / 60), s = secs % 60;
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${s}s`;
  return `${s}s`;
}

// TTL in ms → human string; -1 = no expiry.
export function fmtTTL(ms) {
  if (ms === -1) return "∞";
  if (ms === -2) return "gone";
  if (ms < 1000) return ms + "ms";
  return fmtDuration(ms / 1000);
}

export function fmtMicros(us) {
  us = Number(us) || 0;
  if (us >= 1e6) return (us / 1e6).toFixed(2) + "s";
  if (us >= 1000) return (us / 1000).toFixed(2) + "ms";
  return us + "µs";
}

export function fmtTime(unixMs) {
  return new Date(unixMs).toLocaleTimeString(undefined, { hour12: false });
}

// hexPreview renders binary data as a spaced hex string, capped.
export function hexPreview(text, max = 64) {
  const bytes = [];
  for (let i = 0; i < Math.min(text.length, max); i++) {
    bytes.push(text.charCodeAt(i).toString(16).padStart(2, "0"));
  }
  let out = bytes.join(" ");
  if (text.length > max) out += ` … (${text.length} bytes)`;
  return out;
}

// el creates a DOM element with attrs and children. Text children go through
// createTextNode — the app-wide discipline that keeps hostile key names inert.
export function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") node.className = v;
    else if (k === "dataset") Object.assign(node.dataset, v);
    else if (k.startsWith("on") && typeof v === "function") node.addEventListener(k.slice(2), v);
    else if (v !== null && v !== undefined) node.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined) continue;
    node.appendChild(typeof c === "string" ? document.createTextNode(c) : c);
  }
  return node;
}
