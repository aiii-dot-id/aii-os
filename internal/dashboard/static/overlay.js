import { anyPending } from './pending.js';

let swapped = {};

export function onOverlayChanged(token, paths) {
  if (typeof token !== 'number' || !Array.isArray(paths)) return;
  if (location.pathname.startsWith('/shipped/')) return;
  if (token <= lastToken) return;
  lastToken = token;
  let needsReload = false;
  for (const p of paths) {
    if (typeof p !== 'string') continue;
    if (swapped[p] === token) continue;
    swapped[p] = token;
    if (p.endsWith('.css')) swapCSS(p, String(token));
    else needsReload = true;
  }
  if (needsReload) reloadWhenFree();
}

const holders = new Set();
export function holdReloadWhile(unsaved) { holders.add(unsaved); }
function workHeld() {
  if (anyPending()) return true;
  for (const unsaved of holders) {
    try { if (unsaved()) return true; } catch (err) { return true; }
  }
  return false;
}

let waiting = null;
function reloadWhenFree() {
  if (!workHeld()) { draftSafeReload(); return; }
  showUpdate();
  if (waiting) return;
  waiting = setInterval(() => {
    if (workHeld()) return;
    clearInterval(waiting); waiting = null;
    draftSafeReload();
  }, 1000);
}
function showUpdate() {
  if (document.getElementById('overlay-update')) return;
  const box = document.createElement('div');
  box.id = 'overlay-update';
  box.className = 'update-notice';
  box.setAttribute('role', 'status');
  const text = document.createElement('span');
  text.textContent = 'The dashboard was updated. It reloads once your unsaved changes are saved or cancelled.';
  const now = document.createElement('button');
  now.type = 'button';
  now.className = 'btn ghost sm';
  now.textContent = 'Reload now, discarding them';
  now.onclick = draftSafeReload;
  box.append(text, now);
  document.body.appendChild(box);
}

let lastToken = 0;

function swapCSS(p, token) {
  const base = p.startsWith('/') ? p.slice(1) : p;
  for (const link of document.querySelectorAll('link[rel="stylesheet"]')) {
    const href = link.getAttribute('href');
    if (!href) continue;
    const clean = stripVersion(href);
    if (clean === p || clean === './' + base) {
      link.setAttribute('href', clean + '?v=' + encodeURIComponent(token));
      return;
    }
  }
}

function stripVersion(href) {
  const i = href.indexOf('?');
  return i <  0 ? href : href.slice(0, i);
}

function saveDraft() {
  const ta = document.getElementById('msg-input'); if (!ta) return;
  try {
    if (ta.value) sessionStorage.setItem('aii.draft', ta.value);
    else sessionStorage.removeItem('aii.draft');
  } catch (err) {}
}
function draftSafeReload() { saveDraft(); location.reload(); }

export function restoreDraft() {
  const ta = document.getElementById('msg-input'); if (!ta) return;
  try { ta.value = sessionStorage.getItem('aii.draft') || ta.value; } catch (err) {}
  ta.addEventListener('input', saveDraft);
  window.addEventListener('pagehide', saveDraft);
  ta.dispatchEvent(new Event('input', { bubbles: true }));
}
