import { S } from '../state.js';
import { $, esc, bytes, b64, fill, loadingCard } from '../util.js';
import { send } from '../ws.js';
import { pendingSlot } from '../pending.js';
import { holdReloadWhile } from '../overlay.js';
import { sheet } from '../shell/sheet.js';
import { render as osNote } from '../components/os-note/os-note.js';
import W from '../strings/settings-backups.js';
import C from '../strings/components.js';

let styled = false;
function style() { if (!styled) sheet('views/backups.css').then(() => { styled = true; }, () => {}); }
const nameOf = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };
const named = (withName, unnamed) => { const n = nameOf(); return n ? fill(withName, { name: n }) : unnamed; };

let said = null;
let step = 1;
let held = '';
let savedAs = '';
let plan = null;
let restarting = false;
let keyRestarting = false;
let choiceRestarting = false;
let form = null;
let allShown = false;
let restoreOpen = false;
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
  clearControls(id => !['bk-keep', 'bk-enabled', 'bk-confirm'].includes(id) && !id.startsWith('bk-pick-'));
}
function actionCard(action) { return action.startsWith('identity_') ? 'choice' : action.startsWith('escrow') || action === 'key_back' || action === 'snapshot_key_back' ? 'escrow' : action.startsWith('restore') ? 'restore' : 'kept'; }
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
    say('bad', C.notConnected, actionCard(action));
    return;
  }
  said = null;
  if (action === 'restore_plan' || action === 'restore') clearControls(id => id === 'bk-confirm');
  rerender();
}
function rerender() { if (S.renderBackups) S.renderBackups(); if (S.renderRecordState) S.renderRecordState(); }

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
  if (act === 'key_back' && reply.restarting) { clearEscrow(); keyRestarting = true; said = null; rerender(); return; }
  if (act === 'identity_choose' && reply.restarting) { clearControls(id => id.startsWith('bk-pick-')); choiceRestarting = true; said = null; rerender(); return; }
  if (reply.error) {
    said = { kind: 'bad', text: reply.error, at };
  } else if (reply.action === 'take') {
    said = { kind: 'good', text: W.taken, at };
  } else if (reply.action === 'verify') {
    said = { kind: 'good', text: reply.said || W.proved, at };
  } else if (reply.action === 'escrow_create' && reply.file_b64) {
    saveFile(reply.file_name || 'escrow.age', reply.file_b64);
    savedAs = reply.file_name || 'escrow.age';
    step = 2;
    clearControls(id => id === 'bk-pass' || id === 'bk-again');
  } else if (reply.action === 'snapshot_key_back') {
    clearEscrow();
    said = { kind: 'good', text: reply.said || W.keyInPlace, at };
  } else if (reply.action === 'escrow_check') {
    clearEscrow(); form = 'none';
    said = { kind: 'good', text: W.escrowCheckedSaid, at };
  }
  rerender();
}
function saidHTML(at) {
  return said && said.at === at ? '<div class="config-result ' + said.kind + '" data-said="' + at + '" data-announce>' + esc(said.text) + '</div>' : '';
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

const relativeDay = new Intl.RelativeTimeFormat([], { numeric: 'auto' });
const clock = d => d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
function dayOf(d) {
  const start = x => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((start(d) - start(new Date())) / 86400000);
  if (Math.abs(days) <= 1) return { near: true, words: relativeDay.format(days, 'day') };
  return { near: false, words: d.toLocaleDateString([], d.getFullYear() === new Date().getFullYear() ? { day: 'numeric', month: 'long' } : { dateStyle: 'long' }) };
}
function when(iso) {
  const d = new Date(iso || '');
  return isNaN(d) ? String(iso || '') : dayOf(d).words + ', ' + clock(d);
}
const rowWhen = iso => { const w = when(iso); return w.charAt(0).toUpperCase() + w.slice(1); };
function lastWhen(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return { time: String(iso || ''), day: '' };
  const day = dayOf(d);
  return { time: clock(d), day: day.near ? day.words : fill(W.onDay, { date: day.words }) };
}

function passLine(v) {
  const p = v.last_pass;
  const off = v.enabled ? '' : '<span class="backups__bad" data-off>' + esc(W.dailyOff) + '</span>' + (p ? ' ' : '');
  if (!p) return off || (v.safe ? '' : esc(W.noPass));
  if (p.outcome === 'ok') return off + (v.enabled && !v.safe ? '<span class="backups__good">' + esc(W.dailyOn) + '</span> ' : '') + esc(fill(W.dailyLast, lastWhen(p.at)));
  const at = when(p.at);
  if (p.outcome === 'safe') return off + 'SAFE: the record was walked and verifies; no snapshot is made in SAFE — ' + esc(at);
  if (p.outcome === 'disabled') return off + esc(W.passOff) + ' ' + esc(at);
  return off + '<span class="backups__bad">' + esc(W.passFailed) + ' ' + esc(at) +
    (p.failing_since && p.failing_since !== p.at ? ' Failing since ' + esc(when(p.failing_since)) + '.' : '') + '</span>' +
    (p.detail ? '<span class="backups__hint backups__block">' + esc(p.detail) + '</span>' : '');
}

export function backupsHTML() {
  const active = document.activeElement;
  focus = active && controls.get(active.id)?.el === active
    ? { el: active, start: active.selectionStart, end: active.selectionEnd } : null;
  const v = S.backups;
  if (!v) return loadingCard('backups');
  if (!v.secrets_ok || (v.safe && !v.key_absent && !v.snapshot_key_mismatch)) clearEscrow();
  style();
  const busy = busyAction();
  const waiting = !!busy || !!reader;
  return '<div class="backups" data-backups>' +
    '<header class="backups__head"><h2 class="backups__title">' + esc(W.title) + '</h2>' +
    '<p class="backups__intro">' + esc(named(W.intro, W.introUnnamed)) + '</p></header>' +
    choiceHTML(v, busy, waiting) + keptHTML(v, busy, waiting) + wayBackHTML(v, busy, waiting) + restoreHTML(v) + '</div>';
}

function keptHTML(v, busy, waiting) {
  const snaps = v.snapshots || [];
  const shown = allShown ? snaps : snaps.slice(0, 3);
  let html = '<section class="backups__part" data-card="kept" aria-labelledby="bk-kept" aria-describedby="bk-kept-line">' +
    '<div class="backups__top"><h3 id="bk-kept" class="backups__h3">' + esc(W.kept) + '</h3>' +
    '<button class="c-button" id="bk-take"' + (waiting || v.safe || !v.enabled ? ' disabled' : '') + '>' + esc(busy === 'take' ? W.taking : W.takeNow) + '</button></div>' +
    '<p id="bk-kept-line" class="backups__line" data-lastpass>' + passLine(v) + '</p>';
  if (v.waiting) html += '<p class="backups__line backups__bad">' + esc(W.degraded) + '</p>';
  else if (v.safe) html += '<p class="backups__line backups__bad">' + 'This start is SAFE: the record is walked every day and no snapshot is made.' + '</p>';
  if (shown.length) {
    html += '<ul class="backups__rows">' + shown.map(s =>
      '<li class="backups__snap" data-snapshot="' + esc(s.name) + '"><span>' +
      esc([rowWhen(s.at), fill(W.throughRecord, { n: s.record })].concat(s.on_demand ? [W.askedFor] : []).join(' · ')) + ' · ' +
      (s.encrypted ? esc(W.encrypted) : '<span class="backups__bad">' + esc(W.notEncrypted) + '</span>') + '</span>' +
      '<span class="backups__acts">' +
      (s.encrypted ? '<form method="post" action="/backups/download"><button class="backups__act" type="submit" name="name" value="' + esc(s.name) + '" data-download="' + esc(s.name) + '">' + esc(W.download) + '</button></form>' : '') +
      '<button class="backups__act" data-verify="' + esc(s.name) + '"' + (waiting || v.safe || !v.enabled ? ' disabled' : '') + '>' + esc(busy === 'verify:' + s.name ? W.proving : W.prove) + '</button>' +
      '<button class="backups__act" data-restore="' + esc(s.name) + '" data-source="snapshot"' + (waiting ? ' disabled' : '') + '>' + esc(W.restore) + '</button>' +
      '</span></li>').join('') + '</ul>';
    if (snaps.length > shown.length) {
      const oldest = dayOf(new Date(snaps[snaps.length - 1].at)).words;
      html += '<p class="backups__more">' + esc(fill(W.more, { n: snaps.length - shown.length, date: oldest })) +
        '<button class="backups__act" data-snaps-all>' + esc(fill(W.all, { n: snaps.length })) + '</button></p>';
    }
  } else {
    html += '<p class="backups__hint">No snapshot yet.</p>';
  }
  if (v.next_pass || v.chain) html += '<details class="backups__details"><summary>' + esc(C.details) + '</summary>' +
    (v.next_pass ? '<div class="sp-kv"><span>next pass</span><b>' + esc(when(v.next_pass)) + '</b></div>' : '') +
    (v.chain ? '<div class="sp-kv"><span>record</span><b>' + esc(v.chain) + '</b></div>' : '') + '</details>';
  html += saidHTML('kept');
  html += '<div class="backups__keep" data-card="keep">' +
    '<label class="backups__field"><span class="backups__words"><span>' + esc(W.keepDays) + '</span>' +
    '<span class="backups__hint">' + esc(W.keepDaysHint) + '</span></span>' +
    '<input type="number" id="bk-keep" min="1" max="365" value="' + esc(v.backup_keep) + '"></label>' +
    '<label class="backups__switch"><input type="checkbox" role="switch" class="backups__sw" id="bk-enabled"' + (v.enabled ? ' checked' : '') + '>' + esc(W.verifyDaily) + '</label>' +
    '<p class="backups__hint" id="bk-draft" hidden>' + esc(W.unsaved) + '</p>' +
    '<div class="backups__buttons"><button class="c-button" id="bk-save"' + (preferenceReply.waiting() ? ' disabled' : '') + '>' + esc(preferenceReply.waiting() ? C.waiting : C.save) + '</button></div>' +
    saidHTML('keep') + '</div>';
  return html + '</section>';
}

function wayBackHTML(v, busy, waiting) {
  const snaps = v.snapshots || [];
  let html = '<section class="backups__part" data-card="escrow" aria-labelledby="bk-way" aria-describedby="bk-way-what">' +
    '<h3 id="bk-way" class="backups__h3">' + esc(W.wayBack) + '<span class="backups__h3-more"> · ' + esc(W.wayBackHint) + '</span></h3>' +
    '<p class="backups__line" data-escrow-line>' + (v.escrow_checked_at
      ? '<span class="backups__ok">' + esc(fill(v.escrow_covers ? W.escrowChecked : W.escrowCheckedOther, { when: when(v.escrow_checked_at) })) + '</span>'
      : '<span class="backups__bad">' + esc(W.escrowNone) + '</span>') + '</p>';
  if (!v.encrypting) html += '<p class="backups__line backups__bad" data-encrypting>' + (v.unencrypted_text ? '<span data-why>' + esc(v.unencrypted_text) + '</span>' : esc(W.notEncryptedNow)) + '</p>';
  html += '<p id="bk-way-what" class="backups__hint">' + esc(named(W.escrowExplained, W.escrowExplainedUnnamed)) + ' ' + esc(named(W.escrowComesBack, W.escrowComesBackUnnamed)) + '</p>';
  const kept = snaps.some(s => s.encrypted);
  const note = osNote({ text: kept ? W.keepOff : W.downloadNeedsEscrow, live: false }).outerHTML.replace('<div class="c-os-note', '<div data-offsite class="c-os-note');
  html += (kept ? '' : note) + escrowHTML(v, busy, waiting) + (kept ? note : '');
  return html + saidHTML('escrow') + '</section>';
}

function escrowHTML(v, busy, waiting) {
  if (!v.secrets_ok) {
    return '<p class="backups__line backups__bad" data-nosecrets>' + esc(W.noSecrets) + '</p>';
  }
  if (keyRestarting) {
    return '<div data-key-restarting><b data-announce>' + esc(W.keyRestarting) + '</b><p class="backups__hint">' + (v.record_absent ? esc(W.keyRestartRestore) : esc(W.keyRestartCheck)) + ' ' + esc(W.reconnects) + '</p></div>';
  }
  if (v.key_absent) {
    return '<div class="backups__form" data-keyback><b>' + esc(W.keyBackTitle) + '</b>' +
      '<p class="backups__hint">' + (v.record_absent
        ? 'Neither the record nor the key that signs it is on this machine, and a snapshot of the record is, so this start is SAFE. Pick your escrow file and type its passphrase. The key in it is put in place only if it is the key a snapshot kept here names; the identity then restarts at the page that restores a snapshot.'
        : 'The record is on this machine and the key that signs it is not, so this start is SAFE. Pick your escrow file and type its passphrase. The key in it is put in place only if it is the key this record names; the identity then restarts and checks its whole record under it.') + '</p>' +
      '<input type="file" id="bk-keyfile" accept=".age">' +
      '<label for="bk-keypass">' + esc(W.passphrase) + '</label><input type="password" id="bk-keypass" autocomplete="off">' +
      '<div class="backups__buttons"><button class="c-button c-button--primary" id="bk-keyback"' + (waiting ? ' disabled' : '') + '>' + (busy === 'key_back' ? esc(W.keyBackBusy) : esc(W.keyBackGo)) + '</button></div></div>';
  }
  if (v.key_unnamed) {
    return '<div data-key-unnamed><b>' + esc(W.keyBackTitle) + '</b>' +
      '<p class="backups__hint">' + esc(W.keyUnnamed) + '</p></div>';
  }
  if (v.safe && v.snapshot_key_mismatch) {
    return '<div class="backups__form" data-snapkeyback><b>' + esc(W.snapKeyGo) + '</b>' +
      '<p class="backups__hint">' + (v.snapshot_key_absent
        ? esc(fill(W.snapKeyAbsent, { newest: v.snapshot_key_mismatch }))
        : esc(fill(W.snapKeyMismatch, { newest: v.snapshot_key_mismatch }))) + '</p>' +
      '<input type="file" id="bk-snapfile" accept=".age">' +
      '<label for="bk-snappass">' + esc(W.passphrase) + '</label><input type="password" id="bk-snappass" autocomplete="off">' +
      '<div class="backups__buttons"><button class="c-button c-button--primary" id="bk-snapback"' + (waiting ? ' disabled' : '') + '>' + (busy === 'snapshot_key_back' ? esc(W.snapKeyBusy) : esc(W.snapKeyGo)) + '</button></div></div>';
  }
  if (v.safe) return '<p class="backups__hint">' + esc(W.safeEscrow) + '</p>';
  if (step === 2) {
    return '<div class="backups__form" data-step="2"><b>' + esc(W.step2) + '</b>' +
      '<p class="backups__hint">' + fill(esc(W.step2Saved), { file: '<span class="backups__code">' + esc(savedAs) + '</span>' }) + '</p>' +
      '<input type="file" id="bk-file2" accept=".age">' +
      '<div class="backups__buttons"><button class="c-button c-button--primary" id="bk-check2"' + (waiting ? ' disabled' : '') + '>' + (busy === 'escrow_check' ? esc(W.checking) : esc(W.checkIt)) + '</button>' +
      '<button class="c-button c-button--quiet" id="bk-cancel2">' + esc(W.startOver) + '</button></div></div>';
  }
  const open = shownForm(v);
  let html = '<div class="backups__buttons">' +
    '<button class="c-button" data-escrow-open="check" aria-expanded="' + (open === 'check') + '"' + (waiting ? ' disabled' : '') + '>' + esc(W.check) + '</button>' +
    '<button class="c-button" data-escrow-open="make" aria-expanded="' + (open === 'make') + '"' + (waiting ? ' disabled' : '') + '>' + esc(v.escrow_checked_at ? W.makeNew : W.makeFirst) + '</button></div>';
  if (open === 'make') {
    html += v.export_ok
      ? '<div class="backups__form" data-step="1"><p class="backups__hint">' + esc(W.makeNewHint) + '</p>' +
        '<label for="bk-pass">' + esc(W.passphrase) + '</label><input type="password" id="bk-pass" autocomplete="new-password" aria-describedby="bk-pass-hint">' +
        '<p id="bk-pass-hint" class="backups__hint">' + esc(W.passphraseHint) + '</p>' +
        '<label for="bk-again">' + esc(W.passphraseAgain) + '</label><input type="password" id="bk-again" autocomplete="new-password">' +
        '<div class="backups__buttons"><button class="c-button c-button--primary" id="bk-create"' + (waiting ? ' disabled' : '') + '>' + (busy === 'escrow_create' ? esc(named(W.exporting, W.exportingUnnamed)) : esc(W.createFile)) + '</button>' +
        '<button class="c-button c-button--quiet" data-escrow-close>' + esc(C.cancel) + '</button></div></div>'
      : '<div class="backups__form" data-step="1" data-noexport><p class="backups__line backups__bad">' + esc(v.export_why_not || esc(W.noExport)) + '</p></div>';
  } else if (open === 'check') {
    html += '<div class="backups__form" data-recheck><p class="backups__hint">' + esc(W.recheckHint) + '</p>' +
      '<input type="file" id="bk-file" accept=".age">' +
      '<label for="bk-pass2">' + esc(W.passphrase) + '</label><input type="password" id="bk-pass2" autocomplete="off">' +
      '<div class="backups__buttons"><button class="c-button c-button--primary" id="bk-check"' + (waiting ? ' disabled' : '') + '>' + (busy === 'escrow_check' ? esc(W.checking) : esc(W.checkIt)) + '</button>' +
      '<button class="c-button c-button--quiet" data-escrow-close>' + esc(C.cancel) + '</button></div></div>';
  }
  return html;
}
function shownForm(v) {
  if (form) return form === 'none' ? null : form;
  return v.escrow_checked_at ? null : 'make';
}

function choiceHTML(v, busy, waiting) {
  const c = v.identity_choice;
  if (!c && !choiceRestarting) return '';
  let html = '<section class="backups__part" data-card="choice" aria-labelledby="bk-choice" aria-describedby="bk-choice-why">' +
    '<h3 id="bk-choice" class="backups__h3">Which files are this identity\'s</h3>';
  if (choiceRestarting) {
    return html + '<div data-choice-restarting><b data-announce>' + esc(W.choiceRestarting) + '</b><p id="bk-choice-why" class="backups__hint">' + esc(W.choiceRestartingSays) + ' ' + esc(W.reconnects) + '</p></div></section>';
  }
  const record = c.files.find(f => f.key === 'ledger_path');
  const records = (record && record.found) || [];
  html += '<p id="bk-choice-why" class="backups__line">' + esc(W.choiceWhy) + ' ' +
    (records.length
      ? esc(W.choiceRecords)
      : esc(W.choiceNone)) + '</p>';
  c.files.forEach(f => {
    const found = f.found || [];
    html += '<div data-choice-file="' + esc(f.key) + '"><b>' + esc(f.what.charAt(0).toUpperCase() + f.what.slice(1)) + '</b>';
    if (!found.length) {
      html += '<p class="backups__hint">' + esc(W.noneFound) + (records.length ? ' It goes to <span class="backups__code">' + esc(f.standard) + '</span>, where a first start puts it.' : '') + '</p>';
    } else if (!records.length) {
      found.forEach(x => { html += '<p class="backups__line"><span class="backups__code">' + esc(x.path) + '</span> · ' + esc(bytes(x.bytes)) + (x.modified ? ' · last changed ' + esc(when(x.modified)) : '') + '</p>'; });
    } else {
      found.forEach((x, i) => {
        html += '<label class="backups__switch"><input type="radio" name="bk-pick-' + esc(f.key) + '" id="bk-pick-' + esc(f.key) + '-' + i + '" value="' + esc(x.path) + '"' + (found.length === 1 ? ' checked' : '') + '>' +
          '<span><span class="backups__code">' + esc(x.path) + '</span> · ' + esc(bytes(x.bytes)) + (x.modified ? ' · last changed ' + esc(when(x.modified)) : '') + '</span></label>';
      });
    }
    html += '</div>';
  });
  if (!records.length) return html + saidHTML('choice') + '</section>';
  html += '<div class="backups__buttons"><button class="c-button c-button--primary" id="bk-choose"' + (waiting ? ' disabled' : '') + '>' + (busy === 'identity_choose' ? esc(W.choosing) : esc(W.chooseGo)) + '</button></div>';
  return html + saidHTML('choice') + '</section>';
}

function restoreHTML(v) {
  const busy = busyAction();
  const sets = v.set_aside || [];
  const due = !!(plan || restarting || v.restore_failed || said?.at === 'restore');
  let html = '<details class="backups__restore" data-card="restore" aria-describedby="bk-restore-what"' + (due || restoreOpen ? ' open' : '') + '>' +
    '<summary><h3 id="bk-restore" class="backups__h3">' + esc(W.restoreWhat) + '</h3><span class="backups__hint"> · ' + esc(W.restoreWhatHint) + '</span></summary>' +
    '<p id="bk-restore-what" class="backups__line">' + esc(W.restoreExplained) + ' ' + esc(W.restoreConfirmHow) + '</p>';
  if (restarting) {
    return html + '<div data-restarting><b data-announce>' + esc(W.restoreRestarting) + '</b><p class="backups__hint">' + esc(W.restoreRestartingSays) + ' ' + esc(W.reconnects) + '</p></div></details>';
  }
  if (v.restore_failed) html += '<div class="config-result bad" data-restore-failed data-announce>' + esc(v.restore_failed) + '</div>';
  if (v.restored) html += '<p class="backups__hint" data-restored>' + esc(v.restored) + '</p>';
  if (plan) {
    const p = plan;
    html += '<div class="backups__plan" data-plan><b>Restore from ' + esc(p.source === 'set-aside' ? 'the set kept aside' : 'the snapshot') + ' of ' + esc(when(p.at)) + '</b>' +
      '<p class="backups__hint backups__code">' + esc(p.name) + '</p>' +
      '<div class="sp-kv"><span>the record will end at</span><b data-plan-to>record ' + esc(p.restored_to) + '</b></div>' +
      '<div class="sp-kv"><span>set aside</span><b data-plan-aside>' + (p.no_live_record ? esc(W.planNoLive)
        : p.set_aside < 0 ? esc(W.planUnread)
        : p.set_aside === 0 ? esc(W.planNone)
        : esc(p.set_aside) + ' record' + (p.set_aside === 1 ? '' : 's') + ' (everything after record ' + esc(p.restored_to) + ', through ' + esc(p.live_through) + '), and every conversation since') + '</b></div>';
    if (p.witnessed < 0) {
      html += '<div class="sp-kv"><span>the witness</span><b data-plan-witness>' + 'no witness receipt is known on this machine' + '</b></div>';
    } else if (p.witnessed_set_aside === 0) {
      html += '<div class="sp-kv"><span>the witness</span><b data-plan-witness>will notice nothing: it has attested through record ' + esc(p.witnessed) + ', which this restore keeps</b></div>';
    } else {
      html += '<div class="sp-kv"><span>the witness</span><b data-plan-witness class="backups__bad">' + esc(p.witnessed_set_aside) + ' of those records ' + (p.witnessed_set_aside === 1 ? 'has' : 'have') + ' been attested by the witness (through record ' + esc(p.witnessed) + ')</b></div>' +
        '<p class="backups__hint" data-plan-anchoring><b>Read this before you confirm.</b> ' + 'The witness holds its own record of how far this identity\'s history reached. After this restore, the next time the identity anchors, the witness refuses it as a rollback — and this runtime treats that as an integrity conflict:' + ' <b>the identity enters SAFE and its record is frozen</b> ' + 'until you deal with it. It does not go on running unwitnessed. (A snapshot older than this identity\'s first anchor is the one exception: the witness bookmarks by a key record that snapshot predates, so it may not recognise the restored identity at all — no refusal, and no attestation of what it once held either.) Teaching the witness about a restore has not been built yet; until it is, the ways round it are to restore no further back than the record the witness has attested, or to take this identity off the witness first (Settings → Technical → Verification, clear the URL — it applies at the next start, which this restore is), which leaves its later records unattested. These are the facts that work, or that witness\'s operator, would need:' + '</p>' +
        '<pre class="quote backups__facts" data-witness-facts>' + esc(witnessFacts(p)) + '</pre>' +
        '<div class="backups__buttons"><button class="c-button" id="bk-copyfacts">Copy these</button></div>';
    }
    html += '<p class="backups__hint">' + fill(esc(W.planAside), { folder: '<span class="backups__code">' + esc(p.aside_under) + '</span>' }) + '</p>' +
      '<div class="backups__form"><label for="bk-confirm">' + esc(fill(W.restoreConfirm, { text: p.confirm_text })) + '</label><input type="text" id="bk-confirm" autocomplete="off"></div>' +
      '<div class="backups__buttons"><button class="c-button c-button--danger" id="bk-restore" disabled>' + esc(busy === 'restore' ? C.waiting : W.restoreGo) + '</button>' +
      '<button class="c-button c-button--quiet" id="bk-noplan"' + (busy === 'restore' ? ' disabled' : '') + '>' + esc(W.restoreCancel) + '</button></div></div>';
  }
  if (sets.length) {
    html += '<div><b>' + esc(W.setsTitle) + '</b><ul class="backups__rows">' + sets.map(x =>
      '<li class="backups__snap" data-set="' + esc(x.name) + '"><span>' +
      esc(when(x.set_aside_at)) + ' · through record ' + esc(x.was_through) + ' · ' + esc(bytes(x.bytes)) +
      (x.put_back_at ? ' · <span data-putback>put back ' + esc(when(x.put_back_at)) + ' — ' + esc(W.putBackCopy) + '</span>' : ' · set aside when the record was restored to ' + esc(x.restored_to)) +
      '<span class="backups__hint backups__code backups__block" data-set-path>' + esc(x.path) + '</span></span>' +
      '<span class="backups__acts"><button class="backups__act" data-restore="' + esc(x.name) + '" data-source="set-aside"' + (busy ? ' disabled' : '') + '>' + esc(W.putBack) + '</button></span></li>').join('') + '</ul>' +
      '<p class="backups__hint">' + esc(W.setsNote) + '</p></div>';
  } else if (!plan && !v.restored && !v.restore_failed) {
    html += '<p class="backups__hint" data-restore-none>' + esc(W.restoreNone) + '</p>';
  }
  return html + saidHTML('restore') + '</details>';
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
  if (!f) { say('bad', W.pickFile, 'escrow'); return; }
  if (f.size > 65536) { say('bad', W.fileTooLarge, 'escrow'); return; }
  const r = new FileReader();
  reader = r;
  r.onload = () => {
    if (reader !== r) return;
    reader = null;
    then(b64(r.result));
  };
  r.onerror = () => { if (reader !== r) return; reader = null; say('bad', W.fileUnreadable, 'escrow'); };
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
  const choose = $('bk-choose');
  if (choose && S.backups && S.backups.identity_choice) choose.onclick = () => {
    const picked = {};
    for (const f of S.backups.identity_choice.files) {
      if (!(f.found || []).length) continue;
      const on = root.querySelector('input[name="bk-pick-' + f.key + '"]:checked');
      if (!on) { say('bad', fill(W.chooseOne, { what: f.what }), 'choice'); return; }
      picked[f.key] = on.value;
    }
    ask('identity_choose', { picked });
  };
  const take = $('bk-take');
  if (take) take.onclick = () => ask('take');
  root.querySelectorAll('[data-snaps-all]').forEach(btn => { btn.onclick = () => { allShown = true; rerender(); }; });
  root.querySelectorAll('[data-escrow-open]').forEach(btn => { btn.onclick = () => {
    const which = btn.dataset.escrowOpen;
    if (shownForm(S.backups || {}) === which) return;
    clearEscrow(); form = which; said = null; rerender();
    $(which === 'make' ? 'bk-pass' : 'bk-file')?.focus();
  }; });
  root.querySelectorAll('[data-escrow-close]').forEach(btn => { btn.onclick = () => {
    const asked = actionReply.waiting()?.action.startsWith('escrow');
    clearEscrow(); if (asked) { actionReply.drop(); requestBackups(); }
    form = 'none'; said = null; rerender();
  }; });
  root.querySelectorAll('details[data-card="restore"] > summary').forEach(s => { s.onclick = () => { restoreOpen = !s.parentElement.open; }; });
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
  if (copyFacts && plan) copyFacts.onclick = () => { if (navigator.clipboard) navigator.clipboard.writeText(witnessFacts(plan)); copyFacts.textContent = C.copied; };
  const save = $('bk-save');
  if (save) save.onclick = () => {
    const keep = parseInt($('bk-keep').value, 10);
    if (!(keep >= 1 && keep <= 365)) { say('bad', W.keepDaysRange, 'keep'); return; }
    if (preferenceReply.waiting()) return;
    const enabled = !!$('bk-enabled').checked;
    const fields = new Map(['bk-enabled','bk-keep'].map(id => [id, valueOf($(id))]));
    const requestID = send({ type: 'config_set', config: { 'maintenance.enabled': enabled, 'maintenance.backup_keep': keep } });
    const sent = preferenceReply.arm({ enabled, keep, fields }, requestID);
    say(sent ? 'good' : 'bad', sent ? C.waiting : W.prefsNotSaved, 'keep');
  };
  const create = $('bk-create');
  if (create) create.onclick = () => {
    const p = $('bk-pass').value, again = $('bk-again').value;
    if (p.length < 12) { say('bad', W.passphraseShort, 'escrow'); return; }
    if (p !== again) { say('bad', W.passphrasesDiffer, 'escrow'); return; }
    held = p;
    ask('escrow_create', { passphrase: p, again: again });
  };
  const check2 = $('bk-check2');
  if (check2) check2.onclick = () => readFileB64($('bk-file2'), b64 => ask('escrow_check', { file_b64: b64, passphrase: held }));
  const cancel2 = $('bk-cancel2');
  if (cancel2) cancel2.onclick = () => {
    const asked = actionReply.waiting()?.action.startsWith('escrow');
    clearEscrow(); if (asked) { actionReply.drop(); requestBackups(); }
    said = null; rerender();
  };
  const keyBack = $('bk-keyback');
  if (keyBack) keyBack.onclick = () => {
    const p = $('bk-keypass').value;
    if (!p) { say('bad', W.typePassphrase, 'escrow'); return; }
    readFileB64($('bk-keyfile'), b64 => ask('key_back', { file_b64: b64, passphrase: p }));
  };
  const snapBack = $('bk-snapback');
  if (snapBack) snapBack.onclick = () => {
    const p = $('bk-snappass').value;
    if (!p) { say('bad', W.typePassphrase, 'escrow'); return; }
    readFileB64($('bk-snapfile'), b64 => ask('snapshot_key_back', { file_b64: b64, passphrase: p }));
  };
  const check = $('bk-check');
  if (check) check.onclick = () => {
    const p = $('bk-pass2').value;
    if (!p) { say('bad', W.typePassphrase, 'escrow'); return; }
    readFileB64($('bk-file'), b64 => ask('escrow_check', { file_b64: b64, passphrase: p }));
  };
}

export function forgetBackupsSecrets() {
  clearEscrow(); clearControls(id => id === 'bk-confirm');
  actionReply.drop(); said = null; plan = null; restarting = false; keyRestarting = false; choiceRestarting = false; form = null;
}
export function acceptBackupPreferences(requestID) {
  const saved = S.config?.maintenance;
  if (S.backups && saved) S.backups = { ...S.backups, enabled: saved.enabled, backup_keep: saved.backup_keep };
  const asked = preferenceReply.claim(requestID);
  if (!asked) return false;
  if (saved?.enabled !== asked.enabled || saved?.backup_keep !== asked.keep) {
    say('bad', W.saveUnverified, 'keep');
    return true;
  }
  asked.fields.forEach((value, id) => {
    const field = controls.get(id);
    if (field && valueOf(field.el) === value) { field.saved = value; field.dirty = false; }
  });
  say('good', W.prefsSaved, 'keep');
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
  if (action || preferences) said = {kind:'bad', text:W.connectionLost, at:action ? actionCard(action.action) : 'keep'};
  rerender();
}
export function backupsReconnected() { if (S.backups) { restarting = false; keyRestarting = false; choiceRestarting = false; plan = null; clearControls(id => id === 'bk-confirm'); requestBackups(); } }
export function backupsHolding() { return { step, holding: held !== '', busy: busyAction() }; }
