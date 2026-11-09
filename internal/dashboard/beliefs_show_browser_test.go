//go:build !windows

package dashboard

import (
	"testing"
	"time"
)

// ABOUT {NAME} → BELIEFS, SHOW (the board AboutBeliefs), on the real page,
// with the part fetched when the tab first draws:
//   - A group labelled Show of two toggle buttons, one pressed: "All {n}",
//     pressed at first, n counting every belief held and no longer held,
//     those past the frame's window too; and "Changed this month".
//   - All is the beliefs they hold, then those no longer held, newest
//     retirement first, each in its words with one line: archived, revised
//     to a belief they hold, revised to one no longer held either. Those
//     rows have Why? and no Quote, in the row or in its trail.
//   - Changed this month keeps what changed in this calendar month of the
//     viewer's own: a held belief by changed_at, one no longer held by
//     retired_at; a held belief with no changed_at is not listed. The
//     browsers run in Auckland, where a month read in UTC is the wrong one.
//   - The search narrows both kinds; the list ends by naming those past the
//     window, opening the record; a chip pressed by keyboard keeps its
//     focus; the chip, the open trail and the search hold across redraws.
const beliefsShowJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const live = () => sockets[sockets.length - 1];
const say = m => live().say(m);
const $ = id => document.getElementById(id);
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const stats = { name: 'Mira', version: 'test', build: 'test', ledger_seq: 40, lifetime_ticks: 1, belief_count: 3, intention_count: 0, experience_count: 0, reflection_count: 0 };
// Times on this browser's own calendar: 00:01 on the 1st of this month, and
// 23:59 on the last day of the month before. Away from UTC one of the two
// falls in another month in UTC: east of it the first, west of it the second.
const now = new Date(), Y = now.getFullYear(), M = now.getMonth();
const thisMonth = new Date(Y, M, 1, 0, 1), alsoThisMonth = new Date(Y, M, 1, 0, 2), lastMonth = new Date(Y, M, 0, 23, 59), earlier = new Date(Y, M - 1, 3, 12, 0);
const day = d => d.toLocaleDateString([], { day: 'numeric', month: 'long' });
const held = [
  { id: 'h1', statement: 'A deploy goes to staging first.', ring: 3, status: 'confirmed', evidence_count: 4, confidence: 0.88, changed_at: thisMonth.toISOString() },
  { id: 'h2', statement: 'Short sessions keep the work clearer.', ring: 3, status: 'new', evidence_count: 3, confidence: 0.67, changed_at: lastMonth.toISOString() },
  { id: 'h3', statement: 'The grant deadline can move if we ask early.', ring: 3, status: 'new', evidence_count: 1, confidence: 0.41 },
];
const retired = [
  { id: 'r1', statement: 'Olive oil is the only oil.', retired_at: alsoThisMonth.toISOString() },
  { id: 'r2', statement: 'Deploys go straight to production.', retired_at: lastMonth.toISOString(), replaced_by: { id: 'h1', statement: 'A deploy goes to staging first.' } },
  { id: 'r3', statement: 'Long reports are read whole.', retired_at: earlier.toISOString(), replaced_by: { id: 'r9', statement: 'Reports are skimmed.', retired: true } },
];
const frame = (beliefs, gone, more) => ({ type: 'identity', identity: { beliefs, intentions: [], experiences: [], retired: gone, retired_more: more } });
const group = () => document.querySelector('#beliefs-view [role="group"]');
const chip = n => group() && group().querySelectorAll('button')[n];
const ids = () => [...document.querySelectorAll('#beliefs-view .c-belief')].map(r => r.dataset.beliefId).join(',');
const rowOf = id => document.querySelector('#beliefs-view .c-belief[data-belief-id="' + id + '"]');
const meta = id => norm(rowOf(id).querySelector('.c-belief__held').textContent);
const empty = () => norm((document.querySelector('#beliefs-view .beliefs__empty') || {}).textContent);
const search = async words => { const s = $('mem-search'); s.value = words; s.dispatchEvent(new Event('input', { bubbles: true })); };
// A press as the keyboard makes it: a trusted key where the engine's driver
// gives one (WebKit), elsewhere the click a key press on a button makes.
const press = async (b, key) => { b.focus(); if (window.__pressKey) await window.__pressKey(key); else b.click(); };
const trailAsks = () => sent.filter(f => f.type === 'query' && f.query === 'belief_trail');

async function run() {
  check(now.getTimezoneOffset() !== 0, 'the browser is on UTC, where a month read in UTC cannot be told from one read in local time');
  await until(() => sockets.length > 0);
  live().open();
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 40 } });
  location.hash = '#/identity/beliefs';
  await until(() => S.view === 'memory' && $('beliefs-view'));
  say(frame(held, retired, 3));
  await until(() => group() && ids().split(',').length === 6);

  // ── the group, All pressed, the count ──
  check(group() && group().getAttribute('aria-label') === 'Show', 'the chips are not a group labelled Show');
  check(chip(0) && chip(0).type === 'button' && norm(chip(0).textContent) === 'All 9', 'All does not count every belief, held and no longer held: ' + norm(chip(0) && chip(0).textContent));
  check(chip(1) && chip(1).type === 'button' && norm(chip(1).textContent) === 'Changed this month', 'the second chip is not Changed this month');
  check(chip(0).getAttribute('aria-pressed') === 'true' && chip(1).getAttribute('aria-pressed') === 'false', 'All is not the one pressed at first');

  // ── All: held, then no longer held, each meta form ──
  check(ids() === 'h1,h2,h3,r1,r2,r3', 'All is not the held beliefs then those no longer held, newest retirement first: ' + ids());
  check(norm(rowOf('r1').querySelector('.c-belief__statement').textContent) === 'Olive oil is the only oil.', 'a belief no longer held is not in its words');
  check(meta('r1') === 'No longer held · since ' + day(alsoThisMonth), 'an archived belief does not say since when: ' + meta('r1'));
  check(meta('r2') === 'Revised on ' + day(lastMonth) + ' · now: A deploy goes to staging first.', 'a revised belief does not say what it is now: ' + meta('r2'));
  check(meta('r3') === 'Revised on ' + day(earlier) + ' · what replaced it is no longer held either', 'a revised belief whose replacement is gone does not say so: ' + meta('r3'));
  check(['r1', 'r2', 'r3'].every(id => !rowOf(id).querySelector('.c-belief__quote') && rowOf(id).querySelector('.c-belief__why')), 'a belief no longer held has a Quote, or no Why?');
  check(['h1', 'h2', 'h3'].every(id => rowOf(id).querySelector('.c-belief__quote')), 'a held belief lost its Quote');

  // ── the omission line, at the list's end, opening the record ──
  const moreLine = () => document.querySelector('#beliefs-view .beliefs__more');
  const link = moreLine() && moreLine().querySelector('a');
  check(link && norm(link.textContent) === '3 more beliefs no longer held are in the record.' && link.getAttribute('href') === '#/identity/record', 'the list does not name those past the window, opening the record');
  check(moreLine() && moreLine().previousElementSibling && moreLine().previousElementSibling.matches('ul.beliefs__list'), 'the omission line does not end the list');

  // ── the search, across both kinds ──
  await search('deploy');
  await until(() => ids() === 'h1,r2');
  check(ids() === 'h1,r2', 'the search does not narrow held and no longer held alike: ' + ids());
  await search('');
  await until(() => ids().split(',').length === 6);

  // ── Changed this month, pressed by keyboard ──
  await press(chip(1), 'Enter');
  await until(() => chip(1).getAttribute('aria-pressed') === 'true');
  check(chip(1).getAttribute('aria-pressed') === 'true' && chip(0).getAttribute('aria-pressed') === 'false', 'pressing Changed this month did not make it the one pressed');
  check(document.activeElement === chip(1), 'the chip pressed lost its focus when the list was drawn again');
  await until(() => ids() === 'h1,r1');
  check(ids() === 'h1,r1', 'Changed this month is not what changed in this month of the viewer\'s calendar: ' + ids());
  check(moreLine(), 'the omission line went with the chip');
  await search('olive');
  await until(() => ids() === 'r1');
  check(ids() === 'r1', 'the search does not narrow Changed this month: ' + ids());
  await search('');
  await until(() => ids() === 'h1,r1');

  // ── nothing changed this month; the chip holds across a new frame ──
  say(frame([held[1], held[2]], [retired[1]], 0));
  await until(() => empty() !== '');
  check(empty() === 'Nothing Mira believes changed this month.', 'an empty month does not say so: ' + empty());
  say({ type: 'status', stats: { ...stats, name: 'Unnamed' } }); say(frame([held[1], held[2]], [retired[1]], 0));
  await until(() => empty() === 'Nothing they believe changed this month.');
  check(empty() === 'Nothing they believe changed this month.', 'before they have a name, an empty month reads: ' + empty());
  say({ type: 'status', stats }); say(frame([held[1], held[2]], [retired[1]], 0));
  await until(() => empty() === 'Nothing Mira believes changed this month.');
  check(chip(1).getAttribute('aria-pressed') === 'true' && norm(chip(0).textContent) === 'All 3', 'a new frame lost the chip, or All did not count it: ' + norm(chip(0).textContent));
  check(!moreLine(), 'the omission line stayed with nothing past the window');
  say(frame(held, retired, 3));
  await until(() => ids() === 'h1,r1');

  // ── back to All by keyboard; Why? on a belief no longer held ──
  await press(chip(0), 'Space');
  await until(() => ids().split(',').length === 6);
  check(chip(0).getAttribute('aria-pressed') === 'true' && document.activeElement === chip(0), 'All pressed by keyboard is not pressed, or lost its focus');
  const asks = trailAsks().length;
  rowOf('r1').querySelector('.c-belief__why').click();
  await until(() => trailAsks().length > asks);
  const asked = trailAsks().at(-1);
  check(asked && asked.belief_id === 'r1', 'Why? on a belief no longer held did not ask for its trail');
  say({ type: 'belief_trail', request_id: asked.request_id, belief_trail: { belief: { id: 'r1', statement: 'Olive oil is the only oil.', ring: 3, confidence: 0.6, evidence_count: 1, archived: true, held_since: thisMonth.toISOString(), last_seq: 12, recall: null, recall_count: null, recalled_at: null }, origins: [], contradicts: [], supersedes: [], superseded_by: null, truncated: { origins: false, contradicts: false, supersedes: false } } });
  const trail = () => document.querySelector('#beliefs-view .c-trail[data-state="shown"]');
  await until(() => trail());
  check(trail() && /Olive oil/.test(trail().textContent) && ![...trail().querySelectorAll('button')].some(b => /Quote to/.test(b.textContent)), 'the trail of a belief no longer held is not shown, or offers to quote it');
  say(frame(held, retired, 3));
  await until(() => rowOf('r1') && rowOf('r1').classList.contains('c-belief--selected'));
  check(trail() && rowOf('r1').classList.contains('c-belief--selected') && chip(0).getAttribute('aria-pressed') === 'true', 'a new frame closed the trail, or lost the chip');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

func TestBeliefsShowAllThenNoLongerHeldAndWhatChangedThisMonth(t *testing.T) {
	// The browsers run in Auckland, far from UTC, so a month read in UTC
	// lands this page's fixtures in the wrong month. Go's own zone is read
	// before, so the setting is the browsers' alone.
	_ = time.Local.String()
	t.Setenv("TZ", "Pacific/Auckland")
	runRealPage(t, statusFakeSocketJS, beliefsShowJS, beliefsStyles)
}
