//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestAPersonBacksUpProvesAndEscrowsTheKeysFromThePage(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { backupsHTML, wireBackups, applyBackups, backupsHolding, forgetBackupsSecrets } from './views/backups.js';
import { assert, run } from './__harness.js';

const st = document.getElementById('settings-stack');
S.renderBackups = () => { st.innerHTML = backupsHTML(); wireBackups(st); };
const last = () => frames[frames.length - 1];
// The page reads a picked file asynchronously, and a save asks for the view
// again a moment later: an act is WAITED FOR BY NAME, never slept for and
// never assumed to be the last frame.
const acts = a => frames.filter(f => f.type === 'backups' && f.backups.action === a);
const until = async (cond, what) => { for (let i = 0; i < 400; i++) { if (cond()) return; await new Promise(r => setTimeout(r, 25)); } throw new Error('never happened: ' + what); };
const text = sel => (st.querySelector(sel) || { textContent: '' }).textContent;
const view = over => Object.assign({ enabled: true, backup_keep: 8, next_pass: '2026-09-21T08:00:00Z',
  snapshots: [{ name: 'ledger-20260920T080005Z-seq1402', at: '2026-09-20T08:00:05Z', record: 1402 }],
  last_pass: { at: '2026-09-20T08:00:10Z', outcome: 'ok' }, encrypting: false, unencrypted: 'no_escrow_checked',
  unencrypted_text: 'No escrow of the keys has been checked.', escrow_covers: false, secrets_ok: true }, over || {});

run(async () => {
  // SEE. What is kept, what the last pass said, and why snapshots are not encrypted — in words, not codes.
  applyBackups({ action: 'status', view: view() });
  assert(/through record 1402/.test(text('[data-snapshot]')) && /daily/.test(text('[data-snapshot]')) && /not encrypted/.test(text('[data-snapshot]')), 'the snapshot row: ' + text('[data-snapshot]'));
  assert(/proved to restore/.test(text('[data-lastpass]')), 'the last pass: ' + text('[data-lastpass]'));
  assert(/NOT encrypted/.test(text('[data-encrypting]')) && /No escrow of the keys/.test(text('[data-why]')), 'why not encrypted is said in words');
  assert(!/aii |\/data\/|\.sec|command/i.test(st.textContent), 'the page tells nobody to use a terminal: ' + st.textContent);

  // BACK UP NOW. One press; the button waits; the list gains the one that was asked for.
  document.getElementById('bk-take').click();
  assert(last().type === 'backups' && last().backups.action === 'take', 'Back up now sends take: ' + JSON.stringify(last()));
  assert(document.getElementById('bk-take').disabled && /Taking/.test(document.getElementById('bk-take').textContent), 'the button waits while the snapshot is taken');
  applyBackups({ action: 'take', view: view({ snapshots: [{ name: 'ledger-20260920T181500Z-seq1410-ondemand', at: '2026-09-20T18:15:00Z', record: 1410, on_demand: true }, { name: 'ledger-20260920T080005Z-seq1402', at: '2026-09-20T08:00:05Z', record: 1402 }] }) });
  assert(st.querySelectorAll('[data-snapshot]').length === 2 && /asked for/.test(text('[data-snapshot]')), 'the new snapshot is listed first and marked asked for');
  assert(/taken and proved/.test(text('[data-card="kept"] [data-said]')), 'the page says what happened, beside the button that was pressed: ' + text('[data-said]'));

  // PROVE IT RESTORES. Named snapshot in, one sentence out; a refusal is said as one.
  st.querySelector('[data-verify="ledger-20260920T080005Z-seq1402"]').click();
  assert(last().backups.action === 'verify' && last().backups.name === 'ledger-20260920T080005Z-seq1402', 'verify names the snapshot');
  applyBackups({ action: 'verify', view: view(), error: 'ledger-20260920T080005Z-seq1402 does NOT prove' });
  assert(/does NOT prove/.test(text('[data-said]')) && st.querySelector('[data-said]').classList.contains('bad'), 'a snapshot that no longer proves is said in red');

  // KEEP. Through the config door, as every other setting.
  document.getElementById('bk-keep').value = '14';
  document.getElementById('bk-save').click();
  const saved = frames.filter(f => f.type === 'config_set').pop();
  assert(saved && saved.config['maintenance.backup_keep'] === 14 && saved.config['maintenance.enabled'] === true, 'keep is saved through the door: ' + JSON.stringify(saved));

  // MAKE THE ESCROW. The page holds two passphrases to each other and to a floor before anything is sent.
  document.getElementById('bk-pass').value = 'short'; document.getElementById('bk-again').value = 'short';
  document.getElementById('bk-create').click();
  assert(acts('escrow_create').length === 0 && /shorter than 12/.test(text('[data-said]')), 'a short passphrase is refused on the page');
  document.getElementById('bk-pass').value = 'correct horse battery'; document.getElementById('bk-again').value = 'correct horse bettery';
  document.getElementById('bk-create').click();
  assert(acts('escrow_create').length === 0 && /differ/.test(text('[data-said]')), 'two passphrases that differ are refused on the page');
  document.getElementById('bk-pass').value = 'correct horse battery'; document.getElementById('bk-again').value = 'correct horse battery';
  document.getElementById('bk-create').click();
  const made = acts('escrow_create');
  assert(made.length === 1 && made[0].backups.passphrase === 'correct horse battery' && made[0].backups.again === 'correct horse battery', 'create sends both, for the server to hold to each other');

  // The server answers with the sealed file: the browser saves it, and the card moves to step 2.
  let downloaded = null;
  const click = HTMLAnchorElement.prototype.click;
  HTMLAnchorElement.prototype.click = function () { downloaded = { name: this.download, href: this.href }; };
  applyBackups({ action: 'escrow_create', view: view(), file_b64: btoa('sealed-escrow-bytes'), file_name: 'escrow-501265c63ca1-20260920.age' });
  HTMLAnchorElement.prototype.click = click;
  assert(downloaded && downloaded.name === 'escrow-501265c63ca1-20260920.age' && /^blob:/.test(downloaded.href), 'the browser is handed the file to save: ' + JSON.stringify(downloaded));
  assert(st.querySelector('[data-step="2"]') && /pick the file you just saved/i.test(text('[data-step="2"]')), 'the card moves to step 2 and says why');
  assert(!st.innerHTML.includes('correct horse battery'), 'THE PASSPHRASE IS NEVER RENDERED');
  assert(backupsHolding().holding, 'the page holds the passphrase for step 2, in memory only');
  assert(!/encrypted before/.test(text('[data-encrypting]')), 'making the file turns nothing on: no receipt is signed until the saved file is picked back');

  // PROVE THE ESCROW. The person picks the saved file; the passphrase comes from the page's memory.
  const pick = (id, bytes) => { const dt = new DataTransfer(); dt.items.add(new File([bytes], 'escrow.age')); document.getElementById(id).files = dt.files; };
  pick('bk-file2', new Uint8Array([1, 2, 3, 250]));
  document.getElementById('bk-check2').click();
  await until(() => acts('escrow_check').length === 1, 'the check of the picked file');
  const first = acts('escrow_check')[0].backups;
  assert(first.file_b64 === btoa(String.fromCharCode(1, 2, 3, 250)) && first.passphrase === 'correct horse battery', 'check sends the picked file, byte for byte, with the held passphrase: ' + JSON.stringify(first));
  // A wrong file: said, and the person stays on step 2 to pick another.
  applyBackups({ action: 'escrow_check', view: view(), error: 'That file did not open.' });
  assert(st.querySelector('[data-step="2"]') && /did not open/.test(text('[data-said]')), 'a file that does not open keeps the person on step 2');
  // The right one: checked, encryption on, and nothing of the passphrase stays.
  applyBackups({ action: 'escrow_check', view: view({ encrypting: true, unencrypted: '', unencrypted_text: '', escrow_covers: true, escrow_checked_at: '2026-09-20T18:20:00Z' }) });
  assert(/encrypted before they can leave/.test(text('[data-encrypting]')) && /Checked:/.test(text('[data-said]')), 'checked, and the page says snapshots are now encrypted');
  assert(!backupsHolding().holding && backupsHolding().step === 1, 'THE PASSPHRASE IS FORGOTTEN once the check is answered');
  assert(st.querySelector('[data-card="escrow"] [data-said]') && !st.querySelector('[data-card="kept"] [data-said]'), 'the answer to an escrow act is said in the escrow card, where the person is looking');

  // LATER: check a file again, typing its passphrase.
  pick('bk-file', new Uint8Array([9, 9]));
  document.getElementById('bk-pass2').value = 'correct horse battery';
  document.getElementById('bk-check').click();
  await until(() => acts('escrow_check').length === 2, 'the later check');
  const checks = acts('escrow_check');
  assert(checks.length === 2 && checks[1].backups.passphrase === 'correct horse battery' && checks[1].backups.file_b64 === btoa(String.fromCharCode(9, 9)), 'a later check sends the picked file and the typed passphrase: ' + JSON.stringify(checks));
  applyBackups({ action: 'escrow_check', view: view({ encrypting: true, escrow_covers: true, escrow_checked_at: '2026-09-20T18:25:00Z' }) });

  // A CONNECTION THAT IS NOT PRIVATE is offered no passphrase field at all, and is told what to change.
  forgetBackupsSecrets();
  applyBackups({ action: 'status', view: view({ secrets_ok: false }) });
  assert(st.querySelector('[data-nosecrets]') && !document.getElementById('bk-pass') && !document.getElementById('bk-pass2'), 'no passphrase field on a connection that is not private');
  assert(/TLS/.test(text('[data-nosecrets]')), 'and the page says what to change');

  // SAFE, and a switched-off pass: said in red, and the acts are not offered.
  applyBackups({ action: 'status', view: view({ enabled: false }) });
  assert(st.querySelector('[data-off]') && document.getElementById('bk-take').disabled, 'a switched-off pass is said, and Back up now waits for it');
});
</script>`
	modules := map[string][]byte{}
	data, err := staticFS.ReadFile("static/views/backups.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/views/backups.js"] = data
	modules["/state.js"] = []byte(`export const S = { view: 'settings' };`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; }`)
	if !strings.Contains(string(data), "type: 'backups'") {
		t.Fatal("rig: the shipped module does not speak the backups message")
	}
	runPageInEngines(t, page, modules)
}

// .
// .
func TestTheIdentityPageSaysWhetherThereIsAWayBack(t *testing.T) {
	page := `<!doctype html>
<div id="identity-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { went } from './app.js';
import { opened } from './views/settings.js';
import { renderIdentity } from './views/identity.js';
import { applyBackups } from './views/backups.js';
import { assert, run } from './__harness.js';
run(() => {
  const st = document.getElementById('identity-stack');
  S.view = 'identity'; S.identity = { brief: 'b' }; S.cont = { mode: 'normal', ledger_seq: 9, lifetime_ticks: 3 };
  renderIdentity();
  assert(frames.length === 1 && frames[0].backups.action === 'status', 'the page asks for the view once: ' + JSON.stringify(frames));
  renderIdentity();
  assert(frames.length === 1, 'and only once');
  applyBackups({ action: 'status', view: { enabled: true, snapshots: [{ name: 'n', at: '2026-09-20T08:00:05Z', record: 1402 }], escrow_covers: false, last_pass: { outcome: 'failed', at: '2026-09-20T08:00:10Z' } } });
  const snap = st.querySelector('[data-id-snapshot]').textContent, esc = st.querySelector('[data-id-escrow]').textContent;
  assert(/record 1402/.test(snap) && /not encrypted/.test(snap) && /FAILED/.test(snap), 'the last snapshot line: ' + snap);
  assert(/no/.test(esc), 'keys escrowed: ' + esc);
  st.querySelector('[data-open-backups]').click();
  assert(opened.length === 1 && went[0] === 'settings', 'the link opens Settings at Backups & Keys');
  applyBackups({ action: 'status', view: { enabled: false, snapshots: [], escrow_covers: true, escrow_checked_at: '2026-09-20T18:20:00Z' } });
  assert(/switched off/.test(st.querySelector('[data-id-snapshot]').textContent) && /checked/.test(st.querySelector('[data-id-escrow]').textContent), 'a switched-off pass and a checked escrow are said');
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/backups.js", "static/views/identity.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = {};`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'r'; }`)
	modules["/app.js"] = []byte(`export const went = []; export function go(v) { went.push(v); }`)
	modules["/views/settings.js"] = []byte(`export const opened = []; export function openBackups() { opened.push(1); }`)
	runPageInEngines(t, page, modules)
}

// .
// .
// .
// .
func TestAPersonRestoresFromThePageAndCanFindWhatWasSetAside(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { backupsHTML, wireBackups, applyBackups, backupsReconnected } from './views/backups.js';
import { assert, run } from './__harness.js';
const st = document.getElementById('settings-stack');
S.renderBackups = () => { st.innerHTML = backupsHTML(); wireBackups(st); };
const last = () => frames[frames.length - 1];
const text = sel => (st.querySelector(sel) || { textContent: '' }).textContent;
const view = over => Object.assign({ enabled: true, backup_keep: 8, secrets_ok: true, encrypting: true, escrow_covers: true,
  snapshots: [{ name: 'ledger-20260918T080005Z-seq1300', at: '2026-09-18T08:00:05Z', record: 1300 }] }, over || {});
run(() => {
  applyBackups({ action: 'status', view: view() });
  assert(!st.querySelector('[data-card="restore"]'), 'no restore card until there is something to say');

  // CHOOSE: the plan is asked for by name and source, never a path.
  st.querySelector('[data-restore="ledger-20260918T080005Z-seq1300"]').click();
  assert(last().backups.action === 'restore_plan' && last().backups.source === 'snapshot' && last().backups.name === 'ledger-20260918T080005Z-seq1300', 'the plan is asked for: ' + JSON.stringify(last()));
  applyBackups({ action: 'restore_plan', view: view(), plan: { source: 'snapshot', name: 'ledger-20260918T080005Z-seq1300', at: '2026-09-18T08:00:05Z',
    restored_to: 1300, live_through: 1402, set_aside: 102, witnessed: 1368, witnessed_set_aside: 68, witness_hash: 'abc123', witness_url: 'https://witness.example',
    identity: '501265c6', confirm_text: 'Ivy', aside_under: '/home/ivy/data/set-aside' } });

  // THE COST, IN EXACT NUMBERS.
  assert(/record 1300/.test(text('[data-plan-to]')), 'where the record will end');
  assert(/102 records/.test(text('[data-plan-aside]')) && /through 1402/.test(text('[data-plan-aside]')), 'how many records are set aside, exactly: ' + text('[data-plan-aside]'));
  assert(/68 of those records have been attested/.test(text('[data-plan-witness]')), 'how many of them the witness has attested, exactly: ' + text('[data-plan-witness]'));
  const anchoring = text('[data-plan-anchoring]');
  assert(/SAFE/.test(anchoring) && /frozen/.test(anchoring), 'what actually follows — SAFE, the record frozen — is said: ' + anchoring);
  assert(!/runs normally/.test(anchoring) && !/simply not witnessed/.test(anchoring), 'and the page does not promise the identity keeps running');
  assert(/Settings . Witness/.test(anchoring) || /off the witness/.test(anchoring), 'and it says what a person can actually do today');
  const facts = text('[data-witness-facts]');
  assert(/https:\/\/witness.example/.test(facts) && /501265c6/.test(facts) && /record 1368 abc123/.test(facts) && /ends at: 1300/.test(facts), 'the facts to hand over: ' + facts);
  assert(/\/home\/ivy\/data\/set-aside/.test(text('[data-plan]')) && /Nothing is deleted/.test(text('[data-plan]')), 'where what is live will go is said before it goes');

  // CONFIRM: armed only on the exact name.
  const box = document.getElementById('bk-confirm'), go = document.getElementById('bk-restore');
  assert(go.disabled, 'the button starts unarmed');
  box.value = 'ivy'; box.dispatchEvent(new Event('input'));
  assert(go.disabled, 'a near miss does not arm it');
  box.value = 'Ivy'; box.dispatchEvent(new Event('input'));
  assert(!go.disabled, 'the exact name arms it');
  go.click();
  assert(last().backups.action === 'restore' && last().backups.confirm === 'Ivy' && last().backups.name === 'ledger-20260918T080005Z-seq1300' && last().backups.source === 'snapshot', 'the restore is asked for with what was typed: ' + JSON.stringify(last()));

  // THE RESTART is said, and the page asks again when the socket is back.
  applyBackups({ action: 'restore', restarting: true });
  assert(st.querySelector('[data-restarting]') && /before the record is opened/.test(text('[data-restarting]')), 'the page says what is happening while it restarts');
  const before = frames.length;
  backupsReconnected();
  assert(frames.length === before + 1 && last().backups.action === 'status', 'back on the socket, the page asks for the view again');

  // AFTER: what was set aside is listed with where it is and how to put it back.
  applyBackups({ action: 'status', view: view({ set_aside: [
    { name: 'Ivy-20260920T220000Z-before-restore-to-seq1300', path: '/home/ivy/data/set-aside/Ivy-20260920T220000Z-before-restore-to-seq1300', bytes: 214000000, set_aside_at: '2026-09-20T22:00:00Z', was_through: 1402, restored_to: 1300 },
    { name: 'Ivy-20260901T090000Z-before-restore-to-seq900', path: '/home/ivy/data/set-aside/Ivy-20260901T090000Z-before-restore-to-seq900', bytes: 180000000, set_aside_at: '2026-09-01T09:00:00Z', was_through: 950, restored_to: 900, put_back_at: '2026-09-02T10:00:00Z' } ] }) });
  assert(!st.querySelector('[data-restarting]'), 'the restarting notice is gone once the identity is back');
  const rows = st.querySelectorAll('[data-set]');
  assert(rows.length === 2, 'both sets are listed');
  assert(rows[0].querySelector('[data-set-path]').textContent === '/home/ivy/data/set-aside/Ivy-20260920T220000Z-before-restore-to-seq1300', 'NEVER DELETED MUST NOT BECOME NEVER FOUND: the full path is on the page');
  assert(/through record 1402/.test(rows[0].textContent) && /204 MB/.test(rows[0].textContent), 'what it holds and how big it is: ' + rows[0].textContent);
  assert(rows[1].querySelector('[data-putback]') && !rows[0].querySelector('[data-putback]'), 'a set that was put back says so');
  rows[0].querySelector('[data-restore]').click();
  assert(last().backups.action === 'restore_plan' && last().backups.source === 'set-aside' && last().backups.name === 'Ivy-20260920T220000Z-before-restore-to-seq1300', 'Put this back is the same restore, from the set: ' + JSON.stringify(last()));

  // A plan with nothing witnessed at stake says so, in words.
  applyBackups({ action: 'restore_plan', view: view(), plan: { source: 'snapshot', name: 'n', at: '2026-09-18T08:00:05Z', restored_to: 1300, live_through: 1302, set_aside: 2, witnessed: 1200, witnessed_set_aside: 0, confirm_text: 'Ivy', aside_under: '/x' } });
  assert(/will notice nothing/.test(text('[data-plan-witness]')) && !st.querySelector('[data-witness-facts]'), 'a restore the witness will not notice says so and hands over nothing');

  // A restore that was asked for and not performed is said, in red.
  applyBackups({ action: 'status', view: view({ restore_failed: 'A restore from n was asked for and NOT performed: the source does not prove. Nothing was touched.' }) });
  assert(/NOT performed/.test(text('[data-restore-failed]')), 'a restore that was not performed is said on the page');
});
</script>`
	modules := map[string][]byte{}
	data, err := staticFS.ReadFile("static/views/backups.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/views/backups.js"] = data
	modules["/state.js"] = []byte(`export const S = { view: 'settings' };`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; }`)
	runPageInEngines(t, page, modules)
}

// .
// .
// .
// .
func TestAPersonRestoresAnIdentityAtFirstBoot(t *testing.T) {
	page := `<!doctype html>
<select id="fb-provider"><option value="0" selected>Local</option></select><select id="fb-model"><option>m1</option></select><input id="fb-apikey" value="sk-x">
<button id="fb-restore-open"></button><div id="fb-restore-body" hidden></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderRestoreDoor, applyRestoreNew, restoreDoorState } from './firstboot-restore.js';
import { assert, run } from './__harness.js';
const $ = id => document.getElementById(id);
const acts = a => frames.filter(f => f.type === 'restore_new' && f.restore_new.action === a);
const until = async (cond, what) => { for (let i = 0; i < 400; i++) { if (cond()) return; await new Promise(r => setTimeout(r, 25)); } throw new Error('never happened: ' + what); };
const pick = (id, name, bytes, rel) => { const f = new File([bytes], name); if (rel) Object.defineProperty(f, 'webkitRelativePath', { value: rel }); const dt = new DataTransfer(); dt.items.add(f); $(id).files = dt.files; };
const puts = [];
window.fetch = async (url, opts) => { if (String(url).indexOf('/restore/upload') === 0) { puts.push({ url: String(url), size: opts.body.size }); return { ok: true, text: async () => '' }; } return { ok: false, text: async () => 'unexpected' }; };

run(async () => {
  S.providers = [{ name: 'Local', endpoint: 'http://x/v1' }];
  renderRestoreDoor();
  assert($('fb-restore-body').hidden && !$('fb-restore-open').hidden, 'the door is closed until it is asked for');
  $('fb-restore-open').click();
  assert(acts('hello').length === 1, 'opening the door asks the server whether this connection may carry it');
  applyRestoreNew({ action: 'hello', secrets_ok: true });
  assert(document.querySelector('[data-step="keys"]') && !document.querySelector('[data-step="snapshot"]'), 'only the first step is offered');

  // 1 — the keys.
  $('fbr-keys').click();
  assert(acts('keys').length === 0 && /Pick the escrow file/.test(document.querySelector('[data-restore-said]').textContent), 'nothing is sent without a file');
  pick('fbr-escrow', 'escrow-5012-20260920.age', new Uint8Array([7, 8, 9]));
  $('fbr-pass').value = 'correct horse battery';
  $('fbr-keys').click();
  await until(() => acts('keys').length === 1, 'the keys act');
  const k = acts('keys')[0].restore_new;
  assert(k.file_b64 === btoa(String.fromCharCode(7, 8, 9)) && k.passphrase === 'correct horse battery', 'the escrow file and its passphrase are sent: ' + JSON.stringify(k));
  applyRestoreNew({ action: 'keys', secrets_ok: true, error: 'That file did not open.' });
  assert(!restoreDoorState().keys && /did not open/.test(document.querySelector('[data-restore-said]').textContent), 'a file that does not open keeps the person on the first step');
  applyRestoreNew({ action: 'keys', secrets_ok: true, keys: { identity: '501265c63ca1065fb83d2036b58471092d899450', holds_snapshot_key: true } });
  assert(document.querySelector('[data-keys-ok]') && !$('fbr-pass'), 'the keys are in place, and the passphrase field is gone');
  assert(!$('fb-restore-body').innerHTML.includes('correct horse battery') && !document.querySelector('input[type=password]#fbr-pass'), 'THE PASSPHRASE IS NOT KEPT ON THE PAGE');
  assert(document.querySelector('[data-step="snapshot"]') && !document.querySelector('[data-step="restore"]'), 'the second step opens, the third does not');

  // 2 — the snapshot: one encrypted file, by its own name.
  pick('fbr-snap', 'ledger-20260920T181500Z-seq1410-ondemand.tar.age', new Uint8Array(2048));
  $('fbr-send').click();
  await until(() => restoreDoorState().snapshot !== '', 'the upload');
  assert(puts.length === 1 && puts[0].url === '/restore/upload?snapshot=ledger-20260920T181500Z-seq1410-ondemand.tar.age' && puts[0].size === 2048, 'the snapshot is sent whole, under its own name: ' + JSON.stringify(puts));
  assert(document.querySelector('[data-snapshot-ok]') && document.querySelector('[data-step="restore"]'), 'the third step opens');
  assert(/another identity.s snapshot is refused/.test(document.querySelector('[data-restore-says]').textContent) && /not in it/.test(document.querySelector('[data-restore-says]').textContent), 'what the restore will and will not bring back is said before it is asked for');

  // 3 — restore, with the model from the birth form's own fields.
  $('fbr-restore').click();
  const r = acts('restore')[0].restore_new;
  assert(r.snapshot === 'ledger-20260920T181500Z-seq1410-ondemand.tar.age' && r.provider.provider === 'Local' && r.provider.model === 'm1' && r.provider.api_key === 'sk-x' && r.provider.endpoint === 'http://x/v1', 'the restore names the snapshot and the mind: ' + JSON.stringify(r));
  assert($('fbr-restore').disabled && /Proving/.test($('fbr-restore').textContent), 'the button waits while the snapshot is proved');
  applyRestoreNew({ action: 'restore', secrets_ok: true, error: 'The snapshot was NOT restored: it is another identity\'s.' });
  assert(!$('fbr-restore').disabled && /NOT restored/.test(document.querySelector('[data-restore-said]').textContent) && document.querySelector('[data-keys-ok]'), 'a refusal keeps the keys and the door: another snapshot can be tried');

  // A plaintext snapshot's folder goes file by file, under the folder's name.
  // (A fresh door: the state above is the module's.)
});
</script>`
	modules := map[string][]byte{}
	data, err := staticFS.ReadFile("static/firstboot-restore.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/firstboot-restore.js"] = data
	modules["/state.js"] = []byte(`export const S = { providers: [] };`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; }`)
	runPageInEngines(t, page, modules)
}

// .
// .
// .
func TestTheRestoreDoorOnAConnectionThatIsNotPrivateAndAFolder(t *testing.T) {
	page := `<!doctype html>
<select id="fb-provider"><option value="0" selected>Local</option></select><select id="fb-model"><option>m1</option></select><input id="fb-apikey" value="">
<button id="fb-restore-open"></button><div id="fb-restore-body" hidden></div>
<script type="module">
import { S } from './state.js';
import { renderRestoreDoor, applyRestoreNew, restoreDoorState } from './firstboot-restore.js';
import { assert, run } from './__harness.js';
const $ = id => document.getElementById(id);
const until = async (cond, what) => { for (let i = 0; i < 400; i++) { if (cond()) return; await new Promise(r => setTimeout(r, 25)); } throw new Error('never happened: ' + what); };
const puts = [];
window.fetch = async (url, opts) => { puts.push(String(url)); return { ok: true, text: async () => '' }; };
run(async () => {
  S.providers = [{ name: 'Local', endpoint: 'http://x/v1' }];
  renderRestoreDoor();
  $('fb-restore-open').click();
  applyRestoreNew({ action: 'hello', secrets_ok: false });
  assert(document.querySelector('[data-nosecrets]') && !$('fbr-pass') && !$('fbr-escrow'), 'no file and no passphrase field on a connection that is not private');

  applyRestoreNew({ action: 'hello', secrets_ok: true });
  applyRestoreNew({ action: 'keys', secrets_ok: true, keys: { identity: 'abcdef0123456789abcdef', holds_snapshot_key: false } });
  assert(!$('fbr-snap') && $('fbr-folder') && /before snapshots were encrypted/.test(document.body.textContent), 'an escrow with no snapshot key asks for a plaintext folder, and says why');
  const dt = new DataTransfer();
  for (const rel of ['ledger-20260901T040000Z-seq900/ledger.jsonl', 'ledger-20260901T040000Z-seq900/aii.db', 'ledger-20260901T040000Z-seq900/SHA256SUMS', 'ledger-20260901T040000Z-seq900/witness-keys/k1.json']) {
    const f = new File(['x'], rel.split('/').pop()); Object.defineProperty(f, 'webkitRelativePath', { value: rel }); dt.items.add(f);
  }
  $('fbr-folder').files = dt.files;
  $('fbr-send').click();
  await until(() => restoreDoorState().snapshot !== '', 'the folder upload');
  assert(restoreDoorState().snapshot === 'ledger-20260901T040000Z-seq900', 'the snapshot is named for its folder');
  assert(puts.length === 4 && puts[3] === '/restore/upload?snapshot=ledger-20260901T040000Z-seq900&member=witness-keys%2Fk1.json', 'each file goes under the folder name, with its own relative name: ' + JSON.stringify(puts));
});
</script>`
	modules := map[string][]byte{}
	data, err := staticFS.ReadFile("static/firstboot-restore.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/firstboot-restore.js"] = data
	modules["/state.js"] = []byte(`export const S = { providers: [] };`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; }`)
	runPageInEngines(t, page, modules)
}
