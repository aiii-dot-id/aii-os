//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestTheHomeRendersEveryStateFromRealPayloads(t *testing.T) {
	page := `<!doctype html><html><body>
<nav><div class="nav-item" data-view="home"></div><div class="nav-item" data-view="chat"></div><div class="nav-item" data-view="memory"></div><div class="nav-item" data-view="identity"></div><div class="nav-item" data-view="projects"></div></nav>
<div id="home-inner"></div><div id="identity-stack"></div>
<textarea id="msg-input"></textarea><button id="mic"></button><input id="mem-search">
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { renderHome, ringRows, continuityLine, ago } from '/views/home.js';
import { renderIdentity } from '/views/identity.js';
run(async (assert) => {
  const clicked = []; document.querySelectorAll('.nav-item').forEach(el => { el.onclick = () => clicked.push(el.dataset.view); });
  const root = document.getElementById('home-inner');
  const text = () => root.textContent;
  const briefCard = () => [...root.querySelectorAll('.home-card')].find(c => c.querySelector('h3').textContent === 'THEIR MORNING BRIEF');

  S.connected = false; S.identityExists = false; S.stats = null; S.identity = null; S.work = null; S.cont = null; S.projects = [];
  renderHome();
  assert(text().includes('Connecting'), 'offline says it is connecting');
  assert(!text().includes('is here'), 'offline never claims presence');
  assert(!root.querySelector('.ring-row'), 'offline shows no rings');

  S.connected = true;
  renderHome();
  assert(text().includes('No identity lives here yet'), 'unborn says so and points at Chat');
  assert(root.querySelectorAll('.home-intent:disabled').length === 4, 'unborn disables the four intents');

  S.identityExists = true; S.stats = { name: 'Vega', version: '0.1.0', build: 'abc1234' };
  renderHome();
  assert(text().includes('Vega is here'), 'born greets by name');
  const waiting = [...root.querySelectorAll('.ring-right')].map(e => e.textContent);
  assert(waiting[2] === '…' && waiting[3] === '…', 'unloaded counts read as waiting, not as 0: ' + waiting.join('|'));

  S.stats = { name: 'Unnamed', version: '0.1.0', build: 'abc1234' };
  renderHome();
  assert(root.querySelector('[data-name-hint]') !== null && text().includes('like to be called'), 'an unnamed identity earns the naming hint');
  const hint = root.querySelector('[data-name-hint]');
  assert(!hint.querySelector('code') && !hint.textContent.includes('/') && hint.textContent.includes('theirs to record') && hint.textContent.includes('greeting follows on the next turn'),
    'the naming hint names a file or lost its meaning: ' + hint.textContent);
  assert(!text().includes('Unnamed is here'), 'the greeting does not call them Unnamed: ' + text().slice(0, 120));
  S.stats = { name: 'Vega', version: '0.1.0', build: 'abc1234' };
  renderHome();
  assert(root.querySelector('[data-name-hint]') === null, 'a named identity earns no hint');

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
  assert(briefCard() && briefCard().querySelector('.home-brief'), 'the brief is not headed as their morning brief: ' + [...root.querySelectorAll('h3')].map(h => h.textContent).join('|'));
  renderIdentity();
  const idHeads = [...document.querySelectorAll('#identity-stack h3')].map(h => h.textContent);
  assert(idHeads[0] === 'THEIR MORNING BRIEF', 'the Identity page names the brief differently: ' + idHeads.join('|'));
  assert(root.querySelectorAll('.recent-row').length === 3, 'recently lists the experiences');
  assert(text().includes('5m ago'), 'experiences carry a distance in time');
  const strip = root.querySelector('.home-strip').textContent;
  assert(strip.includes('record sound') && strip.includes('ledger seq 18,422') && strip.includes('witnessed 4m ago') && strip.includes('review clear') && strip.includes('build 0.1.0 · abc1234'), 'the strip reads the continuity payload: ' + strip);
  assert(text().includes('positioning'), 'a project card shows its focus');

  S.identity.brief = ''; S.identity.experiences = []; S.identity.charter = '';
  renderHome();
  const absent = briefCard() && briefCard().querySelector('.home-empty');
  assert(absent && absent.textContent === 'No brief yet — one is written for them each morning.', 'an absent brief is not said truthfully: ' + (absent ? absent.textContent : text()));
  assert(text().includes('Nothing recorded yet'), 'no experiences is said');
  assert([...root.querySelectorAll('.ring-right')][1].textContent === 'not yet minted', 'an unminted charter is said');

  S.cont = { mode: 'safe', safe_reason: 'ledger hash mismatch', ledger_seq: 1 };
  renderHome();
  const safe = root.querySelector('.home-strip .bad');
  assert(safe && safe.textContent.includes('SAFE — record frozen: ledger hash mismatch'), 'SAFE is stated with its reason');
  S.cont = { mode: 'safe', safe_reason: 'witness said <b id="planted">recover here</b>', ledger_seq: 1 };
  renderHome();
  assert(!root.querySelector('#planted'), 'a SAFE reason that carries markup must not become an element of the page');
  assert(root.querySelector('.home-strip .bad').textContent.includes('<b id="planted">recover here</b>'), 'the reason is shown as the text it is');

  S.cont = { mode: 'normal', ledger_seq: 1 }; renderHome();
  let inputEvents = 0; document.getElementById('msg-input').addEventListener('input', () => inputEvents++);
  const field = document.getElementById('home-input');
  field.value = '  plan the launch  ';
  field.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
  assert(clicked[clicked.length - 1] === 'chat', 'Enter goes to Chat');
  assert(document.getElementById('msg-input').value === 'plan the launch', 'the words are carried, trimmed');
  assert(inputEvents === 1, 'the composer is told its value changed (autosize)');

  root.querySelectorAll('.ring-row')[2].click();
  await new Promise(r => setTimeout(r, 20));
  assert(clicked[clicked.length - 1] === 'memory', 'a belief ring opens Memory');
  assert(document.activeElement === document.getElementById('mem-search'), 'recall lands in the search field');

  assert(ago(new Date(Date.now() - 3 * 3600000).toISOString()) === '3h ago', 'ago renders hours');
  assert(ago('not a date') === 'not a date', 'an unparsable time is shown as given, not invented');
  assert(ringRows(null, null)[2].right === '…', 'ringRows waits without an identity');
  assert(continuityLine(null, null).length === 0, 'no continuity, no strip');
  const dark = continuityLine({ mode: 'degraded_witness' }, null)[0].text;
  assert(dark === 'degraded — witness dark', 'a witness that is only unreachable gained words: ' + dark);
  const refused = continuityLine({ mode: 'degraded_witness', degraded_why: 'the server witness.example presented a certificate that expired on 2026-10-13T21:08:42Z (this machine\'s clock reads 2026-10-13T23:00:00Z) and names <b>mail.example</b> and not witness.example: if witness.example is the server\'s own name, it must present a certificate that names it and that this machine accepts; if not, the address configured for it is wrong' }, null)[0].text;
  assert(refused.startsWith('degraded — witness certificate refused: the server witness.example presented a certificate that expired on') && !refused.includes('dark'), 'the strip does not say the witness\'s certificate was refused: ' + refused);
  assert(!refused.includes('<b>') && refused.includes('&lt;b&gt;'), 'the names a server chose reach the strip as markup: ' + refused);
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
	identity, err := staticFS.ReadFile("static/views/identity.js")
	if err != nil {
		t.Fatal(err)
	}
	modules["/views/identity.js"] = identity
	modules["/app.js"] = []byte(`export function go() {}`)
	modules["/views/backups.js"] = []byte(`export function requestBackups() {}`)
	modules["/views/settings.js"] = []byte(`export function openBackups() {}`)
	runPageInEngines(t, page, modules)
}

func TestHomeKeepsUnsentInputAcrossStatusRender(t *testing.T) {
	page := `<!doctype html><div id="home-inner"></div><script type="module">
import {S} from '/state.js';import {renderHome} from '/views/home.js';import {run} from '/__harness.js';
run(assert=>{
 S.stats={name:'Fixture'};S.identityExists=true;S.connected=true;S.projects=[];
 S.identity={beliefs:[],experiences:[]};
 S.work={live:[],queued:0,delivered:[{id:'w1',description:'Result',result:'done'}]};
 renderHome();const input=document.getElementById('home-input');input.value='unsent request';input.focus();input.setSelectionRange(4,8);
 renderHome();const next=document.getElementById('home-input');
 assert(next.value==='unsent request'&&document.activeElement===next&&next.selectionStart===4&&next.selectionEnd===8,'status redraw lost Home input/focus');
 S.connected=false;renderHome();S.connected=true;renderHome();
 assert(document.getElementById('home-input').value==='unsent request','offline redraw lost Home input');
 document.querySelector('[data-grade="partial"]').click();const line=document.querySelector('.grade-input');line.value='explanation';line.dispatchEvent(new Event('input',{bubbles:true}));line.setSelectionRange(2,5);
 renderHome();const kept=document.querySelector('.grade-input');
 assert(kept.value==='explanation'&&document.activeElement===kept&&kept.selectionStart===2&&kept.selectionEnd===5,'status redraw lost grade draft/focus');
});</script>`
	runPageInEngines(t, page, map[string][]byte{
		"/ws.js":  []byte(`export function send(){return 'r';}export function query(){return 'r';}`),
		"/app.js": []byte(`export function go(){}`),
	})
}
