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
    { id: 'ask-6', kind: 'voice', from: 'identity', text: 'Was that you just now?', heard: 'good morning', proposed: '2026-09-12T16:00:07Z' },
    { id: 'ask-7', kind: 'voice', from: 'identity', text: 'And that voice?', heard: 'hello there', proposed: '2026-09-12T16:00:08Z' },
  ]);
  const card = id => document.querySelector('[data-ask-id="' + id + '"]');
  const press = (id, words) => [...card(id).querySelectorAll('button')].find(b => b.textContent === words).click();
  const label = id => card(id).querySelector('.c-ask__label').textContent, text = id => card(id).querySelector('.c-ask__text');
  const cards = document.querySelectorAll('.msg.ask');
  assert(cards.length === 9, 'nine cards');
  // A voice card: their question, the words heard, and only the press names.
  assert(label('ask-6') === 'Quill asks' && text('ask-6').textContent === 'Was that you just now?' && card('ask-6').textContent.includes('“good morning”'), 'a voice card does not hold their question and the words heard: ' + card('ask-6').textContent);
  press('ask-6', "Yes, that's me");
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-6' && f.ask.answer === 'confirm'), 'Yes does not answer confirm');
  card('ask-7').querySelector('.c-ask__input').value = ' Mary ';
  press('ask-7', "No, that's not me");
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-7' && f.ask.answer === 'no' && f.ask.text === 'Mary'), 'No does not carry who it was');
  assert(!document.querySelector('input[type="password"]'), 'no card carries a credential field');
  // Whose question: the identity's under their name and in their voice; the
  // host's check of your words and a plugin's confirm in the page's.
  assert(label('ask-1') === 'Quill asks' && text('ask-1').classList.contains('c-ask__text--theirs'), 'the identity\'s question is not under their name, in their voice');
  assert(label('act-9') === 'org.example.uid' && !text('act-9').classList.contains('c-ask__text--theirs') && !label('act-9').includes('Quill'), 'a plugin\'s confirm is worded as the identity\'s');
  assert(label('ask-4') === 'Were these your words?' && !text('ask-4').classList.contains('c-ask__text--theirs'), 'the host\'s check of your words is worded as the identity\'s');
  press('ask-1', 'window');
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-1' && f.ask.answer === 'chose' && f.ask.choice === 'window'), 'a choice answers through the page');
  const c1 = card('ask-1');
  assert(c1.querySelectorAll('button:not([disabled])').length === 0 && c1.querySelector('[aria-pressed="true"]').textContent === 'window', 'an answered card settles, its answer drawn as chosen');
  assert(c1.querySelector('.c-ask__note').textContent === 'Sending your answer…', 'a sent answer does not say it is on its way');
  const inp = card('ask-2').querySelector('.c-ask__input'); inp.value = 'the week of the 21st';
  press('ask-2', 'Send');
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-2' && f.ask.answer === 'said' && f.ask.text === 'the week of the 21st'), 'a typed answer is said');
  press('ask-3', 'Connect, to read');
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-3' && f.ask.answer === 'connect' && f.ask.scope === 'read'), 'connect answers with a scope and points at the plugins page');
  assert(S.connectRequest && S.connectRequest.connector === 'email' && S.connectRequest.scope === 'read' && went[0] === 'plugins', 'the connect answer lands on the Plugins page with the connector and the scope');
  press('act-9', 'Allow this once');
  assert(frames.some(f => f.type === 'plugin' && f.plugin.action === 'confirm' && f.plugin.id === 'org.example.uid' && f.plugin.act === 'act-9'), 'a confirm card answers through the plugin route');
  press('act-10', 'Always allow');
  assert(frames.some(f => f.type === 'plugin' && f.plugin.action === 'always' && f.plugin.id === 'org.example.uid' && f.plugin.act === 'act-10'), 'Always answers through the plugin route as its own word');
  const word = card('ask-4');
  assert(text('ask-4').textContent.includes('"yes, record it"'), 'the word card shows the spoken words');
  assert(!word.querySelector('input') && word.querySelectorAll('button').length === 2, 'the word card is answered by two buttons and takes no typing');
  press('ask-4', 'Yes, those were yours');
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-4' && f.ask.answer === 'confirm'), 'Yes, those were yours answers confirm');
  assert(word.querySelectorAll('button:not([disabled])').length === 0, 'a confirmed word card settles');
  press('ask-5', 'No');
  assert(frames.some(f => f.type === 'ask' && f.ask.id === 'ask-5' && f.ask.answer === 'no'), 'No answers no');
  renderAsks([]);
  assert([...document.querySelectorAll('.msg.ask')].filter(c => c.dataset.state === 'answered').length === 9, 'cards gone from the list close in place');
  assert(card('ask-1').querySelector('.c-ask__note').textContent.startsWith('Answered '), 'a card answered here does not say it was answered');
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
	modules["/state.js"] = []byte(`export const S = { stats: { name: 'Quill' }, providers: [], config: null, providersLoaded: false, asks: [] };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'); export const hueOf = () => 0; export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; } export function wsReady() { return true; } export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/presence.js"] = []byte(`export function setThinking() {} export function toolPulse() {} export function renderPresence() {}`)
	modules["/app.js"] = []byte(`export function toast() {}`)
	modules["/views/model-picker.js"] = []byte(`export function fillModelPicker() {}`)
	runPageInEngines(t, page, modules)
}
