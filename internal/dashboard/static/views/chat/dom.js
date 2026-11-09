export function setText(el, text) { if (el && el.textContent !== text) el.textContent = text; }
export function setData(el, key, value) { if (el.dataset[key] !== value) el.dataset[key] = value; }
export function syncChildren(parent, nodes) {
  const keep = new Set(nodes);
  let next = parent.firstChild;
  for (const node of nodes) {
    while (next && next !== node && !keep.has(next)) next = next.nextSibling;
    if (node === next) next = next.nextSibling;
    else parent.insertBefore(node, next);
  }
  for (const node of Array.from(parent.childNodes)) if (!keep.has(node)) node.remove();
}
