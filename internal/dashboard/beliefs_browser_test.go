//go:build !windows

package dashboard

import "testing"

// ABOUT {NAME} → BELIEFS (spec step 9), on the real page: the shipped frame,
// driven through its own socket by a scripted host (statusFakeSocketJS).
//   - Their beliefs are rows in their voice, with how firmly each is held in
//     the page's ("Firmly held (0.91) · 4 pieces of evidence").
//   - Search narrows what is loaded; with nothing loaded matching it says
//     so, and "Ask {name} to recall" (or Enter) is today's recall.
//   - Why? asks for that belief's trail and draws it beside the list: as it
//     stands, with a revision; recall on its own line, fading; where it came
//     from, your words quoted in yours and theirs in theirs, a list cut at
//     its bound saying so; what it replaced; nothing contradicting it, said.
//     An answer to an older ask is not taken; a refusal is said in its typed
//     words. A belief's trail has its own address, which opens it.
//   - "Quote to {name}" puts the belief, quoted, in the reply box in the conversation,
//     and sends nothing; no control here changes a belief.
const beliefsPageJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const live = () => sockets[sockets.length - 1];
const say = m => live().say(m);
const $ = id => document.getElementById(id);
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const stats = { name: 'Mira', version: 'test', build: 'test', ledger_seq: 40, lifetime_ticks: 1, belief_count: 3, intention_count: 0, experience_count: 0, reflection_count: 0 };
const beliefs = [
  { id: 'b1', statement: 'A deploy should go to staging first.', ring: 3, status: 'confirmed', evidence_count: 4, confidence: 0.91 },
  { id: 'b2', statement: 'The grant deadline might move if we ask early.', ring: 3, status: 'new', evidence_count: 1, confidence: 0.4 },
  { id: 'b3', statement: 'Short reports are read; long ones are skimmed.', ring: 3, status: 'new', evidence_count: 2, confidence: 0.7 },
];
const rows = () => [...document.querySelectorAll('#beliefs-view .c-belief')];
const rowOf = id => document.querySelector('#beliefs-view .c-belief[data-belief-id="' + id + '"]');
const trail = () => document.querySelector('#beliefs-view .c-trail');
const lastQuery = q => sent.filter(f => f.type === 'query' && f.query === q).at(-1);

async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 40 } });
  location.hash = '#/identity/beliefs'; await tick();
  say({ type: 'identity', identity: { beliefs, intentions: [], experiences: [] } }); await tick();
  await until(() => rows().length === 3, 6000).catch(() => {});
  const from = sent.length;

  // ── their beliefs, in their voice, how firmly held in the page's ──
  check(S.view === 'memory' && rows().length === 3, 'the Beliefs tab does not list their three beliefs: ' + S.view + ' ' + rows().length);
  const first = rowOf('b1'), second = rowOf('b2');
  check(norm(first.querySelector('.c-belief__statement').textContent) === 'A deploy should go to staging first.', 'a belief\'s words are not its own');
  check(norm(first.querySelector('.c-belief__held').textContent) === 'Firmly held (0.91) · 4 pieces of evidence', 'how firmly it is held is not said: ' + norm(first.querySelector('.c-belief__held').textContent));
  check(norm(second.querySelector('.c-belief__held').textContent) === 'Tentative (0.40) · one piece of evidence', 'one piece of evidence is not said as one: ' + norm(second.querySelector('.c-belief__held').textContent));
  const voice = getComputedStyle(first.querySelector('.c-belief__statement')).fontFamily, page = getComputedStyle(first.querySelector('.c-belief__held')).fontFamily;
  check(voice !== page && /Iowan|Charter|Georgia|serif/.test(voice), 'their belief is not in their voice: ' + voice);

  // ── search, and recall ──
  const search = $('mem-search');
  check(search && search.placeholder === 'Search what Mira believes', 'the search does not say what it searches: ' + (search && search.placeholder));
  search.value = 'grant'; search.dispatchEvent(new Event('input', { bubbles: true })); await tick();
  check(rows().length === 1 && rows()[0].dataset.beliefId === 'b2', 'the search does not narrow the list: ' + rows().length);
  const s2 = $('mem-search');
  s2.value = 'orchids'; s2.dispatchEvent(new Event('input', { bubbles: true })); await tick();
  check(norm((document.querySelector('#beliefs-view .beliefs__empty') || {}).textContent) === 'Nothing loaded here matches. Press Enter to ask Mira to recall from the whole record.', 'no match is not said, or says the wrong thing');
  $('mem-search').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  check(lastQuery('recall') && lastQuery('recall').q === 'orchids', 'Enter did not ask them to recall: ' + JSON.stringify(lastQuery('recall')));
  say({ type: 'recall', query: 'orchids', message: '[seq 12, 2026-10-02] the orchids on the sill' }); await tick();
  const recalled = document.querySelector('#beliefs-view .beliefs__recalled');
  check(recalled && norm(recalled.querySelector('h3').textContent) === 'Mira recalled, for "orchids"' && recalled.querySelector('a[href="#/identity/record/12"]'), 'what they recalled is not shown under its cue, with the record\'s rows linked');
  const s3 = $('mem-search'); s3.value = ''; s3.dispatchEvent(new Event('input', { bubbles: true })); await tick();

  // ── Why? ──
  rowOf('b1').querySelector('.c-belief__why').click(); await tick();
  const asked = lastQuery('belief_trail');
  check(asked && asked.belief_id === 'b1', 'Why? did not ask for that belief\'s trail: ' + JSON.stringify(asked));
  check(trail() && norm(trail().textContent) === 'Reading the record…', 'the trail does not say it is being read');
  check(location.hash === '#/identity/beliefs/b1', 'the trail has no address of its own: ' + location.hash);
  const answer = { belief: { id: 'b1', statement: 'A deploy should go to staging first.', ring: 3, confidence: 0.88, evidence_count: 4, archived: false, held_since: '2026-10-21T09:00:00Z', last_seq: 31, recall: 0.15, recall_count: 4, recalled_at: '2026-10-27T10:00:00Z', fading: true },
    origins: [
      { edge_type: 'DERIVED_FROM', strength: 1, created_seq: 29, created_at: '2026-10-21T08:00:00Z', kind: 'experience', id: 'x1', text: 'Always stage first.', provenance: 'operator', turn: null },
      { edge_type: 'DERIVED_FROM', strength: 1, created_seq: 30, created_at: '2026-10-21T08:30:00Z', kind: 'experience', id: 'x2', text: 'The rollback took 58 minutes.', provenance: 'self', turn: null }],
    contradicts: [],
    supersedes: [{ edge_type: 'SUPERSEDES', strength: 1, created_seq: 31, created_at: '2026-10-21T09:00:00Z', kind: 'belief', id: 'b0', text: 'A deploy can go straight to production once the tests pass.', turn: null }],
    superseded_by: null,
    truncated: { origins: true, contradicts: false, supersedes: false } };
  say({ type: 'belief_trail', request_id: 'another-ask', belief_trail: Object.assign({}, answer, { belief: Object.assign({}, answer.belief, { statement: 'not this one' }) }) }); await tick();
  check(norm(trail().textContent) === 'Reading the record…', 'an answer to another ask was taken');
  say({ type: 'belief_trail', request_id: asked.request_id, belief_trail: answer }); await tick();
  const t = trail(), parts = [...t.querySelectorAll('.c-trail__part')];
  const heads = parts.map(p => norm(p.querySelector('h4').textContent));
  check(JSON.stringify(heads) === JSON.stringify(['As it stands', 'Where it came from', 'What it replaced', 'What contradicts it']), 'the trail\'s parts are not as the spec names them: ' + JSON.stringify(heads));
  const stands = norm(t.querySelector('.c-trail__stands').textContent);
  // A date is said in the browser's own order ("21 October", "October 21").
  const oct21 = new Date('2026-10-21T09:00:00Z').toLocaleDateString([], { day: 'numeric', month: 'long' });
  check(stands === 'Firmly held (0.88) · held since ' + oct21 + ' · revised once', 'as it stands does not say how firmly, since when and the revision: ' + stands);
  check(norm((t.querySelector('.c-trail__recall') || {}).textContent) === 'fading in recall (0.15)' && t.querySelector('.c-trail__gloss'), 'recall is not said on its own line, fading, with the line that the two differ');
  const links = [...parts[1].querySelectorAll('.c-trail__link')];
  check(links.length === 2 && links[0].querySelector('.c-trail__text--quoted') && norm(links[0].querySelector('.c-trail__source').textContent) === 'Your words, ' + oct21, 'your words are not quoted as yours with their date: ' + norm(links[0] && links[0].textContent));
  check(links[1] && links[1].querySelector('.c-trail__text--theirs') && norm(links[1].querySelector('.c-trail__source').textContent) === 'Their experience, ' + oct21, 'their experience is not in their voice with its date');
  check(parts[1].querySelector('.c-trail__cut') && norm(parts[1].querySelector('.c-trail__cut').textContent) === 'Shortened. Open the record for all of it.', 'a list cut at its bound does not say so');
  check(norm(parts[2].querySelector('.c-trail__text').textContent) === 'A deploy can go straight to production once the tests pass.', 'what it replaced is not shown');
  check(norm((parts[3] && parts[3].querySelector('.c-trail__none') || {}).textContent) === 'Nothing in the record.', 'nothing contradicting it is not said');
  check(t.querySelector('a.c-trail__record') && t.querySelector('a.c-trail__record').getAttribute('href') === '#/identity/record/31', 'Open in the record does not open its entry');

  // ── a refusal, in its typed words ──
  rowOf('b2').querySelector('.c-belief__why').click(); await tick();
  const refused = lastQuery('belief_trail');
  say({ type: 'error', request_id: refused.request_id, message: 'belief b2: not found', request_error_code: 'belief.not_found' }); await tick();
  check(norm(trail().textContent) === 'AII OS couldn\'t read this trail: that belief is not in their record', 'a refused trail is not said in its words: ' + norm(trail().textContent));

  // ── its own address ──
  location.hash = '#/identity/beliefs/b3'; await tick();
  check(lastQuery('belief_trail').belief_id === 'b3' && rowOf('b3').classList.contains('c-belief--selected'), 'a belief\'s address did not open its trail');

  // ── Quote to Mira: into the reply box, nothing sent ──
  const before = sent.length;
  rowOf('b1').querySelector('.c-belief__quote').click(); await tick();
  check($('msg-input').value === 'Mira, about "A deploy should go to staging first.": ', 'Quote to Mira did not put the belief, quoted, in the reply box: ' + JSON.stringify($('msg-input').value));
  check(S.view === 'chat' && document.activeElement === $('msg-input'), 'Quote to Mira did not go to the reply box: ' + S.view);
  check(!sent.slice(before).some(f => f.type === 'chat'), 'Quote to Mira sent something');
  // No control here changes a belief: everything the page sent was a read.
  const acts = sent.slice(from).filter(f => !['query', 'probe', 'presence', 'hello'].includes(f.type)).map(f => f.type);
  check(acts.length === 0, 'the Beliefs tab sent more than reads: ' + acts.join(','));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
// A run that stops still reports what it had found by then.
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e) + (problems.length ? '\n  - ' + problems.join('\n  - ') : '')));
`

var beliefsStyles = append([]string{"components/belief-row/belief-row.css", "components/why-trail/why-trail.css", "views/identity/beliefs.css"}, homeStyles...)

func TestBeliefsAndWhyTheyAreHeld(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, beliefsPageJS, beliefsStyles)
}

// WHY? ON A REPLY ONLY WHERE ITS TURN READ A BELIEF (spec section 1, step
// 9): a tool result of the reply's own turn names a belief by its id, and
// Why? opens that belief's trail; a reply whose turn named none has no
// Why?, and a belief named in another turn lends it none.
func TestWhyIsOnAReplyOnlyWhereItsTurnReadABelief(t *testing.T) {
	page := `<!doctype html><html><body>
<div id="thread"><div id="thread-inner"></div></div>
<textarea id="msg-input"></textarea><button id="send-btn"></button>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { renderInteractionPages } from '/views/chat.js';
run(async (assert) => {
  S.stats = { name: 'Quill' };
  const row = (id, seq, kind, role, content, turn) => ({ id, sequence: String(seq), kind, role, content, turn_id: turn, details: kind.startsWith('tool') ? { tool: 'recall' } : {} });
  const rows = [
    row('q1', 1, 'message', 'operator', 'Why staging?', 't1'),
    row('c1', 2, 'tool_call', 'resident', '{"query":"deploys"}', 't1'),
    row('r1', 3, 'tool_result', 'system', '[belief_3fa9c2d1 0.88] A deploy should go to staging first.', 't1'),
    row('a1', 4, 'message', 'resident', 'Because of the rollback on the 19th.', 't1'),
    row('q2', 5, 'message', 'operator', 'Thanks.', 't2'),
    row('a2', 6, 'message', 'resident', 'You are welcome.', 't2'),
  ];
  renderInteractionPages(new Map([['recorded', { identity: 'i', incarnation: 'd', rows }], ['transient', { identity: 'i', incarnation: 'm', rows: [] }]]), () => {}, false);
  const why = id => document.querySelector('[data-turn-id="' + id + '"] .why-chip');
  assert(why('a1') && why('a1').getAttribute('href') === '#/identity/beliefs/belief_3fa9c2d1' && why('a1').textContent === 'Why?', 'a reply whose turn read a belief has no Why? to its trail');
  assert(!why('a2'), 'a reply whose turn read no belief has a Why?');
  assert(!why('q1'), 'your own message has a Why?');
});
</script></body></html>`
	runPageInEngines(t, page, chatModuleRig(t))
}

// A SELECTED BELIEF STANDS INSIDE ITS CARD at a phone's width. A row with no card
// runs flush with the page's text; selected, its card is drawn, and its words keep
// off the card's edges.
func TestComponentBeliefRowSelectedAtAPhonesWidth(t *testing.T) {
	runPageInEnginesAtSize(t, componentPage(`
import { run } from '/__harness.js';
import { render } from '/components/belief-row/belief-row.js';
run(async (assert) => {
  const row = selected => render({ belief: { id: 'b', statement: 'Plain words outlast clever ones in a report someone must act on.', confidence: 0.9, evidence_count: 7 }, name: 'Robin', selected });
  const list = document.createElement('ul');
  const plain = row(false), chosen = row(true);
  list.append(plain, chosen);
  document.getElementById('root').append(list);
  const inset = el => {
    const r = el.getBoundingClientRect(), s = el.querySelector('.c-belief__statement').getBoundingClientRect();
    return [s.left - r.left, r.right - s.right];
  };
  assert(inset(plain)[0] === 0, 'a row with no card is not flush with the page: ' + inset(plain));
  assert(inset(chosen)[0] >= 8 && inset(chosen)[1] >= 8, 'a selected belief touches its card: ' + inset(chosen));
});`, "belief-row"), componentModules(t, "belief-row"), 390, 844)
}
