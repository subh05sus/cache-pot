// SVG sparkline: thin 2px line, soft area fill, autoscaled, with a dotted
// baseline and a crosshair tooltip on hover. One series per chart — identity
// comes from the panel title, so no legend.
import { el } from "../fmt.js";

const NS = "http://www.w3.org/2000/svg";
const W = 600, H = 96, PAD = 4;

export function sparkline({ color = "var(--accent-strong)", format = String } = {}) {
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
  svg.setAttribute("preserveAspectRatio", "none");

  const area = document.createElementNS(NS, "path");
  area.setAttribute("fill", color);
  area.setAttribute("opacity", "0.13");

  const line = document.createElementNS(NS, "path");
  line.setAttribute("fill", "none");
  line.setAttribute("stroke", color);
  line.setAttribute("stroke-width", "2");
  line.setAttribute("stroke-linejoin", "round");
  line.setAttribute("vector-effect", "non-scaling-stroke");

  const baseline = document.createElementNS(NS, "line");
  baseline.setAttribute("stroke", "var(--border)");
  baseline.setAttribute("stroke-dasharray", "3 4");
  baseline.setAttribute("x1", "0");
  baseline.setAttribute("x2", String(W));

  const cursor = document.createElementNS(NS, "line");
  cursor.setAttribute("stroke", "var(--faint)");
  cursor.setAttribute("y1", "0");
  cursor.setAttribute("y2", String(H));
  cursor.setAttribute("visibility", "hidden");

  const dot = document.createElementNS(NS, "circle");
  dot.setAttribute("r", "3.5");
  dot.setAttribute("fill", color);
  dot.setAttribute("stroke", "var(--bg-raised)");
  dot.setAttribute("stroke-width", "2");
  dot.setAttribute("visibility", "hidden");

  svg.append(baseline, area, line, cursor, dot);

  const tip = el("div", { class: "spark-tip" });
  tip.style.cssText =
    "position:absolute;pointer-events:none;background:var(--bg-inset);border:1px solid var(--border-strong);" +
    "border-radius:5px;padding:2px 8px;font-size:11px;color:var(--text-dim);visibility:hidden;white-space:nowrap;z-index:5;";

  const wrap = el("div", { style: "position:relative" });
  wrap.append(svg, tip);

  let data = [];
  let lo = 0, hi = 1;

  function xAt(i) {
    return data.length < 2 ? PAD : PAD + (i / (data.length - 1)) * (W - 2 * PAD);
  }
  function yAt(v) {
    return H - PAD - ((v - lo) / (hi - lo || 1)) * (H - 2 * PAD);
  }

  function render(values) {
    data = values;
    if (!data.length) {
      line.setAttribute("d", "");
      area.setAttribute("d", "");
      return;
    }
    lo = Math.min(...data);
    hi = Math.max(...data);
    if (lo === hi) { lo -= 1; hi += 1; }
    // pad the domain so the line does not kiss the frame
    const span = hi - lo;
    lo -= span * 0.08;
    hi += span * 0.08;

    let d = "";
    for (let i = 0; i < data.length; i++) {
      d += (i ? "L" : "M") + xAt(i).toFixed(1) + " " + yAt(data[i]).toFixed(1);
    }
    line.setAttribute("d", d);
    area.setAttribute("d", d + `L${xAt(data.length - 1).toFixed(1)} ${H - PAD}L${xAt(0).toFixed(1)} ${H - PAD}Z`);
    const zeroY = yAt(Math.max(lo, 0));
    baseline.setAttribute("y1", String(zeroY));
    baseline.setAttribute("y2", String(zeroY));
  }

  svg.addEventListener("mousemove", (e) => {
    if (data.length < 2) return;
    const rect = svg.getBoundingClientRect();
    const frac = (e.clientX - rect.left) / rect.width;
    const i = Math.max(0, Math.min(data.length - 1, Math.round(frac * (data.length - 1))));
    const x = xAt(i), y = yAt(data[i]);
    cursor.setAttribute("x1", String(x));
    cursor.setAttribute("x2", String(x));
    cursor.setAttribute("visibility", "visible");
    dot.setAttribute("cx", String(x));
    dot.setAttribute("cy", String(y));
    dot.setAttribute("visibility", "visible");
    tip.textContent = format(data[i]);
    tip.style.visibility = "visible";
    const px = (x / W) * rect.width;
    tip.style.left = Math.min(px + 8, rect.width - tip.offsetWidth - 4) + "px";
    tip.style.top = "4px";
  });
  svg.addEventListener("mouseleave", () => {
    cursor.setAttribute("visibility", "hidden");
    dot.setAttribute("visibility", "hidden");
    tip.style.visibility = "hidden";
  });

  return { node: wrap, render };
}
