//go:build !windows

package dashboard

import "testing"

// SETTINGS → BACKUPS IS THE BOARD, AND DOES WHAT IT SAYS (spec step 18, the
// board SettingsBackups), over the SHIPPED module. The heading and its
// sentence, then What is kept, Your way back and What a restore does. The
// newest three snapshots, then how many more and all of them on a press.
// The way back's two buttons each open their form, which takes the focus,
// and a form closed forgets what was typed in it; with no file checked here
// making one is open from the start, and the page says none was checked,
// never that none was made. What a restore does is folded until there is a
// restore to confirm, stays as the person left it, and says the restore is
// confirmed by typing what the page shows. The stylesheet comes when
// Backups is first drawn, not when the module is imported.
func TestSettingsBackupsIsTheBoardAndDoesWhatItSays(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { backupsHTML, wireBackups, applyBackups as receiveBackups, forgetBackupsSecrets } from './views/backups.js';
import { holders } from './overlay.js';
import { assert, run, until } from './__harness.js';
const st = document.getElementById('settings-stack');
S.renderBackups = () => { st.innerHTML = backupsHTML(); wireBackups(st); };
const acts = a => frames.filter(f => f.type === 'backups' && f.backups.action === a);
const applyBackups = reply => receiveBackups(reply, acts(reply.action).at(-1)?.request_id);
const text = sel => (st.querySelector(sel) || { textContent: '' }).textContent;
const daysAgo = n => new Date(Date.now() - n * 86400000).toISOString();
const snaps = Array.from({ length: 14 }, (_, i) => ({ name: 'ledger-' + i, at: daysAgo(i), record: 1400 - i, encrypted: true }));
const view = over => Object.assign({ enabled: true, backup_keep: 14, secrets_ok: true, export_ok: true, encrypting: true, escrow_covers: true,
  escrow_checked_at: daysAgo(0), snapshots: snaps, last_pass: { at: daysAgo(0), outcome: 'ok' } }, over || {});
run(async () => {
  const css = () => document.querySelector('link[href="./views/backups.css"]');
  assert(!css(), 'the stylesheet was fetched before Backups was drawn');
  S.stats = { name: 'Taylor' };
  applyBackups({ action: 'status', view: view() });
  const root = st.querySelector('[data-backups]');
  assert(root.querySelector('h2').textContent === 'Backups' && text('.backups__intro') === 'What is kept of Taylor, each copy proved to restore, and your way back if this computer is lost.', 'the heading and its sentence: ' + text('.backups__intro'));
  await until(() => css()?.sheet);
  assert(getComputedStyle(root).maxWidth === '820px', 'the stylesheet did not load: ' + getComputedStyle(root).maxWidth);
  const heads = [...root.querySelectorAll('h3')].map(h => h.textContent);
  assert(JSON.stringify(heads) === JSON.stringify(['What is kept', 'Your way back · the escrow file', 'What a restore does']), 'the parts in the board\'s order: ' + JSON.stringify(heads));

  // WHAT IS KEPT: the last pass's two sentences; the newest three; then how many more, and all on a press.
  assert(/^Verified and backed up every day\. Last at .+ today: the record was checked and the copy proved to restore\.$/.test(text('[data-lastpass]')), 'the last pass: ' + text('[data-lastpass]'));
  assert(st.querySelectorAll('[data-snapshot]').length === 3 && /^Today, .+ · through record 1400 · encrypted/.test(text('[data-snapshot]')), 'the newest three: ' + text('[data-snapshot]'));
  assert(/^11 more, back to .+\.All 14$/.test(text('.backups__more')), 'how many more: ' + text('.backups__more'));
  st.querySelector('[data-snaps-all]').click();
  assert(st.querySelectorAll('[data-snapshot]').length === 14 && !st.querySelector('[data-snaps-all]'), 'All 14 lists every snapshot');
  const row = st.querySelector('[data-snapshot]');
  assert(row.querySelector('[data-download]') && row.querySelector('[data-verify]') && row.querySelector('[data-restore]'), 'a row keeps Download, Prove it restores and Restore…');

  // YOUR WAY BACK: the check said for what it proves; no form open; each button opens its own.
  assert(/^Checked today, .+: it holds the key that encrypts snapshots here\.$/.test(text('[data-escrow-line]')), 'the escrow line: ' + text('[data-escrow-line]'));
  assert(!document.getElementById('bk-pass') && !document.getElementById('bk-file'), 'with a file checked here, no form is open');
  const opener = w => st.querySelector('[data-escrow-open="' + w + '"]');
  assert(opener('check').textContent === 'Check the file' && opener('make').textContent === 'Make a new one…', 'the two buttons');
  opener('make').click();
  assert(document.activeElement === document.getElementById('bk-pass') && opener('make').getAttribute('aria-expanded') === 'true', 'Make opens its form, which takes the focus');
  document.getElementById('bk-pass').value = 'typed then left';
  st.querySelector('[data-escrow-close]').click();
  assert(!document.getElementById('bk-pass'), 'Cancel closes the form');
  assert(!holders.some(unsaved => unsaved()), 'A CLOSED FORM STILL HOLDS WHAT WAS TYPED: the page would refuse a reload over it');
  opener('make').click();
  assert(document.getElementById('bk-pass').value === '', 'WHAT WAS TYPED IN A CLOSED FORM CAME BACK');
  opener('check').click();
  assert(!document.getElementById('bk-pass') && document.getElementById('bk-file') && document.getElementById('bk-pass2'), 'Check opens its form in Make\'s place');
  assert(/^Keep a recent snapshot somewhere other than this computer\./.test(text('[data-offsite]')), 'the way back from a lost machine: ' + text('[data-offsite]'));

  // No file checked here: making one is open from the start, and the page says none was checked.
  forgetBackupsSecrets();
  applyBackups({ action: 'status', view: view({ escrow_checked_at: '', escrow_covers: false }) });
  assert(text('[data-escrow-line]') === 'No escrow file has been checked on this computer. Without one, a lost computer means a lost identity.', 'no check: ' + text('[data-escrow-line]'));
  assert(document.getElementById('bk-pass') && opener('make').textContent === 'Make an escrow file…', 'making one is open from the start');
  // A FORM LEFT WHILE ITS ANSWER IS AWAITED TAKES NO ANSWER: the file a late
  // answer carries is not saved, no step 2 follows, and the page asks again
  // what the host holds.
  let saves = 0;
  const click = HTMLAnchorElement.prototype.click;
  HTMLAnchorElement.prototype.click = function () { saves++; };
  document.getElementById('bk-pass').value = 'correct horse battery'; document.getElementById('bk-again').value = 'correct horse battery';
  document.getElementById('bk-create').click();
  assert(acts('escrow_create').length === 1, 'the file was not asked for');
  const statusBefore = acts('status').length;
  st.querySelector('[data-escrow-close]').click();
  assert(acts('status').length === statusBefore + 1, 'leaving the form did not ask again what the host holds');
  receiveBackups({ action: 'escrow_create', view: view({ escrow_checked_at: '', escrow_covers: false }), file_b64: btoa('late'), file_name: 'late.age' }, acts('escrow_create')[0].request_id);
  HTMLAnchorElement.prototype.click = click;
  assert(saves === 0 && !st.querySelector('[data-step="2"]') && acts('escrow_check').length === 0, 'A CANCELLED FORM TOOK ITS LATE ANSWER: saved ' + saves + ', step 2 ' + !!st.querySelector('[data-step="2"]'));

  // A check made for a key this computer no longer holds is said as that, never as covering what is here.
  applyBackups({ action: 'status', view: view({ escrow_covers: false, encrypting: false }) });
  assert(/^Checked today, .+, for a key no longer on this computer\.$/.test(text('[data-escrow-line]')), 'a check for another key: ' + text('[data-escrow-line]'));

  // SAFE IS NOT PROOF: the line says the record verifies only when a pass
  // recorded it; a home with no record, or a pass that failed, says that.
  applyBackups({ action: 'status', view: view({ safe: true, key_absent: true, record_absent: true, last_pass: undefined, snapshots: [] }) });
  assert(!/verif/.test(text('[data-lastpass]')), 'a SAFE start with no record is said to verify: ' + text('[data-lastpass]'));
  applyBackups({ action: 'status', view: view({ safe: true, last_pass: { at: daysAgo(0), outcome: 'failed', detail: 'entry 9 does not verify' } }) });
  assert(/FAILED/.test(text('[data-lastpass]')) && /entry 9 does not verify/.test(text('[data-lastpass]')) && !/walked and verifies/.test(text('[data-lastpass]')), 'a failed pass in SAFE: ' + text('[data-lastpass]'));
  applyBackups({ action: 'status', view: view() });

  // WHAT A RESTORE DOES: folded, as the person leaves it, open on a plan; confirmed by what the page shows.
  const box = () => st.querySelector('details[data-card="restore"]');
  assert(!box().open && text('[data-restore-none]') === 'No restore has been made on this computer.', 'folded, and it says none was made');
  assert(text('#bk-restore-what') === 'Restoring from a snapshot sets aside what is here now, named before you confirm. Nothing is deleted. You confirm by typing what the page shows, and AII OS restarts into the restored copy.', 'what a restore does: ' + text('#bk-restore-what'));
  box().querySelector('summary').click();
  applyBackups({ action: 'status', view: view() });
  assert(box().open, 'opened by the person, it stays open through a redraw');
  box().querySelector('summary').click();
  applyBackups({ action: 'status', view: view() });
  assert(!box().open, 'closed by the person, it stays closed');
  st.querySelector('[data-restore]').click();
  applyBackups({ action: 'restore_plan', view: view(), plan: { source: 'snapshot', name: 'ledger-0', at: daysAgo(0), restored_to: 1400, live_through: 1400, set_aside: 0, witnessed: -1, witnessed_set_aside: 0, confirm_text: 'Taylor', aside_under: '/aside' } });
  assert(box().open && text('label[for="bk-confirm"]') === 'To confirm, type: Taylor', 'a plan opens it, and asks for what the host compares: ' + text('label[for="bk-confirm"]'));

  // NOT YET NAMED: the sentences that name them say "them".
  S.stats = {};
  forgetBackupsSecrets();
  applyBackups({ action: 'status', view: view() });
  assert(text('.backups__intro').startsWith('What is kept of them,') && text('#bk-way-what').startsWith('A sealed copy of their two keys'), 'the unnamed forms: ' + text('#bk-way-what'));
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
	// What the page would refuse a reload over, as the view says it.
	modules["/overlay.js"] = []byte(`export const holders = []; export function holdReloadWhile(unsaved) { holders.push(unsaved); }`)
	runPageInEngines(t, page, modules)
}

// BACKUPS ASKS FOR ITS STYLESHEET AGAIN AFTER A FETCH THAT FAILED, through
// the page's one owner of sheets (shell/sheet.js, here a stand-in that holds
// each fetch for the test to answer). The view is drawn while its sheet is
// held: the escrow's forms must not wait on a stylesheet. A failed fetch is
// asked for again at the next drawing, and a loaded one is not asked again.
func TestBackupsAsksForItsStylesheetAgainAfterAFailedFetch(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { asks } from './shell/sheet.js';
import { backupsHTML, wireBackups, applyBackups } from './views/backups.js';
import { assert, run, until } from './__harness.js';
const st = document.getElementById('settings-stack');
S.renderBackups = () => { st.innerHTML = backupsHTML(); wireBackups(st); };
const view = { enabled: true, backup_keep: 8, secrets_ok: true, export_ok: true, encrypting: false, escrow_covers: false, snapshots: [] };
run(async () => {
  assert(asks.length === 0, 'the stylesheet was asked for before Backups was drawn');
  applyBackups({ action: 'status', view });
  assert(asks.length === 1 && asks[0].href === 'views/backups.css', 'the first drawing did not ask for the stylesheet: ' + JSON.stringify(asks.map(a => a.href)));
  assert(document.getElementById('bk-take') && document.getElementById('bk-pass'), 'the view waited for its stylesheet');
  applyBackups({ action: 'status', view });
  assert(asks.length === 1, 'a fetch already on its way was asked for again');
  asks[0].fail(new Error('held, then failed'));
  await until(() => asks[0].settled);
  applyBackups({ action: 'status', view });
  assert(asks.length === 2, 'A STYLESHEET THAT FAILED WAS NEVER ASKED FOR AGAIN');
  asks[1].done();
  await until(() => asks[1].settled);
  applyBackups({ action: 'status', view });
  assert(asks.length === 2, 'a stylesheet that loaded was asked for again');
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
	// shell/sheet.js's contract, held for the test to answer: one fetch per
	// sheet at a time, forgotten when it fails.
	modules["/shell/sheet.js"] = []byte(`export const asks = []; const pending = new Map();
export const sheet = href => {
  if (!pending.has(href)) pending.set(href, new Promise((done, fail) => {
    const ask = { href };
    ask.done = () => { done(); Promise.resolve().then(() => { ask.settled = true; }); };
    ask.fail = err => { pending.delete(href); fail(err); Promise.resolve().then(() => { ask.settled = true; }); };
    asks.push(ask);
  }));
  return pending.get(href);
};`)
	runPageInEngines(t, page, modules)
}
