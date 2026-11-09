const inThread = el => !!el.closest('#thread, #composer-wrap');
const nearId = el => { for (let e = el.parentElement; e; e = e.parentElement) if (e.id) return e.id; return ''; };
const summaryOf = d => (d.querySelector(':scope > summary')?.textContent || '').trim();
function disclosures(root) {
  const seen = new Map(), out = [];
  for (const d of root.querySelectorAll('details')) {
    if (inThread(d)) continue;
    const base = nearId(d) + '|' + summaryOf(d), nth = seen.get(base) || 0;
    seen.set(base, nth + 1);
    out.push([base + '|' + nth, d]);
  }
  return out;
}
const scrollerKey = el => (el === document.scrollingElement ? 'page' : el.id || nearId(el) + '|' + el.className);
const scrollers = root => [document.scrollingElement, root, ...root.querySelectorAll('*')].filter(el => el && !inThread(el) && el.scrollHeight > el.clientHeight &&
  (el === document.scrollingElement || /(auto|scroll)/.test(getComputedStyle(el).overflowY)));
const isSecret = el => el.type === 'password' || el.type === 'file' || el.hasAttribute('data-pkey-input') || /key|secret|token|password|code/i.test(el.id);
const edited = el => (el.type === 'checkbox' || el.type === 'radio' ? el.checked !== el.defaultChecked
  : el.tagName === 'SELECT' ? [...el.options].some(o => o.selected !== o.defaultSelected)
  : el.value !== el.defaultValue);
const fields = root => [...root.querySelectorAll('input[id], textarea[id], select[id]')].filter(el => !inThread(el) && !isSecret(el));

export function collect(root) {
  const kept = { open: {}, scroll: {}, fields: {}, pressed: [...root.querySelectorAll('[data-keep-open]')].map(el => el.dataset.keepOpen), asked: {} };
  for (const [key, d] of disclosures(root)) if (d.getClientRects().length) kept.open[key] = d.open;
  for (const el of scrollers(root)) if (el.scrollTop > 0) kept.scroll[scrollerKey(el)] = Math.round(el.scrollTop);
  for (const el of fields(root)) if (edited(el)) kept.fields[el.id] = el.type === 'checkbox' || el.type === 'radio' ? el.checked : el.value;
  for (const el of fields(root)) if (el.dataset.keepAsked === el.value) kept.asked[el.id] = true;
  return kept;
}

export function putBack(root, kept) {
  const left = { fields: { ...kept.fields }, pressed: [...(kept.pressed || [])] };
  const wanted = Object.entries(kept.scroll);
  if (!Object.keys(kept.open).length && !wanted.length && !Object.keys(left.fields).length && !left.pressed.length) return;
  const place = key => (key === 'page' ? document.scrollingElement : document.getElementById(key) || scrollers(root).find(el => scrollerKey(el) === key));
  const apply = () => {
    for (const [key, d] of disclosures(root)) if (key in kept.open && d.open !== kept.open[key]) d.open = kept.open[key];
    for (const key of [...left.pressed]) {
      const name = CSS.escape(key), button = root.querySelector('[data-keep-press="' + name + '"]');
      if (!button) continue;
      left.pressed.splice(left.pressed.indexOf(key), 1);
      if (!root.querySelector('[data-keep-open="' + name + '"]')) button.click();
    }
    for (const id of Object.keys(left.fields)) {
      const el = document.getElementById(id), value = left.fields[id];
      if (!el || !root.contains(el)) continue;
      const box = el.type === 'checkbox' || el.type === 'radio';
      if (box) el.checked = value; else el.value = value;
      if ((box ? el.checked : el.value) !== value) continue;
      el.dispatchEvent(new Event(el.tagName === 'SELECT' || box ? 'change' : 'input', { bubbles: true }));
      if (kept.asked && kept.asked[id]) el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
      delete left.fields[id];
    }
    for (const [key, want] of wanted) {
      const el = place(key);
      if (!el) continue;
      if (Math.abs(el.scrollTop - want) > 1) el.scrollTop = want;
      for (const child of el.children) hold?.watch(child);
    }
  };
  let hold = null;
  hold = holdUntilHand(root, apply);
}

export function holdUntilHand(root, apply) {
  const seen = new MutationObserver(() => apply()), sizes = new ResizeObserver(() => apply());
  const hand = ['wheel', 'touchstart', 'keydown', 'pointerdown'];
  const end = e => { if (e && !e.isTrusted) return; seen.disconnect(); sizes.disconnect(); for (const ev of hand) window.removeEventListener(ev, end, true); };
  for (const ev of hand) window.addEventListener(ev, end, true);
  seen.observe(root, { childList: true, subtree: true });
  sizes.observe(root);
  apply();
  return { end: () => end(), watch: el => sizes.observe(el) };
}
