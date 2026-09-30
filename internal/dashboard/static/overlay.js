

let swapped = {};

export function onOverlayChanged(token, paths) {
  if (typeof token !== 'number' || !Array.isArray(paths)) return;
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
  if (needsReload) draftSafeReload();
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

// Ordinary reload and overlay refresh preserve the same unsent words in
// this tab only. Empty drafts remove the old value; nothing is sent.
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
