//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
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
    { id: 'act-9', kind: 'confirm', from: 'org.example.uid', plugin: 'org.example.uid', operation: 'enroll', text: 'Enroll Sam', args: { name: 'Sam' }, proposed: '2026-09-12T16:00:03Z' },
    { id: 'act-10', kind: 'confirm', from: 'org.example.uid', plugin: 'org.example.uid', operation: 'enroll', text: 'Enroll Jim', args: { name: 'Jim' }, proposed: '2026-09-12T16:00:04Z' },
  ]);
  const cards = document.querySelectorAll('.msg.ask');
  assert(cards.length === 5, 'five cards');
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
  renderAsks([]);
  assert(document.querySelectorAll('.msg.ask.closed').length === 5, 'cards gone from the list close in place');
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
	modules["/state.js"] = []byte(`export const S = { stats: { name: 'Ember' }, providers: [], config: null, providersLoaded: false, asks: [] };`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'); export const hueOf = () => 0;`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; } export function wsReady() { return true; } export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/presence.js"] = []byte(`export function setThinking() {} export function toolPulse() {} export function renderPresence() {}`)
	modules["/app.js"] = []byte(`export function toast() {}`)
	modules["/views/model-picker.js"] = []byte(`export function fillModelPicker() {}`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){ return true; }, claim(){ return false; }, drop(){}, waiting(){ return false; } }; }`)
	runPageInEngines(t, page, modules)
}
