// Key tree: groups the loaded key list by a delimiter into collapsible
// namespaces. Pure client-side view over whatever keys are loaded.
import { el } from "../fmt.js";

// buildTree returns a DOM node. keys: [{name, display, type, ttl}] where
// name is the raw key string. onSelect(name) fires on leaf click.
export function buildTree(keys, delimiter, onSelect, selectedName, renderLeaf) {
  const root = { children: new Map(), leaves: [] };
  for (const k of keys) {
    const parts = k.display.split(delimiter);
    let node = root;
    for (let i = 0; i < parts.length - 1; i++) {
      const seg = parts[i];
      if (!node.children.has(seg)) node.children.set(seg, { children: new Map(), leaves: [] });
      node = node.children.get(seg);
    }
    node.leaves.push(k);
  }

  const expanded = new Set();

  function countLeaves(node) {
    let n = node.leaves.length;
    for (const child of node.children.values()) n += countLeaves(child);
    return n;
  }

  function renderNode(node, path) {
    const ul = el("ul", {});
    const names = [...node.children.keys()].sort();
    for (const seg of names) {
      const child = node.children.get(seg);
      const childPath = path ? path + delimiter + seg : seg;
      const isOpen = expanded.has(childPath);
      const li = el("li", {});
      const caret = el("span", { class: "caret" }, isOpen ? "▾" : "▸");
      const label = el("span", { class: "node-label" },
        caret,
        seg + delimiter,
        el("span", { class: "count" }, "(" + countLeaves(child) + ")"),
      );
      label.addEventListener("click", () => {
        if (expanded.has(childPath)) expanded.delete(childPath);
        else expanded.add(childPath);
        rerender();
      });
      li.appendChild(label);
      if (isOpen) li.appendChild(renderNode(child, childPath));
      ul.appendChild(li);
    }
    const sortedLeaves = [...node.leaves].sort((a, b) => (a.display < b.display ? -1 : 1));
    for (const leaf of sortedLeaves) {
      const li = el("li", {});
      const row = renderLeaf(leaf);
      row.classList.add("leaf");
      if (leaf.name === selectedName) row.classList.add("selected");
      row.addEventListener("click", () => onSelect(leaf.name));
      li.appendChild(row);
      ul.appendChild(li);
    }
    return ul;
  }

  const host = el("div", { class: "tree" });
  function rerender() {
    host.innerHTML = "";
    host.appendChild(renderNode(root, ""));
  }
  rerender();
  return host;
}
