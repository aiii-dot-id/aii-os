//go:build !windows

package dashboard

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"
)

// THEIR VOICE HOLDS ONLY THEIR WORDS (spec section 6a; steps 4 and 6). On
// the real page, for one fixture identity, on every view the registry
// names and in Search: every element set in the voice font holds text
// equal to a field the identity wrote and the server sent (a reply, a
// question, a belief, an experience, an intention, the charter, the
// portrait, a record entry's words, their name). Each of those strings is
// passed through T() where the fixture sends it; nothing else counts as
// theirs. The fixture also sends words that are not theirs (your messages,
// a host's question, the brief a reflection pass writes, your words quoted
// beside the charter, your words kept verbatim as their testimony), so a
// voice set on any of those fails here. Beyond the views: what they
// recalled for a cue, a belief's Why? trail, and the conversation with their note
// kept, whose sentences are theirs and whose sources and lines are the
// page's.
//
// The face is read from the note's own text, and every stylesheet the page
// ships is served: a sheet left unserved would drop the voice font and
// pass this test on nothing. Each view where the fixture puts their words
// must show at least that many elements in the voice, so an empty walk is
// a failure, not a pass. Hidden elements count: a voice element is theirs
// whether or not it is open.
const theirVoicePageJS = `import { report, until } from '/__harness.js';
import { S } from '/state.js';
const sockets = window.__sockets, sent = window.__sent;
const live = () => sockets[sockets.length - 1];
const say = m => live().say(m);
const $ = id => document.getElementById(id);
const norm = s => (s || '').replace(/\s+/g, ' ').trim();
// Their words as drawn in words: the markers the in-words renderer turns
// into bold, italic and code are not drawn.
const plain = s => norm(String(s || '').replace(/\*\*|__|\*|` + "`" + `/g, ''));
const problems = [];
const check = (ok, what) => { if (!ok) problems.push(what); };
const tick = () => new Promise(r => setTimeout(r, 40));
const soft = async (fn, ms) => { try { await until(fn, ms); } catch (e) {} };
const theirs = new Set();
const T = s => { theirs.add(plain(s)); return s; };
const now = new Date().toISOString(), day = 86400000;
const ago = d => new Date(Date.now() - d * day).toISOString();

const stats = { name: T('Mira'), version: 'test', build: 'test', ledger_seq: 106, lifetime_ticks: 1, belief_count: 3, intention_count: 1, experience_count: 3, reflection_count: 0 };
const identity = {
  synthesis: T('I work on writing and on the build.'),
  charter: T('I will be straight with you.'),
  brief: 'Today: the grant letter.',
  approval: { state: 'said', excerpt: 'Be straight with me.', turn: 3, said_at: '2026-10-02T09:00:00Z', record: 4 },
  intentions: [{ id: 'i1', statement: T('Draft the grant letter'), state: 'active' }],
  beliefs: [
    { id: 'b1', statement: T('A summary comes **first**.'), ring: 2, status: 'confirmed', evidence_count: 2, confidence: 0.95 },
    { id: 'b2', statement: T('Staging comes before production.'), ring: 2, evidence_count: 1, confidence: 0.9 },
    { id: 'b3', statement: T('Tea at four.'), ring: 3, evidence_count: 1, confidence: 0.5 }],
  experiences: [
    { id: 'x1', content: T('Ola **prefers** staging first.'), category: 'observation', created_at: ago(0), provenance: 'self', created_seq: '105' },
    // Your words, kept verbatim as their testimony: yours, not theirs (the
    // trail quotes an operator's experience as "Your words").
    { id: 'x2', content: 'Deploy to staging first.', category: 'testimony', created_at: ago(2), provenance: 'operator', operator_verbatim: true, created_seq: '41' },
    { id: 'x4', content: T('They said something like staging matters.'), category: 'testimony', created_at: ago(2), provenance: 'operator', created_seq: '42' },
    { id: 'x3', content: T('The backup ran.'), category: 'event', created_at: ago(3), provenance: 'system', created_seq: '40' }],
  trust_level: 'standard', autonomy_level: 'ask first' };
// The record's words, as the served /record carries them (the Go side
// sends the same strings).
['Two deploys went wrong without staging.', 'A deploy can go straight to production.', 'I learned the grant letter wants a summary first.'].forEach(T);

const words = (n, role, content) => ({ id: 'w' + n, sequence: String(n), kind: 'message', role, content, turn_id: 't' + n, recorded_at: now, details: {} });
const held = [words(1, 'operator', 'Hello there, this is Avery.'), words(2, 'resident', T('Good morning. The draft is **done**.')),
  words(3, 'operator', 'Thanks, send it on.'), words(4, 'resident', T('You are welcome.'))];
const answered = new Set();
function readBack() {
  for (const q of sent.filter(f => f.type === 'query' && f.query === 'interaction')) {
    if (answered.has(q.request_id)) continue;
    answered.add(q.request_id);
    const source = q.interaction_query.source, rows = source === 'recorded' ? held : [];
    const fingerprint = 'fp-' + q.request_id;
    say({ type: 'interaction_page', request_id: q.request_id, interaction_window: { fingerprint, has_older: false, has_newer: false },
      interaction_page: { version: 1, source, identity: 'i1', incarnation: source, fingerprint, cursor: '', rows } });
  }
}

let face = '';
const voiced = el => getComputedStyle(el).fontFamily === face;
// The elements that set the voice: in the voice, under a parent that is not.
const voiceRoots = () => [...document.body.querySelectorAll('*')].filter(el => voiced(el) && !(el.parentElement && voiced(el.parentElement)) && norm(el.textContent));
const seen = {};
function walk(view, least) {
  const roots = voiceRoots();
  seen[view] = roots.length;
  check(roots.length >= least, view + ': ' + roots.length + ' elements in their voice, fewer than the ' + least + ' the fixture puts there; the walk saw nothing to judge');
  for (const el of roots) {
    const said = plain(el.textContent);
    // The note draws its text in pieces, one per claim it checks, so inside
    // the note a voice element may hold a contiguous piece of their note
    // text; anywhere else, and for anything shorter than a word or two, the
    // whole text must be theirs.
    const piece = el.closest('.c-note') && said.length >= 8 && [...theirs].some(t => t.includes(said));
    if (!theirs.has(said) && !piece) problems.push(view + ': words that are not theirs are in their voice: "' + said.slice(0, 80) + '" (' + el.tagName.toLowerCase() + '.' + [...el.classList].join('.') + ')');
  }
}

// Every view the registry names, at its address, and how many of their
// words the fixture puts on it at least (their name is in the bar on all).
const VISITS = [
  ['#/chat', 'chat', 3],
  ['#/identity/who', 'identity', 4], ['#/identity/between', 'between', 3], ['#/identity/beliefs', 'memory', 4],
  ['#/identity/experiences', 'experiences', 4], ['#/identity/record', 'record', 3],
  ['#/projects', 'projects', 1],
  ['#/settings/status', 'status', 1], ['#/settings/phone', 'phone', 1], ['#/settings/model', 'settings', 1], ['#/settings/voice', 'settings', 1],
  ['#/settings/permissions', 'plugins', 1], ['#/settings/people', 'settings', 1], ['#/settings/access', 'settings', 1],
  ['#/settings/backups', 'settings', 1], ['#/settings/app', 'settings', 1], ['#/settings/technical', 'technical', 1]];

async function run() {
  await until(() => sockets.length > 0);
  sockets[0].open(); await tick();
  setInterval(readBack, 50);
  say({ type: 'status', stats }); say({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 106 } });
  say({ type: 'config', config: { llm: { provider: 'Example AI', model: 'm1', resolved_provider: 'Example AI', resolved_model: 'm1', timeout_seconds: 120,
    effort_choice: { levels: ['low', 'high'], in_force: 'high', checked: [] } }, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], dashboard: {} } });
  say({ type: 'identity', identity });
  say({ type: 'work', work: { live: [], queued: 0, delivered: [], alarms: [{ id: 'a1', tag: 'grant', message: T('check the grant deadline'), due_at: Date.now() + day, every: day }] } });
  say({ type: 'asks', asks: [
    { id: 'q1', kind: 'choose', from: 'identity', text: T('Could I archive the September drafts?'), choices: ['Yes, archive them', 'Keep them'] },
    { id: 'q2', kind: 'confirm', from: 'host', text: 'The host needs your word on a backup.' }] });
  S.go('chat');
  say({ type: 'interaction_changed' });
  const theirs = () => document.querySelector('#thread-inner .msg.identity .body');
  await until(() => theirs() && norm(theirs().textContent) !== '', 5000);
  face = getComputedStyle(theirs()).fontFamily;
  if (!/Fraunces/.test(face)) { report('FAIL: their note is not in the voice font, so there is nothing to compare against: ' + face); return; }

  for (const [hash, view, least] of VISITS) {
    location.hash = hash;
    await soft(() => S.view === view, 5000);
    check(S.view === view, hash + ' did not open ' + view + ': ' + S.view);
    await soft(() => voiceRoots().length >= least, 5000);
    await tick(); await tick();
    walk(hash, least);
  }

  // Beliefs: what they recalled for a cue, and a belief's Why? trail, where
  // their experience is in their voice and yours is quoted as yours.
  location.hash = '#/identity/beliefs'; await soft(() => S.view === 'memory' && $('mem-search'), 5000);
  const cue = $('mem-search');
  cue.value = 'orchids'; cue.dispatchEvent(new Event('input', { bubbles: true })); await tick();
  $('mem-search').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  say({ type: 'recall', query: 'orchids', message: '[seq 12, 2026-10-02] ' + T('the orchids on the sill') }); await tick();
  await soft(() => document.querySelector('.beliefs__recalled'), 3000);
  check(document.querySelector('.beliefs__recalled'), 'what they recalled was not drawn');
  walk('beliefs, recalled "orchids"', 5);
  const c2 = $('mem-search'); c2.value = ''; c2.dispatchEvent(new Event('input', { bubbles: true })); await tick();
  const why = document.querySelector('#beliefs-view .c-belief[data-belief-id="b2"] .c-belief__why');
  check(why, 'no Why? on their belief');
  if (why) {
    why.click(); await tick();
    const asked = sent.filter(f => f.type === 'query' && f.query === 'belief_trail').at(-1);
    say({ type: 'belief_trail', request_id: asked && asked.request_id, belief_trail: {
      belief: { id: 'b2', statement: identity.beliefs[1].statement, ring: 2, confidence: 0.9, evidence_count: 2, archived: false, held_since: '2026-10-21T09:00:00Z', last_seq: 31, recall: 0.6, recall_count: 4, recalled_at: '2026-10-27T10:00:00Z', fading: false },
      origins: [
        { edge_type: 'DERIVED_FROM', strength: 1, created_seq: 29, created_at: '2026-10-21T08:00:00Z', kind: 'experience', id: 'x2', text: 'Always stage first.', provenance: 'operator', turn: null },
        { edge_type: 'DERIVED_FROM', strength: 1, created_seq: 30, created_at: '2026-10-21T08:30:00Z', kind: 'experience', id: 'x5', text: T('The rollback took 58 minutes.'), provenance: 'self', turn: null }],
      contradicts: [],
      supersedes: [{ edge_type: 'SUPERSEDES', strength: 1, created_seq: 31, created_at: '2026-10-21T09:00:00Z', kind: 'belief', id: 'b0', text: T('A deploy can go straight to production once the tests pass.'), turn: null }],
      superseded_by: null, truncated: { origins: false, contradicts: false, supersedes: false } } });
    await soft(() => document.querySelector('#beliefs-view .c-trail__text--theirs'), 3000);
    check(document.querySelector('#beliefs-view .c-trail__text--theirs'), 'the trail drew no experience of theirs');
    walk('beliefs, Why? trail', 6);
  }

  // The conversation with their note kept at its head: its sentences in their
  // voice, its sources and AII OS's lines beside them in the page's.
  const noteText = 'Good morning. I shipped the parser. The email to Ola did not go out.';
  say({ type: 'identity', identity: Object.assign({}, identity, { note: { text: T(noteText), written_at: now, unchecked: 0, not_succeeded_more: 0,
    claims: [{ sentence: T('I shipped the parser.'), entries: ['12'], outcomes: [] }, { sentence: T('The email to Ola did not go out.'), entries: [], outcomes: ['e1'] }],
    not_succeeded: [{ id: 'e1', kind: 'tool_result', outcome: 'failed', summary: 'send_email: to Ola', at: now }] } }) });
  ['Good morning.'].forEach(T);
  location.hash = '#/chat'; await soft(() => S.view === 'chat' && document.querySelector('#chat-note .c-note__voice'), 5000);
  check(document.querySelector('#chat-note .c-note__voice'), 'the conversation did not draw their note at its head');
  walk('#/chat, their note', 4);

  // Search: the beliefs it finds are set in their voice, the rest is not.
  location.hash = '#/chat'; await soft(() => S.view === 'chat', 5000);
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true }));
  await soft(() => document.querySelector('.c-menu input'), 3000);
  const box = document.querySelector('.c-menu input');
  check(box, 'Ctrl K did not open Search');
  if (box) {
    box.value = 'staging'; box.dispatchEvent(new Event('input', { bubbles: true }));
    await soft(() => document.querySelector('.c-menu__label--voice'), 3000);
    check(document.querySelector('.c-menu__label--voice'), 'Search set no belief in their voice for "staging"');
    walk('search "staging"', 2);
  }

  report(problems.length ? 'FAIL: ' + problems.join('\n  - ') + '\n  (seen: ' + JSON.stringify(seen) + ')' : 'OK\n' + JSON.stringify(seen));
}
run().catch(e => report('FAIL: the run stopped: ' + ((e && e.message) || e)));
`

func TestTheirVoiceHoldsOnlyTheirWords(t *testing.T) {
	// Every stylesheet the page ships, so no voice rule goes unloaded.
	var styles []string
	if err := fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".css") {
			styles = append(styles, strings.TrimPrefix(p, "static/"))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(styles) < 12 {
		t.Fatalf("found %d stylesheets; the voice font alone is set in twelve", len(styles))
	}
	at := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339Nano) }
	record := fmt.Sprintf(`{"version":1,"rows":[`+
		`{"seq":"106","time":%q,"type":"belief.supersede","ring":2,"bytes":40,"head":"{\"old_id\":\"b0\",\"new_id\":\"b1\",\"reason\":\"Two deploys went wrong without staging.\"}"},`+
		`{"seq":"105","time":%q,"type":"belief.upsert","ring":2,"bytes":40,"head":"{\"id\":\"b9\",\"statement\":\"A deploy can go straight to production.\"}"},`+
		`{"seq":"104","time":%q,"type":"experience.create","ring":3,"bytes":60,"head":"{\"category\":\"reflection\",\"content\":\"I learned the grant letter wants a summary first.\"}"},`+
		`{"seq":"103","time":%q,"type":"relationship.upsert","ring":1,"bytes":30,"head":"{}"},`+
		`{"seq":"100","time":%q,"type":"system.witnessed","ring":0,"bytes":20,"head":"{}"}],"first_view_bytes":0,"range_bytes":0}`,
		at(time.Hour), at(90*time.Minute), at(2*time.Hour), at(3*time.Hour), at(7*24*time.Hour))
	first := fmt.Sprintf(`{"version":1,"rows":[{"seq":"1","time":%q,"type":"ring0.genesis","ring":0,"bytes":2,"head":"{}"}],"first_view_bytes":0,"range_bytes":0}`, at(30*24*time.Hour))
	runRealPageServing(t, sixDaysAgoJS+statusFakeSocketJS, theirVoicePageJS, styles, map[string][]byte{"/record": []byte(record), "/record?before=2": []byte(first)})
}
