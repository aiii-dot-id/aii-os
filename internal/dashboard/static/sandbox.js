
import { S } from './state.js';
import { $, esc } from './util.js';
import { send } from './ws.js';
import { pendingSlot } from './pending.js';

const pending = pendingSlot();
let input = null;
let focus = null;
let notice = '';
let warning = false;
let warningOpened = false;
let switchRefused = false;
const render = () => { if (S.renderSandbox) S.renderSandbox(); };
function saveRoots(roots, added = null) {
  if (pending.waiting()) return;
  switchRefused = false;
  const requestID = send({ type: 'sandbox_set', roots });
  notice = pending.arm({ added }, requestID) ? 'Waiting for the runtime to confirm…' : 'Not connected — roots were not changed.';
  render();
}
function switchSandbox(off) {
  if (pending.waiting()) return;
  warning = false; switchRefused = false;
  const requestID = send({ type: 'sandbox_switch', sandbox_off: off });
  notice = pending.arm({ off }, requestID) ? 'Waiting for the runtime to confirm…' : 'Not connected. The sandbox was not changed.';
  render();
}
export function acceptSandbox(requestID) {
  const asked = pending.claim(requestID);
  if (!asked) return false;
  if ('off' in asked) {
    notice = S.sandbox && S.sandbox.off ? 'The sandbox is off.' : 'The sandbox is on.';
    return true;
  }
  if (asked.added !== null && input?.value === asked.added) input.value = '';
  notice = 'Sandbox roots saved.';
  return true;
}
export function rejectSandbox(requestID, text) {
  const asked = pending.claim(requestID);
  if (!asked) return false;
  notice = text;
  switchRefused = 'off' in asked;
  if (switchRefused) send({ type: 'query', query: 'sandbox' });
  render(); return true;
}
export function sandboxConnectionLost() {
  const asked = pending.drop();
  if (!asked) return;
  notice = 'off' in asked
    ? 'Connection lost. It is not known whether the sandbox was changed. The box shows what the runtime last confirmed and is read again on reconnecting; nothing is retried.'
    : 'Connection lost — completion is unknown. Review the current roots before trying again; nothing is automatically retried.';
  render();
}

const OFF_LABEL = 'TURN THE SANDBOX OFF';
const WARNING =
  '<b>Turn the sandbox off?</b>' +
  '<div style="margin-top:6px">With the sandbox off they are no longer kept to their home and the folders listed here. Their tools, the shell included, can read, change and delete any file this computer\'s account can: your own documents, and the keys and sign-ins other programs have saved.</div>' +
  '<div style="margin-top:6px">Their tools still refuse these, with the sandbox off as with it on: their own record, keys, database, config and data directory; every other identity on this account, whole; this program\'s own update and start-up files and the keys it borrows from other tools; and on Linux the running programs\' own files (/proc). ' +
  'These are checks their tools make on each path. With the sandbox off an identity that sets out to get around them can, for example by changing what this account runs at its next login. Turn it off only for an identity you trust with this account.</div>';
const WARNING_END =
  '<div style="margin-top:6px">A plugin you granted files still reaches only their home and the folders listed here.</div>' +
  '<div style="margin-top:6px">It stays off until you untick this box, through restarts too.</div>';
const OFF_LINE = 'The sandbox is off. Their tools reach any file this computer\'s account can. Their tools still refuse their own record, keys, database, config and data directory, the other identities on this account, and this program\'s own update and start-up files: a check on each path, which an identity that sets out to get around it can.';
const ROOTS_IDLE = 'Not limiting them while the sandbox is off. A plugin you granted files still reaches only their home and these folders. Outside these folders a file named like one of their own (config.json, ledger.jsonl and the like) is still refused by their tools.';
const NOT_KEPT = 'Not kept, because a folder you listed holds it: ';
const notKept = () => (S.sandbox.not_kept || []).length
  ? '<div data-sandbox-not-kept style="margin-top:6px">' + NOT_KEPT + '<span style="font-family:var(--mono);font-size:12px">' + S.sandbox.not_kept.map(esc).join('; ') + '</span></div>' : '';

const HELD_FIRM = ['SAFE', 'ACCESS_TOKEN_OFF'];

export function sandboxCardHTML() {
  focus = input && document.activeElement === input ? { start: input.selectionStart, end: input.selectionEnd } : null;
  if (!S.sandbox) return '';
  const off = !!S.sandbox.off;
  const firm = !off && HELD_FIRM.includes(S.sandbox.held_why);
  if (off || firm) warning = false;
  let html = '';
  html += '<div class="card"><h3>SANDBOX — WHERE THEY MAY REACH</h3>' +
    '<label class="f" style="display:flex;gap:6px;align-items:center"><input type="checkbox" id="sbx-off"' + (off ? ' checked' : '') + (pending.waiting() || warning || firm ? ' disabled' : '') + '> ' + OFF_LABEL + '</label>';
  if (warning) {
    html += '<div class="config-result" data-sandbox-warning style="line-height:1.5;color:var(--txt)"><div data-announce>' + WARNING + notKept() + WARNING_END + '</div>' +
      '<div style="display:flex;gap:10px;margin-top:10px"><button class="btn" id="sbx-off-allow">Allow</button>' +
      '<button class="btn ghost" id="sbx-off-cancel">Cancel</button></div></div>';
  }
  if (off) html += '<div class="config-result bad" data-sandbox-off style="line-height:1.5">' + OFF_LINE + notKept() + '</div>';
  if (!off && S.sandbox.held_why) html += '<div class="config-result bad" data-sandbox-held style="line-height:1.5">' + esc(S.sandbox.held) + '</div>';
  html += '<label class="f">HOME ROOT</label><div style="font-family:var(--mono);font-size:12px;color:var(--dim);padding:4px 0">' + esc(S.sandbox.root) + '</div>' +
    '<label class="f">EXTRA ROOTS</label>';
  if (off) html += '<div data-roots-idle style="color:var(--faint);font-size:12.5px;padding:6px 0;line-height:1.5">' + ROOTS_IDLE + '</div>';
  if ((S.sandbox.extra_roots || []).length) {
    html += S.sandbox.extra_roots.map((r, i) =>
      '<div class="tool-row"' + (off ? ' style="opacity:.55"' : '') + '><span class="td" style="font-family:var(--mono);font-size:12px">' + esc(r) + '</span>' +
      '<button class="btn ghost" data-rmroot="' + i + '"' + (pending.waiting() ? ' disabled' : '') + ' style="padding:4px 12px;font-size:12px">remove</button></div>').join('');
  } else if (!off) {
    html += '<div style="color:var(--faint);font-size:12.5px;padding:6px 0">none — their world is their home tree</div>';
  }
  html += '<label class="f">ADD A ROOT (ABSOLUTE PATH)</label>' +
    '<div style="display:flex;gap:10px"><input type="text" id="sbx-add" placeholder="/work/some/project" style="font-family:var(--mono)">' +
    '<button class="btn" id="sbx-grant"' + (pending.waiting() ? ' disabled' : '') + '>Add</button></div>' +
    '<div data-announce>' + esc(notice + (switchRefused ? (off ? ' The sandbox is off.' : ' The sandbox is still on.') : '')) + '</div>' +
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
  const box = $('sbx-off');
  if (box) box.onchange = () => {
    if (S.sandbox.off) { switchSandbox(false); return; }
    warning = true; warningOpened = true; switchRefused = false; notice = '';
    render();
  };
  const allow = $('sbx-off-allow'), cancel = $('sbx-off-cancel');
  if (allow) allow.onclick = () => switchSandbox(true);
  if (cancel) cancel.onclick = () => { warning = false; render(); $('sbx-off')?.focus(); };
  if (warningOpened && cancel) cancel.focus();
  warningOpened = false;
}
