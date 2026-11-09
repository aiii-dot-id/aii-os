//go:build !windows

package dashboard

import "testing"

// A WHY? ANSWER IS FOR THE BELIEF AS IT STANDS, AND A QUOTE IS THE BELIEF
// WHOLE (step 9, round two; a review 17:03Z, host 17:04Z). On the real page:
// a belief that changes while its trail is shown asks again, and the new
// answer is drawn; opening it again on purpose asks again; a lost
// connection ends the shown answer, and once connected it is asked again;
// "Quote to {name}" carries a long belief whole, its late qualifier too.
const beliefFreshnessJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const live = () => sockets[sockets.length - 1];
const say = m => live().say(m);
const $ = id => document.getElementById(id);
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const stats = { name: 'Mira', version: 'test', build: 'test', ledger_seq: 40, lifetime_ticks: 1, belief_count: 2, intention_count: 0, experience_count: 0, reflection_count: 0 };
const LONG = 'A deploy should go to staging first, and it should stay there for a full working day while the people who use it try the change on their own work, and only then go to production, ' + 'but only when Ola has signed off.';
const belief = statement => ({ id: 'b1', statement, ring: 3, status: 'confirmed', evidence_count: 4, confidence: 0.91 });
const identity = statement => ({ type: 'identity', identity: { beliefs: [belief(statement), { id: 'b2', statement: LONG, ring: 3, status: 'new', evidence_count: 1, confidence: 0.4 }], intentions: [], experiences: [] } });
const trails = () => sent.filter(f => f.type === 'query' && f.query === 'belief_trail' && f.belief_id === 'b1');
const answer = (q, statement) => say({ type: 'belief_trail', request_id: q.request_id, belief_trail: { belief: { id: 'b1', statement, ring: 3, confidence: 0.91, evidence_count: 4, archived: false, held_since: '2026-10-21T09:00:00Z', last_seq: 31, recall: null, recall_count: null, recalled_at: null }, origins: [], contradicts: [], supersedes: [], superseded_by: null, truncated: { origins: false, contradicts: false, supersedes: false } } });
const stands = () => norm(($('beliefs-view').querySelector('.c-trail') || {}).textContent);
async function run() {
  await until(() => sockets.length > 0);
  live().open(); await tick();
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 40 } });
  location.hash = '#/identity/beliefs'; await tick();
  say(identity('A deploy should go to staging first.')); await tick();
  await until(() => document.querySelector('.c-belief[data-belief-id="b1"] .c-belief__why'), 6000).catch(() => {});
  document.querySelector('.c-belief[data-belief-id="b1"] .c-belief__why').click(); await tick();
  answer(trails().at(-1), 'A deploy should go to staging first.'); await tick();
  check(/staging first/.test(stands()), 'the first answer is not drawn');

  // The belief changes while its trail is shown: asked again, the new answer drawn.
  const before = trails().length;
  say(identity('A deploy goes to staging, always.')); await tick();
  check(trails().length === before + 1, 'a changed belief did not ask for its trail again: ' + (trails().length - before) + ' asks');
  answer(trails().at(-1), 'A deploy goes to staging, always.'); await tick();
  check(/staging, always/.test(stands()) && !/staging first/.test(stands()), 'the trail still says the belief as it stood: ' + stands());

  // Opened again on purpose: asked again.
  const opened = trails().length;
  document.querySelector('.c-belief[data-belief-id="b1"] .c-belief__why').click(); await tick();
  check(trails().length === opened + 1, 'opening Why? again did not ask again');
  answer(trails().at(-1), 'A deploy goes to staging, always.'); await tick();

  // A lost connection ends the shown answer; once connected it is asked again.
  const lost = trails().length;
  live().drop(); await tick();
  check(!/staging, always/.test(stands()), 'a lost connection left the answer shown as current: ' + stands());
  await until(() => sockets.length > 1 && live().readyState === 0, 8000);
  live().open(); await tick();
  say({ type: 'status', stats }); say(identity('A deploy goes to staging, always.')); await tick();
  check(trails().length === lost + 1, 'reconnected, the trail was not asked for again: ' + (trails().length - lost) + ' asks');

  // Quote carries a long belief whole.
  document.querySelector('.c-belief[data-belief-id="b2"] .c-belief__quote').click(); await tick();
  check($('msg-input').value === 'Mira, about "' + LONG + '": ', 'the quote is not the belief whole: ' + JSON.stringify($('msg-input').value.slice(-60)));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestAWhyAnswerIsForTheBeliefAsItStandsAndAQuoteIsWhole(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, beliefFreshnessJS, beliefsStyles)
}
