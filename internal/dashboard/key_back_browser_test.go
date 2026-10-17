//go:build !windows

package dashboard

import "testing"

func TestAPersonPutsTheKeyBackFromThePage(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { backupsHTML, wireBackups, applyBackups as receiveBackups, backupsReconnected, forgetBackupsSecrets } from './views/backups.js';
import { assert, run } from './__harness.js';
const st = document.getElementById('settings-stack');
S.renderBackups = () => { st.innerHTML = backupsHTML(); wireBackups(st); };
const last = () => frames[frames.length - 1];
const acts = a => frames.filter(f => f.type === 'backups' && f.backups.action === a);
const applyBackups = reply => receiveBackups(reply, acts(reply.action).at(-1)?.request_id);
const until = async (cond, what) => { for (let i = 0; i < 400; i++) { if (cond()) return; await new Promise(r => setTimeout(r, 25)); } throw new Error('never happened: ' + what); };
const text = sel => (st.querySelector(sel) || { textContent: '' }).textContent;
const pick = (id, bytes) => { const dt = new DataTransfer(); dt.items.add(new File([bytes], 'escrow.age')); document.getElementById(id).files = dt.files; };
const view = over => Object.assign({ enabled: true, backup_keep: 8, secrets_ok: true, export_ok: true, safe: true, key_absent: true, encrypting: false, escrow_covers: false, snapshots: [] }, over || {});
run(async () => {
  applyBackups({ action: 'status', view: view({ key_absent: false }) });
  assert(!st.querySelector('[data-keyback]') && !document.getElementById('bk-keypass'), 'a SAFE start that holds its key offers no key form');

  applyBackups({ action: 'status', view: view() });
  assert(st.querySelector('[data-keyback]') && /only if it is the key this record names/.test(text('[data-keyback]')), 'the form says what it holds the key to: ' + text('[data-keyback]'));
  assert(!document.getElementById('bk-pass') && !document.getElementById('bk-create'), 'no escrow is made from a start with no key');

  document.getElementById('bk-keypass').value = 'correct horse battery';
  pick('bk-keyfile', new Uint8Array([1, 2, 3, 250]));
  applyBackups({ action: 'status', view: view() });
  assert(document.getElementById('bk-keypass').value === 'correct horse battery' && document.getElementById('bk-keyfile').files.length === 1, 'a redraw clears neither the passphrase nor the picked file');

  document.getElementById('bk-keyback').click();
  await until(() => acts('key_back').length === 1, 'the act is sent once the file is read');
  assert(last().backups.passphrase === 'correct horse battery' && last().backups.file_b64 === btoa(String.fromCharCode(1, 2, 3, 250)), 'the act carries the picked file and the typed passphrase: ' + JSON.stringify(last()));
  assert(document.getElementById('bk-keyback').disabled && /Putting the key back/.test(document.getElementById('bk-keyback').textContent), 'the button waits');

  applyBackups({ action: 'key_back', view: view(), error: 'That escrow file holds another identity\'s keys, not the key this record was signed with. Nothing was written.' });
  assert(/another identity/.test(text('[data-card="escrow"] [data-said]')) && st.querySelector('[data-keyback]'), 'a refusal is said where the person is looking, and they can pick another file');

  document.getElementById('bk-keypass').value = 'correct horse battery';
  pick('bk-keyfile', new Uint8Array([9, 9]));
  document.getElementById('bk-keyback').click();
  await until(() => acts('key_back').length === 2, 'the retry with the right file');
  applyBackups({ action: 'key_back', restarting: true });
  assert(st.querySelector('[data-key-restarting]') && /checks the whole record under the key/.test(text('[data-key-restarting]')), 'the page says what is happening while it restarts');
  assert(!document.getElementById('bk-keypass'), 'the form, and the passphrase in it, are gone');
  const before = frames.length;
  backupsReconnected();
  assert(frames.length === before + 1 && last().backups.action === 'status', 'back on the socket, the page asks for the view again');
  applyBackups({ action: 'status', view: view({ safe: false, key_absent: false }) });
  assert(!st.querySelector('[data-key-restarting]') && !st.querySelector('[data-keyback]') && document.getElementById('bk-create'), 'a normal start is back: the escrow card is the ordinary one');

  applyBackups({ action: 'status', view: view({ record_absent: true }) });
  assert(/Neither the record nor the key that signs it is on this machine/.test(text('[data-keyback]')) && /only if it is the key a snapshot kept here names/.test(text('[data-keyback]')) && !/this record names/.test(text('[data-keyback]')),
    'A HOME THAT LOST ITS RECORD TOO IS TOLD THE KEY IS HELD TO THE RECORD: ' + text('[data-keyback]'));
  document.getElementById('bk-keypass').value = 'correct horse battery';
  pick('bk-keyfile', new Uint8Array([7]));
  document.getElementById('bk-keyback').click();
  await until(() => acts('key_back').length === 3, 'the act from a home with no record');
  applyBackups({ action: 'key_back', restarting: true });
  assert(/offers the restore of a snapshot kept here/.test(text('[data-key-restarting]')) && !/checks the whole record/.test(text('[data-key-restarting]')), 'the restart says the restore comes next: ' + text('[data-key-restarting]'));
  backupsReconnected();
  applyBackups({ action: 'status', view: view({ safe: false, key_absent: false }) });

  applyBackups({ action: 'status', view: view({ key_absent: false, key_unnamed: true }) });
  assert(/does not say which key signs it/.test(text('[data-key-unnamed]')) && /Put the identity's key file back where the configuration names it/.test(text('[data-key-unnamed]')) && !document.getElementById('bk-keyfile') && !document.getElementById('bk-keypass'),
    'A RECORD THAT NAMES NO KEY IS OFFERED A FORM, or not told how the key goes back: ' + text('[data-card="escrow"]'));

  applyBackups({ action: 'status', view: view({ key_absent: false, snapshot_key_mismatch: 'ledger-20261014T040000Z-seq9.tar.age' }) });
  assert(st.querySelector('[data-snapkeyback]') && /does not open the snapshots kept here \(the newest is ledger-20261014T040000Z-seq9\.tar\.age\)/.test(text('[data-snapkeyback]')) && /moved aside beside it, never deleted/.test(text('[data-snapkeyback]')),
    'A SNAPSHOT KEY THAT DOES NOT OPEN THE SNAPSHOTS IS NOT SAID, or not what the act does: ' + text('[data-snapkeyback]'));
  document.getElementById('bk-snappass').value = 'correct horse battery';
  pick('bk-snapfile', new Uint8Array([5, 6]));
  applyBackups({ action: 'status', view: view({ key_absent: false, snapshot_key_mismatch: 'ledger-20261014T040000Z-seq9.tar.age' }) });
  assert(document.getElementById('bk-snappass').value === 'correct horse battery', 'a redraw clears the passphrase typed for the snapshot key');
  document.getElementById('bk-snapback').click();
  await until(() => acts('snapshot_key_back').length === 1, 'the snapshot key act is sent');
  assert(last().backups.passphrase === 'correct horse battery' && last().backups.file_b64 === btoa(String.fromCharCode(5, 6)), 'the act carries the file and the passphrase');
  applyBackups({ action: 'snapshot_key_back', view: view({ key_absent: false }), said: 'The snapshot key from your escrow file is in place and opens the snapshots kept here; the one that stood here is kept beside it, as identity.snapshot.key.set-aside-20261014T050000Z. A restore can be asked for now.' });
  assert(/is in place and opens the snapshots kept here/.test(text('[data-card="escrow"] [data-said]')) && !st.querySelector('[data-snapkeyback]') && !document.getElementById('bk-snappass'), 'what was done is said, and the form and the passphrase are gone');
  applyBackups({ action: 'status', view: view({ key_absent: false, snapshot_key_mismatch: 'ledger-20261014T040000Z-seq9.tar.age', snapshot_key_absent: true }) });
  assert(/No snapshot key is on this machine/.test(text('[data-snapkeyback]')), 'no snapshot key at all is said as such: ' + text('[data-snapkeyback]'));
  applyBackups({ action: 'status', view: view({ key_absent: false }) });

  forgetBackupsSecrets();
  applyBackups({ action: 'status', view: view({ secrets_ok: false }) });
  assert(st.querySelector('[data-nosecrets]') && !document.getElementById('bk-keypass'), 'no key form on a connection that is not private');
});
</script>`
	modules := map[string][]byte{}
	data, err := staticFS.ReadFile("static/views/backups.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/views/backups.js"] = data
	modules["/state.js"] = []byte(`export const S = { view: 'settings' };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { f.request_id=String(frames.length+1); frames.push(f); return f.request_id; }`)
	runPageInEngines(t, page, modules)
}
