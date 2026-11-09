
import { S } from './state.js';
import { $, esc, fill } from './util.js';
import { send } from './ws.js';
import { pendingSlot } from './pending.js';
import { sheet } from './shell/sheet.js';
import W from './strings/settings-permissions.js';
import C from './strings/components.js';

const pending = pendingSlot();
let input = null;
let focus = null;
let notice = '';
let warning = false;
let warningOpened = false;
let switchRefused = false;
const render = () => { if (S.renderSandbox) S.renderSandbox(); };
function changeRoot(type, root, added = null) {
  if (pending.waiting()) return;
  switchRefused = false;
  const requestID = send({ type, root });
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
export function acceptSandbox(requestID, said) {
  const asked = pending.claim(requestID);
  if (!asked) return false;
  if (asked.notes) { notesSaid = said || ''; return true; }
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
  if (asked.notes) { notesSaid = text; render(); return true; }
  notice = text;
  switchRefused = 'off' in asked;
  if (switchRefused) send({ type: 'query', query: 'sandbox' });
  render(); return true;
}
export function sandboxConnectionLost() {
  const asked = pending.drop();
  if (!asked) return;
  if (asked.notes) { notesSaid = NOTES_LOST; render(); return; }
  notice = 'off' in asked
    ? 'Connection lost. It is not known whether the sandbox was changed. The box shows what the runtime last confirmed and is read again on reconnecting; nothing is retried.'
    : 'Connection lost — completion is unknown. Review the current roots before trying again; nothing is automatically retried.';
  render();
}

const nameOf = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };
const named = (withName, unnamed) => { const n = nameOf(); return n ? fill(withName, { name: n }) : unnamed; };
const WARNING =
  '<b>Turn the sandbox off?</b>' +
  '<p class="permissions__line">With the sandbox off they are no longer kept to their home and the folders listed here. Their tools, the shell included, can read, change and delete any file this computer\'s account can: your own documents, and the keys and sign-ins other programs have saved.</p>' +
  '<p class="permissions__line">Their tools still refuse these, with the sandbox off as with it on: their own record, keys, database, config and data directory; every other identity on this account, whole; this program\'s own update and start-up files and the keys it borrows from other tools; and on Linux the running programs\' own files (/proc). ' +
  'These are checks their tools make on each path. With the sandbox off an identity that sets out to get around them can, for example by changing what this account runs at its next login. Turn it off only for an identity you trust with this account.</p>';
const WARNING_END =
  '<p class="permissions__line">A plugin you granted files still reaches only their home and the folders listed here.</p>' +
  '<p class="permissions__line">It stays off until you untick this box, through restarts too.</p>';
const OFF_LINE = 'The sandbox is off. Their tools reach any file this computer\'s account can. Their tools still refuse their own record, keys, database, config and data directory, the other identities on this account, and this program\'s own update and start-up files: a check on each path, which an identity that sets out to get around it can.';
const ROOTS_IDLE = 'Not limiting them while the sandbox is off. A plugin you granted files still reaches only their home and these folders. Outside these folders a file named like one of their own (config.json, ledger.jsonl and the like) is still refused by their tools.';
const NOT_KEPT = 'Not kept, because a folder you listed holds it: ';
const notKept = () => (S.sandbox.not_kept || []).length
  ? '<p class="permissions__line" data-sandbox-not-kept>' + NOT_KEPT + '<span class="permissions__code">' + S.sandbox.not_kept.map(esc).join('; ') + '</span></p>' : '';

let notesSaid = '';
const NOTES_HEADING = 'OTHER AI AGENTS\u2019 NOTES';
const NOTES_BUTTON = 'Copy notes';
const NOTES_COPYING = 'Copying\u2026';
const NOTES_NOT_CONNECTED = 'Not connected. Nothing was copied.';
const NOTES_LOST = 'Connection lost. It is not known whether the notes were copied; this card reads the home again on reconnecting. Nothing is retried.';
const NOTES_COPIED = (folder) => 'A folder stands in their home: ' + folder + '. It is theirs to change; to copy again, delete it first.';
const NOTES_TIP = 'Copies the notes other AI agents keep on this machine into their home, as a birth does.';
const NOTES_ASK = 'Copy the notes other AI agents keep on this machine into their home, as a new identity\u2019s birth does: by default Claude Code\u2019s, the Claude desktop app\u2019s and Codex\u2019s, or those of the stores set in genesis.agent_memory in config.json. Only notes of the kinds the copy takes are copied, by default .md and .txt: never credentials, sessions or databases, and a note that holds what looks like a key or a token is left where it is. They read them as another agent\u2019s notes, not as their own memories, and a note they read goes to their model\u2019s provider like anything else they read. It is copied once and does not follow the originals.';
function copyNotes() {
  if (pending.waiting()) return;
  notesSaid = '';
  const requestID = send({ type: 'agent_notes_copy' });
  notesSaid = pending.arm({ notes: true }, requestID) ? NOTES_COPYING : NOTES_NOT_CONNECTED;
  render();
}
function notesCardHTML() {
  const n = S.sandbox.agent_notes;
  if (!n) return '';
  let html = '<section class="permissions__part" data-agent-notes aria-labelledby="notes-h" aria-describedby="notes-hint">' +
    '<h3 id="notes-h" class="permissions__h3">' + esc(W.otherNotes) + '</h3>';
  if (n.copied) {
    html += '<p id="notes-hint" class="permissions__line" data-agent-notes-copied>' + NOTES_COPIED('<span class="permissions__code">' + esc(n.folder || '') + '</span>') + '</p>';
  } else {
    html += '<p id="notes-hint" class="permissions__line">' + NOTES_ASK + '</p>' +
      '<div class="permissions__buttons"><button class="c-button" id="notes-copy"' + (pending.waiting() ? ' disabled' : '') + '>' + esc(W.otherNotesCopy) + '</button></div>';
  }
  return html + '<p class="permissions__said" data-announce data-agent-notes-said>' + esc(notesSaid) + '</p></section>';
}

const HELD_FIRM = ['SAFE', 'ACCESS_TOKEN_OFF'];

export function sandboxCardHTML() {
  focus = input && document.activeElement === input ? { start: input.selectionStart, end: input.selectionEnd } : null;
  if (!S.sandbox) return '';
  sheet('views/settings/permissions.css').catch(() => {});
  const off = !!S.sandbox.off;
  const firm = !off && HELD_FIRM.includes(S.sandbox.held_why);
  if (off || firm) warning = false;
  const waiting = pending.waiting() ? ' disabled' : '';
  let html = '<section class="permissions__part" data-folders aria-labelledby="sbx-h" aria-describedby="' + (off ? 'sbx-offline' : 'sbx-hint') + '">' +
    '<h3 id="sbx-h" class="permissions__h3">' + esc(named(W.folders, W.foldersUnnamed)) + '</h3>' +
    (off ? '' : '<p id="sbx-hint" class="permissions__hint">' + esc(named(W.foldersHint, W.foldersHintUnnamed)) + '</p>') +
    '<ul class="permissions__rows"><li class="permissions__row"><span class="permissions__words"><span>' + esc(W.home) + '<span class="permissions__soft"> · ' + esc(W.always) + '</span></span>' +
    '<span class="permissions__code">' + esc(S.sandbox.root) + '</span></span></li>' +
    (S.sandbox.extra_roots || []).map((r, i) =>
      '<li class="permissions__row' + (off ? ' permissions__row--idle' : '') + '"><span class="permissions__code">' + esc(r) + '</span>' +
      '<button class="permissions__act" data-rmroot="' + i + '"' + waiting + '>' + esc(C.remove) + '</button></li>').join('') + '</ul>';
  if (off) html += '<p class="permissions__hint" data-roots-idle>' + ROOTS_IDLE + '</p>';
  html += '<div class="permissions__add"><label for="sbx-add">' + esc(W.addFolder) + '</label><p class="permissions__hint">' + esc(W.addFolderHint) + '</p>' +
    '<div class="permissions__inline"><input type="text" id="sbx-add" placeholder="/work/some/project"><button class="c-button" id="sbx-grant"' + waiting + '>' + esc(C.add) + '</button></div></div>';
  if (!off && S.sandbox.held_why) html += '<div class="config-result bad" data-sandbox-held>' + esc(S.sandbox.held) + '</div>';
  if (S.sandbox.labelled) html += '<p class="permissions__hint" data-sandbox-labelled>' + esc(S.sandbox.labelled) + '</p>';
  html += '<label class="permissions__switch permissions__switch--row"><span class="permissions__words"><span>' + esc(W.sandboxOff) + '</span><span class="permissions__hint">' + esc(W.limitOffHint) + '</span></span>' +
    '<input type="checkbox" role="switch" class="permissions__sw" id="sbx-off"' + (off ? ' checked' : '') + (pending.waiting() || warning || firm ? ' disabled' : '') + '></label>';
  if (warning) {
    html += '<div class="config-result permissions__warning" data-sandbox-warning><div data-announce>' + WARNING + notKept() + WARNING_END + '</div>' +
      '<div class="permissions__buttons"><button class="c-button c-button--danger" id="sbx-off-allow">Allow</button>' +
      '<button class="c-button c-button--quiet" id="sbx-off-cancel">Cancel</button></div></div>';
  }
  if (off) html += '<div class="config-result bad" id="sbx-offline" data-sandbox-off>' + OFF_LINE + notKept() + '</div>';
  html += '<p class="permissions__said" data-announce>' + esc(notice + (switchRefused ? (off ? ' The sandbox is off.' : ' The sandbox is still on.') : '')) + '</p>' +
    '<p class="permissions__hint">Adding a folder widens their world — e.g. letting a trusted identity work on AII OS itself — and the world of every plugin you grant files, which works here under the same wall. Their data directory, key, database and config stay refused inside every folder — that wall is structural and server-side. The in-process wall is best-effort; a namespace wrapper (bwrap) must bind added folders too.</p>' +
    '</section>';
  return html + notesCardHTML();
}

export function wireSandboxCard(st) {
  const notes = $('notes-copy');
  if (notes) notes.onclick = copyNotes;
  const fresh = $('sbx-add');
  if (fresh && input) fresh.replaceWith(input);
  else input = fresh;
  if (focus && input) { input.focus(); input.setSelectionRange(focus.start, focus.end); }
  focus = null;
  const grantBtn = $('sbx-grant');
  if (grantBtn) grantBtn.onclick = () => {
    const v = ($('sbx-add').value || '').trim();
    if (!v) return;
    changeRoot('sandbox_add', v, $('sbx-add').value);
  };
  st.querySelectorAll('[data-rmroot]').forEach(btn => {
    btn.onclick = () => changeRoot('sandbox_remove', (S.sandbox.extra_roots || [])[parseInt(btn.dataset.rmroot, 10)]);
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
