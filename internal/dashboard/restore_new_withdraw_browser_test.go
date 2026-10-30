//go:build !windows

package dashboard

import "testing"

// restoreDoorPage runs one script against first boot's restore door as it
// ships, with the socket recorded and the confirm answered by the script.
func restoreDoorPage(t *testing.T, script string) {
	t.Helper()
	page := `<!doctype html>
<select id="fb-provider"><option value="0" selected>Local</option></select><select id="fb-model"><option>m1</option></select><input id="fb-apikey" value=""><input id="fb-url" value="">
<div class="fb-result" id="fb-result" role="status"></div>
<button id="fb-restore-open"></button><div id="fb-restore-body" hidden></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderRestoreDoor, applyRestoreNew, restoreDoorState } from './firstboot-restore.js';
import { assert, run } from './__harness.js';
const $ = id => document.getElementById(id);
const acts = a => frames.filter(f => f.type === 'restore_new' && f.restore_new.action === a);
const text = sel => (document.querySelector(sel) || { textContent: '' }).textContent;
const until = async (cond, what) => { for (let i = 0; i < 400; i++) { if (cond()) return; await new Promise(r => setTimeout(r, 25)); } throw new Error('never happened: ' + what); };
const pick = (id, name, bytes) => { const dt = new DataTransfer(); dt.items.add(new File([bytes], name)); $(id).files = dt.files; };
const whole = '501265c63ca1065fb83d2036b58471092d899450';
let question = '', answer = false;
window.confirm = q => { question = q; return answer; };
S.providers = [{ name: 'Local', endpoint: 'http://x/v1' }];
run(async () => {
` + script + `
});
</script>`
	modules := map[string][]byte{}
	data, err := staticFS.ReadFile("static/firstboot-restore.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/firstboot-restore.js"] = data
	modules["/state.js"] = []byte(`export const S = { providers: [] };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; }`)
	runPageInEngines(t, page, modules)
}

// A PERSON WITHDRAWS THE KEYS AT FIRST BOOT. A door with nothing begun
// offers nothing to withdraw. A page loaded over keys that are already on
// the machine (a reload, or a start after the one that placed them) is told
// so at hello and shows them, with the button and what it does. The button
// asks first, in words that say what is removed and what is not; declined,
// nothing is sent. A refusal is said in the door and the keys stay. Done,
// the door closes, the line is said where a birth's outcome is said, and

func TestAPersonWithdrawsTheKeysAtFirstBoot(t *testing.T) {
	restoreDoorPage(t, `
  renderRestoreDoor();

  // NOTHING BEGUN, NOTHING TO WITHDRAW.
  $('fb-restore-open').click();
  applyRestoreNew({ action: 'hello', secrets_ok: true });
  assert($('fbr-escrow') && !$('fbr-withdraw') && !document.querySelector('[data-begun]'), 'a door with nothing begun offers the first step and no withdrawal');

  // THE KEYS ACT, answered.
  applyRestoreNew({ action: 'keys', secrets_ok: true, keys: { identity: whole, holds_snapshot_key: true } });
  assert(document.querySelector('[data-keys-ok]') && $('fbr-snap'), 'the keys are in place and the snapshot is asked for');
  assert($('fbr-withdraw') && $('fbr-withdraw').textContent === 'Withdraw these keys', 'the way back is offered: ' + text('#fbr-withdraw'));

  // IT ASKS FIRST, and a person who declines has sent nothing.
  $('fbr-withdraw').click();
  assert(question === 'Withdraw the keys of identity 501265c63ca1065f… from this machine? Only the keys this page put here from your escrow file are removed. The escrow file is not touched, and the keys can be put back from it here. This identity cannot be restored on this machine until they are.', 'the question is not the one row 9.35 approved: ' + question);
  assert(acts('withdraw').length === 0 && restoreDoorState().step === 'snapshot' && !$('fbr-withdraw').disabled, 'declined: nothing was sent and the door is as it was');

  // CONFIRMED: one act, and every button waits under its own name.
  answer = true;
  $('fbr-withdraw').click();
  assert(acts('withdraw').length === 1 && Object.keys(acts('withdraw')[0].restore_new).join() === 'action', 'the act is sent once, and carries nothing but its name: ' + JSON.stringify(frames.at(-1)));
  assert($('fbr-withdraw').disabled && $('fbr-withdraw').textContent === 'Withdrawing…' && $('fbr-close').disabled, 'the door waits for the answer');
  assert($('fbr-send').disabled && $('fbr-send').textContent === 'Send it to this machine', 'the other buttons wait and do not say they are doing what they are not');

  // A REFUSAL is said in the door, and the keys stay where they are.
  applyRestoreNew({ action: 'withdraw', secrets_ok: true, error: 'Nothing was withdrawn: a snapshot is on this machine.' });
  assert(/Nothing was withdrawn/.test(text('[data-restore-said]')) && document.querySelector('[data-keys-ok]') && !$('fbr-withdraw').disabled && restoreDoorState().step === 'snapshot', 'a refusal is said where the person is looking, and nothing changed: ' + text('[data-restore-said]'));
  assert(!$('fb-result').textContent, 'a refusal says nothing in the birth form');

  // WITHDRAWN: the door closes and this is first boot's page again.
  $('fbr-withdraw').click();
  assert(acts('withdraw').length === 2, 'asked again');
  applyRestoreNew({ action: 'withdraw', secrets_ok: true, withdrawn: true });
  assert($('fb-restore-body').hidden && !$('fb-restore-open').hidden && !$('fbr-withdraw'), 'the door is closed and the first-boot page is back');
  assert(text('#fb-result [data-withdrawn]') === 'The keys of identity 501265c63ca1065f… are withdrawn from this machine, and nothing else here was changed. Your escrow file still holds them. You can birth a new identity here now, or restore one.', 'what was done is said where a birth says its outcome: ' + text('#fb-result'));
  const door = restoreDoorState();
  assert(door.step === 'closed' && !door.keys && !door.begun && !door.busy && door.snapshot === '', 'the page keeps nothing of the restore: ' + JSON.stringify(door));

  // The door opened again is the first step.
  $('fb-restore-open').click();
  applyRestoreNew({ action: 'hello', secrets_ok: true });
  assert($('fbr-escrow') && $('fbr-pass') && !document.querySelector('[data-keys-ok]') && !$('fbr-withdraw'), 'the door opens on its first step after a withdrawal');
`)
}

// A RELOADED DOOR SAYS A RESTORE STANDS, AND OFFERS BOTH WAYS ON. The page
// was reloaded, or the machine started again, after the keys act: hello
// names whose restore stands. The page says so, keeps the escrow form (the
// same act made again goes on: a key in place is checked, never written
// over) and offers the withdrawal with the same question. A connection
// that is not private is shown none of it.
func TestAReloadedDoorSaysARestoreStandsAndOffersBothWaysOn(t *testing.T) {
	restoreDoorPage(t, `
  renderRestoreDoor();
  $('fb-restore-open').click();

  // Not private: the notice, and nothing of what stands here, whatever the frame held.
  applyRestoreNew({ action: 'hello', secrets_ok: false, begun: { identity: whole } });
  assert(document.querySelector('[data-nosecrets]') && !document.querySelector('[data-begun]') && !$('fbr-withdraw') && !$('fb-restore-body').textContent.includes('501265c6'), 'a connection that is not private is shown nothing of the restore');

  applyRestoreNew({ action: 'hello', secrets_ok: true, begun: { identity: whole } });
  assert(text('[data-begun]') === 'A restore of identity 501265c63ca1065f… was begun on this machine and has been neither finished nor withdrawn. To go on with it, open the escrow file again below; to end it, press "Withdraw these keys".', 'the page says a restore stands here: ' + text('[data-begun]'));
  assert(!document.querySelector('[data-keys-ok]') && !document.querySelector('[data-step="snapshot"]'), 'no key is said to be in place, and no snapshot is asked for');
  assert($('fbr-escrow') && $('fbr-pass') && $('fbr-keys') && $('fbr-withdraw'), 'both ways on are offered: the first act again, and the withdrawal');

  // THE SAME ACT AGAIN, from this page: the button that was pressed waits under its own name.
  pick('fbr-escrow', 'escrow-5012-20260920.age', new Uint8Array([7, 8, 9]));
  $('fbr-pass').value = 'correct horse battery';
  $('fbr-keys').click();
  await until(() => acts('keys').length === 1, 'the keys act');
  assert($('fbr-keys').disabled && $('fbr-keys').textContent === 'Opening…' && $('fbr-withdraw').textContent === 'Withdraw these keys', 'the button that was pressed waits, under its own name');
  applyRestoreNew({ action: 'keys', secrets_ok: true, keys: { identity: whole, holds_snapshot_key: true } });
  assert(document.querySelector('[data-keys-ok]') && !document.querySelector('[data-begun]') && !$('fbr-pass'), 'the act answered: the keys are in place, and the passphrase field is gone');

  // A later hello that finds no restore standing (withdrawn from another page) unshows the keys.
  applyRestoreNew({ action: 'hello', secrets_ok: true });
  assert(!document.querySelector('[data-keys-ok]') && !$('fbr-withdraw') && $('fbr-escrow') && restoreDoorState().step === 'keys', 'keys no restore stands for are not shown in place: ' + JSON.stringify(restoreDoorState()));

  // Withdrawn from a reloaded page: the same question and the same act.
  applyRestoreNew({ action: 'hello', secrets_ok: true, begun: { identity: whole } });
  answer = true;
  $('fbr-withdraw').click();
  assert(/^Withdraw the keys of identity 501265c63ca1065f… from this machine\?/.test(question) && acts('withdraw').length === 1 && restoreDoorState().step === 'withdrawing', 'it asks first, in the same words, and sends the act: ' + question);
  applyRestoreNew({ action: 'withdraw', secrets_ok: true, error: 'Nothing was withdrawn: the key at /h/identity.sec cannot be shown to be the one this restore put in place.' });
  assert(/Nothing was withdrawn/.test(text('[data-restore-said]')) && document.querySelector('[data-begun]') && $('fbr-escrow') && restoreDoorState().step === 'keys', 'a refusal returns the door to the step it was on: ' + JSON.stringify(restoreDoorState()));
  $('fbr-withdraw').click();
  applyRestoreNew({ action: 'withdraw', secrets_ok: true, withdrawn: true });
  assert($('fb-restore-body').hidden && document.querySelector('#fb-result [data-withdrawn]') && !restoreDoorState().begun, 'ended: the door is closed and the first-boot page is back: ' + text('#fb-result'));
`)
}

func TestAWithdrawalSaysTheSnapshotsItTakes(t *testing.T) {
	restoreDoorPage(t, `
  window.fetch = async () => ({ ok: true, text: async () => '' });
  const one = 'ledger-20260920T181500Z-seq1410.tar.age', two = 'ledger-20261001T040000Z-seq1500.tar.age';
  renderRestoreDoor();
  $('fb-restore-open').click();
  applyRestoreNew({ action: 'hello', secrets_ok: true, begun: { identity: whole, received: [one] } });
  $('fbr-withdraw').click();
  assert(question === 'Withdraw the keys of identity 501265c63ca1065f… from this machine? Only the keys this page put here from your escrow file are removed, with the snapshot this page received (' + one + '); your own copy of it is not touched. The escrow file is not touched, and the keys can be put back from it here. This identity cannot be restored on this machine until they are.', 'THE QUESTION DOES NOT SAY THE SNAPSHOT GOES: ' + question);
  assert(acts('withdraw').length === 0, 'declined: nothing was sent');

  // The keys act again, and one more snapshot sent from this page: both are named.
  applyRestoreNew({ action: 'keys', secrets_ok: true, keys: { identity: whole, holds_snapshot_key: true } });
  pick('fbr-snap', two, new Uint8Array(8));
  $('fbr-send').click();
  await until(() => restoreDoorState().snapshot === two, 'the upload');
  $('fbr-withdraw').click();
  assert(question.includes('with the snapshots this page received (' + one + ', ' + two + '); your own copies of them are not touched.'), 'both are named: ' + question);

  answer = true;
  $('fbr-withdraw').click();
  applyRestoreNew({ action: 'withdraw', secrets_ok: true, withdrawn: true, snapshots: [one, two] });
  assert(text('#fb-result [data-withdrawn]') === 'The keys of identity 501265c63ca1065f… are withdrawn from this machine, with the snapshots this page received, and nothing else here was changed. Your escrow file still holds them. You can birth a new identity here now, or restore one.', 'the line after says they went: ' + text('#fb-result'));
`)
}
