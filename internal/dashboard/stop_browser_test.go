//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"strings"
	"testing"
)

// The stop control, exercised as a real click in a real engine.
//
// a0f2754 put a "cancel" verb on the wire that no client could send —
// which is the same net effect as not building it. This pins the other
// end: while a turn is in flight the send control READS as stop, and
// clicking it puts a cancel on the wire rather than a chat.
//
// The transport is stubbed (the socket has its own round-trip test);
// what is real here is the shipped presence.js, the shipped chat.js,
// the shipped stylesheet, and the click.

const stopControlPage = `<!doctype html>
<style>__LAYOUT_CSS__</style>
<div class="app">
  <div class="presence"><div class="orb-wrap"><div class="orb" id="orb"></div></div>
    <span id="p-name"></span><span id="p-state"></span>
    <span class="pill" id="pill-mode"></span></div>
  <div class="thread" id="thread"><div class="thread-inner" id="thread-inner"></div></div>
  <div class="composer-wrap" id="composer-wrap"><div class="composer">
    <button class="mic" id="mic" disabled>m</button>
    <textarea id="msg-input" rows="1"></textarea>
    <button class="sendb" id="send-btn" title="Send">&#10148;</button>
  </div></div>
</div>
<script type="module">
import { assert, run } from './__harness.js';
import { setThinking } from './presence.js';
import { sent } from './ws.js';
import './views/chat.js';

run(() => {
  const btn = document.getElementById('send-btn');
  const input = document.getElementById('msg-input');

  // 0. Guard: the modules actually wired a handler, or every click below
  //    is a click on nothing and the assertions pass vacuously.
  assert(typeof btn.onclick === 'function', 'no handler is bound to the send control');

  // 1. Idle: it sends what the operator typed.
  input.value = 'hello there';
  btn.click();
  assert(sent.length === 1, 'an idle click sent ' + sent.length + ' messages, want 1');
  assert(sent[0].type === 'chat', 'an idle click sent ' + sent[0].type + ', want chat');
  assert(sent[0].message === 'hello there', 'the typed text did not travel: ' + JSON.stringify(sent[0]));

  // 2. In flight: the control must SAY stop. A control that still reads
  //    "Send" while it cancels is a label the operator must discount.
  setThinking(true);
  assert(btn.classList.contains('stopping'),
    'the control does not change while a turn runs — it still looks like Send');
  assert(/stop/i.test(btn.title), 'the control still says ' + JSON.stringify(btn.title));
  const stopRect = btn.getBoundingClientRect();
  assert(stopRect.width > 0 && stopRect.height > 0, 'the stop control is not visible');

  // 3. And clicking it must stop the turn, not send a message.
  input.value = 'this must not be sent';
  btn.click();
  assert(sent.length === 2, 'the stop click produced ' + (sent.length - 1) + ' messages, want 1');
  assert(sent[1].type === 'cancel',
    'the stop control sent ' + sent[1].type + ' — the cancel verb is still unreachable');

  // 4. Once stopped, it is a send control again, or the operator cannot
  //    speak after stopping.
  setThinking(false);
  assert(!btn.classList.contains('stopping'), 'the control stayed a stop button after the turn ended');
  assert(/send/i.test(btn.title), 'the control still says ' + JSON.stringify(btn.title));
  input.value = 'after stopping';
  btn.click();
  assert(sent.length === 3 && sent[2].type === 'chat',
    'the operator could not speak again after stopping: ' + JSON.stringify(sent.slice(2)));
});
</script>`

func TestStopControlPutsCancelOnTheWireInBrowser(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	layoutCSS := read("layout.css")
	page := strings.Replace(stopControlPage, "__LAYOUT_CSS__", string(layoutCSS), 1)
	if page == stopControlPage {
		t.Fatal("stylesheet placeholder not substituted — the page would assert against no CSS at all")
	}

	runPageInEngines(t, page, map[string][]byte{
		// The shipped modules under test.
		"/presence.js":           read("presence.js"),
		"/views/chat.js":         read("views/chat.js"),
		"/state.js":              read("state.js"),
		"/util.js":               read("util.js"),
		"/views/model-picker.js": []byte(`export function fillModelPicker() {}`),
		// The transport, stubbed so the wire is inspectable. Everything
		// chat.js imports from it must exist or the module fails to load.
		"/ws.js": []byte(`export const sent = [];
export function send(obj) { sent.push(obj); return true; }
export function wsReady() { return true; }
export function query() {}`),
		"/app.js": []byte(`export function toast() {}
export function go() {}
export function renderFirstbootVisibility() {}`),
	})
}
