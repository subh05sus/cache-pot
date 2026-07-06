// Fetch + SSE helpers, and the $b64 binary-string envelope decoder.

export async function fetchJSON(url, opts = {}) {
  const res = await fetch(url, opts);
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(body.error || `${res.status} ${res.statusText}`);
  }
  return body;
}

export function postJSON(url, data) {
  return fetchJSON(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
}

export function patchJSON(url, data) {
  return fetchJSON(url, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
}

export function putJSON(url, data) {
  return fetchJSON(url, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
}

export function del(url) {
  return fetchJSON(url, { method: "DELETE" });
}

// sse opens an EventSource and returns a close function. handlers maps event
// names to callbacks receiving parsed JSON. onstate (optional) is told
// "open" / "down" for the connection indicator.
export function sse(url, handlers, onstate) {
  const es = new EventSource(url);
  for (const [event, fn] of Object.entries(handlers)) {
    es.addEventListener(event, (e) => {
      let data;
      try { data = JSON.parse(e.data); } catch { return; }
      fn(data);
    });
  }
  es.onopen = () => onstate && onstate("open");
  es.onerror = () => onstate && onstate("down");
  return () => es.close();
}

// Server strings arrive either as plain strings or {"$b64", len} envelopes
// for binary data. str() decodes to a display object.
export function decodeServerStr(v) {
  if (typeof v === "string") return { text: v, binary: false };
  if (v && typeof v === "object" && "$b64" in v) {
    const raw = atob(v.$b64);
    return { text: raw, binary: true, len: v.len ?? raw.length };
  }
  return { text: String(v), binary: false };
}

// encodeClientStr wraps a JS string for sending: plain when it is clean
// text, $b64 when it contains control characters the server would flag.
export function encodeClientStr(s) {
  // eslint-disable-next-line no-control-regex
  if (/[\x00-\x08\x0b\x0c\x0e-\x1f]/.test(s)) {
    return { $b64: btoa(s), len: s.length };
  }
  return s;
}
