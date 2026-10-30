//go:build !windows

package dashboard

import "testing"

func TestAPersonChoosesTheIdentitysFilesOnThePage(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { backupsHTML, wireBackups, applyBackups as receiveBackups, backupsReconnected } from './views/backups.js';
import { assert, run } from './__harness.js';
const st = document.getElementById('settings-stack');
S.renderBackups = () => { st.innerHTML = backupsHTML(); wireBackups(st); };
const acts = a => frames.filter(f => f.type === 'backups' && f.backups.action === a);
const applyBackups = reply => receiveBackups(reply, acts(reply.action).at(-1)?.request_id);
const text = sel => (st.querySelector(sel) || { textContent: '' }).textContent;
const choice = { files: [
  { key: 'ledger_path', what: 'the record', was: '/old/Documents/data/ledger.jsonl', found: [
    { path: '/home/data/ledger.jsonl', bytes: 2048, modified: '2026-10-19T08:00:00Z' },
    { path: '/home/ledger.jsonl', bytes: 512, modified: '2026-10-01T08:00:00Z' } ] },
  { key: 'key_path', what: 'the key', was: '/old/Documents/data/identity.sec', found: [ { path: '/home/identity.sec', bytes: 64 } ] },
  { key: 'db_path', what: 'the database', was: '/old/Documents/data/aii.db', standard: '/home/data/aii.db' } ] };
const view = over => Object.assign({ enabled: true, backup_keep: 8, secrets_ok: true, export_ok: true, safe: true, encrypting: false, escrow_covers: false, snapshots: [] }, over || {});
run(async () => {
  applyBackups({ action: 'status', view: view() });
  assert(!st.querySelector('[data-card="choice"]'), 'a start with no choice to make shows no choice card');

  applyBackups({ action: 'status', view: view({ identity_choice: choice }) });
  const card = text('[data-card="choice"]');
  assert(/record is not where its configuration names it/.test(card) && /Nothing was opened, moved or written/.test(card) && /The files you do not choose stay where they are/.test(card), 'the card says what happened and what the choice does: ' + card);

  // NO EXPORT: the card offers the choice and nothing that copies a record.
  assert(st.querySelector('#bk-choose') && st.querySelector('[data-card="choice"] input[type="radio"]'), 'the card does not offer the choice: ' + card);
  assert(!st.querySelector('#bk-export') && !/export/i.test(card) && ![...st.querySelectorAll('[data-card="choice"] button')].some(b => /export/i.test(b.textContent)), 'THE CARD STILL OFFERS AN EXPORT: ' + card);
  assert(/\/home\/data\/ledger\.jsonl/.test(text('[data-choice-file="ledger_path"]')) && /\/home\/ledger\.jsonl/.test(text('[data-choice-file="ledger_path"]')), 'both records found are named');
  assert(/None was found here\. It goes to \/home\/data\/aii\.db/.test(text('[data-choice-file="db_path"]')), 'a file not found says where it goes: ' + text('[data-choice-file="db_path"]'));
  assert(document.getElementById('bk-pick-key_path-0').checked, 'the one key found is the choice already');

  // Nothing is sent until a record is chosen.
  document.getElementById('bk-choose').click();
  assert(acts('identity_choose').length === 0 && /Choose one file for the record/.test(text('[data-card="choice"] [data-said]')), 'a choice with no record was sent, or not said: ' + text('[data-card="choice"]'));

  // A pick survives a redraw the page did not ask for.
  document.getElementById('bk-pick-ledger_path-1').checked = true;
  applyBackups({ action: 'status', view: view({ identity_choice: choice }) });
  assert(document.getElementById('bk-pick-ledger_path-1').checked, 'a redraw cleared the pick');
  document.getElementById('bk-choose').click();
  assert(acts('identity_choose').length === 1 && JSON.stringify(acts('identity_choose')[0].backups.picked) === JSON.stringify({ ledger_path: '/home/ledger.jsonl', key_path: '/home/identity.sec' }), 'the act carries the picks: ' + JSON.stringify(acts('identity_choose')[0]));

  // A refusal is said in the card, and the card stays.
  applyBackups({ action: 'identity_choose', view: view({ identity_choice: choice }), error: 'Choose one of the files found for the record. Nothing was written.' });
  assert(/Nothing was written/.test(text('[data-card="choice"] [data-said]')) && st.querySelector('#bk-choose'), 'a refusal is said where the person is looking');

  // Written: the restart is said until the socket is back.
  document.getElementById('bk-pick-ledger_path-0').checked = true;
  document.getElementById('bk-choose').click();
  assert(acts('identity_choose').length === 2 && acts('identity_choose')[1].backups.picked.ledger_path === '/home/data/ledger.jsonl', 'the second choice is sent');
  applyBackups({ action: 'identity_choose', restarting: true });
  assert(/Restarting on the files you chose/.test(text('[data-choice-restarting]')) && !st.querySelector('#bk-choose'), 'the restart is said: ' + text('[data-card="choice"]'));
  backupsReconnected();
  applyBackups({ action: 'status', view: view({ safe: false }) });
  assert(!st.querySelector('[data-card="choice"]'), 'back on the socket after the choice, no card');

  // NO RECORD AT ALL: what is here is named, and nothing is offered to
  // choose.
  applyBackups({ action: 'status', view: view({ identity_choice: { files: [
    { key: 'ledger_path', what: 'the record', was: '/home/data/ledger.jsonl', standard: '/home/data/ledger.jsonl' },
    { key: 'key_path', what: 'the key', was: '/home/data/identity.sec', found: [ { path: '/home/data/identity.sec', bytes: 64 } ] } ] } }) });
  const none = text('[data-card="choice"]');
  assert(/No record of this identity was found in this home/.test(none) && /\/home\/data\/identity\.sec/.test(none) && !st.querySelector('#bk-choose') && !/export/i.test(none) && !st.querySelector('[data-card="choice"] input'), 'A HOME WITH NO RECORD IS OFFERED A CHOICE OR AN EXPORT, or not told: ' + none);
});
</script>`
	runPageInEngines(t, page, identityChoiceModules(t))
}

func TestOnAPhoneThePageOffersTheChoiceOfTheIdentitysFiles(t *testing.T) {
	css := func(name string) string {
		data, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	page := `<!doctype html>
<html><head><meta name="viewport" content="width=device-width,initial-scale=1"><style>` + css("theme.css") + css("layout.css") + `</style></head><body>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { backupsHTML, wireBackups, applyBackups } from './views/backups.js';
import { assert, run } from './__harness.js';
const st = document.getElementById('settings-stack');
S.renderBackups = () => { st.innerHTML = backupsHTML(); wireBackups(st); };
const home = '/data/user/0/id.aiii.shell/files';
run(async () => {
  const width = document.documentElement.clientWidth;
  assert(width <= 767, 'the viewport is ' + width + 'px: the phone rules never applied');
  applyBackups({ action: 'status', view: { enabled: true, backup_keep: 8, secrets_ok: true, export_ok: true, safe: true, encrypting: false, escrow_covers: false, snapshots: [], identity_choice: { files: [
    { key: 'ledger_path', what: 'the record', was: '/old/Documents/data/ledger.jsonl', found: [ { path: home + '/data/ledger.jsonl', bytes: 2048 }, { path: home + '/ledger.jsonl', bytes: 512 } ] },
    { key: 'key_path', what: 'the key', was: '/old/Documents/data/identity.sec', found: [ { path: home + '/data/identity.sec', bytes: 64 } ] } ] } } });
  const card = st.querySelector('[data-card="choice"]');
  assert(card, 'no choice card at a phone width');
  const records = card.querySelectorAll('input[type="radio"][name="bk-pick-ledger_path"]');
  assert(records.length === 2, 'the two records found are not both offered: ' + records.length);
  const choose = document.getElementById('bk-choose');
  const box = choose && choose.getBoundingClientRect();
  assert(box && box.width > 0 && box.height > 0 && box.left >= 0 && box.right <= width + 0.5, 'the button that takes the choice is not on a phone screen: ' + JSON.stringify(box) + ' in ' + width);
  assert(!/export/i.test(card.textContent) && !document.getElementById('bk-export'), 'the card offers an export: ' + card.textContent);
  records[0].checked = true;
  choose.click();
  const sent = frames.filter(f => f.type === 'backups' && f.backups.action === 'identity_choose');
  assert(sent.length === 1 && sent[0].backups.picked.ledger_path === home + '/data/ledger.jsonl' && sent[0].backups.picked.key_path === home + '/data/identity.sec', 'a press at a phone width did not send the choice: ' + JSON.stringify(frames));
});
</script></body></html>`
	runPageInEnginesAtSize(t, page, identityChoiceModules(t), 375, 812)
}

// identityChoiceModules is what the choice card's page imports: the
// shipped backups view, and the state, socket and helpers it reads.
func identityChoiceModules(t *testing.T) map[string][]byte {
	t.Helper()
	modules := map[string][]byte{}
	data, err := staticFS.ReadFile("static/views/backups.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/views/backups.js"] = data
	modules["/state.js"] = []byte(`export const S = { view: 'settings' };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;');`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { f.request_id=String(frames.length+1); frames.push(f); return f.request_id; }`)
	return modules
}
