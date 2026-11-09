import { announce, renderInto } from '../../announce.js';

const lines = root => new Set([...root.querySelectorAll('[data-announce]')].map(el => el.textContent.trim()).filter(Boolean));

export function renderKeeping(root, html, quiet, keep) {
  const before = lines(root);
  const twins = keep.length ? keepInPlace(root, html, keep) : null;
  if (!twins) { renderInto(root, html, quiet); return null; }
  if (!quiet) announce([...lines(root)].filter(text => !before.has(text)).join(' '));
  return twins;
}

function keepInPlace(root, html, keep) {
  const tpl = document.createElement('template');
  tpl.innerHTML = html;
  const twins = new Map(), pair = new Map(), back = new Map();
  for (const node of keep) {
    const twin = node && node.id && root.contains(node) ? tpl.content.getElementById(node.id) : null;
    if (!twin || twin.nodeName !== node.nodeName) return null;
    twins.set(node, twin);
    let o = node, n = twin;
    for (; o !== root; o = o.parentNode, n = n.parentNode) {
      if (!n || n === tpl.content || n.nodeName !== o.nodeName) return null;
      if ((pair.has(n) && pair.get(n) !== o) || (back.has(o) && back.get(o) !== n)) return null;
      pair.set(n, o); back.set(o, n);
    }
    if (n !== tpl.content) return null;
  }
  const kept = [...twins.keys()];
  for (let i = 1; i < kept.length; i++) {
    const after = (a, b) => Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
    if (after(kept[i - 1], kept[i]) !== after(twins.get(kept[i - 1]), twins.get(kept[i]))) return null;
  }
  morph(root, tpl.content, pair, new Set(back.keys()), twins);
  return twins;
}

function morph(old, fresh, pair, stays, twins) {
  for (const child of [...old.childNodes]) if (!stays.has(child)) child.remove();
  let ref = old.firstChild;
  for (const n of [...fresh.childNodes]) {
    const o = pair.get(n);
    if (!o) { old.insertBefore(n, ref); continue; }
    ref = o.nextSibling;
    takeAttributes(o, n);
    if (!twins.has(o)) morph(o, n, pair, stays, twins);
    else if (differs(o, n)) o.innerHTML = n.innerHTML;
  }
}

const choices = menu => [...menu.options].map(o => o.value).join('\0');
function differs(o, n) {
  if (o.nodeName === 'DATALIST') return o.innerHTML !== n.innerHTML;
  return o.nodeName === 'SELECT' && choices(o) !== choices(n);
}

function takeAttributes(o, n) {
  const own = name => name === 'value' || name === 'checked';
  for (const a of [...o.attributes]) if (!own(a.name) && !n.hasAttribute(a.name)) o.removeAttribute(a.name);
  for (const a of [...n.attributes]) if (!own(a.name) && o.getAttribute(a.name) !== a.value) o.setAttribute(a.name, a.value);
}
