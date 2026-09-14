//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestTheHomeRendersEveryStateFromRealPayloads(t *testing.T) {
	page := `<!doctype html><html><body>
<nav><div class="nav-item" data-view="home"></div><div class="nav-item" data-view="chat"></div><div class="nav-item" data-view="memory"></div><div class="nav-item" data-view="identity"></div><div class="nav-item" data-view="projects"></div></nav>
<div id="home-inner"></div>
<textarea id="msg-input"></textarea><button id="mic"></button><input id="mem-search">
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { renderHome, ringRows, continuityLine, ago } from '/views/home.js';
run(async (assert) => {
  const clicked = []; document.querySelectorAll('.nav-item').forEach(el => { el.onclick = () => clicked.push(el.dataset.view); });
  const root = document.getElementById('home-inner');
  const text = () => root.textContent;

  // Offline: says so, shows nothing it cannot know.
  S.connected = false; S.identityExists = false; S.stats = null; S.identity = null; S.work = null; S.cont = null; S.projects = [];
  renderHome();
  assert(text().includes('Connecting'), 'offline says it is connecting');
  assert(!text().includes('is here'), 'offline never claims presence');
  assert(!root.querySelector('.ring-row'), 'offline shows no rings');

  // Unborn: names the way in; intents wait.
  S.connected = true;
  renderHome();
  assert(text().includes('No identity lives here yet'), 'unborn says so and points at Chat');
  assert(root.querySelectorAll('.home-intent:disabled').length === 4, 'unborn disables the four intents');

  // Born, identity not yet loaded: counts wait, they are not zero.
  S.identityExists = true; S.stats = { name: 'Ivy', version: '0.1.0', build: 'abc1234' };
  renderHome();
  assert(text().includes('Ivy is here'), 'born greets by name');
  const waiting = [...root.querySelectorAll('.ring-right')].map(e => e.textContent);
  assert(waiting[2] === '…' && waiting[3] === '…', 'unloaded counts read as waiting, not as 0: ' + waiting.join('|'));

  // Unnamed: the operator is told to ask, and the greeting does not
  // call them "Unnamed"; a named identity earns no hint.
  S.stats = { name: 'Unnamed', version: '0.1.0', build: 'abc1234' };
  renderHome();
  assert(root.querySelector('[data-name-hint]') !== null && text().includes('like to be called'), 'an unnamed identity earns the naming hint');
  assert(!text().includes('Unnamed is here'), 'the greeting does not call them Unnamed: ' + text().slice(0, 120));
  S.stats = { name: 'Ivy', version: '0.1.0', build: 'abc1234' };
  renderHome();
  assert(root.querySelector('[data-name-hint]') === null, 'a named identity earns no hint');

  // Loaded: every number is a count from the payload.
  const fiveMin = new Date(Date.now() - 5 * 60000).toISOString();
  S.identity = {
    beliefs: [{ring: 2}, {ring: 2}, {ring: 2}, {ring: 3}, {ring: 3}, {ring: 3}, {ring: 3}, {ring: 3}, {ring: 1}],
    experiences: [{content: 'Read the Tier 2 ruling', category: 'work', created_at: fiveMin}, {content: 'Heard a plan', category: 'conversation', created_at: fiveMin}, {content: 'Third', created_at: fiveMin}],
    brief: 'I am steady and curious this morning.', charter: 'We build together.',
  };
  S.work = { live: [{id: 'w1'}], queued: 2 };
  S.cont = { mode: 'normal', ledger_seq: 18422, witnessed_at: new Date(Date.now() - 4 * 60000).toISOString(), unanchored: 0, review_status: 'clear' };
  S.projects = [{id: 'p1', name: 'Product strategy', description: 'Q3', state: 'open', active: true, focus: 'positioning'}];
  renderHome();
  const rights = [...root.querySelectorAll('.ring-right')].map(e => e.textContent);
  assert(rights[2] === '3 promoted', 'Ring 2 counts the ring-2 beliefs: ' + rights[2]);
  assert(rights[3] === '5 held', 'Ring 3 counts the ring-3 beliefs: ' + rights[3]);
  assert(rights[4] === '1 live · 2 queued', 'Ring 4 is the live work: ' + rights[4]);
  assert(rights[1] === 'minted', 'Ring 1 reads the charter: ' + rights[1]);
  assert(text().includes('I am steady and curious'), 'the brief is the identity\'s own words');
  assert(root.querySelectorAll('.recent-row').length === 3, 'recently lists the experiences');
  assert(text().includes('5m ago'), 'experiences carry a distance in time');
  const strip = root.querySelector('.home-strip').textContent;
  assert(strip.includes('record sound') && strip.includes('ledger seq 18,422') && strip.includes('witnessed 4m ago') && strip.includes('review clear') && strip.includes('build 0.1.0 · abc1234'), 'the strip reads the continuity payload: ' + strip);
  assert(text().includes('positioning'), 'a project card shows its focus');

  // Absences are said, not blank.
  S.identity.brief = ''; S.identity.experiences = []; S.identity.charter = '';
  renderHome();
  assert(text().includes('No brief yet'), 'an absent brief is said');
  assert(text().includes('Nothing recorded yet'), 'no experiences is said');
  assert([...root.querySelectorAll('.ring-right')][1].textContent === 'not yet minted', 'an unminted charter is said');

  // SAFE reaches the strip in red words.
  S.cont = { mode: 'safe', safe_reason: 'ledger hash mismatch', ledger_seq: 1 };
  renderHome();
  const safe = root.querySelector('.home-strip .bad');
  assert(safe && safe.textContent.includes('SAFE — record frozen: ledger hash mismatch'), 'SAFE is stated with its reason');

  // The hand-off: the words reach the one composer; nothing is sent here.
  S.cont = { mode: 'normal', ledger_seq: 1 }; renderHome();
  let inputEvents = 0; document.getElementById('msg-input').addEventListener('input', () => inputEvents++);
  const field = document.getElementById('home-input');
  field.value = '  plan the launch  ';
  field.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  assert(clicked[clicked.length - 1] === 'chat', 'Enter goes to Chat');
  assert(document.getElementById('msg-input').value === 'plan the launch', 'the words are carried, trimmed');
  assert(inputEvents === 1, 'the composer is told its value changed (autosize)');

  // A ring row opens its view; the memory search takes focus.
  root.querySelectorAll('.ring-row')[2].click();
  await new Promise(r => setTimeout(r, 20));
  assert(clicked[clicked.length - 1] === 'memory', 'a belief ring opens Memory');
  assert(document.activeElement === document.getElementById('mem-search'), 'recall lands in the search field');

  // Pure helpers.
  assert(ago(new Date(Date.now() - 3 * 3600000).toISOString()) === '3h ago', 'ago renders hours');
  assert(ago('not a date') === 'not a date', 'an unparsable time is shown as given, not invented');
  assert(ringRows(null, null)[2].right === '…', 'ringRows waits without an identity');
  assert(continuityLine(null, null).length === 0, 'no continuity, no strip');
});
</script></body></html>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/home.js", "static/state.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/views/projects.js"] = []byte(`export function viewProject() {}`)
	modules["/views/work.js"] = []byte(`export function workSectionHTML() { return '<div class="card work-stub">live work</div>'; } export function wireWork() {}`)
	runPageInEngines(t, page, modules)
}
