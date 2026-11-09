//go:build !windows

package dashboard

import "testing"

// THE READER'S PAGE, shared by the conversation's tests: the real page against a
// reader that keeps its bounds (internal/interaction: a source's rows, the
// query's filter, its window and its limit, 50 to a page), and the note at the
// conversation's head (#chat-note), where Home's note was until the operator
// took Home away (2026-11-05).
const homeReaderJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const say = m => sockets[sockets.length - 1].say(m);
const $ = id => document.getElementById(id);
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const shown = el => !!el && el.getClientRects().length > 0;
const at = (h, m) => { const d = new Date(); d.setHours(h, m, 0, 0); return d.toISOString(); };
const row = (n, role, content, extra) => Object.assign({ id: 'm' + n, sequence: String(n), kind: 'message', role, content, turn_id: 't' + Math.ceil(n / 2), recorded_at: at(9, 0), details: {} }, extra);
const notice = n => ({ id: 'm' + n, sequence: String(n), kind: 'notice', role: 'system', content: 'Step ' + n, turn_id: 'tx', recorded_at: at(9, 30), details: {} });
const held = { recorded: [], transient: [] };
function answer(q) {
  const iq = q.interaction_query, w = q.interaction_window, f = iq.filter || {}, source = iq.source, limit = iq.limit || 50;
  const rows = held[source].filter(r => (!f.kinds || f.kinds.includes(r.kind)) && (!f.role || r.role === f.role));
  const index = id => rows.findIndex(r => r.id === id);
  let chosen = [];
  if (w.mode === 'latest') chosen = rows.slice(-limit);
  else if (w.mode === 'older') chosen = rows.slice(Math.max(0, index(w.anchor_id) - limit), Math.max(0, index(w.anchor_id)));
  else if (w.mode === 'newer') chosen = rows.slice(index(w.anchor_id) + 1, index(w.anchor_id) + 1 + limit);
  else if (w.mode === 'range' && index(w.anchor_id) >= 0) chosen = rows.slice(index(w.anchor_id), index(w.end_id) + 1);
  const first = chosen[0], last = chosen.at(-1), fingerprint = 'fp-' + q.request_id;
  say({ type: 'interaction_page', request_id: q.request_id,
    interaction_window: { fingerprint, has_older: !!first && rows.indexOf(first) > 0, has_newer: !!last && rows.indexOf(last) < rows.length - 1 },
    interaction_page: { version: 1, source, identity: 'i1', incarnation: source, fingerprint, cursor: iq.cursor || '', rows: chosen } });
}
const queries = () => sent.filter(f => f.type === 'query' && f.query === 'interaction');
// A note read the reader refuses once, for the failure cases.
let failNote = '';
function refuse(q) {
  failNote = '';
  say({ type: 'interaction_error', request_id: q.request_id, interaction_reason: 'INTERACTION_UNAVAILABLE', message: 'The record could not be read.' });
}
let served = 0;
// before: called with each query just before it is answered.
async function serve(before) {
  for (let n = 0; n < 400; n++) {
    await tick();
    if (served >= queries().length) return;
    const q = queries()[served++];
    if (before) before(q);
    if (failNote && q.interaction_query.limit === 1 && q.interaction_query.source === failNote) refuse(q); else answer(q);
  }
  throw Error('the reader did not settle');
}
// hide: the tab is hidden while one of the first window reads is answered, then shown.
async function open(hide) {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 300, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 300 } });
  say({ type: 'interaction_changed' });
  // The tab hides between the last window read's request and its answer.
  const hideTab = q => { if (hide && q.interaction_query.limit !== 1 && q.interaction_query.source === 'transient') Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' }); };
  await serve(hideTab);
  if (hide) { delete document.visibilityState; document.dispatchEvent(new Event('visibilitychange')); await serve(); }
}
const noteText = () => (($('chat-note').querySelector('.c-note__text') || {}).textContent || '').trim();
const noteLabel = () => (($('chat-note').querySelector('.c-note__label') || {}).textContent || '').trim();
const underNote = text => [...$('thread-inner').querySelectorAll(':scope > .interaction-item')].filter(el => shown(el) && (el.querySelector('.body') || {}).textContent === text);
const done = () => report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
`

// A READING THE PERSON'S HAND GIVES UP IS NOT KEPT OVER THEIRS (the reload
// e2e's drift at 390, traced at opus 1901). After a reload the conversation
// reads its kept range back, one source at a time; while a source's read is
// still on its way the person scrolls down the conversation, and the page
// goes. What is saved is where they are, not the bookmark they scrolled away
// from: the unfinished restore is given up with the scroll.
const givenUpJS = homeReaderJS + `
const item = id => document.querySelector('[data-turn-id="' + id + '"]');
const offset = el => el.getBoundingClientRect().top - $('thread').getBoundingClientRect().top;
for (let n = 1; n <= 150; n++) held.recorded.push(row(n, n % 2 ? 'operator' : 'resident', 'Message ' + n + '\\nAn ordinary line of the conversation.\\nAnother line to read.'));
held.transient.push(row(1, 'resident', 'Kept while the record was closed.', { turn_id: 'tr1', recorded_at: at(8, 0) }));
const kept = () => JSON.parse(sessionStorage.getItem('aii.history.position') || '{}').reading?.key || '';
async function run() {
  if (sessionStorage.getItem('given-up-pass') !== '1') {
    await open(); S.go('chat'); await serve();
    const thread = $('thread');
    for (let i = 0; i < 8 && !item('m60'); i++) {
      thread.scrollTop = 0;
      thread.dispatchEvent(new WheelEvent('wheel', { deltaY: -80, bubbles: true }));
      thread.dispatchEvent(new Event('scroll'));
      await serve();
    }
    if (!item('m60')) { report('FAIL: the conversation did not read back to m60'); return; }
    thread.scrollTop += offset(item('m60')) - 17; thread.dispatchEvent(new Event('scroll')); await tick();
    sessionStorage.setItem('given-up-pass', '1');
    location.reload(); return;
  }
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 300, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 300 } });
  say({ type: 'interaction_changed' });
  // The record's read comes back; the other source's is still on its way.
  const recorded = () => queries().find(q => q.interaction_query.source === 'recorded' && q.interaction_query.limit !== 1);
  await until(recorded, 3000);
  if (!recorded()) { report('FAIL: the kept range was not asked for'); return; }
  answer(recorded()); await tick(); await tick();
  const before = kept(), thread = $('thread');
  check(before, 'nothing was kept before the reload');
  thread.dispatchEvent(new WheelEvent('wheel', { deltaY: 120, bubbles: true }));
  thread.scrollTop = thread.scrollHeight; thread.dispatchEvent(new Event('scroll')); await tick();
  window.dispatchEvent(new Event('pagehide'));
  check(kept() && kept() !== before, 'the reading the person scrolled away from was kept over where they are: ' + before + ', then ' + kept());
  done();
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestAReadingGivenUpByTheHandIsNotKeptOverTheirs(t *testing.T) {
	runRealPage(t, `try { sessionStorage.length || localStorage.removeItem('aii.view'); } catch (e) {}
`+statusFakeSocketJS, givenUpJS, homeStyles)
}
