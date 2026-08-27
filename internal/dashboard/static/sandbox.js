/* sandbox.js — the sandbox card: the home root, the extra roots the
   operator has opened, and adding/removing them. A setting like any
   other (operator ruling 2026-08-20: there is no "grant" ceremony).
   The structural wall stands server-side: substrate paths are refused.
   R66: frame. */
import { S } from './state.js';
import { $, esc } from './util.js';
import { send } from './ws.js';

// The card's HTML: '' when there is no sandbox state (the settings
// renderer concatenates unconditionally).
export function sandboxCardHTML() {
  if (!S.sandbox) return '';
  let html = '';
  html += '<div class="card"><h3>SANDBOX — WHERE THEY MAY REACH</h3>' +
    '<label class="f">HOME ROOT</label><div style="font-family:var(--mono);font-size:12px;color:var(--dim);padding:4px 0">' + esc(S.sandbox.root) + '</div>' +
    '<label class="f">EXTRA ROOTS</label>';
  if ((S.sandbox.extra_roots || []).length) {
    html += S.sandbox.extra_roots.map((r, i) =>
      '<div class="tool-row"><span class="td" style="font-family:var(--mono);font-size:12px">' + esc(r) + '</span>' +
      '<button class="btn ghost" data-rmroot="' + i + '" style="padding:4px 12px;font-size:12px">remove</button></div>').join('');
  } else {
    html += '<div style="color:var(--faint);font-size:12.5px;padding:6px 0">none — their world is their home tree</div>';
  }
  html += '<label class="f">ADD A ROOT (ABSOLUTE PATH)</label>' +
    '<div style="display:flex;gap:10px"><input type="text" id="sbx-add" placeholder="/work/some/project" style="font-family:var(--mono)">' +
    '<button class="btn" id="sbx-grant">Add</button></div>' +
    '<div style="font-size:11.5px;color:var(--faint);margin-top:10px;line-height:1.5">Adding a root widens their world — e.g. letting a trusted identity work on AII OS itself. Their own substrate (ledger, database, key) stays refused inside every root — that wall is structural and server-side. The in-process wall is best-effort; a namespace wrapper (bwrap) must bind added paths too.</div>' +
    '</div>';
  return html;
}

// Wire the card's controls inside the settings stack `st`.
export function wireSandboxCard(st) {
  const grantBtn = $('sbx-grant');
  if (grantBtn) grantBtn.onclick = () => {
    const v = ($('sbx-add').value || '').trim();
    if (!v) return;
    const roots = (S.sandbox.extra_roots || []).concat([v]);
    send({ type: 'sandbox_set', roots: roots });
  };
  st.querySelectorAll('[data-rmroot]').forEach(btn => {
    btn.onclick = () => {
      const roots = (S.sandbox.extra_roots || []).slice();
      roots.splice(parseInt(btn.dataset.rmroot, 10), 1);
      send({ type: 'sandbox_set', roots: roots });
    };
  });
}
