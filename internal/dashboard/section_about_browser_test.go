//go:build !windows

package dashboard

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// ABOUT IN ITS SECTION DOES WHAT IT DOES IN THE PAGE, its four views unchanged,
// each reach of theirs beyond the frame asked of the bridge alone:
//   - one view at a time, by the address the page says, each with its own scroll;
//   - the portrait's date from the entry that wrote it, read whole through the
//     frame's key, never the section's;
//   - a link says the page's own address, and asks the page to follow it, as a
//     view's own setting of its address does;
//   - Why? asks for the trail and sets the page's address; a typed refusal is said
//     in the trail's words, and a lost connection ends what it showed;
//   - Copy and Quote reach the page, each answering beside itself as it does there;
//   - recall and Memory's one setting go by their own doors;
//   - the visit is the frame's, and Between you is read from it;
//   - what About shows of Backups comes as its topic, and its one way to act is
//     the page's own address.
func TestAboutInItsSectionDoesWhatItDoesInThePage(t *testing.T) {
	tree, err := builtInAboutTree()
	if err != nil {
		t.Fatal(err)
	}
	page, modules := sectionAtTopLevel(t, tree, `
import W from '/strings/identity.js';
import WC from '/strings/components.js';
const BASE = 'https://page.example/', DAY = 86400000;
const portrait = 'I keep a careful eye on what changes.';
const whole = JSON.stringify({ synthesis_text: portrait });
const head = whole.slice(0, 12), rest = new TextEncoder().encode(whole.slice(12)).buffer;
const rows = [
  { seq: '9', time: '2026-11-01T09:00:00Z', type: 'belief.upsert', head: '{"statement":"Plain words last."}', bytes: 34 },
  { seq: '8', time: '2026-11-01T08:00:00Z', type: 'turn', head: 'x', bytes: 1 },
  { seq: '5', time: '2026-10-29T08:00:00Z', type: 'experience.create', head: '{"content":"older"}', bytes: 18 },
];
host.answers['record.read'] = p => p.type === 'self_model.synthesize'
  ? { rows: [{ seq: '6', time: '2026-10-30T10:00:00Z', type: p.type, head, bytes: new TextEncoder().encode(whole).length }], range_bytes: 4096 }
  : { rows };
host.answers['record.payload'] = () => rest;
host.answers['visit'] = () => ({ at: Date.now() - 3 * DAY, seq: 5, known: true });
host.answers['recall.search'] = a => ({ query: a.q, message: '[seq 7, score 1.000] found it' });
const trail = { belief: { id: 'b1', statement: 'Plain words last.', last_seq: 4 }, origins: [], contradicts: [], supersedes: [], truncated: {} };
host.answers['belief.trail'] = a => { if (a.id === 'gone') throw Object.assign(new Error('no such belief'), { code: 'belief.not_found' }); return trail; };
const identity = {
  synthesis: portrait, brief: 'today', trust_level: 'high', autonomy_level: 'wide', intentions: [{ statement: 'Finish it', state: 'active' }],
  beliefs: [{ id: 'b1', statement: 'Plain words last.', ring: 3, status: 'active', evidence_count: 2, confidence: 0.9 }, { id: 'gone', statement: 'Something gone.', ring: 3, status: 'active', evidence_count: 1, confidence: 0.2 }],
  retired: [], retired_more: 0,
  experiences: [{ id: 'x1', content: 'A long day.', category: 'work', created_at: '2026-11-01T07:00:00Z', created_seq: '7' }],
  meaning: { source: '', unavailable: 'none set up', choice: 'none', plugins: ['one'], provider_model: true, changes: [{ choice: 'provider', dropped: 0, to_embed: 4 }] },
};
await import('/main.js');
const stack = id => document.getElementById(id + '-stack');
const says = id => stack(id).textContent;
const click = el => el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
const button = (root, words) => [...root.querySelectorAll('button')].find(b => b.textContent.trim() === words);
const go = (view, hash) => host.tell('route', { view, hash, base: BASE });
const sent = c => host.sent(c).map(a => a.args);
run(async assert => {
  host.tell('status', { name: 'Mira' });
  host.tell('continuity', { mode: 'normal', ledger_seq: 9, witness_url: '' });
  host.tell('identity', identity);
  host.tell('work', { alarms: [] });
  go('identity', '#/identity/who');
  const on = () => [...document.querySelectorAll('.view.on')].map(v => v.id).join();
  assert(on() === 'view-identity', 'Who they are is not the one view shown: ' + on());
  assert(document.getElementById('view-memory').inert, 'a view not shown is reachable');
  await until(() => /last on/.test(says('identity')));
  assert(says('identity').includes(portrait) && /last on/.test(says('identity')), 'the portrait\'s date was not read whole from its entry: ' + says('identity'));
  assert(sent('record.payload')[0] && sent('record.payload')[0].seq === '6', 'the rest of the entry was not read through the frame: ' + JSON.stringify(sent('record.payload')));
  // Links
  await until(() => [...document.querySelectorAll('a[href*="#/"]')].length);
  const hrefs = [...document.querySelectorAll('a[href]')].map(a => a.getAttribute('href')).filter(h => h !== '#');
  assert(hrefs.length && hrefs.every(h => h.startsWith(BASE + '#/')), 'a link does not say the page\'s address: ' + hrefs.join(' '));
  const was = location.href;
  // The press is the page's to follow: left to itself it would load the page in this frame.
  const pressed = new MouseEvent('click', { bubbles: true, cancelable: true });
  document.querySelector('a[href*="#/identity/record/6"]').dispatchEvent(pressed);
  assert(pressed.defaultPrevented, 'a link was left to move this frame');
  await until(() => sent('page.go').length === 1);
  assert(sent('page.go')[0] && sent('page.go')[0].to === '#/identity/record/6' && !sent('page.go')[0].replace, 'a link did not ask the page to follow it: ' + JSON.stringify(sent('page.go')));
  assert(location.href === was, 'a link moved this frame: ' + location.href);
  // Entered again: drawn and read anew.
  const portraitReads = () => sent('record.read').filter(a => a.type === 'self_model.synthesize').length;
  const readsWere = portraitReads();
  go('experiences', '#/identity/experiences');
  go('identity', '#/identity/who');
  await until(() => portraitReads() === readsWere + 1);
  assert(portraitReads() === readsWere + 1, 'entered again, the portrait\'s date was not read again');
  // The state of their record is Technical → Verification's, not About's (the first-ten-minutes walk).
  assert(!document.querySelector('[data-id-snapshot], [data-continuity]') && !/ledger seq|keys escrowed/.test(says('identity')), 'About in its section still shows the state of their record: ' + says('identity'));
  // An identity's older copy of a view may still set its own address: the page is
  // asked, and this frame keeps none.
  host.acts.length = 0;
  location.hash = '#/settings/backups';
  await until(() => sent('page.go').length === 1 && location.hash === '');
  assert(sent('page.go')[0] && sent('page.go')[0].to === '#/settings/backups', 'a view that set its own address did not ask the page: ' + JSON.stringify(sent('page.go')));
  assert(location.hash === '', 'this frame kept the address: ' + location.hash);
  // A second time: the frame's address did not stay in the way.
  host.acts.length = 0;
  click(document.querySelector('a[href*="#/identity/record/"]'));
  await until(() => sent('page.go').length === 1);
  assert(sent('page.go').length === 1, 'the second press went nowhere');

  // Beliefs: Why?, its address, its refusal, Copy, Quote.
  host.acts.length = 0;
  go('memory', '#/identity/beliefs');
  assert(on() === 'view-memory', 'Beliefs is not the one view shown: ' + on());
  assert(says('memory').includes('Plain words last.'), 'the beliefs were not drawn: ' + says('memory'));
  click(button(stack('memory'), W.why));
  await until(() => sent('belief.trail').length === 1 && document.querySelector('.c-trail'));
  assert(sent('belief.trail')[0] && sent('belief.trail')[0].id === 'b1', 'Why? did not ask for the trail: ' + JSON.stringify(sent('belief.trail')));
  await until(() => sent('page.go').length === 1);
  assert(sent('page.go')[0] && sent('page.go')[0].to === '#/identity/beliefs/b1' && sent('page.go')[0].replace === true, 'the trail did not set the page\'s address: ' + JSON.stringify(sent('page.go')));
  host.answers['clipboard.write'] = () => true;
  click(button(stack('memory'), W.trailCopyLink));
  await until(() => sent('clipboard.write').length === 1);
  assert(sent('clipboard.write').length === 1, 'Copy link did not ask the page to copy');
  assert(sent('clipboard.write')[0]?.text === BASE + '#/identity/beliefs/b1', 'the link copied is not the page\'s: ' + sent('clipboard.write')[0]?.text);
  const quoteTo = W.quoteTo.replace('{name}', 'Mira');
  click(button(document.querySelector('.c-trail'), quoteTo));
  await until(() => sent('reply.quote').length === 1);
  assert(sent('reply.quote')[0] && sent('reply.quote')[0].text.includes('Plain words last.'), 'Quote did not put the belief in the reply box: ' + JSON.stringify(sent('reply.quote')));
  host.tell('connection', false);
  await until(() => says('memory').includes(W.trailWhyOffline));
  assert(says('memory').includes(W.trailWhyOffline), 'a lost connection did not end the trail shown: ' + says('memory'));
  host.tell('connection', true);
  // A deep address, and the same address again: one ask.
  host.acts.length = 0;
  go('memory', '#/identity/beliefs/gone');
  await until(() => says('memory').includes(W.trailWhyNotFound));
  assert(says('memory').includes(W.trailWhyNotFound), 'a typed refusal was not said in the trail\'s words: ' + says('memory'));
  const asks = sent('belief.trail').length;
  go('memory', '#/identity/beliefs/gone');
  await new Promise(r => setTimeout(r, 0));
  assert(sent('belief.trail').length === asks, 'the same address asked again: ' + sent('belief.trail').length);

  // Recall.
  host.acts.length = 0;
  const search = document.getElementById('mem-search');
  search.value = 'plain';
  search.dispatchEvent(new Event('input', { bubbles: true }));
  document.getElementById('mem-search').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
  await until(() => document.getElementById('mem-recall'));
  assert(sent('recall.search')[0] && sent('recall.search')[0].q === 'plain', 'recall was not asked: ' + JSON.stringify(sent('recall.search')));
  assert(document.getElementById('mem-recall').textContent.includes('found it'), 'recall\'s answer was not drawn: ' + says('memory'));
  await until(() => document.querySelector('#mem-recall a[href^="' + BASE + '"]'));
  assert(document.querySelector('#mem-recall a[href^="' + BASE + '#/identity/record/7"]'), 'a recalled row\'s link is not the page\'s');

  // Memory's one setting: nothing until Change.
  host.acts.length = 0;
  const source = document.getElementById('mem-source');
  source.value = 'provider';
  source.dispatchEvent(new Event('change', { bubbles: true }));
  assert(document.querySelector('[data-mem-change]'), 'the choice did not say what it does: ' + says('memory'));
  assert(!sent('meaning.source').length, 'a choice was stored before Change');
  click(document.querySelector('[data-mem-change]'));
  await until(() => sent('meaning.source').length === 1);
  assert(sent('meaning.source')[0] && sent('meaning.source')[0].choice === 'provider', 'Change did not ask for the choice: ' + JSON.stringify(sent('meaning.source')));

  // Between you.
  host.acts.length = 0;
  go('between', '#/identity/between');
  assert(on() === 'view-between', 'Between you is not the one view shown: ' + on());
  await until(() => document.querySelector('.between__item'));
  assert(sent('visit').length === 1, 'the visit was not asked of the frame: ' + sent('visit').length);
  const items = [...document.querySelectorAll('.between__item')].map(el => el.dataset.seq).join();
  assert(items === '9', 'the changes since the visit are ' + items);
  assert(document.querySelector('.between__open').getAttribute('href') === BASE + '#/identity/record/9', 'a change\'s link is not the page\'s');

  // Experiences: Copy link, and a refused copy answering beside itself.
  host.acts.length = 0;
  go('experiences', '#/identity/experiences');
  assert(says('experiences').includes('A long day.'), 'the experiences were not drawn: ' + says('experiences'));
  // The page keeps the reader's place in a section (built-in/keep.js): a scroll is
  // said as place.keep 120 ms later. That is the section's place, not Experiences
  // asking for anything; here it is made to come rather than left to the clock
  // (the gate of 35477865, 2026-11-06, caught one landing in this window).
  document.dispatchEvent(new Event('scroll'));
  // waits by design: past keep.js's 120 ms, so the place has been asked to be kept.
  await new Promise(r => setTimeout(r, 300));
  assert(sent('place.keep').length >= 1, 'rig: a scroll in the section did not keep its place');
  click(button(stack('experiences'), WC.copyLink));
  await until(() => document.querySelector('.experience__said').textContent);
  assert(sent('clipboard.write')[0]?.text === BASE + '#/identity/record/7', 'the link copied is not the page\'s: ' + sent('clipboard.write')[0]?.text);
  assert(document.querySelector('.experience__said').textContent === WC.linkCopied, 'a copy did not answer beside itself');
  host.answers['clipboard.write'] = () => { throw new Error('no'); };
  click(button(stack('experiences'), WC.copyLink));
  await until(() => document.querySelector('.experience__said').textContent === WC.copyFailed);
  assert(document.querySelector('.experience__said').textContent === WC.copyFailed, 'a refused copy did not say so');
  for (const c of host.acts) assert(['clipboard.write', 'place.keep'].includes(c.command), 'Experiences asked for ' + c.command);
});
`)
	runPageInEngines(t, page, modules)
}

// aboutRecord is the record the real chain reads through the frame's key.
type aboutRecord struct{}

func (aboutRecord) RecordPage(_ context.Context, before uint64, kind, _ string, _, _ int) ([]RecordRow, error) {
	if before == 2 { // their first entry: the page's mark and About's head stand on it
		return []RecordRow{{Seq: 1, Time: "2026-10-01T09:00:00Z", Type: "birth", Ring: 0, Bytes: 2, Head: []byte("{}")}}, nil
	}
	if before != 0 {
		return nil, nil
	}
	if kind == "self_model.synthesize" {
		const whole = `{"synthesis_text":"I keep a careful eye on what changes."}`
		return []RecordRow{{Seq: 6, Time: "2026-10-30T10:00:00Z", Type: kind, Ring: -1, Bytes: int64(len(whole)), Head: []byte(whole)[:12]}}, nil
	}
	return []RecordRow{{Seq: 9, Time: "2026-11-01T09:00:00Z", Type: "belief.upsert", Ring: 3, Bytes: 34, Head: []byte(`{"statement":"Plain words last."}`)}}, nil
}
func (aboutRecord) RecordPayload(_ context.Context, seq uint64, offset, length int64) ([]byte, int64, error) {
	const whole = `{"synthesis_text":"I keep a careful eye on what changes."}`
	if seq != 6 {
		return nil, 0, errors.New("not held")
	}
	// A busy machine, as the gate's is: the portrait stands well before the line under
	// it that says when it was written and links to the entry.
	time.Sleep(300 * time.Millisecond)
	return []byte(whole)[offset:min(offset+length, int64(len(whole)))], int64(len(whole)), nil
}
func (aboutRecord) RecordTypes(context.Context) ([]string, error) {
	return []string{"belief.upsert", "self_model.synthesize"}, nil
}

func aboutHandler() *WSHandler {
	return &WSHandler{
		Record:   aboutRecord{},
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{Name: "Mira", LifetimeTicks: 24}, nil },
		// Their mark (at About's head) counts the hours together in the pulse a live host answers with.
		GetConfig:     func() *ConfigState { return &ConfigState{Cognition: CognitionConfigState{PulseSeconds: 300}} },
		GetContinuity: func() (*ContinuityState, error) { return &ContinuityState{Mode: "normal", LedgerSeq: 9}, nil },
		GetIdentity: func() (*IdentityState, error) {
			return &IdentityState{
				Synthesis: "I keep a careful eye on what changes.", TrustLevel: "high", AutonomyLevel: "wide",
				Charter: "First we agree.\nThen we work.", Approval: &CharterApproval{State: "said", Excerpt: "Yes, that is agreed.", Turn: 4, Record: 2},
				Relationships: []RelationshipItem{{ID: "r1", Name: "Robin", Role: "peer", Record: 3, Charter: "We check.\nWe say so.", Approval: &CharterApproval{State: "said", Excerpt: "Agreed.", Turn: 5, Record: 3}}},
				Beliefs:       []BeliefItem{{ID: "b1", Statement: "Plain words last.", Ring: 3, Status: "active", EvidenceCount: 2, Confidence: 0.9}, {ID: "gone", Statement: "Something gone.", Ring: 3, Status: "active", EvidenceCount: 1, Confidence: 0.2}},
				Experiences:   []ExperienceItem{{ID: "x1", Content: "A long day.", Category: "work", CreatedAt: "2026-11-01T07:00:00Z", CreatedSeq: "7"}},
				Retired:       []RetiredBeliefItem{},
			}, nil
		},
		Recall: func(q string) (string, error) { return "[seq 7, score 1.000] found " + q, nil },
		BeliefTrail: func(id string) (*BeliefTrail, error) {
			if id == "gone" {
				return nil, errors.New("no such belief")
			}
			return &BeliefTrail{Belief: TrailBelief{ID: id, Statement: "Plain words last."}}, nil
		},
	}
}

func serverWithRealAbout(t *testing.T) *Server {
	t.Helper()
	tree, err := builtInAboutTree()
	if err != nil {
		t.Fatal(err)
	}
	reg := sections.NewRegistry()
	built := builtInSection(t, "about")
	built.FS = reportingTree{tree}
	if err := reg.Register(built); err != nil {
		t.Fatal(err)
	}
	s := New("127.0.0.1", 0, aboutHandler())
	s.SetSections(reg)
	return s
}

// ABOUT RUNS AS A BUILT-IN SECTION, on the dashboard's own server, in three engines,
// without a token and signed in under one:
//   - nothing of it is fetched until one of its places is opened;
//   - one frame draws all four places, and moving between them loads nothing again;
//   - its reads of the record go through the frame's own key;
//   - a link inside it moves the page, to a place that is the page's own, and
//     back again finds the section as it was;
//   - Quote puts the belief in the page's own reply box and takes the reader there;
//   - a trail's refusal is said in the trail, and not by the page's notice;
//   - a belief's address opens the section at that trail.
const aboutSectionDriverJS = `
import { signIn } from '/pagekey.js';
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
const token = '__TOKEN__';
let held = null;
window.addEventListener('message', e => { if (e.data && e.data.type === 'section-holds') held = e.data; });
const frame = () => document.querySelector('iframe.section-frame[title="About"]');
const says = re => held && re.test(held.text);
const press = words => frame().contentWindow.postMessage({ type: 'press', words }, '*');
const follow = words => frame().contentWindow.postMessage({ type: 'follow', words }, '*');
const fetched = p => performance.getEntriesByType('resource').some(r => r.name.includes(p));
(async () => {
  if (token && sessionStorage.getItem('about-signed') !== '1') {
    const signed = await signIn(token);
    if (signed.status !== 204) return report('FAIL: sign-in answered ' + signed.status);
    sessionStorage.setItem('about-signed', '1');
    location.reload();
    return;
  }
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  // waits by design: that nothing was fetched is what is held, once the
  // socket's first frames, the sections list among them, have had time to come.
  await new Promise(r => setTimeout(r, 800));
  check(!frame(), 'the section was mounted before any of its places was opened');
  check(!fetched('/built-in-sections/about/'), 'the section was fetched before any of its places was opened');

  location.hash = '#/identity/who';
  await until(() => says(/last on/));
  if (!says(/last on/)) return report('FAIL: Who they are did not draw its portrait\'s date: ' + (held && held.text));
  check(says(/I keep a careful eye/), 'the portrait was not drawn: ' + held.text);
  const own = document.getElementById('view-identity');
  await until(() => getComputedStyle(own).visibility === 'hidden');
  check(getComputedStyle(own).visibility === 'hidden', 'the page\'s own Who they are stands beside the section');
  check(!document.getElementById('identity-stack').textContent, 'the page drew its own Who they are behind the section');
  check(!performance.getEntriesByType('resource').some(r => /\/views\/memory\.js$/.test(r.name) && !r.name.includes('/built-in-sections/')), 'the page fetched its own About behind the section');

  const was = frame();
  let loads = 0;
  was.addEventListener('load', () => { loads++; });
  location.hash = '#/identity/beliefs';
  await until(() => says(/Plain words last/));
  check(says(/Plain words last/), 'Beliefs did not draw: ' + held.text);
  location.hash = '#/identity/experiences';
  await until(() => says(/A long day/));
  check(says(/A long day/), 'Experiences did not draw: ' + held.text);
  location.hash = '#/identity/between';
  // Between you draws what it has; moving to it is what is held here.
  await until(() => !says(/A long day/));
  check(!says(/A long day/), 'a place moved from stands beside the place moved to: ' + held.text);
  await until(() => says(/We check/));
  check(says(/We check/), 'Between you did not draw the others they work with: ' + held.text);
  check(/We check\.\s*\n\s*We say so\./.test(held.text), 'a relationship\'s text lost its lines: ' + held.text);
  check(frame() === was && !loads, 'moving between its places loaded the section again');
  check(document.querySelectorAll('iframe.section-frame[title="About"]').length === 1, 'more than one frame draws About');

  // A link in it moves the page, to a place that is the page's own.
  location.hash = '#/identity/who';
  await until(() => says(/I keep a careful eye/));
  // About's head is the page's, and its section brings no sheet to the page: it stands
  // in its column above the tabs, the mark beside the name, in the page's own rules.
  const placeHead = () => document.querySelector('#place-head > .place-head');
  await until(() => placeHead());
  const headBox = placeHead()?.getBoundingClientRect(), tabsBox = document.querySelector('#place-tabs > *')?.getBoundingClientRect();
  check(placeHead() && getComputedStyle(placeHead()).display === 'flex', 'About\'s head is unstyled: ' + (placeHead() ? getComputedStyle(placeHead()).display : 'not drawn'));
  check(headBox && tabsBox && Math.abs(headBox.left - tabsBox.left) <= 1, 'About\'s head stands at x ' + headBox?.left + ', its tabs at x ' + tabsBox?.left);
  // The line that holds this link is drawn when its read answers, after the portrait.
  await until(() => says(/Where this comes from/));
  check(says(/Where this comes from/), 'the portrait\'s line did not come: ' + held.text);
  const steps = history.length;
  follow('Where this comes from');
  await until(() => location.hash === '#/identity/record/6');
  // waits by design: a frame's own steps come after the page has moved.
  await new Promise(r => setTimeout(r, 300));
  check(history.length === steps + 1, 'a link added ' + (history.length - steps) + ' steps to Back, not one');
  check(location.hash === '#/identity/record/6', 'a link in the section did not move the page: ' + location.hash);
  // Back takes one step back: the frame adds no steps of its own.
  history.back();
  await until(() => location.hash === '#/identity/who');
  check(location.hash === '#/identity/who', 'Back from a link in the section went to ' + location.hash);
  history.forward();
  await until(() => location.hash === '#/identity/record/6');
  const place = document.querySelector('[id="view-section:about"]');
  await until(() => getComputedStyle(place).visibility === 'hidden');
  check(getComputedStyle(place).visibility === 'hidden', 'the section stands over a place of the page\'s own');
  location.hash = '#/identity/who';
  await until(() => getComputedStyle(place).visibility !== 'hidden');
  check(frame() === was && !loads, 'coming back loaded the section again');

  // Why? and its refusal.
  location.hash = '#/identity/beliefs/gone';
  await until(() => says(/that belief is not in their record|no such belief/));
  check(says(/no such belief/), 'a trail\'s refusal was not said in the trail: ' + held.text);
  const notice = document.body.innerText;
  check(!/no such belief/.test(notice), 'a trail\'s refusal was said by the page too: ' + notice.slice(0, 200));

  // Copy: where the rig can click as a reader does, the page copies on that click.
  if (window.__click) {
    location.hash = '#/identity/experiences';
    await until(() => says(/A long day/));
    let at = null;
    const heard = e => { if (e.data && e.data.type === 'where') at = e.data; };
    window.addEventListener('message', heard);
    frame().contentWindow.postMessage({ type: 'where', words: 'Copy link' }, '*');
    await until(() => at);
    window.removeEventListener('message', heard);
    const box = frame().getBoundingClientRect();
    if (at && at.x >= 0) await window.__click(box.x + at.x, box.y + at.y);
    await until(() => says(/Link copied/));
    check(says(/Link copied/), 'a reader\'s click inside the section did not copy: ' + held.text);
    press('Copy link');
    await until(() => says(/Couldn.t copy/));
  }

  // Quote.
  location.hash = '#/identity/beliefs';
  await until(() => says(/Plain words last/));
  press('Quote to Mira');
  await until(() => location.hash === '#/chat' && document.getElementById('msg-input') && document.getElementById('msg-input').value);
  const input = document.getElementById('msg-input');
  check(location.hash === '#/chat', 'Quote did not take the reader to the reply box: ' + location.hash);
  check(input && input.value.includes('Plain words last.'), 'Quote did not put the belief in the reply box: ' + (input && input.value));
  check(input && document.activeElement === input, 'the reply box does not hold the focus');
  check(held && !held.refused.length, 'the section\'s policy refused what it drew: ' + (held && held.refused.join('; ')));
  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') : 'OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestAboutRunsAsABuiltInSection(t *testing.T) {
	const token = "about-token-0123456789abcdef0123456789abcdef"
	for _, run := range []struct{ name, token string }{{"without a token", ""}, {"signed in under a token", token}} {
		t.Run(run.name, func(t *testing.T) {
			runDashboardInEachEngine(t, func(t *testing.T) *Server {
				s := serverWithRealAbout(t)
				if run.token != "" {
					s.SetAccessToken(true, run.token)
				}
				return s
			}, "/", strings.ReplaceAll(aboutSectionDriverJS, "__TOKEN__", run.token))
		})
	}
}

// THE FRAME GATES WHAT IT DOES FOR A SECTION, in the real page: page.go follows only
// an address the page's own router knows; clipboard.write only on the reader's own
// click, and a script's is refused; visit says the visit before this one, kept by
// the page, and record.payload reads through the frame's own key.
const frameGatesFixtureJS = `import { ready } from '/section-api.js';
const tell = m => parent.postMessage({ type: 'fixture-report', report: m }, '*');
const said = p => p.then(d => ({ ok: true, d }), e => ({ ok: false, e: e.message }));
(async () => {
  const api = await ready();
  const out = {};
  out.nowhere = await said(api.act('page.go', { to: '#/nowhere' }));
  out.other = await said(api.act('page.go', { to: '#/section:other' }));
  out.copied = await said(api.act('clipboard.write', { text: 'taken' }));
  out.visit = await said(api.act('visit'));
  const bytes = await said(api.act('record.payload', { seq: 6, offset: 4, length: 16 }));
  out.bytes = bytes.ok ? new TextDecoder().decode(bytes.d) : bytes;
  // What the frame was told of Backups before this section came is not kept for it.
  let heardBackups = 'nothing';
  await api.data.subscribe('backups', d => { heardBackups = JSON.stringify(d); });
  // waits by design: that nothing kept comes is what is held.
  await new Promise(r => setTimeout(r, 300));
  out.backups = heardBackups;
  out.moved = await said(api.act('page.go', { to: '#/identity/record/6' }));
  tell(out);
})().catch(e => tell({ stopped: String((e && e.message) || e) }));
`

const frameGatesDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const check = (ok, what, problems) => { if (!ok) problems.push(what); };
(async () => {
  let o = null;
  window.addEventListener('message', e => { if (e.data && e.data.type === 'fixture-report') o = e.data.report; });
  const { S } = await import('/state.js');
  for (let i = 0; i < 200 && !S.connected; i++) await new Promise(r => setTimeout(r, 50));
  // waits by design: the sections list must have come, or nothing would be kept anyway.
  await new Promise(r => setTimeout(r, 500));
  (await import('/sections.js')).publish({ type: 'backups', backups: { action: 'status', view: { enabled: true, snapshots: [{ record: 'KEPT' }] } } });
  location.hash = '#/identity/experiences';
  for (let i = 0; i < 300 && !o; i++) await new Promise(r => setTimeout(r, 50));
  if (!o) return report('FAIL: the section told the page nothing');
  if (o.stopped) return report('FAIL: the section stopped: ' + o.stopped);
  const p = [];
  check(!o.nowhere.ok && /an address of the page/.test(o.nowhere.e), 'an address the router does not know was followed: ' + JSON.stringify(o.nowhere), p);
  check(!o.other.ok, 'another section\'s address was followed: ' + JSON.stringify(o.other), p);
  check(!o.copied.ok && /click/.test(o.copied.e), 'a script wrote the clipboard: ' + JSON.stringify(o.copied), p);
  const kept = (await import('/views/home/note.js')).lastVisit();
  check(o.visit.ok && o.visit.d.known === true && JSON.stringify(o.visit.d) === JSON.stringify(kept), 'the visit is not the page\'s: ' + JSON.stringify(o.visit) + ', the page\'s ' + JSON.stringify(kept), p);
  check(o.bytes === 'nthesis_text":"I', 'record.payload did not read through the frame: ' + JSON.stringify(o.bytes), p);
  check(!/KEPT/.test(o.backups), 'the frame kept a Backups frame for the section: ' + o.backups, p);
  check(o.moved.ok && location.hash === '#/identity/record/6', 'an address of the page was not followed: ' + location.hash, p);
  report(p.length ? 'FAIL: ' + p.join('\n  - ') : 'OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestTheFrameGatesWhatItDoesForASection(t *testing.T) {
	runDashboardInEachEngine(t, func(t *testing.T) *Server {
		reg := sections.NewRegistry()
		if err := reg.Register(&sections.Section{
			Decl: sections.Decl{ID: "gates", Title: "Gates", Slot: "view", Entry: "index.html",
				Topics:   []string{"backups"},
				Commands: []string{"page.go", "clipboard.write", "visit", "record.payload"}},
			BuiltIn: true, Views: []string{"experiences"},
			FS: fstest.MapFS{
				"index.html": {Data: []byte(`<!doctype html><meta charset="utf-8"><title>Gates</title><script type="module" src="./main.js"></script>`)},
				"main.js":    {Data: []byte(frameGatesFixtureJS)},
			},
		}); err != nil {
			t.Fatal(err)
		}
		s := New("127.0.0.1", 0, aboutHandler())
		s.SetSections(reg)
		return s
	}, "/", frameGatesDriverJS)
}

// AN IDENTITY'S OVERLAY APPLIES INSIDE ABOUT, as it does in the page: its custom.css,
// its approved copy of a view's words, and of a view itself. A copy of a section's
// stand-in is the page's alone, and does not reach the section.
const aboutOverlayDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
let held = null;
window.addEventListener('message', e => { if (e.data && e.data.type === 'section-holds') held = e.data; });
(async () => {
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  location.hash = '#/identity/experiences';
  await until(() => held && /Words of their own/.test(held.text) && /A long day\., overlaid/.test(held.text) && held.own === '7');
  if (!held) return report('FAIL: the section told the page nothing');
  if (!/Words of their own/.test(held.text)) return report('FAIL: the identity\'s words did not apply inside About: ' + held.text);
  if (!/A long day\., overlaid/.test(held.text)) return report('FAIL: the identity\'s view did not apply inside About: ' + held.text);
  if (held.own !== '7') return report('FAIL: the identity\'s custom.css did not apply inside About: ' + JSON.stringify(held.own));
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestAnIdentitysOverlayAppliesInsideAbout(t *testing.T) {
	runDashboardInEachEngine(t, func(t *testing.T) *Server {
		dir := filepath.Join(t.TempDir(), "ui")
		words, err := staticFS.ReadFile("static/strings/identity.js")
		if err != nil {
			t.Fatal(err)
		}
		view, err := staticFS.ReadFile("static/views/identity/experiences.js")
		if err != nil {
			t.Fatal(err)
		}
		copiedWords := strings.Replace(string(words), `experiencesScope: "`, `experiencesScope: "Words of their own. `, 1)
		copiedView := strings.Replace(string(view), "draw(words, x.content);", "draw(words, x.content + ', overlaid');", 1)
		if copiedWords == string(words) || copiedView == string(view) {
			t.Fatal("rig: what the overlay changes is not in the shipped files")
		}
		writeOverlay(t, dir, "strings/identity.js", copiedWords)
		writeOverlay(t, dir, "views/identity/experiences.js", copiedView)
		// A rule of :root would reach the section as a token, its custom.css or not.
		writeOverlay(t, dir, "custom.css", "body { --overlay-own: 7; }\n")
		s := serverWithRealAbout(t)
		s.SetUIOverlay(dir)
		s.SetApprovedScripts([]ApprovedScript{
			{Path: "strings/identity.js", SHA256: digestOf(t, filepath.Join(dir, "strings/identity.js"))},
			{Path: "views/identity/experiences.js", SHA256: digestOf(t, filepath.Join(dir, "views/identity/experiences.js"))},
		})
		return s
	}, "/", aboutOverlayDriverJS)
}

// ABOUT AND STATUS STAY UNDER SAFE AND WHILE A START WAITS FOR RING 5, each the
// binary's own, in three engines: each draws what the record's state says, from
// the host alone; the way out that About names goes to the page's own place; and
// the page's note leads to Status, which is the section.
const heldUnderModeDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
const held = {};
const frame = title => document.querySelector('iframe.section-frame[title="' + title + '"]');
window.addEventListener('message', e => {
  if (!e.data || e.data.type !== 'section-holds') return;
  for (const title of ['About', 'Status']) if (frame(title) && frame(title).contentWindow === e.source) held[title] = e.data;
});
const says = (title, re) => held[title] && re.test(held[title].text);
const shown = id => getComputedStyle(document.querySelector(id)).visibility !== 'hidden';
const p = [];
const check = (ok, what) => { if (!ok) p.push(what); };
(async () => {
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  location.hash = '#/identity/who';
  // The mode is said on every page, by the page's note (below); About draws under it.
  await until(() => says('About', /I keep a careful eye/));
  check(says('About', /I keep a careful eye/), 'About did not draw under this mode: ' + (held.About && held.About.text));
  await until(() => !shown('#view-identity'));
  check(!shown('#view-identity'), 'the page\'s own view stands beside About');
  if (__SAFE__) {
    const steps = history.length;
    frame('About').contentWindow.postMessage({ type: 'follow', words: 'restore from a backup' }, '*');
    await until(() => location.hash === '#/settings/backups');
    check(location.hash === '#/settings/backups', 'the way out About names did not go to the page\'s place: ' + location.hash);
    // waits by design: a frame's own steps come after the page has moved.
    await new Promise(r => setTimeout(r, 300));
    check(history.length === steps + 1, 'the way out added ' + (history.length - steps) + ' steps to Back, not one');
    history.back();
    await until(() => location.hash === '#/identity/who');
    check(location.hash === '#/identity/who', 'Back from the way out went to ' + location.hash);
    history.forward();
    await until(() => location.hash === '#/settings/backups');
    await until(() => !shown('[id="view-section:about"]'));
    check(!shown('[id="view-section:about"]'), 'About stands over the page\'s own place');
  }
  const note = () => document.querySelector('#page-note a[href="#/settings"]');
  location.hash = '#/chat';
  await until(note);
  check(note(), 'the page\'s note has no way to Status under this mode: ' + (document.getElementById('page-note') || {}).textContent);
  if (note()) note().click();
  await until(() => says('Status', /__STATUS_SAYS__/));
  check(says('Status', /__STATUS_SAYS__/), 'Status does not say the mode: ' + (held.Status && held.Status.text));
  check(shown('[id="view-section:status"]') && !shown('#view-status'), 'the note did not lead to Status, the section');
  report(p.length ? 'FAIL: ' + p.join('\n  - ') : 'OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestAboutAndStatusStayUnderSafeAndWhileAStartWaits(t *testing.T) {
	for _, run := range []struct {
		name, mode, why, statusSays string
		safe                        bool
	}{
		{"under SAFE", "safe", "the record would not open", "the record would not open", true},
		{"while a start waits for Ring 5", "degraded_ring5", "waiting for the witness", "waiting for the founding principles", false},
	} {
		t.Run(run.name, func(t *testing.T) {
			flag := map[bool]string{true: "true", false: "false"}
			runDashboardInEachEngine(t, func(t *testing.T) *Server {
				about, err := builtInAboutTree()
				if err != nil {
					t.Fatal(err)
				}
				status, err := builtInStatusTree()
				if err != nil {
					t.Fatal(err)
				}
				reg := sections.NewRegistry()
				for id, tree := range map[string]builtInTree{"status": status, "about": about} {
					built := builtInSection(t, id)
					built.FS = reportingTree{tree}
					if err := reg.Register(built); err != nil {
						t.Fatal(err)
					}
				}
				if run.safe {
					reg.SetSafeSource(func() (string, bool) { return run.why, true })
				}
				h := aboutHandler()
				h.GetContinuity = func() (*ContinuityState, error) {
					c := &ContinuityState{Mode: run.mode, LedgerSeq: 9}
					if run.safe {
						c.SafeReason = run.why
					} else {
						c.DegradedWhy = run.why
					}
					return c, nil
				}
				s := New("127.0.0.1", 0, h)
				s.SetSections(reg)
				return s
			}, "/", strings.NewReplacer("__MODE__", run.mode, "__WHY__", run.why, "__STATUS_SAYS__", run.statusSays, "__SAFE__", flag[run.safe]).Replace(heldUnderModeDriverJS))
		})
	}
}

// BETWEEN YOU ON A FIRST VISIT SAYS WHAT IT IS FOR, and no changes rather than all of
// them; before it is known whether there has been a visit, it says nothing.
func TestBetweenYouOnAFirstVisitSaysWhatItIsFor(t *testing.T) {
	tree, err := builtInAboutTree()
	if err != nil {
		t.Fatal(err)
	}
	page, modules := sectionAtTopLevel(t, tree, `
import W from '/strings/identity.js';
let release, visit = new Promise(ok => { release = ok; });
host.answers['visit'] = () => visit;
await import('/main.js');
const says = () => document.getElementById('between-stack').textContent;
const named = W.betweenFirstVisit.replace('{name}', 'Mira');
run(async assert => {
  host.tell('status', { name: 'Mira' });
  host.tell('continuity', { mode: 'normal', ledger_seq: 9 });
  host.tell('identity', { beliefs: [], intentions: [], experiences: [], retired: [] });
  host.tell('route', { view: 'between', hash: '#/identity/between', base: 'https://page.example/' });
  assert(says() === '', 'it spoke before it was known whether there has been a visit: ' + says());
  release({ at: 0, seq: 0, known: true });
  await until(() => says());
  assert(says() === named, 'a first visit does not say what the tab is for: ' + says());
  assert(!host.sent('record.read').length, 'a first visit read the record to list everything');
  host.tell('status', { name: 'Unnamed' });
  await until(() => says() === W.betweenFirstVisitUnnamed);
  assert(says() === W.betweenFirstVisitUnnamed, 'before they have a name it does not say so: ' + says());
});
`)
	runPageInEngines(t, page, modules)
}
