const typing = el => !!el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName));
const THEN = 'cpas', WITHIN_MS = 1500;

export function hearPageKeys(api) {
  let pendingG = 0;
  const press = (e, key) => { if (key !== 'g') e.preventDefault(); api.act('page.key', { key, ctrl: e.ctrlKey, meta: e.metaKey }).catch(() => {}); };
  document.addEventListener('keydown', e => {
    if (e.altKey) return;
    const k = e.key.toLowerCase();
    if (e.ctrlKey || e.metaKey) { if (k === 'k') press(e, k); return; }
    if (typing(e.target)) return;
    if (e.key === '?') { press(e, '?'); return; }
    const jump = pendingG && Date.now() - pendingG < WITHIN_MS && THEN.includes(k);
    pendingG = k === 'g' ? Date.now() : 0;
    if (jump || pendingG) press(e, k);
  });
}
