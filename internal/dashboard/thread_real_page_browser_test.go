//go:build !windows

package dashboard

import "testing"

// ON THE REAL PAGE (spec step 4, the fourth and sixth amendments): the
// shipped frame, driven through its own socket by a scripted host
// (statusFakeSocketJS), executes what the page-wide note and a turn's
// failure must do.
//   - A cold arrival: connected with neither frame read, nothing is drawn;
//     with the status alone, still nothing; with both, the mode's note.
//   - A reconnect: with the mode's note up, a disconnect shows the offline
//     note alone; reconnected, nothing is drawn until both frames come again,
//     then the note; mode normal, nothing.
//   - A reconnect whose continuity comes before its status: still nothing
//     until both.
//   - A turn's failure, placed by the reference its frame carries (turn_ref):
//     under its message in the annex's words; a frame that comes before its
//     row is held and placed when the row arrives; after a turn of this
//     page's is answered, another page's failure goes under that page's
//     message; an unclassed failure in the daemon's words; never the composer.
//   - The whole reference: the same id under another identity, another
//     source or another incarnation is another occurrence; its failure waits
//     for that occurrence, and the transient copy takes its own. After a
//     restart (a new incarnation) nothing from before it is drawn, and the
//     new incarnation's failure goes under its message.
const realPageMeasureJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const live = () => sockets[sockets.length - 1];
const say = m => live().say(m);
const $ = id => document.getElementById(id);
const notes = () => [...document.querySelectorAll('#page-note .c-os-note')].map(n => n.textContent.replace(/\s+/g, ' ').trim());
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const stats = { name: 'Mira', version: 'test', build: 'test', ledger_seq: 3, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 };
const safe = { mode: 'safe', safe_reason: 'their record did not verify' };
const idOf = type => sent.filter(f => f.type === type).at(-1).request_id;
const words = (n, role, content) => ({ id: 'w' + n, sequence: String(n), kind: 'message', role, content, turn_id: 't' + n, recorded_at: '2026-10-30T12:0' + n + ':00Z', details: {} });
const answered = new Set(), inc = { recorded: 'recorded', transient: 'transient' };
function readBack(held, heldT = []) {
  for (let q; (q = sent.filter(f => f.type === 'query' && f.query === 'interaction').at(-1)) && !answered.has(q.request_id);) {
    answered.add(q.request_id);
    const w = q.interaction_window, source = q.interaction_query.source;
    const list = source === 'recorded' ? held : heldT, at = id => list.findIndex(r => r.id === id);
    const rows = w.mode === 'range' ? list.slice(at(w.anchor_id), at(w.end_id) + 1)
      : w.mode === 'newer' ? list.slice(at(w.anchor_id) + 1) : w.mode === 'older' ? list.slice(0, Math.max(0, at(w.anchor_id))) : list;
    const fingerprint = 'fp-' + q.request_id;
    say({ type: 'interaction_page', request_id: q.request_id, interaction_window: { fingerprint, has_older: false, has_newer: false },
      interaction_page: { version: 1, source, identity: 'i1', incarnation: inc[source], fingerprint, cursor: '', rows } });
  }
}
// An occurrence of a message, by the key the reader gives its row.
const rowAt = (id, source = 'recorded', identity = 'i1', incarnation = inc[source]) => document.querySelector('#thread-inner > [data-interaction-key="' + CSS.escape(JSON.stringify([identity, source, incarnation, id])) + '"]');
const failureOn = (id, source) => { const n = rowAt(id, source)?.querySelector(':scope > .msg.operator > .turn-failure'); return n ? n.textContent.replace(/\s+/g, ' ').trim() : ''; };

async function run() {
  const parts = window.__parts || 'note,turn';
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  if (parts.includes('note')) await notePart();
  else { say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal' } }); await tick(); }
  if (parts.includes('turn')) await turnPart();
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
async function notePart() {
  // ── a cold arrival ──
  check(notes().length === 0, 'connected with neither frame read, the page said: ' + notes().join(' | '));
  say({ type: 'status', stats }); await tick();
  check(notes().length === 0, 'with only the status read, the page said: ' + notes().join(' | '));
  say({ type: 'continuity', continuity: safe }); await tick();
  check(notes().length === 1 && notes()[0].includes('AII OS has put Mira in safe mode'), 'with both frames read, the mode is not said: ' + notes().join(' | '));
  // ── a reconnect ──
  const before = sockets.length;
  live().drop(); await tick();
  check(notes().length === 1 && notes()[0].includes('Not connected'), 'disconnected, the page said more than that, or not that: ' + notes().join(' | '));
  await until(() => sockets.length > before, 20000);
  check(sockets.length > before, 'the page did not ask for its socket again');
  live().open(); await tick();
  check(notes().length === 0, 'reconnected, a note was drawn before its frames came again: ' + notes().join(' | '));
  say({ type: 'status', stats }); await tick();
  check(notes().length === 0, 'reconnected with only the status read, the page said: ' + notes().join(' | '));
  say({ type: 'continuity', continuity: safe }); await tick();
  check(notes().length === 1 && notes()[0].includes('safe mode'), 'after both frames came again, the note did not: ' + notes().join(' | '));
  say({ type: 'continuity', continuity: { mode: 'normal' } }); await tick();
  check(notes().length === 0, 'in mode normal, the page said: ' + notes().join(' | '));
  // ── a reconnect whose continuity comes first ──
  const again = sockets.length;
  live().drop(); await tick();
  await until(() => sockets.length > again, 20000);
  live().open(); await tick();
  say({ type: 'continuity', continuity: safe }); await tick();
  check(notes().length === 0, 'with only the continuity read, the page said: ' + notes().join(' | '));
  say({ type: 'status', stats }); await tick();
  check(notes().length === 1 && notes()[0].includes('safe mode'), 'continuity first, then the status: the note did not come: ' + notes().join(' | '));
  say({ type: 'continuity', continuity: { mode: 'normal' } }); await tick();
}
async function turnPart() {
  // ── a turn's failure on its turn, by the reference its frame carries ──
  say({ type: 'config', config: { llm: { provider: 'Example AI', model: 'm1', resolved_provider: 'Example AI', resolved_model: 'm1', timeout_seconds: 120 }, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], dashboard: {} } });
  S.go('chat'); await tick();
  const held = [words(1, 'operator', 'Hello'), words(2, 'resident', 'Hello to you.')], heldT = [];
  const changed = async () => { say({ type: 'interaction_changed' }); await tick(); readBack(held, heldT); await tick(); };
  await changed();
  const ref = (n, other) => Object.assign({ identity: 'i1', source: 'recorded', incarnation: inc.recorded, id: 'w' + n }, other);
  const send = async (n, text) => {
    $('msg-input').value = text; $('send-btn').click();
    say({ type: 'chat_ack', request_id: idOf('chat'), message: 'accepted' });
    held.push(words(n, 'operator', text));
    say({ type: 'chat_receipt', request_id: idOf('chat'), chat_recording: { ref: ref(n) } });
    await tick();
  };
  const fail = (n, failure, message, other) => say(Object.assign({ type: 'error', message: message || 'identity error: the provider failed', turn_ref: ref(n, other) }, failure));
  const drawn = (n, source) => !!rowAt('w' + n, source)?.querySelector(':scope > .msg.operator');
  // 1. Its row drawn, then the frame.
  await send(3, 'Are you there?'); readBack(held); await until(() => drawn(3), 5000);
  fail(3, { turn_failure: 'provider_failed', turn_reason: 'connection refused' }); await tick();
  const first = 'AII OS couldn\'t get an answer from Example AI: connection refused. Mira is unchanged; try again, or change the model in Settings.';
  check(failureOn('w3') === first, 'the first failure is not under its turn in the annex\'s words: "' + failureOn('w3') + '"');
  check(!failureOn('w1'), 'an earlier turn took the failure');
  // 2. The frame before its row: held, then placed when the row arrives.
  await send(4, 'Try again?');
  fail(4, { turn_failure: 'provider_timeout' }); await tick();
  check(!drawn(4) && !document.querySelector('.turn-failure[data-text^="Example AI did not answer"]'), 'a failure was drawn before its message was');
  readBack(held); await until(() => drawn(4), 5000); await tick();
  check(failureOn('w4') === 'Example AI did not answer in time. Mira is unchanged.', 'the held failure did not take its place when its row arrived: "' + failureOn('w4') + '"');
  check(failureOn('w3') === first, 'the second turn cleared or took the first failure: "' + failureOn('w3') + '"');
  // 3. A turn of this page's answered, then another page's turn fails: its failure goes under that page's message.
  await send(5, 'One more here.'); readBack(held); await until(() => drawn(5), 5000);
  held.push(words(6, 'resident', 'Here.')); await changed();
  held.push(words(7, 'operator', 'From the other page.')); fail(7, { turn_failure: 'provider_failed', turn_reason: 'Service Unavailable' }); await changed();
  await until(() => drawn(7), 5000); await tick();
  check(!failureOn('w5'), 'another page\'s failure went under this page\'s answered message: "' + failureOn('w5') + '"');
  check(failureOn('w7').includes('Service Unavailable'), 'another page\'s failure is not under its own message: "' + failureOn('w7') + '"');
  // 4. An unclassed failure: the daemon's own words, on its turn.
  await send(8, 'And this?'); readBack(held); await until(() => drawn(8), 5000);
  fail(8, { turn_failure: 'turn' }, 'identity error: the record could not be written'); await tick();
  check(failureOn('w8') === 'identity error: the record could not be written', 'the unclassed failure is not under its turn in the daemon\'s words: "' + failureOn('w8') + '"');
  const composer = ($('chat-error') || {}).textContent || '';
  check(!composer.includes('Example AI') && !composer.includes('identity error'), 'the composer drew a turn\'s failure: ' + composer);
  // 5. The whole reference: the same id under another identity, source or incarnation is another occurrence.
  const said = why => ({ turn_failure: 'provider_failed', turn_reason: why });
  await send(9, 'Which one?'); readBack(held, heldT); await until(() => drawn(9), 5000);
  fail(9, said('another identity'), '', { identity: 'i2' });
  fail(9, said('another incarnation'), '', { incarnation: 'before' });
  fail(9, said('the transient copy'), '', { source: 'transient', incarnation: inc.transient });
  await tick();
  check(!failureOn('w9'), 'another occurrence\'s failure with the same id went under this message: "' + failureOn('w9') + '"');
  heldT.push(words(9, 'operator', 'Which one?')); await changed();
  await until(() => drawn(9, 'transient'), 5000); await tick();
  check(failureOn('w9', 'transient').includes('the transient copy'), 'the transient copy did not take its own failure: "' + failureOn('w9', 'transient') + '"');
  check(!failureOn('w9'), 'the recorded message took another occurrence\'s failure: "' + failureOn('w9') + '"');
  fail(9, said('its own')); await tick();
  check(failureOn('w9').includes('its own'), 'the message\'s own failure is not under it: "' + failureOn('w9') + '"');
  check(failureOn('w9', 'transient').includes('the transient copy'), 'the transient copy lost its own failure to the recorded one: "' + failureOn('w9', 'transient') + '"');
  // 6. A restart: a new incarnation of each source. A failure held for the one left behind never takes the new
  //    incarnation's message of the same id; nothing from before is drawn; the new incarnation's failure is.
  await send(10, 'Before the restart.');
  fail(10, said('before the restart')); await tick();
  const restarted = sockets.length;
  live().drop(); await tick();
  await until(() => sockets.length > restarted, 20000);
  inc.recorded = 'recorded-2'; inc.transient = 'transient-2';
  live().open(); await tick();
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal' } });
  // The reader re-reads only what is on screen; the operator's jump to the latest reads both sources, and each of
  // its questions is answered as it comes.
  await changed(); $('jump-latest').click();
  await until(() => { readBack(held, heldT); return drawn(10) && drawn(9, 'transient'); }, 5000); await tick();
  const before = [...document.querySelectorAll('#thread-inner .turn-failure')].map(n => n.textContent.replace(/\s+/g, ' ').trim());
  check(drawn(10) && before.length === 0, 'after the restart, a failure of the incarnation left behind was drawn: ' + before.join(' | '));
  fail(10, said('after the restart')); await tick();
  check(failureOn('w10').includes('after the restart'), 'the new incarnation\'s failure is not under its message: "' + failureOn('w10') + '"');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// runOnTheRealPage runs the named parts ("note", "turn") of the measure.
func runOnTheRealPage(t *testing.T, parts string) {
	t.Helper()
	runStatusMeasurement(t, "window.__parts = '"+parts+"';\n"+statusFakeSocketJS, realPageMeasureJS)
}
