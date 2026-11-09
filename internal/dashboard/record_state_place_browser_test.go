//go:build !windows

package dashboard

import "testing"

// THE STATE OF THEIR RECORD IS TECHNICAL'S (the first-ten-minutes walk; the
// lead's item 12). On the shipped page and its socket, Settings → Technical →
// Verification draws "The state of their record" with the record's mode and
// entry. (That About in its section draws none of it is
// TestAboutInItsSectionDoesWhatItDoesInThePage's.)
const recordStatePlaceJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const say = m => sockets[sockets.length - 1].say(m);
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 3, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 9, lifetime_ticks: 3, witness_url: 'https://witness.test' } });
  say({ type: 'config', config: { witness: { url: 'https://witness.test', interval_events: 100 }, logs: { dir: 'log' }, restart_required: [] } });
  await until(() => typeof S.go === 'function');
  location.hash = '#/settings/technical/verification';
  const part = () => document.querySelector('#views [data-record-state]');
  await until(() => part() && /The state of their record/.test(part().textContent), 10000);
  check(part() && /The state of their record/.test(part().textContent) && /ledger seq\s*9/.test(part().textContent), 'Technical → Verification does not draw the state of their record: ' + (part() ? part().textContent.slice(0, 200) : 'no part'));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestTheStateOfTheirRecordIsTechnicals(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, recordStatePlaceJS, homeStyles)
}

// VERIFICATION READS THE BACKUPS ONCE FOR EACH VISIT. Entering the panel reads
// them; no draw does. So a redraw asks nothing, here or from another page; a read
// lost with the socket, or one a visit ended waiting on, is asked again on the next
// entry, however many changes of address queue in one task. The snapshot and
// escrow lines used to stay away from the page until a reload.
const recordStateLostJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const pause = ms => new Promise(r => setTimeout(r, ms));
const say = m => sockets[sockets.length - 1].say(m);
const asked = () => sent.filter(f => f.type === 'backups' && f.backups && f.backups.action === 'status').length;
const part = () => document.querySelector('#views [data-record-state]');
const CONTINUITY = { type: 'continuity', continuity: { mode: 'normal', ledger_seq: 9, lifetime_ticks: 3, witness_url: 'https://witness.test' } };
const hello = () => {
  say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 3, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  say(CONTINUITY);
  say({ type: 'config', config: { witness: { url: 'https://witness.test', interval_events: 100 }, logs: { dir: 'log' }, restart_required: [] } });
};
const VERIFICATION = '#/settings/technical/verification';
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); hello();
  await until(() => typeof S.go === 'function');
  location.hash = VERIFICATION;
  await until(() => part() && /The state of their record/.test(part().textContent), 10000);
  await until(() => asked() > 0, 3000);
  check(asked() === 1, 'the page did not ask for the view once: ' + asked());
  // Drawn again while the read is on its way: still one.
  say(CONTINUITY); await pause(50); say(CONTINUITY); await pause(50);
  check(asked() === 1, 'a redraw asked again while the read was on its way: ' + asked());
  // The socket goes before the answer comes: it is asked again on the next.
  sockets[sockets.length - 1].drop();
  await until(() => sockets.length > 1, 8000);
  check(asked() === 1, 'it was counted as asked with no socket to ask on: ' + asked());
  sockets[sockets.length - 1].open(); hello();
  await until(() => asked() === 2, 3000).catch(() => {});
  check(asked() === 2, 'a read lost with the socket was not asked again: ' + asked());
  say(CONTINUITY); await pause(50);
  check(asked() === 2, 'a redraw after the new read asked again: ' + asked());
  // The answer never comes on an open socket: leaving and coming back asks again.
  location.hash = '#/chat'; await until(() => S.view === 'chat', 3000);
  // Away, a frame that redraws the hidden panel asks nothing.
  say(CONTINUITY); await pause(50);
  check(asked() === 2, 'a redraw on another page asked for the view: ' + asked());
  location.hash = VERIFICATION;
  await until(() => asked() === 3, 3000).catch(() => {});
  check(asked() === 3, 'coming back with nothing held did not ask again: ' + asked());
  // Back on the page the form was already drawn for, one read is on its way through redraws.
  say(CONTINUITY); await pause(50); say(CONTINUITY); await pause(50);
  check(asked() === 3, 'a redraw after coming back asked again with the read on its way: ' + asked());
  // Left and back within one task: by the time either change is heard the address
  // is this page's again, and the visit that ended is still over. One fresh read.
  S.go('projects'); location.hash = VERIFICATION;
  await until(() => asked() === 4, 3000).catch(() => {});
  check(asked() === 4, 'leaving and coming back in one task did not ask again: ' + asked());
  say(CONTINUITY); await pause(50); say(CONTINUITY); await pause(50);
  check(asked() === 4, 'a redraw after that asked again with the read on its way: ' + asked());
  // Two hops and back in one task: an earlier hop does not retire the read the return made.
  S.go('projects'); S.go('chat'); location.hash = VERIFICATION;
  await until(() => asked() >= 5, 3000).catch(() => {});
  await pause(100);
  check(asked() === 5, 'two hops and back in one task did not ask once: ' + asked());
  say(CONTINUITY); await pause(50);
  check(asked() === 5, 'a redraw after two hops asked again with the read on its way: ' + asked());
  // The same address twice among them: an actual visit, not an address reused.
  S.go('projects'); location.hash = VERIFICATION; S.go('chat'); location.hash = VERIFICATION;
  await until(() => asked() >= 6, 3000).catch(() => {});
  await pause(100);
  check(asked() === 6, 'the address twice in one task did not ask once: ' + asked());
  say(CONTINUITY); await pause(50);
  check(asked() === 6, 'a redraw after that asked again with the read on its way: ' + asked());
  // Another panel and back is another visit too.
  location.hash = '#/settings/technical/logs';
  await until(() => part() === null, 3000).catch(() => {});
  location.hash = VERIFICATION;
  await until(() => asked() >= 7, 3000).catch(() => {});
  check(asked() === 7, 'another panel and back did not ask once: ' + asked());
  // The answer draws the lines, and nothing is asked after it.
  say({ type: 'backups', backups: { action: 'status', view: { enabled: true, snapshots: [], escrow_covers: false } } });
  await until(() => part() && part().querySelector('[data-id-snapshot]'), 3000).catch(() => {});
  check(part() && part().querySelector('[data-id-snapshot]') && part().querySelector('[data-id-escrow]'), 'the answer did not draw the snapshot and escrow lines: ' + (part() && part().textContent));
  say(CONTINUITY); await pause(50);
  check(asked() === 7, 'the page asked again with the view held: ' + asked());
  location.hash = '#/chat'; await until(() => S.view === 'chat', 3000);
  location.hash = VERIFICATION; await until(() => S.view === 'technical', 3000); await pause(100);
  check(asked() === 7, 'coming back with the view held asked for it: ' + asked());
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestVerificationReadsTheBackupsAgainAfterALoss(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, recordStateLostJS, homeStyles)
}

// A READ WAITING ON SETTINGS' CODE BELONGS TO ITS VISIT. Entering Verification
// asks for the backups once Settings' code is in. Left before then, it asks nothing
// from the page they went to; entered again before then, it asks once.
const recordStateHeldJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const pause = ms => new Promise(r => setTimeout(r, ms));
const say = m => sockets[sockets.length - 1].say(m);
const asked = () => sent.filter(f => f.type === 'backups' && f.backups && f.backups.action === 'status').length;
const VERIFICATION = '#/settings/technical/verification';
// Holds the door the read waits on, and gives what lets it through.
const hold = () => {
  const door = S.settingsIn;
  let open;
  const held = new Promise(r => { open = r; });
  S.settingsIn = () => held.then(door);
  return () => { S.settingsIn = door; open(); };
};
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open();
  say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 3, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 9, lifetime_ticks: 3, witness_url: 'https://witness.test' } });
  say({ type: 'config', config: { witness: { url: 'https://witness.test', interval_events: 100 }, logs: { dir: 'log' }, restart_required: [] } });
  await until(() => typeof S.go === 'function');
  let release = hold();
  location.hash = VERIFICATION;
  await until(() => S.view === 'technical' && document.querySelector('#views [data-panel="verification"]'), 10000);
  check(asked() === 0, 'it asked before the code it waits on was in: ' + asked());
  location.hash = '#/chat'; await until(() => S.view === 'chat', 3000);
  release();
  await S.settingsIn();
  // waits by design: a read that must not be sent gives nothing to wait on
  await pause(100);
  check(asked() === 0, 'a visit that ended sent its read from the page they went to: ' + asked());
  release = hold();
  location.hash = VERIFICATION; await until(() => S.view === 'technical', 3000);
  location.hash = '#/chat'; await until(() => S.view === 'chat', 3000);
  location.hash = VERIFICATION; await until(() => S.view === 'technical', 3000);
  check(asked() === 0, 'it asked before the code it waits on was in, on its return: ' + asked());
  release();
  await until(() => asked() > 0, 3000).catch(() => {});
  // waits by design: a second read would follow the first with nothing to wait on
  await pause(100);
  check(asked() === 1, 'entered twice while Settings was on its way, it did not ask once: ' + asked());
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestAVerificationReadWaitingOnSettingsBelongsToItsVisit(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, recordStateHeldJS, homeStyles)
}
