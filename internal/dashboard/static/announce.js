let sameTask = false;
export function announce(text) {
  const region = document.getElementById('announce');
  if (!region || !text) return;
  if (sameTask) { if (!region.textContent.includes(text)) region.append(' ', text); return; }
  region.replaceChildren(text);
  sameTask = true;
  queueMicrotask(() => { sameTask = false; });
}

const SAID = 400;
export function announceWords(who, words) {
  const whole = (words || '').replace(/\s+/g, ' ').trim();
  if (!whole) return;
  let opening = words.trim().split(/\n\s*\n/)[0].replace(/\s+/g, ' ').trim();
  if (opening.length > SAID) opening = opening.slice(0, SAID + 1).replace(/\s+\S*$/, '').slice(0, SAID);
  announce(who + ': ' + opening + (opening.length < whole.length ? '… The rest is in the conversation.' : ''));
}

const said = root => new Set([...root.querySelectorAll('[data-announce]')].map(el => el.textContent.trim()).filter(Boolean));

export function renderInto(root, html, quiet = false) {
  const before = said(root);
  root.innerHTML = html;
  if (!quiet) announce([...said(root)].filter(text => !before.has(text)).join(' '));
}
