import { S } from '../state.js';
import { $, esc } from '../util.js';
import { send } from '../ws.js';
import { pendingSlot } from '../pending.js';
import { holdReloadWhile } from '../overlay.js';

let said = null;
let step = 1;
let held = '';
let savedAs = '';
let plan = null;
let restarting = false;
const actionReply = pendingSlot();
const preferenceReply = pendingSlot();
function busyAction() {
  const pending = actionReply.waiting();
  return pending ? pending.action === 'verify' ? 'verify:' + pending.name : pending.action : null;
}
const controls = new Map();
let focus = null;
let reader = null;
const valueOf = el => el.type === 'checkbox' ? el.checked : el.value;
holdReloadWhile(() => !!held || [...controls.values()].some(f => valueOf(f.el) !== f.saved));
function clearControls(predicate) {
  controls.forEach((field, id) => {
    if (!predicate(id)) return;
    field.el.value = '';
    controls.delete(id);
  });
}
function clearEscrow() {
  if (reader) { reader.abort(); reader = null; }
  held = ''; step = 1; savedAs = '';
  clearControls(id => !['bk-keep', 'bk-enabled', 'bk-confirm'].includes(id));
}
function actionCard(action) { return action.startsWith('escrow') ? 'escrow' : action.startsWith('restore') ? 'restore' : 'kept'; }
function showPreferenceDraft() {
  const note = $('bk-draft');
  if (note) note.hidden = !['bk-keep','bk-enabled'].some(id => {
    const field = controls.get(id);
    return field && (field.dirty || valueOf(field.el) !== field.saved);
  });
}

function ask(action, extra) {
  if (actionReply.waiting()) return;
  const requestID = send({ type: 'backups', backups: Object.assign({ action }, extra || {}) });
  if (!actionReply.arm({ action, name: extra?.name }, requestID)) {
    if (action === 'escrow_create') held = '';
    say('bad', 'Not connected — nothing was requested.', actionCard(action));
    return;
  }
  said = null;
  if (action === 'restore_plan' || action === 'restore') clearControls(id => id === 'bk-confirm');
  rerender();
}
function rerender() { if (S.renderBackups) S.renderBackups(); if (S.renderIdentityView) S.renderIdentityView(); }

export function requestBackups() { send({ type: 'backups', backups: { action: 'status' } }); }

export function applyBackups(reply, requestID) {
  if (!reply) return;
  if (reply.action !== 'status') {
    if (actionReply.waiting()?.action !== reply.action || !actionReply.claim(requestID)) return;
  }
  if (reply.view) S.backups = reply.view;
  const act = reply.action || '';
  const at = actionCard(act);
  if (act === 'restore_plan' && reply.plan) { clearControls(id => id === 'bk-confirm'); plan = reply.plan; said = null; rerender(); return; }
  if (act === 'restore' && reply.restarting) { restarting = true; said = null; rerender(); return; }
  if (reply.error) {
    said = { kind: 'bad', text: reply.error, at };
  } else if (reply.action === 'take') {
    said = { kind: 'good', text: 'A snapshot was taken and proved to restore.', at };
  } else if (reply.action === 'verify') {
    said = { kind: 'good', text: reply.said || 'Proved.', at };
  } else if (reply.action === 'escrow_create' && reply.file_b64) {
    saveFile(reply.file_name || 'escrow.age', reply.file_b64);
    savedAs = reply.file_name || 'escrow.age';
    step = 2;
    clearControls(id => id === 'bk-pass' || id === 'bk-again');
  } else if (reply.action === 'escrow_check') {
    clearEscrow();
    said = { kind: 'good', text: 'Checked: that file holds this identity\'s keys and opens with your passphrase. From the next pass, snapshots are encrypted. Keep the file and the passphrase where the loss of this machine cannot reach them.', at };
  }
  rerender();
}
function saidHTML(at) {
  return said && said.at === at ? '<div class="config-result ' + said.kind + '" data-said="' + at + '" data-announce style="margin-top:10px">' + esc(said.text) + '</div>' : '';
}
function say(kind, text, at) { said = { kind, text, at }; rerender(); }

function saveFile(name, b64) {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  const url = URL.createObjectURL(new Blob([bytes], { type: 'application/octet-stream' }));
  const a = document.createElement('a');
  a.href = url; a.download = name; a.dataset.escrowDownload = name;
  document.body.appendChild(a); a.click();
  setTimeout(() => { URL.revokeObjectURL(url); a.remove(); }, 1000);
}

function when(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d)) return iso;
  const mins = Math.round((Date.now() - d.getTime()) / 60000);
  const rel = mins < 0 ? 'in ' + human(-mins) : human(mins) + ' ago';
  return d.toLocaleString() + ' (' + rel + ')';
}
function human(mins) {
  if (mins < 1) return 'less than a minute';
  if (mins < 60) return mins + ' min';
  if (mins < 48 * 60) return Math.round(mins / 60) + ' h';
  return Math.round(mins / 1440) + ' days';
}

function passLine(p) {
  if (!p) return '<span class="dim-note">No pass has run yet. The first runs at the next 04:00, or press Back up now.</span>';
  const at = when(p.at);
  if (p.outcome === 'ok') return 'Verified, copied and proved to restore — ' + esc(at);
  if (p.outcome === 'safe') return 'SAFE: the record was walked and verifies; no snapshot is made in SAFE — ' + esc(at);
  if (p.outcome === 'disabled') return 'Switched off: nothing was verified and nothing copied — ' + esc(at);
  return '<span class="bad-text">FAILED — no snapshot was kept, and the older ones stand. ' + esc(at) +
    (p.failing_since && p.failing_since !== p.at ? ' Failing since ' + esc(when(p.failing_since)) + '.' : '') + '</span>' +
    (p.detail ? '<div class="dim-note" style="margin-top:4px">' + esc(p.detail) + '</div>' : '');
}

export function backupsHTML() {
  const active = document.activeElement;
  focus = active && controls.get(active.id)?.el === active
    ? { el: active, start: active.selectionStart, end: active.selectionEnd } : null;
  const v = S.backups;
  if (!v) return '<div class="card"><div class="empty">loading backups…</div></div>';
  if (!v.secrets_ok || v.safe) clearEscrow();
  const snaps = v.snapshots || [];
  const busy = busyAction();
  const waiting = !!busy || !!reader;
  let html = '';

  html += '<div class="card" data-card="kept"><h3>BACKUPS — WHAT IS KEPT</h3>';
  if (!v.enabled) html += '<div class="bad-text" data-off style="margin-bottom:8px">The maintenance pass is switched off: nothing is verified and no snapshot is made. Switch it on below.</div>';
  if (v.safe) html += '<div class="bad-text" style="margin-bottom:8px">This start is SAFE: the record is walked every day and no snapshot is made.</div>';
  html += '<div class="kv"><span>last pass</span><b data-lastpass>' + passLine(v.last_pass) + '</b></div>';
  if (v.next_pass) html += '<div class="kv"><span>next pass</span><b>' + esc(when(v.next_pass)) + '</b></div>';
  if (v.chain) html += '<div class="kv"><span>record</span><b>' + esc(v.chain) + '</b></div>';
  if (!snaps.length) {
    html += '<div class="empty" style="margin-top:10px">No snapshot yet.</div>';
  } else {
    html += '<div style="margin-top:10px">' + snaps.map(s =>
      '<div class="tool-row" data-snapshot="' + esc(s.name) + '"><span class="td">' +
      '<b>' + esc(when(s.at)) + '</b> · through record ' + esc(s.record) +
      ' · ' + (s.on_demand ? 'asked for' : 'daily') + ' · ' + (s.encrypted ? 'encrypted' : '<span class="bad-text">not encrypted</span>') +
      '<div class="dim-note" style="font-family:var(--mono);font-size:11px">' + esc(s.name) + '</div></span>' +
      '<span style="display:flex;gap:6px"><button class="btn ghost" data-verify="' + esc(s.name) + '"' + (waiting || v.safe || !v.enabled ? ' disabled' : '') + ' style="padding:4px 12px;font-size:12px">' +
      (busy === 'verify:' + s.name ? 'Proving…' : 'Prove it restores') + '</button>' +
      '<button class="btn ghost" data-restore="' + esc(s.name) + '" data-source="snapshot"' + (waiting ? ' disabled' : '') + ' style="padding:4px 12px;font-size:12px">Restore…</button></span></div>').join('') + '</div>';
  }
  html += '<div style="display:flex;gap:10px;align-items:center;margin-top:12px">' +
    '<button class="btn" id="bk-take"' + (waiting || v.safe || !v.enabled ? ' disabled' : '') + '>' + (busy === 'take' ? 'Taking a snapshot…' : 'Back up now') + '</button>' +
    '<span class="dim-note">A snapshot holds the whole identity — record, conversations and lived time — and is proved to restore before it is kept. One asked for here stands beside the daily ones; it never replaces one.</span></div>';
  html += saidHTML('kept') + '</div>';

  html += restoreHTML(v);

  html += '<div class="card" data-card="keep"><h3>WHAT IS KEPT, AND WHETHER</h3>' +
    '<label class="f" style="display:flex;gap:6px;align-items:center"><input type="checkbox" id="bk-enabled"' + (v.enabled ? ' checked' : '') + '> VERIFY AND BACK UP EVERY DAY (04:00)</label>' +
    '<label class="f">DAILY SNAPSHOTS TO KEEP</label><input type="number" id="bk-keep" min="1" max="365" value="' + esc(v.backup_keep) + '">' +
    '<div class="dim-note" id="bk-draft" hidden>Unsaved preferences — not yet the running configuration.</div>' +
    '<div class="dim-note" style="margin-top:6px">Each snapshot holds the whole database. Applies at once.</div>' +
    '<div style="margin-top:10px"><button class="btn" id="bk-save"' + (preferenceReply.waiting() ? ' disabled' : '') + '>' + (preferenceReply.waiting() ? 'Saving…' : 'Save') + '</button></div>' + saidHTML('keep') + '</div>';

  html += '<div class="card" data-card="escrow"><h3>KEYS — THE ESCROW</h3>';
  html += '<div class="kv"><span>snapshots</span><b data-encrypting>' + (v.encrypting ? 'encrypted before they can leave this machine' : '<span class="bad-text">NOT encrypted</span>') + '</b></div>';
  if (!v.encrypting && v.unencrypted_text) html += '<div class="dim-note" data-why style="margin:6px 0 10px">' + esc(v.unencrypted_text) + '</div>';
  html += '<div class="kv"><span>escrow</span><b>' + (v.escrow_checked_at ? (v.escrow_covers ? 'checked ' : 'checked (for a key no longer on this machine) ') + esc(when(v.escrow_checked_at)) : 'none checked') + '</b></div>';
  html += '<div class="dim-note" style="margin:8px 0 12px;line-height:1.5">An escrow file is a sealed copy of this identity\'s two keys — the one that signs its record and the one that opens its snapshots — under a passphrase only you know. It is the only way back if this machine is lost. Keep the file AND the passphrase somewhere the loss of this machine cannot reach: neither is any use without the other, and nobody can recover either for you.</div>';
  if (!v.secrets_ok) {
    html += '<div class="bad-text" data-nosecrets>This connection is not private, so a passphrase typed here would cross the network in the clear. Open the dashboard on the machine itself, or switch on TLS in Settings → Dashboard, and come back.</div>';
  } else if (v.safe) {
    html += '<div class="dim-note">An escrow is made and checked from a normal start.</div>';
  } else if (step === 2) {
    html += '<div data-step="2"><b>Step 2 of 2 — pick the file you just saved.</b>' +
      '<div class="dim-note" style="margin:6px 0 10px">Your browser saved <span style="font-family:var(--mono)">' + esc(savedAs) + '</span>. Pick it back here: a file nobody can find protects nothing, and snapshots are not encrypted until this machine has seen the saved file open.</div>' +
      '<input type="file" id="bk-file2" accept=".age">' +
      '<div style="display:flex;gap:10px;margin-top:10px"><button class="btn" id="bk-check2"' + (waiting ? ' disabled' : '') + '>' + (busy === 'escrow_check' ? 'Checking…' : 'Check it') + '</button>' +
      '<button class="btn ghost" id="bk-cancel2">Start over</button></div></div>';
  } else {
    html += '<div data-step="1"><b>Make an escrow file</b>' +
      '<label class="f">PASSPHRASE (12 CHARACTERS OR MORE)</label><input type="password" id="bk-pass" autocomplete="new-password">' +
      '<label class="f">AGAIN</label><input type="password" id="bk-again" autocomplete="new-password">' +
      '<div style="margin-top:10px"><button class="btn" id="bk-create"' + (waiting ? ' disabled' : '') + '>' + (busy === 'escrow_create' ? 'Sealing…' : 'Create escrow file') + '</button></div></div>' +
      '<div style="margin-top:18px;border-top:1px solid var(--line);padding-top:12px" data-recheck><b>Check an escrow file you already have</b>' +
      '<div class="dim-note" style="margin:6px 0 10px">Worth doing now and then: it proves you still have the right file and still know the passphrase.</div>' +
      '<input type="file" id="bk-file" accept=".age">' +
      '<label class="f">ITS PASSPHRASE</label><input type="password" id="bk-pass2" autocomplete="off">' +
      '<div style="margin-top:10px"><button class="btn ghost" id="bk-check"' + (waiting ? ' disabled' : '') + '>' + (busy === 'escrow_check' ? 'Checking…' : 'Check it') + '</button></div></div>';
  }
  html += saidHTML('escrow') + '</div>';
  return html;
}

function size(bytes) {
  if (bytes >= 1 << 30) return (bytes / (1 << 30)).toFixed(1) + ' GB';
  if (bytes >= 1 << 20) return Math.round(bytes / (1 << 20)) + ' MB';
  return Math.max(1, Math.round(bytes / 1024)) + ' KB';
}

function restoreHTML(v) {
  const busy = busyAction();
  const sets = v.set_aside || [];
  if (!plan && !restarting && !sets.length && !v.restore_failed && !v.restored && said?.at !== 'restore') return '';
  let html = '<div class="card" data-card="restore"><h3>RESTORE</h3>';
  if (restarting) {
    return html + '<div data-restarting><b data-announce>Restarting to restore.</b><div class="dim-note">The restore is performed by the next start, before the record is opened: the snapshot is proved first, what is live is moved aside — never deleted — and the snapshot is put in place. This page reconnects by itself.</div></div></div>';
  }
  if (v.restore_failed) html += '<div class="config-result bad" data-restore-failed data-announce style="margin-bottom:10px">' + esc(v.restore_failed) + '</div>';
  if (v.restored) html += '<div class="dim-note" data-restored style="margin-bottom:10px;line-height:1.5">' + esc(v.restored) + '</div>';
  if (plan) {
    const p = plan;
    html += '<div data-plan><b>Restore from ' + esc(p.source === 'set-aside' ? 'the set kept aside' : 'the snapshot') + ' of ' + esc(when(p.at)) + '</b>' +
      '<div class="dim-note" style="font-family:var(--mono);font-size:11px">' + esc(p.name) + '</div>' +
      '<div class="kv"><span>the record will end at</span><b data-plan-to>record ' + esc(p.restored_to) + '</b></div>' +
      '<div class="kv"><span>set aside</span><b data-plan-aside>' + (p.set_aside < 0 ? 'the live record does not read, so it cannot be counted — all of it is set aside'
        : p.set_aside === 0 ? 'no records: the live record ends where this one does'
        : esc(p.set_aside) + ' record' + (p.set_aside === 1 ? '' : 's') + ' (everything after record ' + esc(p.restored_to) + ', through ' + esc(p.live_through) + '), and every conversation since') + '</b></div>';
    if (p.witnessed < 0) {
      html += '<div class="kv"><span>the witness</span><b data-plan-witness>no witness receipt is known on this machine</b></div>';
    } else if (p.witnessed_set_aside === 0) {
      html += '<div class="kv"><span>the witness</span><b data-plan-witness>will notice nothing: it has attested through record ' + esc(p.witnessed) + ', which this restore keeps</b></div>';
    } else {
      html += '<div class="kv"><span>the witness</span><b data-plan-witness class="bad-text">' + esc(p.witnessed_set_aside) + ' of those records ' + (p.witnessed_set_aside === 1 ? 'has' : 'have') + ' been attested by the witness (through record ' + esc(p.witnessed) + ')</b></div>' +
        '<div class="dim-note" data-plan-anchoring style="line-height:1.5"><b>Read this before you confirm.</b> The witness holds its own record of how far this identity\'s history reached. After this restore, the next time the identity anchors, the witness refuses it as a rollback — and this runtime treats that as an integrity conflict: <b>the identity enters SAFE and its record is frozen</b> until you deal with it. It does not go on running unwitnessed. (A snapshot older than this identity\'s first anchor is the one exception: the witness bookmarks by a key record that snapshot predates, so it may not recognise the restored identity at all — no refusal, and no attestation of what it once held either.) Teaching the witness about a restore has not been built yet; until it is, the ways round it are to restore no further back than the record the witness has attested, or to take this identity off the witness first (Settings → Witness, clear the URL — it applies at the next start, which this restore is), which leaves its later records unattested. These are the facts that work, or that witness\'s operator, would need:</div>' +
        '<pre class="quote" data-witness-facts style="white-space:pre-wrap;font-size:11.5px">' + esc(witnessFacts(p)) + '</pre>' +
        '<button class="btn ghost" id="bk-copyfacts" style="padding:4px 12px;font-size:12px">Copy these</button>';
    }
    html += '<div class="dim-note" style="margin-top:8px;line-height:1.5">Nothing is deleted. What is live now is moved, whole, into a folder under <span style="font-family:var(--mono)">' + esc(p.aside_under) + '</span>, listed here afterwards with a button that puts it back.</div>' +
      '<label class="f">TO CONFIRM, TYPE: ' + esc(p.confirm_text) + '</label><input type="text" id="bk-confirm" autocomplete="off">' +
      '<div style="display:flex;gap:10px;margin-top:10px"><button class="btn" id="bk-restore" disabled>' + (busy === 'restore' ? 'Asking…' : 'Restore and restart') + '</button>' +
      '<button class="btn ghost" id="bk-noplan"' + (busy === 'restore' ? ' disabled' : '') + '>Cancel</button></div></div>';
  }
  if (sets.length) {
    html += '<div style="margin-top:' + (plan ? '18' : '0') + 'px"><b>Kept aside by earlier restores</b>' + sets.map(x =>
      '<div class="tool-row" data-set="' + esc(x.name) + '"><span class="td">' +
      '<b>' + esc(when(x.set_aside_at)) + '</b> · through record ' + esc(x.was_through) + ' · ' + esc(size(x.bytes)) +
      (x.put_back_at ? ' · <span data-putback>put back ' + esc(when(x.put_back_at)) + ' — this folder is a copy of what was put back</span>' : ' · set aside when the record was restored to ' + esc(x.restored_to)) +
      '<div class="dim-note" data-set-path style="font-family:var(--mono);font-size:11px">' + esc(x.path) + '</div></span>' +
      '<button class="btn ghost" data-restore="' + esc(x.name) + '" data-source="set-aside"' + (busy ? ' disabled' : '') + ' style="padding:4px 12px;font-size:12px">Put this back…</button></div>').join('') +
      '<div class="dim-note" style="margin-top:6px">These folders are never removed by this page. Remove one by hand when you are sure you will not want it.</div></div>';
  }
  return html + saidHTML('restore') + '</div>';
}
function witnessFacts(p) {
  return 'witness: ' + (p.witness_url || '(none configured)') + '\n' +
    'identity: ' + (p.identity || '') + '\n' +
    'the witness holds: record ' + p.witnessed + (p.witness_hash ? ' ' + p.witness_hash : '') + '\n' +
    'after the restore the record ends at: ' + p.restored_to;
}

function readFileB64(input, then) {
  if (reader || actionReply.waiting()) return;
  const f = input && input.files && input.files[0];
  if (!f) { say('bad', 'Pick the escrow file first.', 'escrow'); return; }
  if (f.size > 65536) { say('bad', 'That file is larger than any escrow file.', 'escrow'); return; }
  const r = new FileReader();
  reader = r;
  r.onload = () => {
    if (reader !== r) return;
    reader = null;
    const bytes = new Uint8Array(r.result);
    let bin = '';
    for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    then(btoa(bin));
  };
  r.onerror = () => { if (reader !== r) return; reader = null; say('bad', 'That file could not be read.', 'escrow'); };
  r.readAsArrayBuffer(f);
  rerender();
}

export function wireBackups(root) {
  root.querySelectorAll('[data-card] input').forEach(fresh => {
    const field = controls.get(fresh.id);
    const preference = fresh.id === 'bk-keep' || fresh.id === 'bk-enabled';
    if (field) {
      const dirty = field.dirty || valueOf(field.el) !== field.saved;
      if (preference && !dirty) {
        if (fresh.type === 'checkbox') field.el.checked = fresh.checked;
        else field.el.value = fresh.value;
      }
      field.dirty = dirty;
      field.saved = valueOf(fresh);
      fresh.replaceWith(field.el);
    } else controls.set(fresh.id, { el: fresh, saved: valueOf(fresh), dirty: false });
    const heldField = controls.get(fresh.id);
    heldField.el.oninput = heldField.el.onchange = () => { heldField.dirty = true; showPreferenceDraft(); };
  });
  showPreferenceDraft();
  if (focus && root.contains(focus.el)) {
    focus.el.focus();
    if (focus.start !== null) focus.el.setSelectionRange(focus.start, focus.end);
  }
  focus = null;
  const take = $('bk-take');
  if (take) take.onclick = () => ask('take');
  root.querySelectorAll('[data-verify]').forEach(btn => { btn.onclick = () => ask('verify', { name: btn.dataset.verify }); });
  root.querySelectorAll('[data-restore]').forEach(btn => { btn.onclick = () => ask('restore_plan', { name: btn.dataset.restore, source: btn.dataset.source }); });
  const confirmBox = $('bk-confirm'), doRestore = $('bk-restore');
  if (confirmBox && doRestore && plan) {
    confirmBox.oninput = () => { doRestore.disabled = !!busyAction() || confirmBox.value.trim() !== plan.confirm_text; };
    confirmBox.oninput();
    doRestore.onclick = () => ask('restore', { name: plan.name, source: plan.source, confirm: confirmBox.value.trim() });
  }
  const noplan = $('bk-noplan');
  if (noplan) noplan.onclick = () => {
    plan = null;
    if (actionReply.waiting()?.action === 'restore_plan') actionReply.drop();
    clearControls(id => id === 'bk-confirm'); said = null; rerender();
  };
  const copyFacts = $('bk-copyfacts');
  if (copyFacts && plan) copyFacts.onclick = () => { if (navigator.clipboard) navigator.clipboard.writeText(witnessFacts(plan)); copyFacts.textContent = 'Copied'; };
  const save = $('bk-save');
  if (save) save.onclick = () => {
    const keep = parseInt($('bk-keep').value, 10);
    if (!(keep >= 1 && keep <= 365)) { say('bad', 'Keep between 1 and 365 daily snapshots.', 'keep'); return; }
    if (preferenceReply.waiting()) return;
    const enabled = !!$('bk-enabled').checked;
    const fields = new Map(['bk-enabled','bk-keep'].map(id => [id, valueOf($(id))]));
    const requestID = send({ type: 'config_set', config: { 'maintenance.enabled': enabled, 'maintenance.backup_keep': keep } });
    const sent = preferenceReply.arm({ enabled, keep, fields }, requestID);
    say(sent ? 'good' : 'bad', sent ? 'Waiting for the runtime to confirm…' : 'Not connected — preferences were not saved.', 'keep');
  };
  const create = $('bk-create');
  if (create) create.onclick = () => {
    const p = $('bk-pass').value, again = $('bk-again').value;
    if (p.length < 12) { say('bad', 'The passphrase is shorter than 12 characters.', 'escrow'); return; }
    if (p !== again) { say('bad', 'The two passphrases differ.', 'escrow'); return; }
    held = p;
    ask('escrow_create', { passphrase: p, again: again });
  };
  const check2 = $('bk-check2');
  if (check2) check2.onclick = () => readFileB64($('bk-file2'), b64 => ask('escrow_check', { file_b64: b64, passphrase: held }));
  const cancel2 = $('bk-cancel2');
  if (cancel2) cancel2.onclick = () => {
    clearEscrow();
    if (actionReply.waiting()?.action.startsWith('escrow')) actionReply.drop();
    said = null; rerender();
  };
  const check = $('bk-check');
  if (check) check.onclick = () => {
    const p = $('bk-pass2').value;
    if (!p) { say('bad', 'Type the file\'s passphrase.', 'escrow'); return; }
    readFileB64($('bk-file'), b64 => ask('escrow_check', { file_b64: b64, passphrase: p }));
  };
}

export function forgetBackupsSecrets() {
  clearEscrow(); clearControls(id => id === 'bk-confirm');
  actionReply.drop(); said = null; plan = null; restarting = false;
}
export function acceptBackupPreferences(requestID) {
  const saved = S.config?.maintenance;
  if (S.backups && saved) S.backups = { ...S.backups, enabled: saved.enabled, backup_keep: saved.backup_keep };
  const asked = preferenceReply.claim(requestID);
  if (!asked) return false;
  if (saved?.enabled !== asked.enabled || saved?.backup_keep !== asked.keep) {
    say('bad', 'Save could not be verified. Your edits are kept; review the saved preferences.', 'keep');
    return true;
  }
  asked.fields.forEach((value, id) => {
    const field = controls.get(id);
    if (field && valueOf(field.el) === value) { field.saved = value; field.dirty = false; }
  });
  say('good', 'Preferences saved.', 'keep');
  requestBackups();
  return true;
}
export function rejectBackups(requestID, text) {
  const action = actionReply.claim(requestID);
  const preferences = preferenceReply.claim(requestID);
  if (!action && !preferences) return false;
  say('bad', text, preferences ? 'keep' : actionCard(action.action));
  return true;
}
export function backupsConnectionLost() {
  const action = actionReply.waiting();
  const preferences = preferenceReply.drop();
  forgetBackupsSecrets();
  if (action || preferences) said = {kind:'bad', text:'Connection lost — completion is unknown. Review current state before trying again; nothing is automatically retried.', at:action ? actionCard(action.action) : 'keep'};
  rerender();
}
export function backupsReconnected() { if (S.backups) { restarting = false; plan = null; clearControls(id => id === 'bk-confirm'); requestBackups(); } }
export function backupsHolding() { return { step, holding: held !== '', busy: busyAction() }; }
