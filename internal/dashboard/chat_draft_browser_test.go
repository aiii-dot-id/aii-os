//go:build !windows

package dashboard

import "testing"

// A REPLY IS DRAWN AS IT IS WRITTEN AND SETTLED ONCE, on the
// real page with the socket's frames as draft.go sends them:
//   - drawn as it grows, at the thread's live end, named as still being
//     written, and said to nobody (no live region holds it, and the page's
//     announcer says nothing);
//   - a frame not newer, a delta out of step and another turn's draft are
//     not drawn over it;
//   - a call withdrawn shows its words struck, with its cause, beside the
//     kept text;
//   - its end and then the recorded reply settle it: the draft goes, the
//     recorded row stands once, and the withdrawn words stay above it;
//   - a late frame of the settled turn draws nothing;
//   - a reader who has scrolled up stays where they are as it grows, and the
//     composer keeps its focus and words.
const chatDraftHeadJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const live = () => sockets[sockets.length - 1];
const say = m => live().say(m);
const $ = id => document.getElementById(id);
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const now = new Date().toISOString();
const stats = { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 };
const answered = new Set();
function readBack(rows) {
  for (let q; (q = sent.filter(f => f.type === 'query' && f.query === 'interaction').at(-1)) && !answered.has(q.request_id);) {
    answered.add(q.request_id);
    const source = q.interaction_query.source, fingerprint = 'fp-' + q.request_id;
    say({ type: 'interaction_page', request_id: q.request_id, interaction_window: { fingerprint, has_older: false, has_newer: false },
      interaction_page: { version: 1, source, identity: 'i1', incarnation: source, fingerprint, cursor: '', rows: source === 'recorded' ? rows : [] } });
  }
}
const changed = async rows => { say({ type: 'interaction_changed' }); await tick(); readBack(rows); await tick(); };
const ref = { identity: 'i1', source: 'recorded', incarnation: 'recorded', id: 'op-1' };
const draft = d => say({ type: 'draft', draft: Object.assign({ ref }, d) });
const draftEl = () => document.querySelector('[data-reply-draft]');
const draftText = () => norm(draftEl()?.querySelector('.body')?.textContent);
async function begin(rows) {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 9 } });
  S.go('chat'); await tick();
  await changed(rows);
}
`

const chatDraftSettleJS = chatDraftHeadJS + `async function run() {
  await begin([]);
  const said = $('announce').textContent;
  say({ type: 'response', stream: true, message: '' });
  draft({ seq: 1, snapshot: true, call: 1, add: 'Reading ' });
  await until(() => draftText() === 'Reading', 4000).catch(() => {});
  draft({ seq: 2, call: 1, add: 'the file.' });
  await until(() => draftText() === 'Reading the file.', 4000).catch(() => {});
  check(draftText() === 'Reading the file.', 'the draft was not drawn as it grew: ' + draftText());
  const el = draftEl();
  check(el && el.getAttribute('aria-busy') === 'true' && el.getAttribute('aria-label') === "Mira's reply, still being written", 'the draft is not named as still being written');
  check(el && !el.closest('[aria-live]') && $('announce').textContent === said, 'the draft was said as it grew');
  check(el && el.parentElement === $('thread-inner'), 'the draft is not in the thread');

  draft({ seq: 2, call: 1, add: ' not newer' });
  draft({ seq: 9, call: 1, add: ' out of step' });
  draft({ ref: Object.assign({}, ref, { id: 'op-2' }), seq: 1, snapshot: true, call: 1, add: 'Another turn' });
  await tick();
  check(draftText() === 'Reading the file.' && document.querySelectorAll('[data-reply-draft]').length === 1, 'a frame not newer, out of step or of another turn was drawn: ' + draftText());

  draft({ seq: 3, snapshot: true, call: 1, kept: 'Reading the file.' });
  draft({ seq: 4, snapshot: true, call: 2, kept: 'Reading the file.', add: 'Half' });
  draft({ seq: 5, snapshot: true, call: 2, kept: 'Reading the file.', withdrawals: [{ call: 2, words: 'Half', why: 'stopped' }] });
  await until(() => draftEl()?.querySelector('del'), 4000).catch(() => {});
  const del = draftEl()?.querySelector('del'), why = draftEl()?.querySelector('.reply-draft__why');
  check(draftText() === 'Reading the file.', 'the kept text did not stand beside the withdrawn words: ' + draftText());
  check(del && del.textContent === 'Half' && why && why.textContent === 'These words were not kept: you stopped the reply.', 'the withdrawn words were not shown struck with their cause');
  // An older snapshot, as a page that connected may be sent after newer frames, is not drawn.
  draft({ seq: 4, snapshot: true, call: 2, kept: 'An older draft', add: '' });
  await tick();
  check(draftText() === 'Reading the file.', 'an older snapshot was drawn over a newer draft: ' + draftText());

  draft({ seq: 6, snapshot: true, call: 2, kept: 'Reading the file.', withdrawals: [{ call: 2, words: 'Half', why: 'stopped' }], end: 'kept' });
  await until(() => draftEl() && !draftEl().hasAttribute('aria-busy'), 4000).catch(() => {});
  check(draftEl() && !draftEl().hasAttribute('aria-busy'), 'the ended draft is still named as being written');
  say({ type: 'response', role: 'identity', done: true, message: 'Reading the file.' });
  await changed([
    { id: 'op-1', sequence: '1', kind: 'message', role: 'operator', content: 'Read it', turn_id: 'tA', recorded_at: now, details: {} },
    { id: 'r-1', sequence: '2', kind: 'message', role: 'resident', content: 'Reading the file.', turn_id: 'tA', recorded_at: now, details: {} },
  ]);
  await until(() => !draftEl(), 4000).catch(() => {});
  const bodies = [...$('thread-inner').querySelectorAll('.record-message .body')].map(b => norm(b.textContent)).filter(t => t === 'Reading the file.');
  check(!draftEl() && bodies.length === 1, 'the recorded reply did not settle the draft once: ' + bodies.length + ' drawn, draft ' + !!draftEl());
  const note = document.querySelector('.reply-draft__settled');
  const reply = [...$('thread-inner').children].find(c => norm(c.querySelector('.record-message .body')?.textContent) === 'Reading the file.');
  check(note && reply && note.nextElementSibling === reply, 'the withdrawn words did not stay above the recorded reply');

  draft({ seq: 9, snapshot: true, call: 3, add: 'late' });
  await tick();
  check(!draftEl(), 'a late frame of the settled turn was drawn');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

const chatDraftReaderJS = chatDraftHeadJS + `async function run() {
  const rows = [];
  for (let i = 1; i <= 40; i++) rows.push({ id: 'w' + i, sequence: String(i), kind: 'message', role: i % 2 ? 'operator' : 'resident', content: 'A long enough line of the conversation, number ' + i + ', to give the thread room to scroll.', turn_id: 't' + Math.ceil(i / 2), recorded_at: now, details: {} });
  await begin(rows);
  const thread = $('thread');
  await until(() => thread.scrollHeight > thread.clientHeight + 200, 4000).catch(() => {});
  thread.scrollTop = 0;
  thread.dispatchEvent(new WheelEvent('wheel', { deltaY: -80, bubbles: true }));
  await tick();
  const input = $('msg-input');
  input.focus(); input.value = 'half a thought';
  const top = thread.scrollTop;
  draft({ seq: 1, snapshot: true, call: 1, add: 'Line one.\\n' });
  for (let s = 2; s <= 12; s++) draft({ seq: s, call: 1, add: 'Another line of the reply as it is written.\\n' });
  await until(() => draftText().includes('Another line'), 4000).catch(() => {});
  await tick();
  check(Math.abs(thread.scrollTop - top) <= 1, 'a reader who had scrolled up was moved as the draft grew: ' + top + ' to ' + thread.scrollTop);
  check(document.activeElement === input && input.value === 'half a thought', 'the composer lost its focus or its words as the draft grew');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

// THE DRAFT SETTLES IN EITHER ORDER, AND GIVES WAY AFTER A LOST CONNECTION:
//   - its module held from arriving while the turn's frames and its recorded
//     reply come: released, it draws no draft beside the reply;
//   - the recorded reply read before the turn's end: the draft is not
//     settled until its end comes, and that end's withdrawal stands above
//     the reply;
//   - the connection lost before the turn's end, and found again too late to
//     be sent it: the next turn's draft is drawn;
//   - the same while its module is still being fetched, so that nothing of
//     it has run when the connection is lost: released, it is told the loss
//     after the frame that came before it, and the next turn's draft is
//     drawn;
//   - the connection lost before the turn's end, with the reply already
//     read: the draft stands aside for the reply but is not settled, and the
//     end that comes when the page connects again leaves its withdrawal
//     above the reply.
const chatDraftHeldModuleJS = `export * from './draft-real.js';
await new Promise(r => { window.__releaseDraft = r; });
`

// chatDraftUnrunModuleJS holds the draft module before any of it runs: its
// hooks are not set until it is released.
const chatDraftUnrunModuleJS = `await new Promise(r => { window.__releaseDraft = r; });
const real = await import('./draft-real.js');
export const receiveDraft = d => real.receiveDraft(d);
export const lost = () => real.lost();
`

const chatDraftHeldJS = chatDraftHeadJS + `async function run() {
  await begin([]);
  draft({ seq: 1, snapshot: true, call: 1, add: 'Done.' });
  await until(() => window.__releaseDraft, 4000).catch(() => {});
  draft({ seq: 2, snapshot: true, call: 1, kept: 'Done.', end: 'kept' });
  say({ type: 'response', role: 'identity', done: true, message: 'Done.' });
  await changed([
    { id: 'op-1', sequence: '1', kind: 'message', role: 'operator', content: 'Go', turn_id: 'tA', recorded_at: now, details: {} },
    { id: 'r-1', sequence: '2', kind: 'message', role: 'resident', content: 'Done.', turn_id: 'tA', recorded_at: now, details: {} },
  ]);
  await until(() => [...$('thread-inner').querySelectorAll('.record-message .body')].some(b => norm(b.textContent) === 'Done.'), 4000).catch(() => {});
  window.__releaseDraft?.();
  await until(() => S.draftRows, 4000).catch(() => {});
  await tick(); await tick();
  const drawn = [...$('thread-inner').querySelectorAll('.record-message .body')].filter(b => norm(b.textContent) === 'Done.').length;
  check(window.__releaseDraft && !draftEl() && drawn === 1, 'a draft whose module came after its reply was drawn beside it: draft ' + !!draftEl() + ', replies ' + drawn);
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

const chatDraftRecordFirstJS = chatDraftHeadJS + `async function run() {
  await begin([]);
  draft({ seq: 1, snapshot: true, call: 1, add: 'Kept.' });
  draft({ seq: 2, snapshot: true, call: 1, kept: 'Kept.' });
  draft({ seq: 3, snapshot: true, call: 2, kept: 'Kept.', add: 'Half' });
  await until(() => draftText() === 'Kept.\n\nHalf' || draftText() === 'Kept. Half', 4000).catch(() => {});
  await changed([
    { id: 'op-1', sequence: '1', kind: 'message', role: 'operator', content: 'Go', turn_id: 'tA', recorded_at: now, details: {} },
    { id: 'r-1', sequence: '2', kind: 'message', role: 'resident', content: 'Kept.', turn_id: 'tA', recorded_at: now, details: {} },
  ]);
  await tick();
  check(draftEl(), 'the draft was settled by its reply before its turn had ended');
  draft({ seq: 4, snapshot: true, call: 2, kept: 'Kept.', withdrawals: [{ call: 2, words: 'Half', why: 'provider' }], end: 'kept' });
  await until(() => !draftEl(), 4000).catch(() => {});
  const note = document.querySelector('.reply-draft__settled');
  const reply = [...$('thread-inner').children].find(c => norm(c.querySelector('.record-message .body')?.textContent) === 'Kept.');
  check(!draftEl() && note && reply && note.nextElementSibling === reply && note.querySelector('del')?.textContent === 'Half' &&
    note.querySelector('.reply-draft__why')?.textContent === 'These words were not kept: the provider stopped.', 'the end that came after the reply did not leave its withdrawal above it');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

const chatDraftLostJS = chatDraftHeadJS + `async function run() {
  await begin([]);
  draft({ seq: 1, snapshot: true, call: 1, add: 'The first turn' });
  await until(() => draftText() === 'The first turn', 4000).catch(() => {});
  sockets[0].drop();
  await until(() => sockets.length > 1, 8000).catch(() => {});
  sockets[sockets.length - 1].open(); await tick();
  say({ type: 'status', stats });
  draft({ ref: Object.assign({}, ref, { id: 'op-2' }), seq: 1, snapshot: true, call: 1, add: 'The next turn' });
  await until(() => draftText() === 'The next turn', 4000).catch(() => {});
  check(draftText() === 'The next turn' && document.querySelectorAll('[data-reply-draft]').length === 1, 'after a lost connection, the next turn\'s draft was not drawn: ' + draftText());
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

const chatDraftLostWhileFetchedJS = chatDraftHeadJS + `async function run() {
  await begin([]);
  draft({ seq: 1, snapshot: true, call: 1, add: 'The first turn' });
  await until(() => window.__releaseDraft, 4000).catch(() => {});
  check(window.__releaseDraft && !S.draftRows, 'the draft module ran before it was released');
  sockets[0].drop();
  await until(() => sockets.length > 1, 8000).catch(() => {});
  sockets[sockets.length - 1].open(); await tick();
  say({ type: 'status', stats });
  draft({ ref: Object.assign({}, ref, { id: 'op-2' }), seq: 1, snapshot: true, call: 1, add: 'The next turn' });
  window.__releaseDraft?.();
  await until(() => draftText() === 'The next turn', 4000).catch(() => {});
  check(draftText() === 'The next turn' && document.querySelectorAll('[data-reply-draft]').length === 1, 'with its module still being fetched when the connection was lost, the next turn\'s draft was not drawn: ' + draftText());
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

const chatDraftLostThenEndJS = chatDraftHeadJS + `async function run() {
  await begin([]);
  const rows = [
    { id: 'op-1', sequence: '1', kind: 'message', role: 'operator', content: 'Go', turn_id: 'tA', recorded_at: now, details: {} },
    { id: 'r-1', sequence: '2', kind: 'message', role: 'resident', content: 'Kept.', turn_id: 'tA', recorded_at: now, details: {} },
  ];
  draft({ seq: 1, snapshot: true, call: 1, add: 'Kept.' });
  draft({ seq: 2, snapshot: true, call: 1, kept: 'Kept.' });
  draft({ seq: 3, snapshot: true, call: 2, kept: 'Kept.', add: 'Half' });
  await until(() => draftText().includes('Half'), 4000).catch(() => {});
  await changed(rows);
  check(draftEl(), 'the draft was settled by its reply before its turn had ended');
  sockets[0].drop();
  await until(() => !draftEl(), 4000).catch(() => {});
  check(!draftEl(), 'a draft whose connection was lost was still drawn beside its reply');
  await until(() => sockets.length > 1, 8000).catch(() => {});
  sockets[sockets.length - 1].open(); await tick();
  say({ type: 'status', stats });
  await changed(rows);
  check(!draftEl() && !document.querySelector('.reply-draft__settled'), 'drawn again before the end came: draft ' + !!draftEl() + ', a note ' + !!document.querySelector('.reply-draft__settled'));
  draft({ seq: 4, snapshot: true, call: 2, kept: 'Kept.', withdrawals: [{ call: 2, words: 'Half', why: 'provider' }], end: 'kept' });
  await until(() => document.querySelector('.reply-draft__settled'), 4000).catch(() => {});
  const note = document.querySelector('.reply-draft__settled');
  const replies = [...$('thread-inner').children].filter(c => norm(c.querySelector('.record-message .body')?.textContent) === 'Kept.');
  check(!draftEl() && note && replies.length === 1 && note.nextElementSibling === replies[0] && note.querySelector('del')?.textContent === 'Half' &&
    note.querySelector('.reply-draft__why')?.textContent === 'These words were not kept: the provider stopped.', 'the end that came after a lost connection did not leave its withdrawal above the reply');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

// A REPLY BEING WRITTEN STANDS ABOVE ITS TURN'S WORK: the operator's
// words, the reply as it is written, the turn's tool calls, the dots — as
// every chat with an AI reads. An earlier turn's work stays under its own
// reply, and the draft does not go above it.
const chatDraftAboveWorkJS = chatDraftHeadJS + `async function run() {
  const row = (id, n, kind, role, turn, extra) => Object.assign({ id, sequence: String(n), kind, role, turn_id: turn, recorded_at: now, content: '', details: {} }, extra);
  const call = (id, n, turn) => [row(id, n, 'tool_call', 'system', turn, { content: '{}', details: { tool: 'shell' } }), row('r' + id, n + 1, 'tool_result', 'system', turn, { related_id: id, outcome: 'succeeded', content: 'ok', details: { tool: 'shell' } })];
  const earlier = [row('q0', 1, 'message', 'operator', 't0', { content: 'Before' }), ...call('c0', 2, 't0'), row('a0', 4, 'message', 'resident', 't0', { content: 'Done before.' })];
  const asked = row('op-1', 5, 'message', 'operator', 't1', { content: 'Run all three' });
  await begin([...earlier, asked]);
  say({ type: 'response', stream: true, message: '' });
  draft({ seq: 1, snapshot: true, call: 1, add: 'I will run all three.' });
  await until(() => draftEl(), 4000).catch(() => {});
  const order = () => [...$('thread-inner').children].map(c => c === draftEl() ? 'draft' : c.classList.contains('thinking-row') ? 'dots' :
    c.classList.contains('turn-receipt') ? 'R[' + [...c.querySelectorAll('[data-record-key]')].map(e => JSON.parse(e.dataset.recordKey)[3]).filter(id => !id.startsWith('r')).join('+') + ']' :
    c.dataset.turnId || '').filter(Boolean).join();
  check(order() === 'q0,a0,R[c0],op-1,draft,dots', 'before its turn has work, the draft is not after the operator\'s words, or went above an earlier turn\'s work: ' + order());
  // Nothing drawn is taken out and put back as the work arrives: a row moved is a row the reader sees redrawn.
  let moved = 0; const watch = new MutationObserver(l => l.forEach(m => moved += m.removedNodes.length)); watch.observe($('thread-inner'), { childList: true });
  await changed([...earlier, asked, ...call('c1', 6, 't1')]);
  check(order() === 'q0,a0,R[c0],op-1,draft,R[c1],dots', 'the draft is not above its turn\'s work: ' + order());
  draft({ seq: 2, call: 1, add: ' Starting with one.' });
  await tick();
  check(order() === 'q0,a0,R[c0],op-1,draft,R[c1],dots' && draftText() === 'I will run all three. Starting with one.', 'the draft moved, or did not grow, as it was written: ' + order() + ' / ' + draftText());
  await changed([...earlier, asked, ...call('c1', 6, 't1'), ...call('c2', 8, 't1')]);
  check(order() === 'q0,a0,R[c0],op-1,draft,R[c1+c2],dots', 'more work moved the draft: ' + order());
  watch.disconnect();
  check(moved === 0, 'rows were taken out and put back as the work arrived: ' + moved);
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestADraftSettlesInEitherOrderAndGivesWayAfterALostConnection(t *testing.T) {
	real, err := staticFS.ReadFile("static/views/chat/draft.js")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("its module after its reply", func(t *testing.T) {
		runRealPageServing(t, statusFakeSocketJS, chatDraftHeldJS, homeStyles, map[string][]byte{"/views/chat/draft.js": []byte(chatDraftHeldModuleJS), "/views/chat/draft-real.js": real})
	})
	t.Run("its reply before its end", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, chatDraftRecordFirstJS, homeStyles)
	})
	t.Run("a lost connection, then the next turn", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, chatDraftLostJS, homeStyles)
	})
	t.Run("a lost connection while its module is fetched, then the next turn", func(t *testing.T) {
		runRealPageServing(t, statusFakeSocketJS, chatDraftLostWhileFetchedJS, homeStyles, map[string][]byte{"/views/chat/draft.js": []byte(chatDraftUnrunModuleJS), "/views/chat/draft-real.js": real})
	})
	t.Run("a lost connection before its end, its reply, then its end", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, chatDraftLostThenEndJS, homeStyles)
	})
}

func TestAReplyIsDrawnAsItIsWrittenAndSettledOnce(t *testing.T) {
	t.Run("drawn, withdrawn and settled", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, chatDraftSettleJS, homeStyles)
	})
	t.Run("a reader and a composer kept", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, chatDraftReaderJS, homeStyles)
	})
	t.Run("above its turn's work", func(t *testing.T) {
		runRealPage(t, statusFakeSocketJS, chatDraftAboveWorkJS, homeStyles)
	})
}
