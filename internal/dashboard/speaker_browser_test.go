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
func TestSpeakerAttributionRendersInBrowser(t *testing.T) {
	page := `<!doctype html>
<div id="thread"><div id="thread-inner"></div></div>
<textarea id="msg-input"></textarea><button id="send-btn"></button>
<script type="module">
import { addMsg, attachSpeaker, renderHistory } from './views/chat.js';
import { assert, run } from './__harness.js';
run(() => {
  const el = addMsg('operator', '[voice] hello there', '', 'vs-1/7');
  assert(el && el.dataset.voiceRef === 'vs-1/7' && el.querySelector('.who').textContent === 'you', 'a spoken bubble carries its voice reference: ' + (el && el.outerHTML));
  assert(attachSpeaker('vs-1/7', 'Sam') && el.querySelector('.who').textContent === 'you · Sam', 'an observation attaches to the bubble it names: ' + el.querySelector('.who').textContent);
  assert(attachSpeaker('vs-1/7', 'uncertain: Sam 0.60') && el.querySelector('.who').textContent === 'you · uncertain: Sam 0.60', 'a late result amends the same bubble');
  assert(el.querySelectorAll('.speaker').length === 1, 'one attribution per bubble');
  assert(!attachSpeaker('vs-1/99', 'Jim'), 'a bubble the page never had is passed over');
  const plain = addMsg('operator', 'typed words');
  assert(plain && !plain.dataset.voiceRef, 'a typed message carries no reference');
  renderHistory([{ role: 'operator', content: '[voice] again', voice_ref: 'vs-1/8', note: 'unknown speaker' }, { role: 'resident', content: 'hi' }]);
  const replayed = document.querySelector('[data-voice-ref="vs-1/8"]');
  assert(replayed && replayed.querySelector('.who').textContent === 'you · unknown speaker', 'history replays the reference and the attribution: ' + (replayed && replayed.outerHTML));
  assert(attachSpeaker('vs-1/8', 'Sam') && replayed.querySelector('.who').textContent === 'you · Sam', 'a late result reaches a replayed bubble too');
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
	modules["/state.js"] = []byte(`export const S = { stats: { name: 'Ember' }, providers: [], config: null, providersLoaded: false };`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'); export const hueOf = () => 0;`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; } export function wsReady() { return true; } export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/presence.js"] = []byte(`export function setThinking() {} export function toolPulse() {} export function renderPresence() {}`)
	modules["/app.js"] = []byte(`export function toast() {}`)
	modules["/views/model-picker.js"] = []byte(`export function fillModelPicker() {}`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){ return true; }, claim(){ return false; }, drop(){}, waiting(){ return false; } }; }`)
	runPageInEngines(t, page, modules)
}
