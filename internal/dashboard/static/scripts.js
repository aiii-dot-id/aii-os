import { esc } from './util.js';

let loaded = false, loading = false;
let scripts = [], canApprove = false, whyNot = '';
const shown = new Map();
let busy = '', said = null, failed = '';

async function load(rerender) {
  if (loading) return;
  loading = true;
  try {
    const res = await fetch('/overlay/scripts', { credentials: 'same-origin', cache: 'no-store' });
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
    const res = await fetch('/overlay/scripts/file?path=' + encodeURIComponent(path), { credentials: 'same-origin', cache: 'no-store' });
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
    const res = await fetch('/overlay/scripts/approve', {
      method: 'POST', credentials: 'same-origin', cache: 'no-store',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path, sha256: seen.sha256 }),
    });
    const text = await res.text();
    if (!res.ok) throw new Error(text.trim() || ('status ' + res.status));
    said = { path, kind: 'good', text: 'Approved. It runs from the next load of this page.' };
    shown.delete(path);
  } catch (e) {
    said = { path, kind: 'bad', text: ((e && e.message) || String(e)) };
  }
  busy = '';
  loaded = false;
  load(rerender);
}

export function scriptsCardHTML() {
  let html = '<div class="card" data-scripts-card><h3>SCRIPTS IN THE IDENTITY\'S INTERFACE FOLDER</h3>' +
    '<div class="muted" style="font-size:12px">The identity may restyle this page freely. A script of its own runs here only after you approve that exact file; a change to it needs your approval again. Until then the page runs as shipped.</div>';
  if (!loaded) return html + '<div class="muted" data-scripts-loading>Reading the interface folder…</div></div>';
  if (failed) return html + '<div class="config-result bad" data-scripts-failed>' + esc(failed) + '</div></div>';
  if (!canApprove && whyNot) html += '<div class="muted" data-scripts-why-not style="font-size:12px;margin-top:6px">' + esc(whyNot.charAt(0).toUpperCase() + whyNot.slice(1)) + '.</div>';
  if (scripts.length === 0) return html + '<div class="muted" data-scripts-none>The interface folder holds no script.</div></div>';
  for (const s of scripts) {
    const seen = shown.get(s.path);
    html += '<div class="sp-kv" data-script="' + esc(s.path) + '" style="margin-top:8px"><span>' + (s.approved ? 'approved' : 'awaiting your approval') + '</span><b>' + esc(s.path) + '</b></div>' +
      '<div class="muted" style="font-size:11.5px;word-break:break-all">sha256 ' + esc(s.sha256) + ' · ' + esc(String(s.size)) + ' bytes</div>';
    if (seen) {
      html += '<pre data-script-text style="max-height:240px;overflow:auto;font-size:11.5px;white-space:pre-wrap"></pre>';
      if (seen.sha256 !== s.sha256) html += '<div class="config-result bad">The file changed after it was listed. Show it again.</div>';
    }
    if (!s.approved) {
      html += '<div class="savebar"><button type="button" class="btn ghost" data-script-show="' + esc(s.path) + '">' + (seen ? 'Show again' : 'Show the file') + '</button>';
      if (canApprove && seen && seen.sha256 === s.sha256) {
        html += '<button type="button" class="btn" data-script-approve="' + esc(s.path) + '"' + (busy ? ' disabled' : '') + '>' + (busy === s.path ? 'Approving…' : 'Approve this file') + '</button>';
      }
      html += '</div>';
    }
    if (said && said.path === s.path) html += '<div class="config-result ' + said.kind + '" data-script-said data-announce>' + esc(said.text) + '</div>';
  }
  return html + '</div>';
}

export function wireScriptsCard(root, rerender) {
  const card = root.querySelector('[data-scripts-card]');
  if (!card) return;
  if (!loaded) { load(rerender); return; }
  card.querySelectorAll('[data-script]').forEach(row => {
    const seen = shown.get(row.dataset.script);
    let box = row.nextElementSibling;
    while (box && !box.matches('[data-script-text]') && !box.matches('[data-script]')) box = box.nextElementSibling;
    if (seen && box && box.matches('[data-script-text]')) box.textContent = seen.text;
  });
  card.querySelectorAll('[data-script-show]').forEach(btn => { btn.onclick = () => show(btn.dataset.scriptShow, rerender); });
  card.querySelectorAll('[data-script-approve]').forEach(btn => { btn.onclick = () => approve(btn.dataset.scriptApprove, rerender); });
}

export function scriptsCardStale() { loaded = false; }
