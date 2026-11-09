import { esc, fill } from './util.js';
import { signedFetch } from './pagekey.js';
import { S } from './state.js';
import { sheet } from './shell/sheet.js';
import { render as osNote } from './components/os-note/os-note.js';
import W from './strings/settings-permissions.js';

const nameOf = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };
const named = (withName, unnamed) => { const n = nameOf(); return n ? fill(withName, { name: n }) : unnamed; };

let loaded = false, loading = false;
let scripts = [], canApprove = false, whyNot = '';
const shown = new Map();
let busy = '', said = null, failed = '';

async function load(rerender) {
  if (loading) return;
  loading = true;
  try {
    const res = await signedFetch('/overlay/scripts', { credentials: 'same-origin', cache: 'no-store' });
    if (!res.ok) throw new Error((await res.text()).trim() || ('status ' + res.status));
    const body = await res.json();
    scripts = body.scripts || [];
    canApprove = !!body.can_approve;
    whyNot = body.why_not || '';
    failed = '';
  } catch (e) {
    failed = 'The scripts could not be listed: ' + ((e && e.message) || String(e));
  }
  loading = false; loaded = true;
  rerender();
}

async function show(path, rerender) {
  try {
    const res = await signedFetch('/overlay/scripts/file?path=' + encodeURIComponent(path), { credentials: 'same-origin', cache: 'no-store' });
    const text = await res.text();
    if (!res.ok) throw new Error(text.trim() || ('status ' + res.status));
    shown.set(path, { text, sha256: res.headers.get('X-Script-SHA256') || '' });
  } catch (e) {
    said = { path, kind: 'bad', text: 'The file could not be shown: ' + ((e && e.message) || String(e)) };
  }
  rerender();
}

async function approve(path, rerender) {
  const seen = shown.get(path);
  if (!seen || busy) return;
  if (window.confirm && !window.confirm('Approve ' + path + ' as shown (sha256 ' + seen.sha256.slice(0, 12) + '…)? It then runs in this page, with everything this page can do. A change to the file needs your approval again.')) return;
  busy = path; said = null; rerender();
  try {
    const res = await signedFetch('/overlay/scripts/approve', {
      method: 'POST', credentials: 'same-origin', cache: 'no-store',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, sha256: seen.sha256 }),
    });
    const text = await res.text();
    if (!res.ok) throw new Error(text.trim() || ('status ' + res.status));
    said = { path, kind: 'good', text: W.approved };
    shown.delete(path);
  } catch (e) {
    said = { path, kind: 'bad', text: ((e && e.message) || String(e)) };
  }
  busy = '';
  loaded = false;
  load(rerender);
}

export function scriptsCardHTML() {
  sheet('views/settings/permissions.css').catch(() => {});
  let html = '<section class="permissions__part" data-scripts-card aria-labelledby="adapt-h" aria-describedby="adapt-hint">' +
    '<h3 id="adapt-h" class="permissions__h3">' + esc(named(W.adapters, W.adaptersUnnamed)) + '</h3>' +
    '<p id="adapt-hint" class="permissions__hint">' + esc(named(W.adaptersHint, W.adaptersHintUnnamed) + ' ' + W.adaptersHow) + '</p>';
  const shipped = '<p class="permissions__hint" data-scripts-shipped>' + esc(named(W.shipped, W.shippedUnnamed)) + '</p>';
  if (!loaded) return html + '<p class="permissions__hint" data-scripts-loading>Reading the interface folder…</p></section>';
  if (failed) return html + '<div class="config-result bad" data-scripts-failed>' + esc(failed) + '</div></section>';
  if (!canApprove && whyNot) html += '<p class="permissions__hint" data-scripts-why-not>' + esc(whyNot.charAt(0).toUpperCase() + whyNot.slice(1)) + '.</p>';
  if (scripts.length === 0) return html + '<p class="permissions__line" data-scripts-none>' + esc(named(W.noScripts, W.noScriptsUnnamed)) + '</p>' + shipped + '</section>';
  html += '<ul class="permissions__rows">';
  for (const s of scripts) {
    const seen = shown.get(s.path);
    html += '<li class="permissions__row" data-script="' + esc(s.path) + '"><span class="permissions__words"><span>' + (s.approved ? 'approved' : 'awaiting your approval') +
      '<span class="permissions__soft"> · </span><span class="permissions__code">' + esc(s.path) + '</span></span>' +
      '<span class="permissions__hint permissions__code">sha256 ' + esc(s.sha256) + ' · ' + esc(String(s.size)) + ' bytes</span>';
    if (seen) {
      if (!s.approved) html += osNote({ tone: 'warn', text: 'AII OS: ' + W.scriptRisk + ' ' + W.scriptRiskApprove, live: false }).outerHTML;
      html += '<pre class="permissions__text" data-script-text></pre>';
      if (seen.sha256 !== s.sha256) html += '<div class="config-result bad">The file changed after it was listed. Show it again.</div>';
    }
    if (!s.approved) {
      html += '<span class="permissions__buttons"><button type="button" class="c-button" data-script-show="' + esc(s.path) + '">' + (seen ? 'Show again' : 'Show the file') + '</button>';
      if (canApprove && seen && seen.sha256 === s.sha256) {
        html += '<button type="button" class="c-button c-button--primary" data-script-approve="' + esc(s.path) + '"' + (busy ? ' disabled' : '') + '>' + (busy === s.path ? 'Approving…' : esc(W.approve)) + '</button>';
      }
      html += '</span>';
    }
    if (said && said.path === s.path) html += '<div class="config-result ' + said.kind + '" data-script-said data-announce>' + esc(said.text) + '</div>';
    html += '</span></li>';
  }
  return html + '</ul>' + shipped + '</section>';
}

export function wireScriptsCard(root, rerender) {
  const card = root.querySelector('[data-scripts-card]');
  if (!card) return;
  if (!loaded) { load(rerender); return; }
  card.querySelectorAll('[data-script]').forEach(row => {
    const seen = shown.get(row.dataset.script), box = row.querySelector('[data-script-text]');
    if (seen && box) box.textContent = seen.text;
  });
  card.querySelectorAll('[data-script-show]').forEach(btn => { btn.onclick = () => show(btn.dataset.scriptShow, rerender); });
  card.querySelectorAll('[data-script-approve]').forEach(btn => { btn.onclick = () => approve(btn.dataset.scriptApprove, rerender); });
}

export function scriptsCardStale() { loaded = false; }
