//go:build !windows

package dashboard

import (
	"fmt"
	"testing"
	"time"
)

// THEIR MARK IS THEIRS. The drawing: a spiral within a circle, one turn in
// the first hour lived together and one more each time the hours double
// (each time they quadruple at 40 px or less), one line of one weight, so it
// winds tighter inside the same circle; the circle and its centre alone
// before the first pulse, a part of a turn after it; another first entry
// draws another mark; one point of light on its path, which is the spiral
// and then once round the circle; the tab's icon without it; "today" is
// this device's calendar date. On the real page, from GET /record and the
// hours Status counts: the static icon until those hours are known, then
// the mark in the bar and at About's head, and the tab's icon, the same
// after a reload. The light follows the page's own state, in the bar: its
// pace and way at rest, answering, listening and speaking (a reply read by
// the browser's own voice, through the voice itself), and the keyframes
// move it along its path; under reduced motion (the engines run with it)
// it is paused and hidden; and with the connection lost the mark is faded
// and still. TestTheMarkFollowsTheVoiceItself proves the voice's facts.
const markDrawingJS = `import * as mark from '/components/mark/mark.js';
import { bornToday } from '/shell/mark-place.js';
import { run } from '/__harness.js';
run(assert => {
  const born = Date.parse('2026-10-02T09:30:00Z');
  assert(mark.turns(0) === 0 && mark.turns(0.5) === 0.5 && mark.turns(1) === 1 && mark.turns(2) === 2 && mark.turns(4) === 3 && mark.turns(1024) === 11,
    'not one turn in the first hour and one more at each doubling: ' + [0, 0.5, 1, 2, 4, 1024].map(h => mark.turns(h)));
  assert(mark.turns(1, 32) === 1 && mark.turns(4, 32) === 2 && mark.turns(16, 32) === 3, 'at 40 px or less a turn is not a quadrupling: ' + [1, 4, 16].map(h => mark.turns(h, 32)));
  const at = (d, h, m) => new Date(2026, 10, d, h, m).getTime();
  assert(!bornToday(at(4, 23, 55), at(5, 0, 5)) && bornToday(at(5, 0, 1), at(5, 23, 59)), 'today is not the calendar date: yesterday at 23:55 is today at 00:05, or this morning is not');
  const birth = mark.render({ born, hours: 0, size: 96 });
  assert(birth.querySelectorAll('circle').length === 2 && birth.querySelector('.c-mark__bound') && !birth.querySelector('path'), 'before the first pulse it is not the circle and the centre alone');
  assert(mark.render({ born, hours: 5 / 60, size: 96 }).querySelector('.c-mark__turns'), 'the first pulse does not begin the spiral');
  const drawn = mark.render({ born, hours: 1023, size: 240 });
  assert(drawn.querySelectorAll('.c-mark__turns').length === 1 && drawn.querySelectorAll('.c-mark__life').length === 1, 'not one line of turns and one light: ' + drawn.innerHTML.slice(0, 200));
  const bound = r => Number(r.querySelector('.c-mark__bound').getAttribute('r'));
  assert(bound(mark.render({ born, hours: 4095, size: 240 })) === bound(drawn), 'the circle grew with the hours');
  const ends = el => { const p = el.querySelector('.c-mark__turns').getAttribute('d').trim().split(/\s*[ML]\s*/).filter(Boolean).pop().split(' ').map(Number); return Math.hypot(p[0], p[1]); };
  assert(Math.abs(ends(drawn) - bound(drawn)) < 1.2, 'the spiral does not reach the circle');
  const life = drawn.querySelector('.c-mark__life'), turns = drawn.querySelector('.c-mark__turns');
  assert(life.getAttribute('pathLength') === '1000' && life.getAttribute('d').length > turns.getAttribute('d').length, 'the light does not run the spiral and then round the circle');
  const other = mark.render({ born: born + 1000, hours: 1023, size: 240 });
  assert(other.querySelector('.c-mark__turns').getAttribute('d') !== turns.getAttribute('d'), 'another first entry draws the same mark');
  const a = mark.render({ born, hours: 10, size: 96 }).outerHTML, b = mark.render({ born, hours: 10, size: 96 }).outerHTML;
  assert(a === b && a.includes('aria-hidden="true"'), 'two drawings of one mark differ, or the mark is not decoration');
  assert(!mark.markup({ born, hours: 10, size: 64 }, '#9a5b2e').includes('c-mark__life'), 'the tab\'s icon carries the light');
  // THEIR LOOK: the default is the circle as drawn without one; a brain is
  // its outline, its stem, the turns and the light, the turns meeting its
  // edge; half way is between; the centre moves and the circle does not; the
  // folds fold it; the look is read from the page's tokens, held to its
  // ranges; and the tab's icon takes it.
  const look = (shape, cx = 0, cy = 0, folds = 12) => ({ shape, cx, cy, folds });
  const pts = d => d.trim().split(/\s*[ML]\s*/).filter(Boolean).map(p => p.replace(' Z', '').split(' ').map(Number));
  const turnsD = el => el.querySelector('.c-mark__turns').getAttribute('d');
  const plain = mark.render({ born, hours: 40, size: 240 }), brainy = mark.render({ born, hours: 40, size: 240, look: look(1) });
  assert(plain.outerHTML === mark.render({ born, hours: 40, size: 240, look: look(0) }).outerHTML, 'the default look draws other than no look');
  assert(brainy.querySelector('path.c-mark__bound') && !brainy.querySelector('circle.c-mark__bound') && brainy.querySelectorAll('path').length === 4, 'a brain is not its outline, its stem, the turns and the light: ' + brainy.innerHTML.slice(0, 160));
  const last = pts(turnsD(brainy)).pop(), rim = pts(brainy.querySelector('.c-mark__bound').getAttribute('d'));
  assert(Math.min(...rim.map(p => Math.hypot(p[0] - last[0], p[1] - last[1]))) < 1.5, 'the turns do not meet the brain\'s edge: ' + last);
  const half = turnsD(mark.render({ born, hours: 40, size: 240, look: look(0.5) }));
  assert(half !== turnsD(brainy) && half !== turnsD(plain), 'half way is not between the circle and the brain');
  const moved = mark.render({ born, hours: 40, size: 240, look: look(0, -10, 6) }), centre = moved.querySelector('.c-mark__centre');
  assert(centre.getAttribute('cx') === '-10' && centre.getAttribute('cy') === '6' && bound(moved) === bound(plain), 'the centre did not move, or the circle moved with it');
  const start = pts(turnsD(moved))[0];
  assert(Math.hypot(start[0] + 10, start[1] - 6) < 4 && Math.abs(ends(moved) - bound(moved)) < 1.2, 'the spiral does not run from the moved centre to the circle: ' + start);
  assert(turnsD(mark.render({ born, hours: 40, size: 240, look: look(1, 0, 0, 0) })) !== turnsD(brainy), 'the folds do not fold the brain');
  // How much the turns wander in and out: it grows with the folds, and without
  // them the turns follow the edge's shape alone.
  const wander = f => { const p = pts(turnsD(mark.render({ born, hours: 40, size: 240, look: look(1, 0, 0, f) }))); let s = 0; for (let i = 1; i < p.length; i++) s += Math.abs(Math.hypot(p[i][0], p[i][1]) - Math.hypot(p[i - 1][0], p[i - 1][1])); return s; };
  const folds = [0, 12, 24].map(wander);
  assert(folds[0] < folds[1] * 0.6 && folds[1] < folds[2], 'the folds do not deepen with the setting: ' + folds.map(Math.round));
  assert(mark.render({ born, hours: 40, size: 240, look: look(1) }).outerHTML === brainy.outerHTML, 'two drawings of one brain differ');
  const style = v => ({ getPropertyValue: n => v[n] || '' });
  assert(JSON.stringify(mark.lookOf(style({ '--mark-shape': ' 3', '--mark-centre-x': '-40', '--mark-centre-y': 'x', '--mark-folds': '7' }))) === '{"shape":1,"cx":-16,"cy":0,"folds":7}', 'the look is not read and held to its ranges');
  const icon = mark.markup({ born, hours: 40, size: 64, look: look(1) }, '#9a5b2e');
  assert(/path class="c-mark__bound"/.test(icon) && !icon.includes('c-mark__life'), 'the tab\'s icon does not take the look, or carries the light');
  const s = mark.render({ born, hours: 10, size: 32, state: 'answering' });
  assert(s.getAttribute('data-state') === 'answering', 'the state is not set on the mark');
  mark.update(s, { born, hours: 10, size: 32, state: 'listening' });
  assert(s.getAttribute('data-state') === 'listening', 'the state is not set again');
});
`

func TestMarkIsTheirs(t *testing.T) {
	t.Run("drawing", func(t *testing.T) {
		runPageInEngines(t, `<!doctype html><script type="module" src="/__page.js"></script>`, map[string][]byte{"/__page.js": []byte(markDrawingJS)})
	})
	t.Run("page", func(t *testing.T) {
		born := time.Now().Add(-27*24*time.Hour - time.Hour).UTC().Format(time.RFC3339Nano)
		record := fmt.Sprintf(`{"version":1,"rows":[{"seq":"1","time":%q,"type":"ring0.genesis","ring":0,"bytes":10,"head":"{}"}],"first_view_bytes":0,"range_bytes":0}`, born)
		runRealPageServing(t, statusFakeSocketJS, markPageJS, append([]string{"views/identity/who.css", "components/tabs/tabs.css", "components/top-bar/top-bar.css", "components/mark/mark.css"}, homeStyles...), map[string][]byte{"/record": []byte(record)})
	})
}

const markPageJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
import { setThinking } from '/presence.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const again = sessionStorage.getItem('mark-probe');
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 1320, lifetime_ticks: 120, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  sockets[0].say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 1320 } });
  await tick(); await tick();
  check(document.querySelector('#bar-mark img') && !document.querySelector('#bar-mark svg'), 'the bar draws a mark before the hours lived together are known');
  sockets[0].say({ type: 'config', config: { cognition: { pulse_seconds: 300 } } });
  // The mark shown: the sidebar's head on a computer, the bar's on a phone.
  await until(() => document.querySelector('#bar-mark svg.c-mark'), 5000);
  const bar = [...document.querySelectorAll('#rail-mark svg.c-mark, #bar-mark svg.c-mark')].find(e => e.getClientRects().length);
  check(bar.querySelector('.c-mark__bound') && bar.querySelector('.c-mark__turns') && bar.getAttribute('aria-hidden') === 'true', 'the bar does not carry their mark at 32 px as decoration: ' + bar.innerHTML.slice(0, 120));
  check(bar.getAttribute('data-state') === 'rest', 'the bar\'s mark is not at rest once connected: ' + bar.getAttribute('data-state'));
  // The light's pace and way, read from its animation; the engines run with
  // reduced motion, so it is paused and hidden, and its keyframes are moved
  // by hand to see where it is along its path.
  const light = () => bar.querySelector('.c-mark__life'), anim = () => light().getAnimations()[0];
  const timing = () => { const a = anim(), c = a && a.effect.getComputedTiming(); return c ? c.duration + ' ' + c.direction : 'none'; };
  const where = ms => { anim().currentTime = ms; return Math.round(parseFloat(getComputedStyle(light()).strokeDashoffset)); };
  check(timing() === '14000 normal', 'the light does not run outward over 14 s at rest: ' + timing());
  check(anim().playState === 'paused' && getComputedStyle(light()).visibility === 'hidden', 'under reduced motion the light still moves or shows: ' + anim().playState + ' ' + getComputedStyle(light()).visibility);
  check(where(0) === 1128 && where(7000) === 564 && where(13999) < 2, 'the light does not run its path outward: ' + [where(0), where(7000), where(13999)]);
  check(!document.getElementById('orb'), 'the orb is still in the page');
  // THE LIGHT FOLLOWS THE PAGE'S OWN STATE: a turn running, the voice
  // listening or speaking, the connection lost.
  setThinking(true); await tick();
  check(bar.getAttribute('data-state') === 'answering' && timing() === '3000 normal', 'a running turn does not set the mark answering: ' + bar.getAttribute('data-state') + ' ' + timing());
  setThinking(false); await tick();
  check(bar.getAttribute('data-state') === 'rest', 'the end of a turn does not set the mark at rest: ' + bar.getAttribute('data-state'));
  // The voice is fetched once the page has drawn; it says what it is doing.
  await until(() => S.voiceNow, 5000);
  const voiceNow = S.voiceNow;
  S.voiceNow = () => ({ ...voiceNow(), listening: true }); S.renderMarks(); await tick();
  check(bar.getAttribute('data-state') === 'listening' && timing() === '4000 reverse' && where(0) === 0 && where(1000) === 282, 'listening does not set the light inward over 4 s: ' + bar.getAttribute('data-state') + ' ' + timing());
  S.voiceNow = voiceNow; S.renderMarks(); await tick();
  check(bar.getAttribute('data-state') === 'rest', 'the microphone stopping does not set the mark at rest: ' + bar.getAttribute('data-state'));
  // A reply read by the browser's own voice, through the voice itself.
  const synth = { speaking: false, u: null, speak(u) { this.u = u; this.speaking = true; if (u.onstart) u.onstart(); }, cancel() { this.u = null; this.speaking = false; } };
  Object.defineProperty(window, 'speechSynthesis', { configurable: true, value: synth });
  window.SpeechSynthesisUtterance = class { constructor(text) { this.text = text; } };
  const voice = await import('/voice.js');
  S.voiceSpeak = true;
  voice.speak('The grant letter is drafted.', { route: 'browser' }); await tick();
  check(bar.getAttribute('data-state') === 'speaking' && timing() === '1600 normal', 'a reply read aloud does not set the mark speaking: ' + bar.getAttribute('data-state') + ' ' + timing());
  synth.speaking = false; synth.u.onend(); await tick();
  check(bar.getAttribute('data-state') === 'rest', 'the reply read to its end does not set the mark at rest: ' + bar.getAttribute('data-state'));
  S.voiceSpeak = false;
  const icon = document.querySelector('link[rel="icon"][data-mark]');
  check(icon && icon.href.startsWith('data:image/svg+xml,') && decodeURIComponent(icon.href).includes('c-mark__turns') && !decodeURIComponent(icon.href).includes('c-mark__life'), 'the tab\'s icon is not their mark, still');
  S.go('identity'); await until(() => document.querySelector('#place-head svg.c-mark'), 5000);
  const head = document.getElementById('place-head');
  check(head.querySelector('svg.c-mark .c-mark__turns') && head.querySelector('svg.c-mark').getAttribute('data-state') === 'rest', 'About\'s head does not carry their mark, at rest');
  check(/With you since .* · 10 hours together · 1,320 entries in their record · their mark gains a turn each time your time together doubles/.test(head.textContent), 'About\'s head does not say how long: ' + head.textContent);
  const drawn = bar.outerHTML + icon.href;
  if (!again) { sessionStorage.setItem('mark-probe', drawn); sessionStorage.setItem('mark-problems', JSON.stringify(problems)); location.reload(); return; }
  problems.unshift(...JSON.parse(sessionStorage.getItem('mark-problems') || '[]'));
  check(drawn === again, 'their mark differs after a reload');
  // The connection lost: faded, and the light still and hidden.
  sockets[0].drop(); await tick();
  check(bar.getAttribute('data-state') === 'offline' && getComputedStyle(bar).opacity === '0.42' && getComputedStyle(light()).visibility === 'hidden', 'with the connection lost the mark is not faded and still: ' + bar.getAttribute('data-state') + ' ' + getComputedStyle(bar).opacity);
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

// BETWEEN YOU FROM THE RECORD (spec step 10): after a recorded visit at
// entry 100, six days ago, About → Between you lists each change since,
// newest first, labelled by its type in the page's words, with their words
// where that entry's own payload gives them (a belief's statement as it
// was then, a supersede's reason, an experience's content; never today's
// words lent to an older entry), each opening its row of the record; the
// entries of kinds it has no words for, counted in one line that opens the
// Record page; nothing from before the visit.
const betweenPageJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 106, lifetime_ticks: 1, belief_count: 1, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  sockets[0].say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 106 } });
  sockets[0].say({ type: 'identity', identity: { beliefs: [{ id: 'b1', statement: 'A deploy goes to staging first.', ring: 2, status: 'confirmed', evidence_count: 2, confidence: 0.9 }], intentions: [], experiences: [], synthesis: '', brief: '', charter: '', trust_level: '', autonomy_level: '' } });
  S.go('between');
  await until(() => document.querySelector('.between__list'), 5000);
  const items = [...document.querySelectorAll('.between__item')].map(el => ({ seq: el.dataset.seq, label: el.querySelector('.between__label').textContent, words: el.querySelector('.between__words')?.textContent || '', open: el.querySelector('.between__open').getAttribute('href') }));
  check(items.map(i => i.seq).join() === '106,105,104,103', 'not each worded change since entry 100, newest first: ' + items.map(i => i.seq));
  const want = [['Changed their mind', 'Two deploys went wrong without staging.'], ['Came to believe', 'A deploy can go straight to production.'], ['Noted', 'I learned the grant letter wants a summary first.'], ['The charter changed', '']];
  items.forEach((i, n) => check(want[n] && i.label.startsWith(want[n][0]) && i.words === want[n][1] && i.open === '#/identity/record/' + i.seq, 'entry ' + i.seq + ' is not labelled by its type with their words: ' + JSON.stringify(i)));
  check(/Since your last visit, 6 days ago/.test(document.getElementById('between-stack').textContent), 'the heading does not say when the last visit was');
  const unsaid = document.querySelector('.between__more a');
  check(unsaid && unsaid.textContent === '2 more entries between you are on the Record page; this page has no words for them yet.' && unsaid.getAttribute('href') === '#/identity/record', 'the entries without words are not counted in one line to the Record page: ' + (unsaid && unsaid.textContent));
  check(document.querySelector('[role="tab"][aria-selected="true"]')?.textContent === 'Between you', 'Between you is not the selected tab');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestBetweenYouFromTheRecord(t *testing.T) {
	at := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339Nano) }
	record := fmt.Sprintf(`{"version":1,"rows":[`+
		`{"seq":"106","time":%q,"type":"belief.supersede","ring":2,"bytes":40,"head":"{\"old_id\":\"b0\",\"new_id\":\"b1\",\"reason\":\"Two deploys went wrong without staging.\"}"},`+
		`{"seq":"105","time":%q,"type":"belief.upsert","ring":2,"bytes":40,"head":"{\"id\":\"b1\",\"statement\":\"A deploy can go straight to production.\"}"},`+
		`{"seq":"104","time":%q,"type":"experience.create","ring":3,"bytes":60,"head":"{\"category\":\"reflection\",\"content\":\"I learned the grant letter wants a summary first.\"}"},`+
		`{"seq":"103","time":%q,"type":"relationship.upsert","ring":1,"bytes":30,"head":"{}"},`+
		`{"seq":"102","time":%q,"type":"edge.create","ring":2,"bytes":20,"head":"{}"},`+
		`{"seq":"101","time":%q,"type":"system.witnessed","ring":0,"bytes":20,"head":"{}"},`+
		`{"seq":"100","time":%q,"type":"experience.create","ring":3,"bytes":20,"head":"{\"content\":\"Before your visit.\"}"}],"first_view_bytes":0,"range_bytes":0}`,
		at(time.Hour), at(90*time.Minute), at(2*time.Hour), at(3*time.Hour), at(4*time.Hour), at(5*time.Hour), at(7*24*time.Hour))
	// Their first entry, before the visit six days ago: the visit is theirs.
	first := fmt.Sprintf(`{"version":1,"rows":[{"seq":"1","time":%q,"type":"ring0.genesis","ring":0,"bytes":2,"head":"{}"}],"first_view_bytes":0,"range_bytes":0}`, at(30*24*time.Hour))
	runRealPageServing(t, sixDaysAgoJS+statusFakeSocketJS, betweenPageJS, append([]string{"views/identity/who.css", "components/tabs/tabs.css"}, homeStyles...), map[string][]byte{"/record": []byte(record), "/record?before=2": []byte(first)})
}

// WHO THEY ARE FROM THEIR STATE (spec step 10; the board AboutWho): their
// portrait and charter in their voice, your words quoted whole in the
// page's with the entry that signed them; what they work on (not what is
// done); what they set to come back to, from the work frame, each with its
// repeat and its last firing; their three most firmly held beliefs and the
// way to all; their brief behind "Read it"; how far they act on their own;
// and beside them the state of their record, as the registry places it.
// Before they have a name, the page says "they".
const whoPageJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const text = sel => (document.querySelector(sel) || {}).textContent || '';
const voiced = el => !!el && el.closest('.who__voice') !== null;
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  const status = name => sockets[0].say({ type: 'status', stats: { name, version: 'test', build: 'test', ledger_seq: 40, lifetime_ticks: 1, belief_count: 4, intention_count: 2, experience_count: 0, reflection_count: 0 } });
  status('Mira');
  sockets[0].say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 40, review_at: '09:00', review_status: 'clear' } });
  const day = 86400000, due = Date.now() + day;
  sockets[0].say({ type: 'work', work: { live: [], queued: 0, delivered: [], alarms: [{ id: 'a1', tag: 'grant', message: 'check the grant deadline', due_at: due, every: day }, { id: 'a2', message: 'read the reply', due_at: due + 3600000, fired_at: Date.now() - 3600000 }] } });
  sockets[0].say({ type: 'identity', identity: {
    synthesis: 'I work on writing and on the build.', brief: 'Today: the grant letter.', charter: 'I will be straight with you.',
    approval: { state: 'said', excerpt: 'Be straight with me.', turn: 3, said_at: '2026-10-02T09:00:00Z', record: 4 },
    intentions: [{ id: 'i1', statement: 'Draft the grant letter', state: 'active' }, { id: 'i2', statement: 'An old task', state: 'done' }],
    beliefs: [{ id: 'b1', statement: 'A summary comes first.', confidence: 0.95, status: 'confirmed' }, { id: 'b2', statement: 'Staging first.', confidence: 0.9 }, { id: 'b3', statement: 'Tea at four.', confidence: 0.5 }, { id: 'b4', statement: 'Rarely held.', confidence: 0.2 }],
    experiences: [], trust_level: 'standard', autonomy_level: 'ask first' } });
  S.go('identity'); await until(() => document.querySelector('.who'), 5000);
  check(text('#who-portrait') === 'How Mira describes themselves' && voiced([...document.querySelectorAll('.who__portrait')][0]), 'their portrait is not under its heading in their voice');
  const charter = document.querySelector('[aria-labelledby="who-charter"]');
  check(charter && charter.children[1] && charter.children[1].matches('[data-charter-approval]'), 'your words are not first under the charter\'s heading');
  check(voiced(document.querySelector('.who__charter')) && text('[aria-labelledby="who-charter"] [data-approval-words]') === 'Be straight with me.' && !voiced(document.querySelector('[data-approval-words]')), 'the charter and your words are not each in their own voice');
  check(!/record entry|Signed in/.test(text('.who__main')) && document.querySelector('.who__main a[href="#/identity/record/4"]'), 'the signing entry is named by its number, or not opened');
  const working = [...document.querySelectorAll('[aria-labelledby="who-working"] li')].map(li => li.textContent);
  check(working.join('|') === 'Draft the grant letter', 'Working on is not what is open: ' + working.join('|'));
  const alarms = [...document.querySelectorAll('.who__alarm summary')].map(s => s.textContent);
  check(alarms.length === 2 && /check the grant deadline/.test(alarms[0]) && /every day/.test(alarms[0]) && /Last fired/.test(text('[aria-labelledby="who-alarms"]')), 'Set to come back does not list their alarms with repeat and firing: ' + JSON.stringify(alarms));
  const firm = [...document.querySelectorAll('[aria-labelledby="who-firm"] li')].map(li => li.textContent);
  check(firm.join('|') === 'A summary comes first.|Staging first.|Tea at four.' && document.querySelector('a[href="#/identity/beliefs"]')?.textContent === 'All 4 beliefs, and why', 'the three most firmly held are not shown with the way to all: ' + firm.join('|'));
  check(document.querySelector('.who__brief summary')?.textContent === 'Read it' && !document.querySelector('.who__brief').open, 'their brief is not behind "Read it"');
  check(/standard/.test(text('.who__levels')) && /ask first/.test(text('.who__levels')), 'how far they act is not said');
  // The state of their record is Technical → Verification's, not hers (the first-ten-minutes walk).
  check(!document.querySelector('[data-continuity]') && !/ledger seq|keys escrowed|ticks/.test(text('#identity-stack')), 'About still shows the raw state of their record: ' + text('#identity-stack'));
  status('Unnamed'); S.go('chat'); await tick(); S.go('identity'); await tick(); await tick();
  check(text('#who-portrait') === 'How they describe themselves', 'before a name it does not say "they": ' + text('#who-portrait'));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestWhoTheyAreFromTheirState(t *testing.T) {
	record := `{"version":1,"rows":[{"seq":"1","time":"2026-10-02T09:00:00Z","type":"self_model.synthesize","ring":3,"bytes":10,"head":"{}"}],"first_view_bytes":0,"range_bytes":0}`
	runRealPageServing(t, statusFakeSocketJS, whoPageJS, append([]string{"views/identity/who.css", "components/tabs/tabs.css"}, homeStyles...), map[string][]byte{"/record": []byte(record)})
}

// A PORTRAIT IS DATED BY ITS OWN ENTRY (step 10, round two; a review 17:03Z,
// host 17:04Z; the prefix carried into step 11, host 18:29Z). On the real
// page, with the record's answer changed between reads: their portrait is
// dated and cited by the self_model.synthesize entry that wrote it, word
// for word; when the portrait is rewritten the date is read with it; an
// entry that did not write the portrait shown never dates it, an older one
// whose words begin the new portrait among them; an entry whose head the
// record cut is read whole before it dates anything; and entering About
// again reads again.
const portraitPageJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets;
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
// The record's newest synthesis entry, as each read finds it; its head is
// cut at cut bytes where cut is set, the rest served by /record/payload.
let entry = { seq: '7', text: 'I work on writing.' }, reads = 0, payloadReads = 0, cut = 0;
const bytesOf = e => new TextEncoder().encode(JSON.stringify({ id: 's' + e.seq, synthesis_text: e.text }));
const realFetch = window.fetch.bind(window);
window.fetch = (url, init) => {
  const u = new URL(String(url), location.href);
  if (u.pathname === '/record' && u.searchParams.get('type') === 'self_model.synthesize') {
    reads++;
    const all = bytesOf(entry), head = new TextDecoder().decode(cut ? all.slice(0, cut) : all);
    return Promise.resolve(new Response(JSON.stringify({ version: 1, rows: [{ seq: entry.seq, time: '2026-10-28T09:00:00Z', type: 'self_model.synthesize', ring: 3, bytes: all.length, head }], first_view_bytes: cut, range_bytes: 16 }), { status: 200 }));
  }
  if (u.pathname === '/record/payload') {
    payloadReads++;
    const all = bytesOf(entry), from = Number(u.searchParams.get('offset')), n = Number(u.searchParams.get('length'));
    return Promise.resolve(new Response(all.slice(from, from + n), { status: 200, headers: { 'X-Total-Bytes': String(all.length) } }));
  }
  return realFetch(url, init);
};
const cited = () => [...document.querySelectorAll('[aria-labelledby="who-portrait"] a')].map(a => a.getAttribute('href'));
const identity = synthesis => sockets[0].say({ type: 'identity', identity: { synthesis, beliefs: [], intentions: [], experiences: [], brief: '', charter: '', trust_level: '', autonomy_level: '' } });
async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  sockets[0].say({ type: 'status', stats: { name: 'Mira', version: 'test', build: 'test', ledger_seq: 9, lifetime_ticks: 1, belief_count: 0, intention_count: 0, experience_count: 0, reflection_count: 0 } });
  identity('I work on writing.');
  S.go('identity');
  await until(() => cited().length > 0, 5000);
  check(cited().join() === '#/identity/record/7', 'the portrait is not cited by the entry that wrote it: ' + cited().join());
  // Rewritten, but the record still answers with the old entry: no date is lent.
  identity('I work on writing and on the build.'); await tick(); await tick();
  check(cited().length === 0, 'an entry that did not write the portrait shown dates it: ' + cited().join());
  // An older entry whose words begin the new portrait is not its writer.
  identity('I work on writing. I also build tools.'); await tick(); await tick();
  check(cited().length === 0, 'an older entry that is a prefix of the portrait dates it: ' + cited().join());
  // The record answers with the new entry: the date is read with it.
  entry = { seq: '9', text: 'I work on writing and on the build.' };
  identity('I work on writing and on the build. '); identity('I work on writing and on the build.'); await tick(); await tick();
  check(cited().join() === '#/identity/record/9', 'the rewritten portrait is not cited by its own entry: ' + cited().join());
  // A head the record cut: read whole, then it dates the portrait it wrote.
  entry = { seq: '11', text: 'I work on writing, on the build, and on the long tail of the release notes. 😀' }; cut = 40;
  identity(entry.text + ' '); identity(entry.text); await until(() => cited().length > 0, 5000).catch(() => {});
  check(cited().join() === '#/identity/record/11' && payloadReads > 0, 'an entry whose head was cut is not read whole before it dates the portrait: ' + cited().join() + ', ' + payloadReads + ' payload reads');
  // Entering About again reads again.
  const before = reads;
  S.go('chat'); await tick(); S.go('identity'); await tick(); await tick();
  check(reads === before + 1, 'entering About again did not read the portrait\'s entry again: ' + (reads - before) + ' reads');
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestAPortraitIsDatedByItsOwnEntry(t *testing.T) {
	runRealPage(t, statusFakeSocketJS, portraitPageJS, append([]string{"views/identity/who.css", "components/tabs/tabs.css"}, homeStyles...))
}
