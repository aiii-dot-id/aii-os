//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestAskCardsRenderAndAnswerInBrowser(t *testing.T) {
	page := `<!doctype html>
<div id="thread"><div id="thread-inner"></div></div>
<textarea id="msg-input"></textarea><button id="send-btn"></button>
<script type="module">
import { S } from './state.js';
import { renderAsks } from './views/chat.js';
import { frames } from './ws.js';
import { assert, run } from './__harness.js';
const went = []; S.go = v => went.push(v);
run(() => {
  renderAsks([
    { id: 'ask-1', kind: 'choose', from: 'identity', text: 'Aisle or window?', choices: ['aisle', 'window'], proposed: '2026-09-12T16:00:00Z' },
    { id: 'ask-2', kind: 'clarify', from: 'identity', text: 'Which week?', proposed: '2026-09-12T16:00:01Z' },
    { id: 'ask-3', kind: 'connect', from: 'identity', text: 'I need your email.', connector: 'email', proposed: '2026-09-12T16:00:02Z' },
    { id: 'act-9', kind: 'confirm', from: 'org.example.uid', plugin: 'org.example.uid', operation: 'enroll', text: 'Enroll Quinn', args: { name: 'Quinn' }, proposed: '2026-09-12T16:00:03Z' },
    { id: 'act-10', kind: 'confirm', from: 'org.example.uid', plugin: 'org.example.uid', operation: 'enroll', text: 'Enroll Guest', args: { name: 'Guest' }, proposed: '2026-09-12T16:00:04Z' },
    { id: 'ask-4', kind: 'word', from: 'host', text: 'At turn 41 you said: "yes, record it".', proposed: '2026-09-12T16:00:05Z' },
    { id: 'ask-5', kind: 'word', from: 'host', text: 'At turn 42 you said: "and the mornings".', proposed: '2026-09-12T16:00:06Z' },
  ]);
  const cards = document.querySelectorAll('.msg.ask');
  assert(cards.length === 7, 'seven cards');
  assert(!document.querySelector('input[type="password"]'), 'no card carries a credential field');
  document.querySelector('[data-ask-id="ask-1"] [data-ask-choice="window"]').click();
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-1' && f.ask.answer === 'chose' && f.ask.choice === 'window'), 'a choice answers through the page');
  const c1 = document.querySelector('[data-ask-id="ask-1"]');
  assert(c1.querySelectorAll('button:not([disabled])').length === 0, 'an answered card settles');
  const inp = document.querySelector('[data-ask-id="ask-2"] .ask-input'); inp.value = 'the week of the 21st';
  document.querySelector('[data-ask-id="ask-2"] [data-ask-say]').click();
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-2' && f.ask.answer === 'said' && f.ask.text === 'the week of the 21st'), 'a typed answer is said');
  document.querySelector('[data-ask-id="ask-3"] [data-ask-connect="read"]').click();
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-3' && f.ask.answer === 'connect' && f.ask.scope === 'read'), 'connect answers with a scope and points at the plugins page');
  assert(S.connectRequest && S.connectRequest.connector === 'email' && S.connectRequest.scope === 'read' && went[0] === 'plugins', 'the connect answer lands on the Plugins page with the connector and the scope');
  document.querySelector('[data-ask-id="act-9"] [data-ask-act="confirm"]').click();
  assert(frames.some(f => f.type === 'plugin' && f.plugin.action === 'confirm' && f.plugin.id === 'org.example.uid' && f.plugin.act === 'act-9'), 'a confirm card answers through the plugin route');
  document.querySelector('[data-ask-id="act-10"] [data-ask-act="always"]').click();
  assert(frames.some(f => f.type === 'plugin' && f.plugin.action === 'always' && f.plugin.id === 'org.example.uid' && f.plugin.act === 'act-10'), 'Always answers through the plugin route as its own word');
  const word = document.querySelector('[data-ask-id="ask-4"]');
  assert(word.querySelector('.who').textContent === 'Confirm your spoken words' && word.querySelector('.ask-text').textContent.includes('"yes, record it"'), 'the word card shows the spoken words under its own heading');
  assert(!word.querySelector('input') && word.querySelectorAll('button').length === 2, 'the word card is answered by two buttons and takes no typing');
  word.querySelector('[data-ask-answer="confirm"]').click();
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-4' && f.ask.answer === 'confirm'), 'These are my words answers confirm');
  assert(word.querySelector('.ask-note').textContent === 'You confirmed them' && word.querySelectorAll('button:not([disabled])').length === 0, 'a confirmed word card settles and says so');
  document.querySelector('[data-ask-id="ask-5"] [data-ask-answer="no"]').click();
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-5' && f.ask.answer === 'no'), 'No answers no');
  renderAsks([]);
  assert(document.querySelectorAll('.msg.ask.closed').length === 7, 'cards gone from the list close in place');
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/chat.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { stats: { name: 'TestIdentity' }, providers: [], config: null, providersLoaded: false, asks: [] };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'); export const hueOf = () => 0; export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; } export function wsReady() { return true; } export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/presence.js"] = []byte(`export function setThinking() {} export function toolPulse() {} export function renderPresence() {}`)
	modules["/app.js"] = []byte(`export function toast() {}`)
	modules["/views/model-picker.js"] = []byte(`export function fillModelPicker() {}`)
	runPageInEngines(t, page, modules)
}
