
import { S } from './state.js';
import { $, esc } from './util.js';
import { send } from './ws.js';
import { pendingSlot } from './pending.js';

const pending = pendingSlot();
let input = null;
let focus = null;
let notice = '';
const render = () => { if (S.renderSandbox) S.renderSandbox(); };
function saveRoots(roots, added = null) {
  if (pending.waiting()) return;
  const requestID = send({ type: 'sandbox_set', roots });
  notice = pending.arm({ added }, requestID) ? 'Waiting for the runtime to confirm…' : 'Not connected — roots were not changed.';
  render();
}
export function acceptSandbox(requestID) {
  const asked = pending.claim(requestID);
  if (!asked) return false;
  if (asked.added !== null && input?.value === asked.added) input.value = '';
  notice = 'Sandbox roots saved.';
  return true;
}
export function rejectSandbox(requestID, text) {
  if (!pending.claim(requestID)) return false;
  notice = text; render(); return true;
}
export function sandboxConnectionLost() {
  if (!pending.drop()) return;
  notice = 'Connection lost — completion is unknown. Review the current roots before trying again; nothing is automatically retried.';
  render();
}

export function sandboxCardHTML() {
  focus = input && document.activeElement === input ? { start: input.selectionStart, end: input.selectionEnd } : null;
  if (!S.sandbox) return '';
  let html = '';
  html += '<div class="card"><h3>SANDBOX — WHERE THEY MAY REACH</h3>' +
    '<label class="f">HOME ROOT</label><div style="font-family:var(--mono);font-size:12px;color:var(--dim);padding:4px 0">' + esc(S.sandbox.root) + '</div>' +
    '<label class="f">EXTRA ROOTS</label>';
  if ((S.sandbox.extra_roots || []).length) {
    html += S.sandbox.extra_roots.map((r, i) =>
      '<div class="tool-row"><span class="td" style="font-family:var(--mono);font-size:12px">' + esc(r) + '</span>' +
      '<button class="btn ghost" data-rmroot="' + i + '"' + (pending.waiting() ? ' disabled' : '') + ' style="padding:4px 12px;font-size:12px">remove</button></div>').join('');
  } else {
    html += '<div style="color:var(--faint);font-size:12.5px;padding:6px 0">none — their world is their home tree</div>';
  }
  html += '<label class="f">ADD A ROOT (ABSOLUTE PATH)</label>' +
    '<div style="display:flex;gap:10px"><input type="text" id="sbx-add" placeholder="/work/some/project" style="font-family:var(--mono)">' +
    '<button class="btn" id="sbx-grant"' + (pending.waiting() ? ' disabled' : '') + '>Add</button></div>' +
    '<div data-announce>' + esc(notice) + '</div>' +
    '<div style="font-size:11.5px;color:var(--faint);margin-top:10px;line-height:1.5">Adding a root widens their world — e.g. letting a trusted identity work on AII OS itself — and the world of every plugin you grant files, which works here under the same wall. Their substrate (the data directory, key, database and config) stays refused inside every root — that wall is structural and server-side. The in-process wall is best-effort; a namespace wrapper (bwrap) must bind added paths too.</div>' +
    '</div>';
  return html;
}

export function wireSandboxCard(st) {
  const fresh = $('sbx-add');
  if (fresh && input) fresh.replaceWith(input);
  else input = fresh;
  if (focus && input) { input.focus(); input.setSelectionRange(focus.start, focus.end); }
  focus = null;
  const grantBtn = $('sbx-grant');
  if (grantBtn) grantBtn.onclick = () => {
    const v = ($('sbx-add').value || '').trim();
    if (!v) return;
    const roots = (S.sandbox.extra_roots || []).concat([v]);
    saveRoots(roots, $('sbx-add').value);
  };
  st.querySelectorAll('[data-rmroot]').forEach(btn => {
    btn.onclick = () => {
      const roots = (S.sandbox.extra_roots || []).slice();
      roots.splice(parseInt(btn.dataset.rmroot, 10), 1);
      saveRoots(roots);
    };
  });
}
