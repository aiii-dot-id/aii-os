

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

function draftSafeReload() {
  const ta = document.getElementById('msg-input');
  if (ta && typeof ta.value === 'string' && ta.value.trim() !== '') {
    try { sessionStorage.setItem('aii.draft', ta.value); } catch (err) {}
  }
  location.reload();
}

export function restoreDraft() {
  let saved = '';
  try { saved = sessionStorage.getItem('aii.draft') || ''; } catch (err) {}
  if (!saved) return;
  try { sessionStorage.removeItem('aii.draft'); } catch (err) {}
  const ta = document.getElementById('msg-input');
  if (ta) ta.value = saved;
}
